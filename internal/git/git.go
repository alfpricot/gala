// Package git wraps the git command line behind an injectable Runner so tests
// can exercise a real repository in a temp dir without mocking anything.
//
// Reads append --end-of-options ahead of revisions and pathspecs so a ref or
// path that happens to look like a flag can never be parsed as one.
package git

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner runs git in dir and returns its standard output. Implementations
// must return a *CommandError carrying stderr on non-zero exit.
type Runner interface {
	Run(dir string, args ...string) (stdout string, err error)
}

// OSRunner is the production Runner: the real git binary.
type OSRunner struct{}

// Run executes git -C dir with args.
func (OSRunner) Run(dir string, args ...string) (string, error) {
	full := make([]string, 0, len(args)+2)
	full = append(full, "-C", dir)
	full = append(full, args...)
	cmd := exec.Command("git", full...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		code := 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return stdout.String(), &CommandError{Args: args, Code: code, Stderr: strings.TrimSpace(stderr.String()), Err: err}
	}
	return stdout.String(), nil
}

// CommandError reports a failed git invocation with enough context to print
// a useful message.
type CommandError struct {
	Args   []string
	Code   int
	Stderr string
	Err    error
}

// Error implements error.
func (e *CommandError) Error() string {
	msg := fmt.Sprintf("git %s failed (exit %d)", strings.Join(e.Args, " "), e.Code)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

// Unwrap exposes the underlying exec error.
func (e *CommandError) Unwrap() error { return e.Err }

// ErrDetached is returned by CurrentBranch when HEAD is not on a branch.
var ErrDetached = errors.New("HEAD is detached")

// Repo is an opened git repository.
type Repo struct {
	dir    string
	runner Runner
}

// Open resolves cwd to its repository root and returns a Repo for it.
func Open(cwd string, runner Runner) (*Repo, error) {
	out, err := runner.Run(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository: %w", cwd, err)
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return nil, fmt.Errorf("%s is not inside a git repository", cwd)
	}
	return &Repo{dir: root, runner: runner}, nil
}

// Root is the repository worktree root.
func (g *Repo) Root() string { return g.dir }

// Run exposes the underlying runner for callers that need a raw read.
func (g *Repo) Run(args ...string) (string, error) { return g.runner.Run(g.dir, args...) }
