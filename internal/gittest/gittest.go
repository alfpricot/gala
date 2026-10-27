// Package gittest builds real git repositories in temp directories for tests:
// commits shaped messages, tags, branches, and optionally a bare file-path
// remote so pushes are exercised for real with no network.
//
// It deliberately does not import testing; callers pass a temp dir or take
// the cleanup func and defer it.
package gittest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a fixture repository.
type Repo struct {
	// Dir is the worktree root.
	Dir string
	// base is the temp dir holding the repo and its remotes.
	base string
}

// New creates a repository in a fresh temp dir, configured so tests are
// hermetic: local identity, no global excludes file (the developer's global
// ignore would otherwise hide fixture files), no signing, no autocrlf, and
// HEAD on main regardless of the machine's init.defaultBranch.
func New() (*Repo, func(), error) {
	base, err := os.MkdirTemp("", "gala-fixture-")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(base) }

	dir := filepath.Join(base, "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		cleanup()
		return nil, nil, err
	}

	r := &Repo{Dir: dir, base: base}
	steps := [][]string{
		{"init", "-q", "--initial-branch=main"},
		{"config", "user.name", "gala fixture"},
		{"config", "user.email", "fixture@example.invalid"},
		{"config", "commit.gpgsign", "false"},
		{"config", "tag.gpgSign", "false"},
		{"config", "core.autocrlf", "false"},
		// Point the excludes file at a path that does not exist: the global
		// one would otherwise filter fixture files out of status and staging.
		{"config", "core.excludesFile", filepath.Join(dir, ".git", "gala-no-excludes")},
	}
	for _, args := range steps {
		if _, err := r.Run(args...); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	return r, cleanup, nil
}

// Run executes git in the fixture repo and returns its stdout.
func (r *Repo) Run(args ...string) (string, error) {
	full := append([]string{"-C", r.Dir}, args...)
	cmd := exec.Command("git", full...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// WriteFile writes a file inside the fixture, creating parent directories.
func (r *Repo) WriteFile(rel, content string) error {
	path := filepath.Join(r.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// ReadFile reads a file from the fixture.
func (r *Repo) ReadFile(rel string) (string, error) {
	data, err := os.ReadFile(filepath.Join(r.Dir, filepath.FromSlash(rel)))
	return string(data), err
}

// CommitFile writes a file and commits it with message.
func (r *Repo) CommitFile(rel, content, message string) error {
	if err := r.WriteFile(rel, content); err != nil {
		return err
	}
	if _, err := r.Run("add", "--", rel); err != nil {
		return err
	}
	return r.Commit(message)
}

// CommitAll stages everything and commits message.
func (r *Repo) CommitAll(message string) error {
	if _, err := r.Run("add", "-A"); err != nil {
		return err
	}
	return r.Commit(message)
}

// Commit commits what is already staged. Multi-line messages are passed as a
// subject plus body, exactly like a real Conventional Commit.
func (r *Repo) Commit(message string) error {
	lines := strings.SplitN(message, "\n", 2)
	args := []string{"commit", "-q", "-m", lines[0]}
	if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
		args = append(args, "-m", strings.TrimRight(lines[1], "\n"))
	}
	_, err := r.Run(args...)
	return err
}

// Tag creates an annotated tag (always annotated: a bare `git tag` would open
// an editor).
func (r *Repo) Tag(name string) error {
	_, err := r.Run("tag", "-a", name, "-m", name)
	return err
}

// Checkout switches to name, creating it at HEAD when it does not exist. An
// existing branch is never reset (that is what -B would do, silently moving
// main onto a sibling branch).
func (r *Repo) Checkout(name string) error {
	if _, err := r.Run("rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
		_, err := r.Run("checkout", "-q", name)
		return err
	}
	_, err := r.Run("checkout", "-q", "-b", name)
	return err
}

// Branch creates (but does not check out) a branch at HEAD.
func (r *Repo) Branch(name string) error {
	_, err := r.Run("branch", name)
	return err
}

// HeadSHA is the fixture's current HEAD.
func (r *Repo) HeadSHA() (string, error) {
	out, err := r.Run("rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// BareRemote creates a bare repository beside the worktree, adds it as
// remote, and pushes the current branch. It returns the remote's URL and a
// cleanup func for the bare directory.
func (r *Repo) BareRemote(name, branch string) (string, func(), error) {
	bare := filepath.Join(r.base, name+".git")
	if _, err := runGit("", "init", "-q", "--bare", "--initial-branch="+branch, bare); err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(bare) }
	if _, err := r.Run("remote", "add", name, bare); err != nil {
		cleanup()
		return "", nil, err
	}
	if _, err := r.Run("push", "-q", name, branch); err != nil {
		cleanup()
		return "", nil, err
	}
	return bare, cleanup, nil
}

// RemoteRef returns the hash a branch ref has in a bare repository.
func RemoteRef(bareDir, branch string) (string, error) {
	return Ref(bareDir, "refs/heads/"+branch)
}

// Ref returns the hash any full ref (refs/heads/..., refs/tags/...) has in a
// repository, peeled to its commit so an annotated tag and the branch pointing
// at the same commit compare equal. It errors when the ref does not exist.
func Ref(dir, fullRef string) (string, error) {
	out, err := runGit(dir, "rev-parse", "--verify", "--quiet", fullRef+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RemoteRefs lists a bare repository's branch refs.
func RemoteRefs(bareDir string) ([]string, error) {
	out, err := runGit(bareDir, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(l) != "" {
			refs = append(refs, l)
		}
	}
	return refs, nil
}

// CommitMessage reads a commit's full message from any repository.
func CommitMessage(dir, sha string) (string, error) {
	out, err := runGit(dir, "log", "-1", "--format=%B", sha)
	return strings.TrimSpace(out), err
}

// runGit runs git in dir (or the process dir when empty) with args.
func runGit(dir string, args ...string) (string, error) {
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", full...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("git %s: %w: %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
