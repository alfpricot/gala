package release

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alfpricot/gala/internal/git"
	"github.com/alfpricot/gala/internal/gittest"
)

// faultGit wraps a real git runner and lets a test replace chosen
// invocations: handled reports that the override answered, so the real git
// is skipped.
type faultGit struct {
	inner    git.Runner
	override func(inner git.Runner, dir string, args []string) (out string, handled bool, err error)
}

func (f faultGit) Run(dir string, args ...string) (string, error) {
	if out, handled, err := f.override(f.inner, dir, args); handled {
		return out, err
	}
	return f.inner.Run(dir, args...)
}

// writeHook installs an executable hook script in a bare repository.
func writeHook(t *testing.T, bare, name, script string) {
	t.Helper()
	dir := filepath.Join(bare, "hooks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// assertCleanAt checks that the repository is back on a clean tree at sha
// with no tags: the state a failed release must leave behind.
func assertCleanAt(t *testing.T, r *gittest.Repo, sha string) {
	t.Helper()
	head, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if head != sha {
		t.Errorf("HEAD = %s, want the starting commit %s", head, sha)
	}
	if got := tags(t, r); got != "" {
		t.Errorf("local tags = %q, want none", got)
	}
	status, err := r.Run("status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(status) != "" {
		t.Errorf("working tree is not clean after the rollback:\n%s", status)
	}
}

// TestRejectedPushIsRolledBack: a pre-receive rejection of the atomic push
// leaves nothing on the remote, so gala removes its local commit and tag and a
// re-run plans the same release instead of reporting nothing to do.
func TestRejectedPushIsRolledBack(t *testing.T) {
	r, bare, cleanup := fixture(t, appConfig)
	defer cleanup()
	if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
		t.Fatal(err)
	}
	start, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	remoteBefore, err := gittest.RemoteRef(bare, "main")
	if err != nil {
		t.Fatal(err)
	}
	writeHook(t, bare, "pre-receive", "echo 'rejected by policy' >&2\nexit 1")

	opts, _, _ := testOptions(r, nil)
	err = Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("a rejected push must fail the release")
	}
	mustContain(t, err.Error(), "atomic push failed", "the error names the push")
	mustContain(t, err.Error(), "rejected by policy", "the remote's reason is kept")
	mustContain(t, err.Error(), "the release commit and tags were removed", "the error says what state is left")
	assertCleanAt(t, r, start)
	if sha, err := gittest.RemoteRef(bare, "main"); err != nil || sha != remoteBefore {
		t.Errorf("remote main = %s, %v; want it untouched at %s", sha, err, remoteBefore)
	}
	if refs, err := gittest.RemoteRefs(bare); err != nil || strings.Contains(strings.Join(refs, " "), "refs/tags/") {
		t.Errorf("remote refs = %v, %v; want no tag", refs, err)
	}

	// With the cause fixed, the same release goes through.
	if err := os.Remove(filepath.Join(bare, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
	opts2, out2, _ := testOptions(r, nil)
	if err := Run(opts2, ModeRelease); err != nil {
		t.Fatalf("the re-run must succeed: %v\n%s", err, out2.String())
	}
	if got := tags(t, r); got != "v0.1.0" {
		t.Errorf("tags after the re-run = %q, want v0.1.0", got)
	}
}

// twoPackageRepo builds a cargo monorepo whose two packages both have a
// releasable commit, with a bare origin.
func twoPackageRepo(t *testing.T, extraConfig string) (*gittest.Repo, string, func()) {
	t.Helper()
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	write := func(path, content string) {
		t.Helper()
		if err := r.WriteFile(path, content); err != nil {
			t.Fatal(err)
		}
	}
	write("gala.toml", `kind = "monorepo"
manager = "cargo"
publish = true
githubRelease = false
releaseBranches = ["main"]
`+extraConfig+`
[[package]]
path = "packages/core"

[[package]]
path = "packages/cli"
`)
	write("packages/core/Cargo.toml", "[package]\nname = \"core\"\nversion = \"1.0.0\"\nedition = \"2021\"\n")
	write("packages/cli/Cargo.toml", "[package]\nname = \"cli\"\nversion = \"1.0.0\"\nedition = \"2021\"\n")
	if err := r.CommitAll("chore: declare the packages"); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"core/v1.0.0", "cli/v1.0.0"} {
		if err := r.Tag(tag); err != nil {
			t.Fatal(err)
		}
	}
	bare, bareCleanup, err := r.BareRemote("origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("packages/core/src/lib.rs", "pub fn w() {}\n", "feat: new widget"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("packages/cli/src/lib.rs", "pub fn m() {}\n", "fix: honor the config file"); err != nil {
		t.Fatal(err)
	}
	return r, bare, func() {
		bareCleanup()
		cleanup()
	}
}

// TestTagFailureIsRolledBack: the second tag cannot be created, so the first
// tag and the release commit are removed again and nothing is pushed.
func TestTagFailureIsRolledBack(t *testing.T) {
	r, bare, cleanup := twoPackageRepo(t, "")
	defer cleanup()
	start, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	remoteBefore, err := gittest.RemoteRef(bare, "main")
	if err != nil {
		t.Fatal(err)
	}

	opts, _, _ := testOptions(r, &captureRunner{})
	opts.Git = faultGit{inner: opts.Git, override: func(_ git.Runner, _ string, args []string) (string, bool, error) {
		if len(args) > 2 && args[0] == "tag" && args[1] == "-a" && args[2] == "cli/v1.0.1" {
			return "", true, fmt.Errorf("tag creation refused")
		}
		return "", false, nil
	}}
	err = Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("a tag failure must fail the release")
	}
	mustContain(t, err.Error(), "cannot tag cli/v1.0.1", "the error names the tag")
	mustContain(t, err.Error(), "nothing was pushed", "the error says nothing was pushed")
	// The starting tags (core/v1.0.0, cli/v1.0.0) stay; the one this run
	// created is gone.
	if got := tags(t, r); strings.Contains(got, "core/v1.1.0") {
		t.Errorf("the first tag must be removed on rollback, got %q", got)
	}
	head, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if head != start {
		t.Errorf("HEAD = %s, want the starting commit %s", head, start)
	}
	if sha, err := gittest.RemoteRef(bare, "main"); err != nil || sha != remoteBefore {
		t.Errorf("remote main = %s, %v; want it untouched", sha, err)
	}
}

