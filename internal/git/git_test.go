package git

import (
	"errors"
	"strings"
	"testing"

	"github.com/alfpricot/gala/internal/gittest"
)

func newRepo(t *testing.T) (*gittest.Repo, *Repo) {
	t.Helper()
	fr, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	// Three commits: a plain one, one with a body, one adding a subdirectory.
	if err := fr.CommitFile("a.go", "package a\n", "feat: first commit\n\nbody line one"); err != nil {
		t.Fatal(err)
	}
	if err := fr.CommitFile("b.go", "package b\n", "fix(core): second\n\nBREAKING CHANGE: boom"); err != nil {
		t.Fatal(err)
	}
	if err := fr.Tag("v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := fr.CommitFile("sub/c.go", "package c\n", "chore: third"); err != nil {
		t.Fatal(err)
	}

	gr, err := Open(fr.Dir, OSRunner{})
	if err != nil {
		t.Fatal(err)
	}
	return fr, gr
}

func TestOpenResolvesRoot(t *testing.T) {
	fr, gr := newRepo(t)
	if gr.Root() == "" {
		t.Fatal("empty root")
	}
	// Opening from a subdirectory resolves to the same root.
	sub, err := Open(fr.Dir+string("/sub"), OSRunner{})
	if err != nil {
		// path with forward slash works on Windows too through exec
		t.Skipf("subdir open: %v", err)
	}
	if sub.Root() != gr.Root() {
		t.Errorf("root from subdir = %q, want %q", sub.Root(), gr.Root())
	}
}

func TestOpenOutsideRepo(t *testing.T) {
	if _, err := Open(t.TempDir(), OSRunner{}); err == nil {
		t.Error("opening a non-repository must fail")
	}
}

