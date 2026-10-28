// Package release builds the plan and runs the pipeline: checks, preflights,
// writes, commit, tag, atomic push, sync push, GitHub Releases, publishes.
//
// Three invariants hold throughout:
//
//   - every check that can run before the first write, does;
//   - all of git completes before any registry publish (tagged-but-unpublished
//     is visible and fixable; published-but-untagged is neither);
//   - plan and --dry-run never write.
package release

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/alfpricot/gala/internal/git"
	"github.com/alfpricot/gala/internal/manager"
)

// Mode selects how far the pipeline runs.
type Mode int

const (
	// ModePlan computes and prints the plan; it never writes and never runs a
	// live preflight.
	ModePlan Mode = iota
	// ModeDryRun runs the read-only checks and the live preflights, then stops
	// before the first write.
	ModeDryRun
	// ModeRelease runs everything.
	ModeRelease
)

// Options are the injected dependencies. The zero value is not usable;
// start from DefaultOptions and override what a test needs.
type Options struct {
	// Cwd is the checkout to run against (the --cwd hook).
	Cwd string
	// ConfigPath overrides config discovery.
	ConfigPath string
	// NoPublish turns publish = true off for this run.
	NoPublish bool

	// Stdout and Stderr receive the prose output.
	Stdout io.Writer
	Stderr io.Writer

	// Git and Exec are the injectable runners.
	Git  git.Runner
	Exec manager.Runner
	// LookPath checks that a tool is on PATH.
	LookPath func(string) error
	// HTTP and APIBase are the GitHub client's transport and base URL.
	HTTP    *http.Client
	APIBase string
	// Token is the GitHub token; read from GITHUB_TOKEN / GH_TOKEN by
	// DefaultOptions.
	Token string

	// GitHubOutput is the file a release appends its step outputs to
	// (released, tag, tags, version); read from GITHUB_OUTPUT by
	// DefaultOptions. Empty writes nothing.
	GitHubOutput string

	// Now is the clock, for the changelog date.
	Now func() time.Time
}

// DefaultOptions fills every dependency with the production implementation.
func DefaultOptions() *Options {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	return &Options{
		Cwd:    ".",
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Git:    git.OSRunner{},
		Exec:   manager.ExecRunner{},
		LookPath: func(name string) error {
			_, err := exec.LookPath(name)
			return err
		},
		HTTP:         http.DefaultClient,
		Token:        token,
		GitHubOutput: os.Getenv("GITHUB_OUTPUT"),
		Now:          time.Now,
	}
}

func (o *Options) out() io.Writer {
	if o.Stdout == nil {
		return io.Discard
	}
	return o.Stdout
}

func (o *Options) warn() io.Writer {
	if o.Stderr == nil {
		return io.Discard
	}
	return o.Stderr
}

// warnf prints a warning that never fails a run.
func (o *Options) warnf(format string, args ...any) {
	_, _ = io.WriteString(o.warn(), "warning: "+fmt.Sprintf(format, args...)+"\n")
}
