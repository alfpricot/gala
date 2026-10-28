package release

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alfpricot/gala/internal/changelog"
	"github.com/alfpricot/gala/internal/config"
	"github.com/alfpricot/gala/internal/github"
	"github.com/alfpricot/gala/internal/manager"
)

// execute is pipeline steps 4 to 11. Everything that could fail has already
// been checked; what remains is ordered so that all of git completes before
// any registry publish.
func (e *Engine) execute(plan *Plan, publish bool) error {
	due := plan.Due()

	// Step 4: write manifests, refresh lockfiles, prepend the changelog.
	e.note("write   %s", describeWrites(e, due))

	var manifests []string
	var lockfiles []string
	for _, pkg := range due {
		if err := pkg.Manager.SetVersion(pkg.AbsDir, pkg.Next); err != nil {
			return fmt.Errorf("package %q: cannot write the manifest: %w", pkg.Name, err)
		}
	}
	// Extra version files are written before the lockfile refresh, so a
	// Cargo.toml or pyproject.toml among them lands in its lockfile too.
	var versionFiles []string
	for _, pkg := range due {
		for _, vf := range pkg.Files {
			if err := manager.WriteVersionFile(e.root, vf, pkg.Next); err != nil {
				return fmt.Errorf("package %q: cannot write versionFile %w", pkg.Name, err)
			}
			versionFiles = append(versionFiles, vf.Path)
		}
	}
	// refreshed records lockfiles a manager's tool has already rewritten after
	// those writes, so a version file sharing the lockfile does not run the
	// tool again. npm is left out: for a single package it reports the lock
	// that `npm version` kept in step without running anything afterwards.
	// Only the nearest lock counts (cargo and uv report the package's own lock
	// before the root's): the tool runs in the package directory and rewrites
	// that one, so a root lock of a separate project is still refreshed below.
	refreshed := map[string]bool{}
	for _, pkg := range due {
		locks, err := pkg.Manager.RefreshLock(pkg.AbsDir, e.root)
		if err != nil {
			return fmt.Errorf("package %q: %w", pkg.Name, err)
		}
		lockfiles = append(lockfiles, locks...)
		if pkg.Manager.Name() != string(config.ManagerNpm) && len(locks) > 0 {
			refreshed[locks[0]] = true
		}
		if m := pkg.Manager.Manifest(pkg.AbsDir, e.root); m != "" {
			manifests = append(manifests, m)
		}
	}
	fileLocks, err := manager.RunLockCommands(e.opts.Exec, e.root,
		manager.VersionFileLocks(e.root, dueVersionFiles(due)), refreshed)
	if err != nil {
		return err
	}
	lockfiles = append(lockfiles, fileLocks...)

	entries := make([]string, 0, len(due))
	for _, pkg := range due {
		entries = append(entries, pkg.Entry)
	}
	changelogPath := filepath.Join(e.root, filepath.FromSlash(e.cfg.Changelog))
	if err := changelog.Prepend(changelogPath, strings.Join(entries, "\n\n")); err != nil {
		return fmt.Errorf("cannot write the changelog: %w", err)
	}

	// The postBump commands regenerate whatever embeds the version, before
	// staging so their output joins the release commit. Nothing is committed
	// or pushed yet, so a failure leaves only local writes behind.
	scopes := e.scopes(due)
	if err := e.postBump(scopes); err != nil {
		return err
	}

	// Step 5: stage exactly what changed — anything else dirty is an error,
	// not a silent extra stage. Paths matching postBumpPaths are the one
	// declared exception.
	expected := map[string]bool{filepath.ToSlash(e.cfg.Changelog): true}
	for _, m := range manifests {
		expected[m] = true
	}
	for _, v := range versionFiles {
		expected[v] = true
	}
	for _, l := range lockfiles {
		expected[l] = true
	}
	changed, err := e.repo.Status()
	if err != nil {
		return err
	}
	var stage, extras []string
	for _, c := range changed {
		if expected[c] || allowedPostBump(scopes, c) {
			stage = append(stage, c)
		} else {
			extras = append(extras, c)
		}
	}
	if len(extras) > 0 {
		return fmt.Errorf("unexpected changes after the version writes; gala stages only manifests, "+
			"version files, lockfiles, the changelog, and postBumpPaths:\n  %s", strings.Join(extras, "\n  "))
	}
	if err := e.repo.Add(stage...); err != nil {
		return err
	}
	e.note("staged  %s", strings.Join(stage, ", "))

	// Step 6: the release commit, carrying the skip-ci marker so the push does
	// not wake the release workflow again.
	subject, body := releaseCommit(e.cfg.Kind == config.KindMonorepo, due)
	if err := e.repo.Commit(subject, body); err != nil {
		return fmt.Errorf("cannot create the release commit: %w\n"+
			"  git needs an author identity for the release commit — set user.name and user.email "+
			"(in CI: git config user.name ... && git config user.email ... before the release step)", err)
	}
	e.note("commit  %s", subject)

	// Step 7: annotated tags, one per released package, message = the entry's
	// header lines.
	for _, pkg := range due {
		if err := e.repo.TagAnnotated(pkg.NextTag, pkg.Header); err != nil {
			return fmt.Errorf("cannot tag %s: %w", pkg.NextTag, err)
		}
		e.note("tag     %s", pkg.NextTag)
	}

	// Step 8: one atomic push — branch and tags land together or not at all.
	refs := make([]string, 0, len(due)+1)
	refs = append(refs, e.branch)
	for _, pkg := range due {
		refs = append(refs, pkg.NextTag)
	}
	if err := e.repo.PushAtomic(e.cfg.Remote, refs...); err != nil {
		return fmt.Errorf("atomic push failed; nothing was pushed and no release was published: %w", err)
	}
	e.note("push    %s → %s (%d tag%s, atomic)", e.branch, e.cfg.Remote, len(due), plural(len(due)))

	// The tags exist remotely from here on, so the workflow outputs say so
	// even if a later step fails.
	if err := e.writeGitHubOutput(due); err != nil {
		e.opts.warnf("%v", err)
	}

	// Step 9: fast-forward the integration branch. A rejection warns with the
	// manual merge command; it never fails a release and is never forced.
	if e.cfg.SyncBranch != "" {
		e.note("sync    %s → %s (fast-forward only)", e.branch, e.cfg.SyncBranch)
		refspec := e.branch + ":" + e.cfg.SyncBranch
		if err := e.repo.Push(e.cfg.Remote, refspec); err != nil {
			e.opts.warnf("could not fast-forward %q to %q: %v\n"+
				"  %q has commits this release does not; merge by hand:\n"+
				"    git fetch %s && git switch %s && git merge %s && git push %s %s",
				e.cfg.SyncBranch, e.branch, err, e.cfg.SyncBranch,
				e.cfg.Remote, e.cfg.SyncBranch, e.branch, e.cfg.Remote, e.cfg.SyncBranch)
		}
	}

	// Step 10: GitHub Releases, only now that the tags exist remotely.
	if err := e.githubReleases(due); err != nil {
		return err
	}

	// Step 11: publishes, in declaration order, last of all.
	return e.publish(due, publish)
}

