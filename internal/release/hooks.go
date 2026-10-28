package release

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alfpricot/gala/internal/config"
	"github.com/alfpricot/gala/internal/manager"
	"github.com/alfpricot/gala/internal/semver"
)

// checkVersionFiles is the plan-time agreement check: every version file of a
// package must already record the version its manifest (or, for go, its tag)
// does, or the release would tag one version and publish metadata saying
// another.
func (e *Engine) checkVersionFiles(s spec, current semver.Version) error {
	manifest := s.mgr.Manifest(s.absDir, e.root)
	for _, vf := range s.files {
		if vf.Path == manifest {
			return fmt.Errorf("%s: versionFile %q is the %s manifest, which gala already writes — remove it",
				e.cfg.Source, vf.Path, s.mgr.Name())
		}
		v, err := manager.ReadVersionFile(e.root, vf)
		if err != nil {
			return fmt.Errorf("package %q: versionFile %w", s.name, err)
		}
		if semver.Compare(v, current) != 0 {
			source := "the latest tag"
			if manifest != "" {
				source = manifest
			}
			return fmt.Errorf("package %q: versionFile %s records %s (%s), but %s is at %s — "+
				"set it to %s by hand before releasing, so the release does not publish mismatched metadata",
				s.name, vf.Path, v, manager.Address(vf), source, current, current)
		}
	}
	return nil
}

// scope is one place hook commands run: the repository root (the top-level
// hooks) or a released package's directory (its own hooks).
type scope struct {
	dir   string
	hooks config.Hooks
	env   []string
}

// scopes lists the hook scopes for a release, packages first in declaration
// order and the repository last, so a top-level command sees every package's
// output.
func (e *Engine) scopes(due []*PackagePlan) []scope {
	var out []scope
	for _, pkg := range due {
		if len(pkg.Hooks.PreRelease)+len(pkg.Hooks.PostBump) > 0 {
			out = append(out, scope{dir: pkg.AbsDir, hooks: pkg.Hooks, env: hookEnv(due, pkg)})
		}
	}
	var single *PackagePlan
	if len(due) == 1 {
		single = due[0]
	}
	return append(out, scope{dir: e.root, hooks: e.cfg.Hooks, env: hookEnv(due, single)})
}

// hookEnv is what a hook command learns about the release: GALA_TAGS always,
// and the package's name, versions, and tag when the scope is one package.
func hookEnv(due []*PackagePlan, pkg *PackagePlan) []string {
	tags := make([]string, 0, len(due))
	for _, p := range due {
		tags = append(tags, p.NextTag)
	}
	env := []string{"GALA_TAGS=" + strings.Join(tags, " ")}
	if pkg != nil {
		env = append(env,
			"GALA_PACKAGE="+pkg.Name,
			"GALA_VERSION="+pkg.Next.String(),
			"GALA_PREVIOUS_VERSION="+pkg.Base.String(),
			"GALA_TAG="+pkg.NextTag,
		)
	}
	return env
}

// allowedPostBump reports whether a changed path is one a postBump command of
// any scope may change.
func allowedPostBump(scopes []scope, p string) bool {
	for _, s := range scopes {
		if s.hooks.MatchesPostBumpPath(p) {
			return true
		}
	}
	return false
}

