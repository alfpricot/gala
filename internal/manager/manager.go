// Package manager adapts each package ecosystem: read the manifest version,
// write it back, refresh the lockfile through its owning tool, preflight, and
// publish.
//
// Two rules every adapter follows:
//
//   - Never marshal TOML. Version writes are section-and-key targeted line
//     edits that preserve comments, ordering, and every other byte.
//   - Fail before the tag, not after. Dynamic or inherited versions are
//     plan-time errors, before any write.
package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alfpricot/gala/internal/config"
	"github.com/alfpricot/gala/internal/semver"
)

// Runner runs a package-manager command in dir. Tests substitute a capture
// runner so argv is asserted without executing anything.
type Runner interface {
	Run(dir, name string, args ...string) (stdout string, err error)
}

// EnvRunner is a Runner that can also add environment variables, which hook
// commands receive (GALA_VERSION and friends). A Runner without it runs hooks
// with the inherited environment only.
type EnvRunner interface {
	RunEnv(dir string, env []string, name string, args ...string) (stdout string, err error)
}

// HookSpec describes one hook command run.
type HookSpec struct {
	Dir  string
	Env  []string // added to the inherited environment, after the filtering HookEnviron applies
	Name string
	Args []string
	// Timeout ends the command when it runs longer; zero means no limit.
	Timeout time.Duration
	// Out, when set, receives the command's output as it is produced, so a
	// slow command shows progress and the error does not repeat the output.
	Out io.Writer
}

// HookRunner runs hook commands with streamed output and a timeout. A Runner
// without it runs hooks through EnvRunner or Run, which print their output
// only when the command ends and have no time limit.
type HookRunner interface {
	RunHook(h HookSpec) (stdout string, err error)
}

// ExecRunner is the production Runner: the real binary.
type ExecRunner struct{}

// Run executes name in dir.
func (ExecRunner) Run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s failed: %w: %s",
			name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// RunEnv executes name in dir as a hook command: with the environment
// HookEnviron builds, and the whole output returned.
func (r ExecRunner) RunEnv(dir string, env []string, name string, args ...string) (string, error) {
	return r.RunHook(HookSpec{Dir: dir, Env: env, Name: name, Args: args})
}

// RunHook executes a hook command. Its environment is the inherited one
// without GALA_* variables (so a stale GALA_VERSION from the parent never
// reaches a command that gala sets no version for) and without the GitHub
// tokens (a hook runs third-party code that has no need of them), plus
// h.Env. With h.Out set, output streams there as it is produced and the
// returned error leaves it out; otherwise the whole output is returned and
// quoted in the error.
func (ExecRunner) RunHook(h HookSpec) (string, error) {
	ctx := context.Background()
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, h.Name, h.Args...)
	cmd.Dir = h.Dir
	cmd.Env = HookEnviron(os.Environ(), h.Env)
	// A hook's own children can hold its output pipes open after it is
	// killed; give up on them shortly instead of waiting for them.
	cmd.WaitDelay = 5 * time.Second

	var captured strings.Builder
	var w io.Writer = &captured
	if h.Out != nil {
		w = h.Out
	}
	// One writer for both streams: exec serializes writes to a comparable
	// writer shared by Stdout and Stderr.
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	out := captured.String()
	if err == nil {
		return out, nil
	}
	detail := ""
	if h.Out == nil {
		detail = ": " + strings.TrimSpace(out)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("%s %s timed out after %s%s", h.Name, strings.Join(h.Args, " "), h.Timeout, detail)
	}
	return out, fmt.Errorf("%s %s failed: %w%s", h.Name, strings.Join(h.Args, " "), err, detail)
}

// HookEnviron builds a hook command's environment from the inherited one:
// every GALA_* variable and the GitHub tokens are removed, then add is
// appended. Names compare without regard to case, as Windows treats them.
func HookEnviron(inherited, add []string) []string {
	out := make([]string, 0, len(inherited)+len(add))
	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		if strings.HasPrefix(upper, "GALA_") || upper == "GITHUB_TOKEN" || upper == "GH_TOKEN" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, add...)
}

// Manager is one ecosystem's adapter.
type Manager interface {
	// Name is the manager id from gala.toml.
	Name() string

	// Tools are the executables that must be on PATH before anything is
	// written. Empty for go: nothing but git runs.
	Tools() []string

	// Manifest is the manifest path relative to the repo root, or "" when the
	// manager has no version to write (go).
	Manifest(dir, root string) string

	// ManifestVersion reads the version the manifest records. ok is false when
	// the manifest carries no version at all — go, where tags are the truth.
	ManifestVersion(dir string) (v semver.Version, ok bool, err error)

	// PackageName reads the manifest's package name, the monorepo default for
	// tag prefixes.
	PackageName(dir string) (string, error)

	// SetVersion writes v into the manifest.
	SetVersion(dir string, v semver.Version) error

	// CheckVersion validates a next version against the ecosystem's rules:
	// for go, the module path must carry /vN once the major is 2 or higher.
	CheckVersion(dir string, v semver.Version) error

	// RefreshLock asks the lockfile's owning tool to record the new version
	// and returns the lockfiles (repo-relative) that exist and should be
	// staged.
	RefreshLock(dir, root string) (lockfiles []string, err error)

	// Preflight runs live checks before anything is written. npm verifies the
	// registry login when publishing is on; nuget checks the version MSBuild
	// evaluates on every run, and packability and the push source when
	// publishing is on; the rest have nothing to check.
	Preflight(dir string, publish bool) error

	// Publish pushes the package to the registry.
	Publish(dir string) error

	// Recovery is the manual command for a publish that failed after the tag
	// was pushed.
	Recovery(dir, root string) string
}

// New returns the adapter for a manager name.
func New(name config.ManagerName, r Runner) (Manager, error) {
	switch name {
	case config.ManagerGo:
		return &goManager{}, nil
	case config.ManagerNpm:
		return &npmManager{r: r}, nil
	case config.ManagerCargo:
		return &cargoManager{r: r}, nil
	case config.ManagerPyPI:
		return &pypiManager{r: r}, nil
	case config.ManagerNuGet:
		return &nugetManager{r: r}, nil
	case config.ManagerNone:
		return &noneManager{}, nil
	default:
		return nil, fmt.Errorf("unknown manager %q", name)
	}
}

// rel returns dir relative to root, in slash form, for messages.
func rel(dir, root string) string {
	r, err := filepath.Rel(root, dir)
	if err != nil {
		return filepath.ToSlash(dir)
	}
	if r == "." {
		return "."
	}
	return filepath.ToSlash(r)
}
