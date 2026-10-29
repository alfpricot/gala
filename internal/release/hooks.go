package release

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/alfpricot/gala/internal/config"
	"github.com/alfpricot/gala/internal/manager"
	"github.com/alfpricot/gala/internal/semver"
)

// checkVersionFiles is the plan-time agreement check: every version file of a
// package must already record the version its manifest (or, for go, its tag)
// does, or the release would tag one version and publish metadata saying
// another. manifests maps every package's manifest path to its package name.
func (e *Engine) checkVersionFiles(s spec, current semver.Version, manifests map[string]string) error {
	manifest := s.mgr.Manifest(s.absDir, e.root)
	for _, vf := range s.files {
		if vf.Path == manifest {
			return fmt.Errorf("%s: versionFile %q is the %s manifest, which gala already writes — remove it",
				e.cfg.Source, vf.Path, s.mgr.Name())
		}
		if owner, ok := manifests[vf.Path]; ok {
			return fmt.Errorf("%s: package %q lists versionFile %q, which is the manifest of package %q — "+
				"a file can carry only one version, so remove it from one of them",
				e.cfg.Source, s.name, vf.Path, owner)
		}
		v, err := manager.ReadVersionFile(e.root, vf)
		if err != nil {
			return fmt.Errorf("package %q: versionFile %w", s.name, err)
		}
		if semver.Compare(v, current) != 0 {
			source := "the package's current version"
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

// checkHookPaths resolves each path-named hook the way os/exec will at run
// time: on Windows that tries the PATHEXT extensions (./scripts/build finds
// build.cmd), and elsewhere it requires the executable bit.
func checkHookPaths(byPath []string) error {
	for _, p := range byPath {
		resolved, err := exec.LookPath(p)
		if err != nil {
			return fmt.Errorf("hook command %s was not found or is not executable: %w", p, err)
		}
		if err := startableOnWindows(runtime.GOOS, resolved, os.Getenv("PATHEXT")); err != nil {
			return err
		}
	}
	return nil
}

// startableOnWindows refuses a path-named hook whose extension Windows cannot
// start directly. LookPath accepts any existing file that has an extension,
// but CreateProcess runs only executables and batch files, so a ./gen.ps1 or
// ./gen.sh would pass the preflight and then fail after the version writes.
func startableOnWindows(goos, program, pathext string) error {
	if goos != "windows" {
		return nil
	}
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	ext := strings.ToLower(filepath.Ext(program))
	for _, e := range strings.Split(strings.ToLower(pathext), ";") {
		if e != "" && e == ext {
			return nil
		}
	}
	return fmt.Errorf("hook command %s cannot be started directly on Windows (%s is not in PATHEXT) — "+
		"name its interpreter as the program instead, such as `pwsh -File %s`",
		program, ext, filepath.ToSlash(program))
}

// checkBatchArgs refuses hook arguments that cmd.exe would interpret. On
// Windows a .cmd or .bat program (npm, npx, and pnpm are npm.cmd and the
// like) runs through cmd.exe, which expands %VAR% even inside quotes and
// treats & | < > ^ as operators, so the "nothing is expanded" promise only
// holds if such arguments never reach it.
func (e *Engine) checkBatchArgs(due []*PackagePlan) error {
	// Elsewhere a .cmd or .bat file is an ordinary executable that receives
	// its arguments unchanged.
	if runtime.GOOS != "windows" {
		return nil
	}
	for _, s := range e.scopes(due) {
		for _, cmd := range slices.Concat(s.hooks.PreRelease, s.hooks.PostBump) {
			program := cmd.Argv[0]
			if strings.ContainsAny(program, `/\`) && !filepath.IsAbs(program) {
				program = filepath.Join(s.dir, filepath.FromSlash(program))
			}
			resolved, err := exec.LookPath(program)
			if err != nil {
				continue // reported by the tool and path checks
			}
			if err := batchArgError(resolved, cmd.Argv[1:]); err != nil {
				return fmt.Errorf("hook command %q: %w", cmd.Line, err)
			}
		}
	}
	return nil
}

// batchUnsafe are the characters cmd.exe interprets in a batch file's
// arguments: % expands variables even inside quotes, a " breaks the quoting
// os/exec adds, and the rest are operators outside quotes.
const batchUnsafe = "%\"&|<>^\r\n"

// batchArgError reports an argument cmd.exe would interpret when program is a
// batch file; any other program receives its arguments unchanged.
func batchArgError(program string, args []string) error {
	switch strings.ToLower(filepath.Ext(program)) {
	case ".cmd", ".bat":
	default:
		return nil
	}
	for _, a := range args {
		if strings.ContainsAny(a, batchUnsafe) {
			return fmt.Errorf("argument %q would be interpreted by cmd.exe, because %s is a batch file — "+
				"move that argument into a package script (`npm run …`) or a checked-in script, or name an "+
				"interpreter that is an .exe as the program, such as `pwsh -File script.ps1`",
				a, filepath.Base(program))
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

// writeGitHubOutput appends the release's step outputs once the tags are
// pushed. It marks the outputs written first, so Run never follows a failed
// write with released=false for a release whose tags exist.
func (e *Engine) writeGitHubOutput(due []*PackagePlan) error {
	e.outputWritten = true
	return appendGitHubOutput(e.opts.GitHubOutput, e.cfg.Kind == config.KindApp, due)
}

// appendGitHubOutput appends step outputs for a workflow: released, and when
// a release happened, tag (the single tag of a one-package release), tags
// (one per line), and version (an app's new version). An empty file writes
// nothing.
func appendGitHubOutput(file string, app bool, due []*PackagePlan) error {
	if file == "" {
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
		if app {
			fmt.Fprintf(&b, "version=%s\n", due[0].Next)
		}
		b.WriteString("tags<<GALA_TAGS\n")
		for _, pkg := range due {
			b.WriteString(pkg.NextTag + "\n")
		}
		b.WriteString("GALA_TAGS\n")
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open GITHUB_OUTPUT: %w", err)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return fmt.Errorf("cannot write GITHUB_OUTPUT: %w", err)
	}
	return f.Close()
}
