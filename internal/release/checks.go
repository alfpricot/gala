package release

import (
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// checks is pipeline step 1: everything that can fail read-only, before any
// plan-dependent work. `plan` skips it; `release` and `release --dry-run` run
// it.
func (e *Engine) checks() error {
	paths, err := e.repo.Status()
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		shown := paths
		if len(shown) > 10 {
			shown = shown[:10]
		}
		return fmt.Errorf("working tree is not clean — gala releases exactly what is checked in; "+
			"commit or stash these first:\n  %s", strings.Join(shown, "\n  "))
	}

	if e.branchErr != nil {
		return fmt.Errorf("HEAD is detached; release needs a branch (releaseBranches: %s) — "+
			"check one out first: git switch %s", e.cfg.ReleaseBranchesList(), e.cfg.ReleaseBranches[0])
	}
	if !e.cfg.OnReleaseBranch(e.branch) {
		return fmt.Errorf("branch %q cannot release: releaseBranches = [%s] "+
			"(plan works on any branch; release does not)", e.branch, e.cfg.ReleaseBranchesList())
	}
	return nil
}

// preflight is pipeline step 3: the live checks that must pass before the
// first local write, so a push can never be rejected after the fact. It runs
// for `release` and `release --dry-run`, never for `plan`. allowPublish is
// false under --no-publish; each package's own publish setting applies on top.
func (e *Engine) preflight(plan *Plan, allowPublish bool) error {
	// Manager binaries on PATH: the write steps shell out to them.
	needed := map[string]bool{}
	for _, pkg := range plan.Due() {
		for _, t := range pkg.Manager.Tools() {
			needed[t] = true
		}
	}
	// Hook commands and the lockfile refreshes for version files need their
	// executables too.
	hookTools, hookPaths := e.hookTools(plan.Due())
	for _, t := range hookTools {
		needed[t] = true
	}
	for _, tool := range slices.Sorted(maps.Keys(needed)) {
		if err := e.opts.LookPath(tool); err != nil {
			return fmt.Errorf("%s is required for this release but was not found on PATH: %w", tool, err)
		}
	}
	if err := checkHookPaths(hookPaths); err != nil {
		return err
	}
	if err := e.checkBatchArgs(plan.Due()); err != nil {
		return err
	}
	if err := e.checkIgnored(plan.Due()); err != nil {
		return err
	}

	// Ecosystem preflights: npm verifies the registry login when publishing
	// is on, and nuget the evaluated version, packability, and push source,
	// so each fails before the tag exists.
	for _, pkg := range plan.Due() {
		if err := pkg.Manager.Preflight(pkg.AbsDir, allowPublish && pkg.Publish); err != nil {
			return fmt.Errorf("package %q: %w", pkg.Name, err)
		}
	}

	// The remote must not have moved past local HEAD, otherwise the atomic
	// push would be rejected after all the local writes are done.
	if err := e.checkRemote(); err != nil {
		return err
	}

	// A missing token warns; it never fails.
	if e.cfg.GitHubRelease && e.opts.Token == "" {
		e.opts.warnf("no GITHUB_TOKEN or GH_TOKEN in the environment — the GitHub Release step will be skipped")
	}

	// Last, the configured preRelease checks: the slowest, so they run only
	// once everything cheaper has passed.
	return e.preRelease(plan.Due())
}

// checkIgnored refuses a release whose changelog, manifest, or version file is
// gitignored: staging takes what git status reports, and git status never
// reports an ignored file, so the release commit would silently leave it out.
// Lockfiles are not checked; an ignored lockfile (a library's Cargo.lock) is
// meant to stay out of the commit.
func (e *Engine) checkIgnored(due []*PackagePlan) error {
	paths := []string{filepath.ToSlash(e.cfg.Changelog)}
	for _, pkg := range due {
		if m := pkg.Manager.Manifest(pkg.AbsDir, e.root); m != "" {
			paths = append(paths, m)
		}
		for _, vf := range pkg.Files {
			paths = append(paths, vf.Path)
		}
	}
	ignored, err := e.repo.Ignored(paths...)
	if err != nil {
		return fmt.Errorf("cannot check .gitignore rules: %w", err)
	}
	if len(ignored) == 0 {
		return nil
	}
	lines := make([]string, len(ignored))
	for i, ig := range ignored {
		lines[i] = fmt.Sprintf("%s (ignored by %s)", ig.Path, ig.Rule)
	}
	return fmt.Errorf("the release commit would leave out files gala writes, because git ignores them — "+
		"remove the matching rule or re-include each one (for example !/%s):\n  %s",
		ignored[0].Path, strings.Join(lines, "\n  "))
}

func (e *Engine) checkRemote() error {
	ref := "refs/heads/" + e.branch
	remoteSHA, err := e.repo.LsRemote(e.cfg.Remote, ref)
	if err != nil {
		return fmt.Errorf("cannot read %s from remote %q — the push would fail after local writes: %w",
			ref, e.cfg.Remote, err)
	}
	if remoteSHA == "" {
		return nil // the branch does not exist remotely yet: nothing can reject the push
	}
	localSHA, err := e.repo.HeadSHA()
	if err != nil {
		return err
	}
	if remoteSHA == localSHA {
		return nil
	}
	moved := func() error {
		return fmt.Errorf("remote %q has moved past local HEAD: %s is at %s, this checkout is at %s — "+
			"bring the release branch up to date first: git fetch %s && git merge %s/%s",
			e.cfg.Remote, ref, remoteSHA[:12], localSHA[:12], e.cfg.Remote, e.cfg.Remote, e.branch)
	}
	if !e.repo.HasObject(remoteSHA) {
		return moved()
	}
	ancestor, err := e.repo.IsAncestor(remoteSHA, localSHA)
	if err != nil {
		return err
	}
	if !ancestor {
		return moved()
	}
	// The remote is behind local: the push fast-forwards, nothing can reject it.
	return nil
}

// note prints a progress line for the release transcript.
func (e *Engine) note(format string, args ...any) {
	_, _ = io.WriteString(e.opts.out(), fmt.Sprintf("  "+format+"\n", args...))
}
