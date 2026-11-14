package release

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/alfpricot/gala/internal/changelog"
	"github.com/alfpricot/gala/internal/commit"
	"github.com/alfpricot/gala/internal/config"
	"github.com/alfpricot/gala/internal/git"
	"github.com/alfpricot/gala/internal/manager"
	"github.com/alfpricot/gala/internal/semver"
)

// Engine holds everything the pipeline needs after config and repo are open.
type Engine struct {
	opts *Options
	repo *git.Repo
	cfg  *config.Config
	root string
	// mgrs holds one adapter per manager the config names: the top-level
	// manager and any per-package override.
	mgrs map[config.ManagerName]manager.Manager

	branch    string
	branchErr error

	// outputWritten records that the release's GITHUB_OUTPUT step outputs
	// were written, so Run does not add released=false after them.
	outputWritten bool
}

// Open loads the config, resolves the repo root, and picks the adapter. It is
// step 0 of the pipeline and is shared by all three modes.
func Open(opts *Options) (*Engine, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = "."
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}

	repo, err := git.Open(abs, opts.Git)
	if err != nil {
		return nil, err
	}
	root := repo.Root()

	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		found, ferr := config.Find(root)
		if ferr != nil {
			return nil, ferr
		}
		cfgPath = found
	} else if !filepath.IsAbs(cfgPath) {
		cfgPath = filepath.Join(abs, cfgPath)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(root); err != nil {
		return nil, err
	}

	mgrs := map[config.ManagerName]manager.Manager{}
	names := []config.ManagerName{cfg.Manager}
	for _, p := range cfg.Packages {
		names = append(names, p.Manager)
	}
	for _, name := range names {
		if name == "" || mgrs[name] != nil {
			continue
		}
		mgr, err := manager.New(name, opts.Exec)
		if err != nil {
			return nil, err
		}
		mgrs[name] = mgr
	}

	e := &Engine{opts: opts, repo: repo, cfg: cfg, root: root, mgrs: mgrs}
	e.branch, e.branchErr = repo.CurrentBranch()
	if e.branchErr != nil {
		e.branch = "(detached)"
	}
	return e, nil
}

// Repo exposes the opened repository (tests and the self-plan gate use it).
func (e *Engine) Repo() *git.Repo { return e.repo }

// Config exposes the loaded config.
func (e *Engine) Config() *config.Config { return e.cfg }

// PackagePlan is one package's slice of the plan.
type PackagePlan struct {
	Name      string
	Manager   manager.Manager
	Publish   bool                 // this package publishes when the run allows it
	Files     []config.VersionFile // extra version files, repository-relative
	Hooks     config.Hooks         // the package's own hooks, run from AbsDir
	Dir       string               // repo-relative, "." for an app
	AbsDir    string
	TagPrefix string
	Tag       string // anchor tag, "" when the plan starts from history/manifest
	Base      semver.Version
	Bump      semver.Bump
	Next      semver.Version
	NextTag   string
	Commits   []commit.Commit
	Skipped   int
	Due       bool
	Reason    string
	Header    string // changelog/tag header line
	Entry     string // rendered changelog entry
}

// Plan is the whole computed release.
type Plan struct {
	Packages []*PackagePlan
	Branch   string
	Date     string
}

// Due returns the packages that will release, in declaration order.
func (p *Plan) Due() []*PackagePlan {
	var out []*PackagePlan
	for _, pkg := range p.Packages {
		if pkg.Due {
			out = append(out, pkg)
		}
	}
	return out
}

// spec is a resolved package: name (from config or manifest), directories,
// and the tag prefix its tags carry.
type spec struct {
	name   string
	absDir string
	relDir string
	prefix string
	// legacyPrefix is the name-based prefix a go package got before its
	// default became directory-based; set only when it differs from prefix, so
	// the plan can refuse to restart a package whose tags still use it.
	legacyPrefix string
	mgr          manager.Manager
	publish      bool
	files        []config.VersionFile
	hooks        config.Hooks
}

