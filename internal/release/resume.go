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
			if only[s.name] {
				return fmt.Errorf("--package %q has no release tag at HEAD — it was not released in this commit, "+
					"so there is nothing to resume for it", s.name)
			}
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
		return fmt.Errorf("no release tag points at HEAD (%s) — resume finishes a release whose tags are pushed, "+
			"so run it on the release commit (the `chore(release)` commit that carries the tags); when the branch has "+
			"moved past it, check that commit out first: git switch --detach <tag>", e.branch)
	}

	// A tag that exists only here would publish a release that was never
	// pushed, and a same-named tag on another object would publish this tree
	// under a version the remote tag does not describe. ls-remote reports the
	// tag object, so it is compared with the local tag object.
	for _, pkg := range due {
		remoteSHA, err := e.repo.LsRemote(e.cfg.Remote, "refs/tags/"+pkg.NextTag)
		if err != nil {
			return fmt.Errorf("cannot read tag %s from remote %q: %w", pkg.NextTag, e.cfg.Remote, err)
		}
		if remoteSHA == "" {
			return fmt.Errorf("tag %s is not on remote %q — resume only finishes a release whose tags are pushed; "+
				"push it first (git push %s %s) or run a normal release", pkg.NextTag, e.cfg.Remote, e.cfg.Remote, pkg.NextTag)
		}
		localSHA, err := e.repo.RefSHA("refs/tags/" + pkg.NextTag)
		if err != nil {
			return fmt.Errorf("cannot read the local tag %s: %w", pkg.NextTag, err)
		}
		if localSHA != remoteSHA {
			return fmt.Errorf("tag %s on remote %q is a different object than the local tag (%s there, %s here) — "+
				"fetch the remote tags or delete the stale local one before resuming",
				pkg.NextTag, e.cfg.Remote, shortSHA(remoteSHA), shortSHA(localSHA))
		}
	}

	// The tags are confirmed pushed from here on, so the workflow outputs say
	// so even if a later check or step fails.
	names := make([]string, len(due))
	for i, pkg := range due {
		names[i] = pkg.NextTag
	}
	e.note("resume  %s", strings.Join(names, ", "))
	if err := e.writeGitHubOutput(due); err != nil {
		e.opts.warnf("%v", err)
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

	if err := e.githubReleases(due, allowPublish); err != nil {
		return err
	}
	return e.publish(due, allowPublish)
}

// shortSHA abbreviates an object name for a message.
func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
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