// hookTools lists the executables a release needs beyond the managers: each
// hook command's program and each version-file lockfile tool. A command named
// by path (./tools/gen.sh) is returned separately, resolved against the
// directory it runs from.
func (e *Engine) hookTools(due []*PackagePlan) (onPath []string, byPath []string) {
	for _, s := range e.scopes(due) {
		for _, cmd := range append(append([]config.Command(nil), s.hooks.PreRelease...), s.hooks.PostBump...) {
			name := cmd.Argv[0]
			if strings.ContainsAny(name, `/\`) {
				if !filepath.IsAbs(name) {
					name = filepath.Join(s.dir, filepath.FromSlash(name))
				}
				byPath = append(byPath, name)
			} else {
				onPath = append(onPath, name)
			}
		}
	}
	for _, c := range manager.VersionFileLocks(e.root, dueVersionFiles(due)) {
		onPath = append(onPath, c.Name)
	}
	return onPath, byPath
}

func checkHookPaths(byPath []string) error {
	for _, p := range byPath {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("hook command %s was not found: %w", p, err)
		}
	}
	return nil
}

// dueVersionFiles collects every due package's extra version files.
func dueVersionFiles(due []*PackagePlan) []config.VersionFile {
	var out []config.VersionFile
	for _, pkg := range due {
		out = append(out, pkg.Files...)
	}
	return out
}

// runCommands runs one scope's commands in order and stops at the first
// failure. Their output is echoed into the transcript.
func (e *Engine) runCommands(key string, s scope, cmds []config.Command) error {
	for _, cmd := range cmds {
		where := e.where(s)
		e.note("%-7s %s%s", hookLabel(key), cmd.Line, where)
		var out string
		var err error
		if r, ok := e.opts.Exec.(manager.EnvRunner); ok {
			out, err = r.RunEnv(s.dir, s.env, cmd.Argv[0], cmd.Argv[1:]...)
		} else {
			out, err = e.opts.Exec.Run(s.dir, cmd.Argv[0], cmd.Argv[1:]...)
		}
		if err != nil {
			return fmt.Errorf("%s command %q%s failed: %w", key, cmd.Line, where, err)
		}
		for _, line := range strings.Split(strings.TrimRight(out, "\r\n"), "\n") {
			if strings.TrimSpace(line) != "" {
				e.note("        %s", strings.TrimRight(line, "\r"))
			}
		}
	}
	return nil
}

// preRelease runs every scope's preRelease commands after the preflights and
// before any write; they are checks, so they must leave the working tree
// clean.
func (e *Engine) preRelease(due []*PackagePlan) error {
	ran := false
	for _, s := range e.scopes(due) {
		if len(s.hooks.PreRelease) == 0 {
			continue
		}
		ran = true
		if err := e.runCommands("preRelease", s, s.hooks.PreRelease); err != nil {
			return fmt.Errorf("%w\n  nothing was written, committed, tagged, or pushed", err)
		}
	}
	if !ran {
		return nil
	}
	changed, err := e.repo.Status()
	if err != nil {
		return err
	}
	if len(changed) > 0 {
		return fmt.Errorf("preRelease commands changed the working tree; they run as checks before the version "+
			"writes and must not modify files (ignore build output in .gitignore):\n  %s", strings.Join(changed, "\n  "))
	}
	return nil
}

// postBump runs every scope's postBump commands after the version writes.
func (e *Engine) postBump(scopes []scope) error {
	for _, s := range scopes {
		if err := e.runCommands("postBump", s, s.hooks.PostBump); err != nil {
			return fmt.Errorf("%w\n  nothing was committed, tagged, or pushed; the working tree holds the version "+
				"writes — review them with git status and discard them with git restore before trying again", err)
		}
	}
	return nil
}

// where names a package scope's directory for messages; the root is implied.
func (e *Engine) where(s scope) string {
	if s.dir == e.root {
		return ""
	}
	rel, err := filepath.Rel(e.root, s.dir)
	if err != nil {
		return ""
	}
	return " (in " + filepath.ToSlash(rel) + ")"
}

func hookLabel(key string) string {
	if key == "preRelease" {
		return "check"
	}
	return "run"
}

// writeGitHubOutput appends step outputs for a workflow: released, and when a
// release happened, tag (the single tag of a one-package release), tags (one
// per line), and version (an app's new version).
func (e *Engine) writeGitHubOutput(due []*PackagePlan) error {
	if e.opts.GitHubOutput == "" {
		return nil
	}
	var b strings.Builder
	if len(due) == 0 {
		b.WriteString("released=false\n")
	} else {
		b.WriteString("released=true\n")
		if len(due) == 1 {
			fmt.Fprintf(&b, "tag=%s\n", due[0].NextTag)
		}
		if e.cfg.Kind == config.KindApp {
			fmt.Fprintf(&b, "version=%s\n", due[0].Next)
		}
		b.WriteString("tags<<GALA_TAGS\n")
		for _, pkg := range due {
			b.WriteString(pkg.NextTag + "\n")
		}
		b.WriteString("GALA_TAGS\n")
	}
	f, err := os.OpenFile(e.opts.GitHubOutput, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open GITHUB_OUTPUT: %w", err)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return fmt.Errorf("cannot write GITHUB_OUTPUT: %w", err)
	}
	return f.Close()
}