// Plan computes the release plan (pipeline step 2). It writes nothing.
func (e *Engine) Plan(now time.Time) (*Plan, error) {
	// An unborn branch has no HEAD to plan against; say so plainly instead of
	// letting git's "malformed object name" leak out.
	if _, err := e.repo.HeadSHA(); err != nil {
		return nil, fmt.Errorf("this repository has no commits yet — make the first commit, then plan")
	}

	specs, err := e.specs()
	if err != nil {
		return nil, err
	}

	tags, err := e.repo.Tags()
	if err != nil {
		return nil, err
	}

	anchors := make([]string, len(specs))
	anchorVersions := make([]semver.Version, len(specs))
	for i, s := range specs {
		tag, v, err := selectTag(tags, s.prefix)
		if err != nil {
			return nil, err
		}
		anchors[i], anchorVersions[i] = tag, v
		if tag == "" && s.legacyPrefix != "" {
			if old, _, err := selectTag(tags, s.legacyPrefix); err != nil {
				return nil, err
			} else if old != "" {
				return nil, legacyPrefixError(s, old)
			}
		}
	}

	bases := make([]semver.Version, len(specs))
	for i, s := range specs {
		mv, ok, err := s.mgr.ManifestVersion(s.absDir)
		if err != nil {
			return nil, fmt.Errorf("package %q: %w", s.name, err)
		}
		// A manager with no manifest version (go, none) takes the package's
		// first version file as its manifest, so adopting gala starts from the
		// version the project already states instead of 0.0.0. With a tag, the
		// tag wins and checkVersionFiles reports a disagreeing file by name, so
		// the manifest drift check below is for real manifests only.
		adopted := false
		if !ok && len(s.files) > 0 {
			if mv, err = manager.ReadVersionFile(e.root, s.files[0]); err != nil {
				return nil, fmt.Errorf("package %q: versionFile %w", s.name, err)
			}
			ok, adopted = true, true
		}
		if anchors[i] != "" && ok && !adopted && semver.Compare(mv, anchorVersions[i]) != 0 {
			return nil, e.driftError(s, anchors[i], mv)
		}
		switch {
		case anchors[i] != "":
			bases[i] = anchorVersions[i]
		case ok:
			bases[i] = mv // adoption path: an existing project keeps its version
		default:
			bases[i] = semver.Version{} // go with no tag: 0.0.0
		}
	}
	// A file holds one version, so a version file may not be any package's
	// manifest: both writes would land on it and the last would win.
	manifests := map[string]string{}
	for _, s := range specs {
		if m := s.mgr.Manifest(s.absDir, e.root); m != "" {
			manifests[m] = s.name
		}
	}
	for i, s := range specs {
		if err := e.checkVersionFiles(s, bases[i], manifests); err != nil {
			return nil, err
		}
	}

	commitsByPkg, err := e.commits(specs, anchors)
	if err != nil {
		return nil, err
	}

	date := now.Format("2006-01-02")
	plan := &Plan{Branch: e.branch, Date: date}
	for i, s := range specs {
		pkg, err := e.buildPackagePlan(s, anchors[i], bases[i], commitsByPkg[i], date, e.cfg.Kind == config.KindMonorepo)
		if err != nil {
			return nil, err
		}
		plan.Packages = append(plan.Packages, pkg)
	}
	return plan, nil
}

// specs resolves each package's name and tag prefix, and rejects duplicate
// names: two packages cannot share a tag namespace.
func (e *Engine) specs() ([]spec, error) {
	if e.cfg.Kind == config.KindApp {
		// The app's name appears only in prose, so a manifest read failure
		// falls back to the directory name instead of failing the plan.
		mgr := e.mgrs[e.cfg.Manager]
		name, err := mgr.PackageName(e.root)
		if err != nil {
			name = filepath.Base(e.root)
		}
		return []spec{{
			name: name, absDir: e.root, relDir: ".", prefix: e.cfg.TagPrefix,
			mgr: mgr, publish: e.cfg.Publish, files: e.cfg.VersionFiles,
		}}, nil
	}

	seen := map[string]string{}
	specs := make([]spec, 0, len(e.cfg.Packages))
	for _, p := range e.cfg.Packages {
		name := p.Name
		absDir := filepath.Join(e.root, filepath.FromSlash(p.Path))
		mgr := e.mgrs[p.Manager]
		if name == "" {
			var err error
			name, err = mgr.PackageName(absDir)
			if err != nil {
				return nil, fmt.Errorf("package %q: %w", p.Path, err)
			}
		}
		if other, dup := seen[name]; dup {
			return nil, fmt.Errorf("packages %q and %q both resolve to name %q — "+
				"set an explicit name on one of them so their tags do not collide", other, p.Path, name)
		}
		seen[name] = p.Path

		prefix, legacy := p.TagPrefix, ""
		if prefix == "" {
			prefix = defaultTagPrefix(p.Manager, p.Path, name)
			if old := name + "/v"; old != prefix {
				legacy = old
			}
		}
		specs = append(specs, spec{
			name: name, absDir: absDir, relDir: p.Path, prefix: prefix, legacyPrefix: legacy,
			mgr: mgr, publish: p.Publish, files: p.VersionFiles, hooks: p.Hooks,
		})
	}
	if err := checkTagPrefixes(specs); err != nil {
		return nil, err
	}
	return specs, nil
}

// majorDirRe matches a Go major-version directory such as v2 or v10.
var majorDirRe = regexp.MustCompile(`^v([2-9]|[1-9][0-9]+)$`)

