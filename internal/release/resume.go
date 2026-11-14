package release

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/alfpricot/gala/internal/changelog"
	"github.com/alfpricot/gala/internal/config"
)

// resume finishes a release whose tags are already pushed: the steps after the
// push. It works from the tags that point at HEAD, so it is run on the release
// commit, and it repeats only what is safe to repeat. The workflow outputs are
// written for the tags, each missing GitHub Release is created (a tag that
// already has one counts as done), and the packages that publish are
// published. The branch sync is not repeated.
//
// A plain release plans nothing once its tags exist, which is why a failure
// after the push could not be re-run before; resume reads the release back from
// the tags and the changelog entry the release commit holds.
func (e *Engine) resume(opts *Options) error {
	specs, err := e.specs()
	if err != nil {
		return err
	}
	only, err := e.resumeSelection(specs, opts.Packages)
	if err != nil {
		return err
	}
	atHead, err := e.repo.TagsAt()
	if err != nil {
		return err
	}

	var due []*PackagePlan
	for _, s := range specs {
		if len(only) > 0 && !only[s.name] {
			continue
		}
		tag, v, err := selectTag(atHead, s.prefix)
		if err != nil {
			return err
		}
		if tag == "" {
			continue
		}
		heading := v.String()
		if e.cfg.Kind == config.KindMonorepo {
			heading = s.name + " " + v.String()
		}
		entry, err := changelog.Entry(filepath.Join(e.root, filepath.FromSlash(e.cfg.Changelog)), heading)
		if err != nil {
			return fmt.Errorf("package %q: %w", s.name, err)
		}
		due = append(due, &PackagePlan{
			Name: s.name, Manager: s.mgr, Publish: s.publish, Dir: s.relDir, AbsDir: s.absDir,
			TagPrefix: s.prefix, Next: v, NextTag: tag, Entry: entry, Due: true,
		})
	}
	if len(due) == 0 {
		return fmt.Errorf("no release tag points at HEAD on branch %s — resume finishes a release whose tags are pushed, "+
			"so run it on the release commit (the one `chore(release)` commit that carries the tags)", e.branch)
	}

	// A tag that exists only here would publish a release that was never
	// pushed.
	for _, pkg := range due {
		remoteSHA, err := e.repo.LsRemote(e.cfg.Remote, "refs/tags/"+pkg.NextTag)
		if err != nil {
			return fmt.Errorf("cannot read tag %s from remote %q: %w", pkg.NextTag, e.cfg.Remote, err)
		}
		if remoteSHA == "" {
			return fmt.Errorf("tag %s is not on remote %q — resume only finishes a release whose tags are pushed; "+
				"push it first (git push %s %s) or run a normal release", pkg.NextTag, e.cfg.Remote, e.cfg.Remote, pkg.NextTag)
		}
	}

	allowPublish := !opts.NoPublish
	for _, pkg := range publishing(due, allowPublish) {
		for _, tool := range pkg.Manager.Tools() {
			if err := e.opts.LookPath(tool); err != nil {
				return fmt.Errorf("%s is required to publish %q but was not found on PATH: %w", tool, pkg.Name, err)
			}
		}
		if err := pkg.Manager.Preflight(pkg.AbsDir, true); err != nil {
			return fmt.Errorf("package %q: %w", pkg.Name, err)
		}
	}

	names := make([]string, len(due))
	for i, pkg := range due {
		names[i] = pkg.NextTag
	}
	e.note("resume  %s", strings.Join(names, ", "))
	if err := e.writeGitHubOutput(due); err != nil {
		e.opts.warnf("%v", err)
	}
	if err := e.githubReleases(due, allowPublish); err != nil {
		return err
	}
	return e.publish(due, allowPublish)
}

// resumeSelection validates the --package names against the configured
// packages and returns them as a set; no names selects everything.
func (e *Engine) resumeSelection(specs []spec, names []string) (map[string]bool, error) {
	if len(names) == 0 {
		return nil, nil
	}
	known := make([]string, len(specs))
	for i, s := range specs {
		known[i] = s.name
	}
	only := map[string]bool{}
	for _, n := range names {
		if !slices.Contains(known, n) {
			return nil, fmt.Errorf("--package %q is not a configured package (packages: %s)", n, strings.Join(known, ", "))
		}
		only[n] = true
	}
	return only, nil
}
