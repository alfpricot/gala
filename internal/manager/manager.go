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
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alfpricot/gala/internal/config"
	"github.com/alfpricot/gala/internal/semver"
)

// Runner runs a package-manager command in dir. Tests substitute a capture
// runner so argv is asserted without executing anything.
type Runner interface {
	Run(dir, name string, args ...string) (stdout string, err error)
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
	// registry login when publishing is on; the rest have nothing to check.
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