// defaultTagPrefix is the tag prefix a monorepo package gets when it sets
// none. Go resolves a nested module's versions only from tags named
// <subdir>/vX.Y.Z, so a go package defaults to its directory (and to plain "v"
// for a module at the repository root); every other manager defaults to its
// name. A trailing major-version directory (foo/v2) is dropped, because Go
// maps module example.com/m/foo/v2 to tags foo/v2.x.y, not foo/v2/v2.x.y.
func defaultTagPrefix(mgr config.ManagerName, relDir, name string) string {
	if mgr != config.ManagerGo {
		return name + "/v"
	}
	dir := relDir
	if base := path.Base(dir); majorDirRe.MatchString(base) {
		dir = path.Dir(dir)
	}
	if dir == "." {
		return "v"
	}
	return dir + "/v"
}

// legacyPrefixError refuses to plan a go package whose existing tags use the
// name-based default it had before the directory-based default: reading no
// tag under the new prefix would restart its versions from 0.0.0 with a
// changelog covering the whole history.
func legacyPrefixError(s spec, oldTag string) error {
	return fmt.Errorf("package %q has tags under its previous default prefix %q (latest %s) but none under "+
		"the current default %q, so its next release would restart from the beginning of history — "+
		"set tagPrefix = %q to keep versioning from those tags, "+
		"or tag the same commit with a %sX.Y.Z tag to continue under the new prefix",
		s.name, s.legacyPrefix, oldTag, s.prefix, s.legacyPrefix, s.prefix)
}

// checkTagPrefixes rejects two packages whose tags could be mistaken for one
// another. An identical prefix shares a tag namespace outright. A prefix that
// another one extends with a digit is just as ambiguous: with "v" and "v1."
// the tag "v1.2.0" parses as a version of the first and belongs to the second.
// semver.Parse drops one leading "v", so an extension that is "v" or "v"
// followed by a digit ("x-" beside "x-v" or "x-v1") reads the same way.
func checkTagPrefixes(specs []spec) error {
	for i, a := range specs {
		for _, b := range specs[i+1:] {
			if a.prefix == b.prefix {
				return fmt.Errorf("packages %q and %q both use tag prefix %q — "+
					"give one of them a distinct tagPrefix so their releases do not share tags",
					a.relDir, b.relDir, a.prefix)
			}
			short, long := a, b
			if len(short.prefix) > len(long.prefix) {
				short, long = long, short
			}
			rest, ok := strings.CutPrefix(long.prefix, short.prefix)
			tail := strings.TrimPrefix(rest, "v")
			if ok && rest != "" && (tail == "" || (tail[0] >= '0' && tail[0] <= '9')) {
				return fmt.Errorf("packages %q and %q have overlapping tag prefixes %q and %q — "+
					"a tag of the longer one can read as a version of the shorter one; "+
					"give them distinct tagPrefix values",
					short.relDir, long.relDir, short.prefix, long.prefix)
			}
		}
	}
	return nil
}

// selectTag returns the highest stable tag under prefix and its version.
// Prerelease tags are filtered out entirely: gala ignores them when planning
// and never creates them. Every tag passed in is already reachable from HEAD.
func selectTag(tags []string, prefix string) (string, semver.Version, error) {
	var best string
	var bestV semver.Version
	for _, tag := range tags {
		if !strings.HasPrefix(tag, prefix) {
			continue
		}
		v, err := semver.Parse(strings.TrimPrefix(tag, prefix))
		if err != nil || !v.Stable() {
			continue
		}
		if best == "" || semver.Compare(v, bestV) > 0 {
			best, bestV = tag, v
		}
	}
	return best, bestV, nil
}

// commits returns, per package, the commits in that package's range. For an
// app that is one direct log. For a monorepo it is one batched
// `git log --name-only` over the union range (merge-base of every anchor) plus
// one cheap rev-list per package for range membership — never a per-commit
// spawn — filtered in process by path.
func (e *Engine) commits(specs []spec, anchors []string) ([][]commit.Commit, error) {
	if e.cfg.Kind == config.KindApp {
		raw, err := e.repo.Log([]string{rangeOf(anchors[0])}, nil)
		if err != nil {
			return nil, err
		}
		return [][]commit.Commit{parseAll(raw)}, nil
	}

	union, err := e.unionRange(anchors)
	if err != nil {
		return nil, err
	}
	raw, err := e.repo.Log(union, nil)
	if err != nil {
		return nil, err
	}

	out := make([][]commit.Commit, len(specs))
	for i, s := range specs {
		members, err := e.repo.RevList([]string{rangeOf(anchors[i])})
		if err != nil {
			return nil, err
		}
		inRange := make(map[string]bool, len(members))
		for _, h := range members {
			inRange[h] = true
		}
		for _, rc := range raw {
			if !inRange[rc.Hash] || !touchesPath(rc.Files, s.relDir) {
				continue
			}
			out[i] = append(out[i], commit.Parse(rc.Hash, rc.Subject, rc.Body))
		}
	}
	return out, nil
}