// githubReleases creates one release per released tag, or warns and skips:
// an absent token or a non-GitHub remote never fails a run whose tags are
// already pushed.
func (e *Engine) githubReleases(due []*PackagePlan) error {
	if !e.cfg.GitHubRelease {
		return nil
	}
	remoteURL, err := e.repo.RemoteURL(e.cfg.Remote)
	if err != nil {
		e.opts.warnf("cannot read the URL of remote %q — skipping the GitHub Release: %v", e.cfg.Remote, err)
		return nil
	}
	remote, err := github.ParseRemoteURL(remoteURL)
	if err != nil {
		e.opts.warnf("cannot parse remote URL %q — skipping the GitHub Release: %v", remoteURL, err)
		return nil
	}
	if !remote.IsGitHub() {
		e.opts.warnf("remote %q is not github.com — skipping the GitHub Release", e.cfg.Remote)
		return nil
	}
	if e.opts.Token == "" {
		e.opts.warnf("no GITHUB_TOKEN or GH_TOKEN in the environment — skipping the GitHub Release")
		return nil
	}

	client := &github.Client{HTTP: e.opts.HTTP, Base: e.opts.APIBase, Token: e.opts.Token}
	for _, pkg := range due {
		title := pkg.NextTag
		if e.cfg.Kind == config.KindMonorepo {
			title = pkg.Name + " " + pkg.Next.String()
		}
		if err := client.CreateRelease(remote.Owner, remote.Repo, pkg.NextTag, title, pkg.Entry); err != nil {
			return fmt.Errorf("%w\n  the tags are pushed but the release was not published and no registry "+
				"publish ran; create it by hand or fix the API access and re-run the remaining steps", err)
		}
		e.note("release %s on %s", pkg.NextTag, remote)
	}
	return nil
}

// publish pushes to the registry in declaration order, each package through
// its own manager. A failure reports the tool output and the manual recovery
// command, then exits non-zero.
func (e *Engine) publish(due []*PackagePlan, allowPublish bool) error {
	publishing := publishing(due, allowPublish)
	if len(publishing) == 0 {
		e.note("publish skipped (publish = false)")
		return nil
	}
	for _, pkg := range publishing {
		e.note("publish %s (%s) ...", pkg.Name, pkg.Manager.Name())
		if err := pkg.Manager.Publish(pkg.AbsDir); err != nil {
			return fmt.Errorf("publish failed for package %q: %w\n"+
				"  the tag %s is pushed and the GitHub Release exists; gala has no resume command — "+
				"fix the registry problem, then run:\n    %s",
				pkg.Name, err, pkg.NextTag, pkg.Manager.Recovery(pkg.AbsDir, e.root))
		}
		e.note("        ok")
	}
	return nil
}

// publishing returns the due packages that publish in this run.
func publishing(due []*PackagePlan, allowPublish bool) []*PackagePlan {
	if !allowPublish {
		return nil
	}
	var out []*PackagePlan
	for _, pkg := range due {
		if pkg.Publish {
			out = append(out, pkg)
		}
	}
	return out
}

// releaseCommit builds the commit subject and body for step 6.
func releaseCommit(monorepo bool, due []*PackagePlan) (string, string) {
	var b strings.Builder
	b.WriteString("[skip ci]")
	if monorepo {
		for _, pkg := range due {
			b.WriteString("\n- ")
			b.WriteString(pkg.Name)
			b.WriteString(" ")
			b.WriteString(pkg.Next.String())
		}
		if len(due) == 1 {
			return "chore(release): 1 package", b.String()
		}
		return fmt.Sprintf("chore(release): %d packages", len(due)), b.String()
	}
	return fmt.Sprintf("chore(release): %s", due[0].NextTag), b.String()
}

// describeWrites lists the files step 4 will touch, before it touches them.
func describeWrites(e *Engine, due []*PackagePlan) string {
	var files []string
	for _, pkg := range due {
		if m := pkg.Manager.Manifest(pkg.AbsDir, e.root); m != "" {
			files = append(files, fmt.Sprintf("%s → %s", m, pkg.Next))
		}
		for _, vf := range pkg.Files {
			files = append(files, fmt.Sprintf("%s → %s", vf.Path, pkg.Next))
		}
	}
	files = append(files, e.cfg.Changelog)
	return strings.Join(files, ", ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