// TestPushThatReachedTheRemoteKeepsLocalState: git reports a failure after the
// remote accepted the push, so rolling back would diverge from the remote.
func TestPushThatReachedTheRemoteKeepsLocalState(t *testing.T) {
	r, bare, cleanup := fixture(t, appConfig)
	defer cleanup()
	if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
		t.Fatal(err)
	}

	opts, _, _ := testOptions(r, nil)
	opts.Git = faultGit{inner: opts.Git, override: func(inner git.Runner, dir string, args []string) (string, bool, error) {
		if len(args) > 1 && args[0] == "push" && args[1] == "--atomic" {
			if _, err := inner.Run(dir, args...); err != nil {
				return "", true, err
			}
			return "", true, fmt.Errorf("connection reset after the remote accepted the push")
		}
		return "", false, nil
	}}
	err := Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("the reported failure must surface")
	}
	mustContain(t, err.Error(), "the remote already has the release commit", "the error explains why nothing was undone")
	if got := tags(t, r); got != "v0.1.0" {
		t.Errorf("the local tag must stay, got %q", got)
	}
	head, err := r.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if sha, err := gittest.RemoteRef(bare, "main"); err != nil || sha != head {
		t.Errorf("remote main = %s, %v; want the release commit %s", sha, err, head)
	}
}