// unionRange is the range for the single batched log: every package's range
// must be a subset of it. One untagged package means the whole history; two or
// more anchors mean their merge-base, whose ancestor set is the intersection
// of the anchors', making mb..HEAD a superset of each tag..HEAD.
func (e *Engine) unionRange(anchors []string) ([]string, error) {
	untagged := 0
	var tagged []string
	for _, a := range anchors {
		if a == "" {
			untagged++
		} else {
			tagged = append(tagged, a)
		}
	}
	switch {
	case untagged > 0 || len(tagged) == 0:
		return []string{"HEAD"}, nil
	case len(tagged) == 1:
		return []string{tagged[0] + "..HEAD"}, nil
	}
	mb, err := e.repo.MergeBase(tagged...)
	if err != nil {
		return nil, fmt.Errorf("cannot find the common ancestor of %s: %w", strings.Join(tagged, ", "), err)
	}
	return []string{mb + "..HEAD"}, nil
}

func (e *Engine) buildPackagePlan(s spec, anchor string, base semver.Version, commits []commit.Commit, date string, monorepo bool) (*PackagePlan, error) {
	pkg := &PackagePlan{
		Name:      s.name,
		Manager:   s.mgr,
		Publish:   s.publish,
		Files:     s.files,
		Hooks:     s.hooks,
		Dir:       s.relDir,
		AbsDir:    s.absDir,
		TagPrefix: s.prefix,
		Tag:       anchor,
		Base:      base,
		Commits:   commits,
	}

	var bump semver.Bump
	for _, c := range commits {
		if c.Skipped() {
			pkg.Skipped++
		}
		bump = semver.Max(bump, c.Bump())
	}
	pkg.Bump = bump
	pkg.Next = base.Bump(bump)
	pkg.Due = bump != semver.None
	pkg.NextTag = s.prefix + pkg.Next.String()

	if monorepo {
		pkg.Header = fmt.Sprintf("## %s %s (%s)", s.name, pkg.Next, date)
	} else {
		pkg.Header = fmt.Sprintf("## %s (%s)", pkg.Next, date)
	}
	pkg.Entry = changelog.Build(pkg.Header, commits)

	if !pkg.Due {
		anchorName := "the beginning of history"
		if anchor != "" {
			anchorName = anchor
		}
		// An app plans against the repo root; "commits to ." reads badly, so
		// only a monorepo package names its directory.
		where := ""
		if s.relDir != "." {
			where = " to " + s.relDir
		}
		if len(commits) == 0 {
			pkg.Reason = fmt.Sprintf("no commits%s since %s", where, anchorName)
		} else {
			pkg.Reason = fmt.Sprintf("no releasable commits%s since %s (%d commit%s, %d skipped)",
				where, anchorName, len(commits), plural(len(commits)), pkg.Skipped)
		}
		return pkg, nil
	}

	// Plan-dependent ecosystem check, before anything is written: a Go module
	// must carry /vN in its path once the major is 2 or higher.
	if err := s.mgr.CheckVersion(s.absDir, pkg.Next); err != nil {
		return nil, fmt.Errorf("package %q: %w", s.name, err)
	}
	return pkg, nil
}

func (e *Engine) driftError(s spec, tag string, mv semver.Version) error {
	msg := fmt.Sprintf("package %q: manifest version %s does not match the latest reachable tag %s — "+
		"reconcile by hand, gala never guesses around drift", s.name, mv, tag)
	if e.cfg.SyncBranch != "" {
		msg += fmt.Sprintf(" (this branch is likely missing the release commit the syncBranch push delivers; "+
			"bring it up to date: git fetch %s && git merge %s/%s)",
			e.cfg.Remote, e.cfg.Remote, e.cfg.SyncBranch)
	} else if len(e.cfg.ReleaseBranches) > 0 {
		msg += fmt.Sprintf(" (set syncBranch, or plan on %s where the release commit lands)",
			e.cfg.ReleaseBranches[0])
	}
	return fmt.Errorf("%s", msg)
}

func rangeOf(anchor string) string {
	if anchor == "" {
		return "HEAD"
	}
	return anchor + "..HEAD"
}

func parseAll(raw []git.RawCommit) []commit.Commit {
	out := make([]commit.Commit, 0, len(raw))
	for _, rc := range raw {
		out = append(out, commit.Parse(rc.Hash, rc.Subject, rc.Body))
	}
	return out
}

// touchesPath reports whether any file lives under the package directory.
func touchesPath(files []string, dir string) bool {
	if dir == "." || dir == "" {
		return true
	}
	prefix := strings.TrimSuffix(dir, "/") + "/"
	for _, f := range files {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}