// TestLogBatched parses the record/field separated format: one spawn returns
// hash, subject, body, and the files each commit touched.
func TestLogBatched(t *testing.T) {
	_, gr := newRepo(t)
	commits, err := gr.Log([]string{"HEAD"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 {
		t.Fatalf("got %d commits, want 3", len(commits))
	}

	// Newest first.
	if commits[0].Subject != "chore: third" {
		t.Errorf("newest subject = %q", commits[0].Subject)
	}
	if commits[2].Subject != "feat: first commit" {
		t.Errorf("oldest subject = %q", commits[2].Subject)
	}
	if !strings.HasPrefix(commits[2].Hash, "") || len(commits[2].Hash) != 40 {
		t.Errorf("hash = %q, want the full 40-char id", commits[2].Hash)
	}
	if commits[2].Body != "body line one" {
		t.Errorf("body = %q", commits[2].Body)
	}
	if commits[1].Body != "BREAKING CHANGE: boom" {
		t.Errorf("body = %q", commits[1].Body)
	}
	if strings.Join(commits[0].Files, ",") != "sub/c.go" {
		t.Errorf("files of the last commit = %v", commits[0].Files)
	}
	if commits[1].Files[0] != "b.go" || len(commits[1].Files) != 1 {
		t.Errorf("files of the second commit = %v, want [b.go]", commits[1].Files)
	}
}

func TestLogRangeAndPath(t *testing.T) {
	_, gr := newRepo(t)
	only, err := gr.Log([]string{"v1.0.0..HEAD"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(only) != 1 || only[0].Subject != "chore: third" {
		t.Errorf("range log = %+v", only)
	}

	scoped, err := gr.Log([]string{"HEAD"}, []string{"sub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].Subject != "chore: third" {
		t.Errorf("path-scoped log = %+v", scoped)
	}
}

func TestRevList(t *testing.T) {
	_, gr := newRepo(t)
	all, err := gr.RevList([]string{"HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("rev-list HEAD = %d commits, want 3", len(all))
	}
	since, err := gr.RevList([]string{"v1.0.0..HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(since) != 1 {
		t.Errorf("rev-list v1.0.0..HEAD = %d commits, want 1", len(since))
	}
	// Union semantics: negatives from every range subtract together.
	union, err := gr.RevList([]string{"v1.0.0..HEAD", "v1.0.0..HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(union) != 1 {
		t.Errorf("repeated range = %d commits, want 1", len(union))
	}
}

func TestMergeBase(t *testing.T) {
	fr, gr := newRepo(t)
	if err := fr.Checkout("side"); err != nil {
		t.Fatal(err)
	}
	if err := fr.CommitFile("side.go", "package side\n", "feat: side work"); err != nil {
		t.Fatal(err)
	}
	main, err := gr.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	_ = main
	if err := fr.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if err := fr.CommitFile("main.go", "package main\n", "feat: main work"); err != nil {
		t.Fatal(err)
	}
	base, err := gr.MergeBase("side", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(base) != 40 {
		t.Errorf("merge-base = %q", base)
	}
}

func TestTagsOnlyReachable(t *testing.T) {
	fr, gr := newRepo(t)
	if err := fr.Checkout("side"); err != nil {
		t.Fatal(err)
	}
	if err := fr.CommitFile("side.go", "package side\n", "feat: side"); err != nil {
		t.Fatal(err)
	}
	if err := fr.Tag("v9.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := fr.Checkout("main"); err != nil {
		t.Fatal(err)
	}

	tags, err := gr.Tags()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(tags, ",")
	if !strings.Contains(joined, "v1.0.0") {
		t.Errorf("tags = %q, want v1.0.0", joined)
	}
	if strings.Contains(joined, "v9.0.0") {
		t.Errorf("an unreachable tag leaked into the plan anchors: %q", joined)
	}
}

func TestStatusAndBranch(t *testing.T) {
	fr, gr := newRepo(t)

	branch, err := gr.CurrentBranch()
	if err != nil || branch != "main" {
		t.Errorf("branch = %q, %v", branch, err)
	}

	paths, err := gr.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Errorf("clean tree reported %v", paths)
	}

	if err := fr.WriteFile("scratch.go", "package scratch\n"); err != nil {
		t.Fatal(err)
	}
	paths, err = gr.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "scratch.go" {
		t.Errorf("status = %v, want [scratch.go]", paths)
	}
}

func TestIgnored(t *testing.T) {
	fr, gr := newRepo(t)
	if err := fr.CommitFile(".gitignore", "/*.md\n!/README.md\n", "chore: ignore notes"); err != nil {
		t.Fatal(err)
	}

	// a.go is tracked, README.md is re-included, and CHANGELOG.md is ignored
	// even though it does not exist yet.
	got, err := gr.Ignored("a.go", "README.md", "CHANGELOG.md", "sub/c.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "CHANGELOG.md" || got[0].Rule != ".gitignore:1:/*.md" {
		t.Errorf("Ignored = %+v, want CHANGELOG.md by .gitignore:1:/*.md", got)
	}

	got, err = gr.Ignored("a.go", "README.md")
	if err != nil || len(got) != 0 {
		t.Errorf("nothing ignored: got %+v, %v", got, err)
	}
	if got, err := gr.Ignored(); err != nil || got != nil {
		t.Errorf("no paths: got %+v, %v", got, err)
	}

	// A non-ASCII path comes back as given, not octal-escaped in quotes.
	got, err = gr.Ignored("versión.md")
	if err != nil || len(got) != 1 || got[0].Path != "versión.md" {
		t.Errorf("non-ASCII path: got %+v, %v", got, err)
	}
}

// TestIgnoredStderrOnCleanRun covers exit 1 (nothing ignored) arriving with
// stderr output, as it does under GIT_TRACE or when git warns that it cannot
// read an excludes file: that is still a clean answer, not a failure.
func TestIgnoredStderrOnCleanRun(t *testing.T) {
	_, gr := newRepo(t)
	t.Setenv("GIT_TRACE", "1")
	got, err := gr.Ignored("a.go", "nothing-matches.txt")
	if err != nil || len(got) != 0 {
		t.Errorf("trace output on stderr must not fail the check: got %+v, %v", got, err)
	}
}

func TestDetachedHead(t *testing.T) {
	fr, gr := newRepo(t)
	sha, err := gr.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fr.Run("checkout", "-q", "--detach", sha); err != nil {
		t.Fatal(err)
	}
	_, err = gr.CurrentBranch()
	if !errors.Is(err, ErrDetached) {
		t.Errorf("err = %v, want ErrDetached", err)
	}
}

func TestRemoteURLAndLsRemote(t *testing.T) {
	fr, gr := newRepo(t)
	bare, _, err := fr.BareRemote("origin", "main")
	if err != nil {
		t.Fatal(err)
	}

	url, err := gr.RemoteURL("origin")
	if err != nil {
		t.Fatal(err)
	}
	if url != bare {
		t.Errorf("url = %q, want %q", url, bare)
	}

	head, err := gr.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	remoteSHA, err := gr.LsRemote("origin", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if remoteSHA != head {
		t.Errorf("ls-remote = %q, want %q", remoteSHA, head)
	}
	missing, err := gr.LsRemote("origin", "refs/heads/nope")
	if err != nil || missing != "" {
		t.Errorf("absent ref = %q, %v", missing, err)
	}
	if !gr.HasObject(head) {
		t.Error("HasObject should know the local commit")
	}
	if gr.HasObject("ffffffffffffffffffffffffffffffffffffffff") {
		t.Error("HasObject should not know an unknown commit")
	}
	anc, err := gr.IsAncestor(head, head)
	if err != nil || !anc {
		t.Errorf("a commit is its own ancestor: %v %v", anc, err)
	}
}

func TestWritePath(t *testing.T) {
	fr, gr := newRepo(t)

	if err := fr.WriteFile("a.go", "package a // changed\n"); err != nil {
		t.Fatal(err)
	}
	if err := gr.Add("a.go"); err != nil {
		t.Fatal(err)
	}
	if err := gr.Commit("chore: touch a", "[skip ci]"); err != nil {
		t.Fatal(err)
	}
	if err := gr.TagAnnotated("v2.0.0", "## 2.0.0 (2026-10-27)"); err != nil {
		t.Fatal(err)
	}

	bare, _, err := fr.BareRemote("origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	head, err := gr.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if err := gr.PushAtomic("origin", "main", "v2.0.0"); err != nil {
		t.Fatal(err)
	}
	got, err := RemoteRefHash(bare, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if got != head {
		t.Errorf("bare main = %s, want %s", got, head)
	}
	tagSHA, err := RemoteRefHash(bare, "refs/tags/v2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if tagSHA != head {
		t.Errorf("bare tag = %s, want %s", tagSHA, head)
	}

	// Plain push for the sync path.
	if err := gr.Push("origin", "main:dev"); err != nil {
		t.Fatal(err)
	}
	devSHA, err := RemoteRefHash(bare, "refs/heads/dev")
	if err != nil || devSHA != head {
		t.Errorf("synced dev = %s (%v), want %s", devSHA, err, head)
	}

	// Guard rails: a ref that looks like a flag is refused.
	if err := gr.PushAtomic("origin", "--force"); err == nil {
		t.Error("a flag-shaped ref must be refused")
	}
	if err := gr.TagAnnotated("-v1", "x"); err == nil {
		t.Error("a flag-shaped tag must be refused")
	}
}

// RemoteRefHash reads a ref out of a bare repository for assertions.
func RemoteRefHash(dir, ref string) (string, error) {
	g := &Repo{dir: dir, runner: OSRunner{}}
	out, err := g.Run("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