// githubFixture releases an npm app whose remote reports a github.com URL
// (git itself still talks to the bare repository) against a fake API.
func githubFixture(t *testing.T, status int, body string) (*gittest.Repo, string, *captureRunner, *Options, func()) {
	t.Helper()
	cfg := "kind = \"app\"\nmanager = \"npm\"\npublish = true\ngithubRelease = true\nreleaseBranches = [\"main\"]\n"
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile("gala.toml", cfg); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile("package.json", "{\n  \"name\": \"widget\",\n  \"version\": \"1.0.0\"\n}\n"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitAll("chore: scaffold"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag("v1.0.0"); err != nil {
		t.Fatal(err)
	}
	bare, bareCleanup, err := r.BareRemote("origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("src.txt", "x\n", "feat: a widget"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	exec := &captureRunner{}
	opts, _, _ := testOptions(r, exec)
	opts.Token = "token"
	opts.HTTP = srv.Client()
	opts.APIBase = srv.URL
	opts.Git = faultGit{inner: opts.Git, override: func(_ git.Runner, _ string, args []string) (string, bool, error) {
		if len(args) > 1 && args[0] == "remote" && args[1] == "get-url" {
			return "https://github.com/octo/widget.git\n", true, nil
		}
		return "", false, nil
	}}
	return r, bare, exec, opts, func() {
		srv.Close()
		bareCleanup()
		cleanup()
	}
}

func publishCalls(exec *captureRunner) int {
	n := 0
	for _, c := range exec.calls {
		if len(c.args) > 0 && c.args[0] == "publish" {
			n++
		}
	}
	return n
}

// TestGitHubReleaseFailurePrintsManualSteps: the API answers 500 after the tag
// is pushed. A re-run would find nothing due, so the error carries the exact
// commands, and the registry publish did not run.
func TestGitHubReleaseFailurePrintsManualSteps(t *testing.T) {
	r, bare, exec, opts, cleanup := githubFixture(t, http.StatusInternalServerError, `{"message":"boom"}`)
	defer cleanup()

	err := Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("a release API error must fail the run")
	}
	mustContain(t, err.Error(), "500", "the API status is kept")
	mustContain(t, err.Error(), "running gala again finds nothing due", "the error explains why re-running does not help")
	mustContain(t, err.Error(), "gh release create v1.1.0 --verify-tag", "the manual release command")
	mustContain(t, err.Error(), "cd . && npm publish", "the publish command that did not run")
	if n := publishCalls(exec); n != 0 {
		t.Errorf("publish ran %d times after the release failed, want 0", n)
	}
	if sha, err := gittest.Ref(bare, "refs/tags/v1.1.0"); err != nil || sha == "" {
		t.Errorf("the tag must be on the remote: %s, %v", sha, err)
	}
	if got := tags(t, r); !strings.Contains(got, "v1.1.0") {
		t.Errorf("the local tag must stay, got %q", got)
	}
}

// TestExistingGitHubReleaseCountsAsDone: a 422 already_exists answer means the
// release is already there, so the run goes on to publish.
func TestExistingGitHubReleaseCountsAsDone(t *testing.T) {
	_, _, exec, opts, cleanup := githubFixture(t, http.StatusUnprocessableEntity,
		`{"message":"Validation Failed","errors":[{"resource":"Release","code":"already_exists","field":"tag_name"}]}`)
	defer cleanup()
	var out strings.Builder
	opts.Stdout = &out

	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("an existing release must not fail the run: %v", err)
	}
	mustContain(t, out.String(), "release v1.1.0 already exists", "the transcript says so")
	if n := publishCalls(exec); n != 1 {
		t.Errorf("publish ran %d times, want 1", n)
	}
}

// TestPublishFailureListsEveryRemainingPackage: the first package's publish
// fails, so the error prints its recovery command and that of the package
// after it, which never ran.
func TestPublishFailureListsEveryRemainingPackage(t *testing.T) {
	r, bare, cleanup := twoPackageRepo(t, "")
	defer cleanup()
	exec := &captureRunner{respond: func(dir, name string, args []string) (string, error) {
		if name == "cargo" && len(args) > 0 && args[0] == "publish" {
			return "", fmt.Errorf("registry unavailable")
		}
		return "", nil
	}}
	opts, _, _ := testOptions(r, exec)

	err := Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("a publish failure must fail the run")
	}
	mustContain(t, err.Error(), "publish failed for package \"core\"", "the failing package is named")
	mustContain(t, err.Error(), "cd packages/core && cargo publish", "the failing package's command")
	mustContain(t, err.Error(), "cd packages/cli && cargo publish", "the package after it never ran")
	if n := publishCalls(exec); n != 1 {
		t.Errorf("publish ran %d times, want 1 (the first failure stops the run)", n)
	}
	if sha, err := gittest.Ref(bare, "refs/tags/core/v1.1.0"); err != nil || sha == "" {
		t.Errorf("the pushed tag must stay on the remote: %s, %v", sha, err)
	}
}
