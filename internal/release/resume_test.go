package release

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alfpricot/gala/internal/gittest"
)

// TestResumeFinishesAfterAGitHubReleaseFailure: a release stops at the GitHub
// Release after pushing its tags, and `release --resume` on the same commit
// creates the release and then publishes, which a plain release could not do
// because nothing was due any more.
func TestResumeFinishesAfterAGitHubReleaseFailure(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	var created atomic.Int32
	_, _, exec, opts, cleanup := githubFixtureHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		created.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer cleanup()
	outputs := filepath.Join(t.TempDir(), "github_output")
	opts.GitHubOutput = outputs

	err := Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("the release API failure must fail the run")
	}
	mustContain(t, err.Error(), "gala release --resume", "the failure names the command that finishes it")
	if n := publishCalls(exec); n != 0 {
		t.Fatalf("publish ran %d times before the release existed", n)
	}

	failing.Store(false)
	exec.calls = nil
	if err := Run(opts, ModeResume); err != nil {
		t.Fatalf("resume must finish the release: %v", err)
	}
	if created.Load() != 1 {
		t.Errorf("GitHub Releases created = %d, want 1", created.Load())
	}
	if n := publishCalls(exec); n != 1 {
		t.Errorf("publish ran %d times on resume, want 1", n)
	}
	data, err := os.ReadFile(outputs)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(data), "released=true", "resume reports the tags that exist")
	mustContain(t, string(data), "tag=v1.1.0", "the tag output")
	mustNotContain(t, string(data), "released=false", "a resume never reports released=false for pushed tags")
}

// TestResumeRefusesWhatItCannotFinish covers the guards: no release at HEAD, a
// tag that is only local, a missing changelog entry, and an unknown package.
func TestResumeRefusesWhatItCannotFinish(t *testing.T) {
	t.Run("no release tag at HEAD", func(t *testing.T) {
		r, _, cleanup := fixture(t, appConfig)
		defer cleanup()
		if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
			t.Fatal(err)
		}
		opts, _, _ := testOptions(r, nil)
		err := Run(opts, ModeResume)
		if err == nil {
			t.Fatal("resume with nothing released must fail")
		}
		mustContain(t, err.Error(), "no release tag points at HEAD", "the error says what is missing")
	})

	t.Run("tag only on this machine", func(t *testing.T) {
		r, _, cleanup := fixture(t, appConfig)
		defer cleanup()
		if err := r.WriteFile("CHANGELOG.md", "# Changelog\n\n## 0.1.0 (2026-10-27)\n\n### Features\n\n* first\n"); err != nil {
			t.Fatal(err)
		}
		if err := r.CommitAll("chore(release): v0.1.0"); err != nil {
			t.Fatal(err)
		}
		if err := r.Tag("v0.1.0"); err != nil {
			t.Fatal(err)
		}
		opts, _, _ := testOptions(r, nil)
		err := Run(opts, ModeResume)
		if err == nil {
			t.Fatal("a tag that was never pushed must not be resumed")
		}
		mustContain(t, err.Error(), "is not on remote", "the error says the tag was never pushed")
	})

	t.Run("no changelog entry", func(t *testing.T) {
		r, _, cleanup := fixture(t, appConfig)
		defer cleanup()
		if err := r.WriteFile("CHANGELOG.md", "# Changelog\n\n## 0.0.1 (2026-10-20)\n\n### Bug Fixes\n\n* older\n"); err != nil {
			t.Fatal(err)
		}
		if err := r.CommitAll("chore(release): v0.1.0"); err != nil {
			t.Fatal(err)
		}
		if err := r.Tag("v0.1.0"); err != nil {
			t.Fatal(err)
		}
		opts, _, _ := testOptions(r, nil)
		err := Run(opts, ModeResume)
		if err == nil {
			t.Fatal("a release with no changelog entry cannot be resumed")
		}
		mustContain(t, err.Error(), "no entry headed", "the error names the missing entry")
	})

	t.Run("unknown package", func(t *testing.T) {
		r, _, cleanup := twoPackageRepo(t, "")
		defer cleanup()
		opts, _, _ := testOptions(r, &captureRunner{})
		opts.Packages = []string{"nope"}
		err := Run(opts, ModeResume)
		if err == nil {
			t.Fatal("an unknown --package must fail")
		}
		mustContain(t, err.Error(), `"nope" is not a configured package`, "the error names the bad package")
		mustContain(t, err.Error(), "core, cli", "the error lists the real packages")
	})
}

// TestResumeLimitedToPackages: the first package's publish fails, the failure
// prints a resume command naming the failing package and the ones after it, and
// resuming with only the later package publishes just that one.
func TestResumeLimitedToPackages(t *testing.T) {
	r, _, cleanup := twoPackageRepo(t, "")
	defer cleanup()
	failing := &captureRunner{respond: func(_, name string, args []string) (string, error) {
		if name == "cargo" && len(args) > 0 && args[0] == "publish" {
			return "", os.ErrPermission
		}
		return "", nil
	}}
	opts, _, _ := testOptions(r, failing)
	err := Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("a failing publish must fail the run")
	}
	mustContain(t, err.Error(), "gala release --resume --package core --package cli", "the exact resume command")

	ok := &captureRunner{}
	opts2, out, _ := testOptions(r, ok)
	opts2.Packages = []string{"cli"}
	if err := Run(opts2, ModeResume); err != nil {
		t.Fatalf("resume must finish the named package: %v\n%s", err, out.String())
	}
	var published []string
	for _, c := range ok.calls {
		if len(c.args) > 0 && c.args[0] == "publish" {
			published = append(published, filepathSlash(c.dir))
		}
	}
	if len(published) != 1 || !strings.HasSuffix(published[0], "packages/cli") {
		t.Errorf("published = %v, want only packages/cli", published)
	}
}

// tagAtHead returns the tag at HEAD that starts with prefix.
func tagAtHead(t *testing.T, r *gittest.Repo, prefix string) string {
	t.Helper()
	out, err := r.Run("tag", "--points-at", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Fields(out) {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no tag starting with %q at HEAD in %q", prefix, out)
	return ""
}

// releasedTwoPackages runs a full release of the two-package repo, leaving both
// tags pushed and HEAD on the release commit.
func releasedTwoPackages(t *testing.T) (*gittest.Repo, func()) {
	t.Helper()
	r, _, cleanup := twoPackageRepo(t, "")
	opts, _, _ := testOptions(r, &captureRunner{})
	if err := Run(opts, ModeRelease); err != nil {
		cleanup()
		t.Fatalf("the release must succeed: %v", err)
	}
	return r, cleanup
}

// TestResumeHintKeepsNoPublish: a run told not to publish prints a resume
// command that repeats that, so following it never publishes.
func TestResumeHintKeepsNoPublish(t *testing.T) {
	_, _, _, opts, cleanup := githubFixtureHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer cleanup()
	opts.NoPublish = true
	err := Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("the release API failure must fail the run")
	}
	mustContain(t, err.Error(), "gala release --resume --no-publish", "the hint repeats --no-publish")
}

// TestResumeNamedPackageWithoutATag: a package named with --package that has
// no tag at HEAD is an error, not a silent skip.
func TestResumeNamedPackageWithoutATag(t *testing.T) {
	r, cleanup := releasedTwoPackages(t)
	defer cleanup()
	if _, err := r.Run("tag", "-d", tagAtHead(t, r, "cli/")); err != nil {
		t.Fatal(err)
	}
	opts, _, _ := testOptions(r, &captureRunner{})
	opts.Packages = []string{"core", "cli"}
	err := Run(opts, ModeResume)
	if err == nil {
		t.Fatal("a named package with no tag at HEAD must fail the resume")
	}
	mustContain(t, err.Error(), `--package "cli" has no release tag at HEAD`, "the error names the package")
}

// TestResumeFromADetachedHead: resume needs only the release commit, so it runs
// from a detached HEAD, and when the branch has moved past that commit the
// error says how to check it out.
func TestResumeFromADetachedHead(t *testing.T) {
	r, cleanup := releasedTwoPackages(t)
	defer cleanup()
	if _, err := r.Run("checkout", "--detach", "HEAD"); err != nil {
		t.Fatal(err)
	}
	ok := &captureRunner{}
	opts, out, _ := testOptions(r, ok)
	opts.Packages = []string{"cli"}
	if err := Run(opts, ModeResume); err != nil {
		t.Fatalf("resume must run from a detached HEAD at the release commit: %v\n%s", err, out.String())
	}
	if n := publishCalls(ok); n != 1 {
		t.Errorf("publish ran %d times, want 1", n)
	}

	if _, err := r.Run("checkout", "main"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("later.txt", "x\n", "docs: a later commit"); err != nil {
		t.Fatal(err)
	}
	opts2, _, _ := testOptions(r, &captureRunner{})
	err := Run(opts2, ModeResume)
	if err == nil {
		t.Fatal("resume past the release commit must fail")
	}
	mustContain(t, err.Error(), "git switch --detach <tag>", "the error says how to reach the release commit")
}

// TestResumeRefusesATagThatDiffersFromTheRemote: a local tag naming another
// object than the remote's would publish a tree the public tag does not
// describe.
func TestResumeRefusesATagThatDiffersFromTheRemote(t *testing.T) {
	r, cleanup := releasedTwoPackages(t)
	defer cleanup()
	tag := tagAtHead(t, r, "cli/")
	if _, err := r.Run("tag", "-f", "-a", "-m", "another annotation", tag); err != nil {
		t.Fatal(err)
	}
	ok := &captureRunner{}
	opts, _, _ := testOptions(r, ok)
	opts.Packages = []string{"cli"}
	err := Run(opts, ModeResume)
	if err == nil {
		t.Fatal("a tag that differs from the remote's must not be resumed")
	}
	mustContain(t, err.Error(), "is a different object than the local tag", "the error names the mismatch")
	if n := publishCalls(ok); n != 0 {
		t.Errorf("publish ran %d times for a mismatched tag", n)
	}
}

// TestResumeReportsTheTagsWhenAPreflightFails: the tags are confirmed pushed
// before the tool and credential checks, so a failing check still reports them.
func TestResumeReportsTheTagsWhenAPreflightFails(t *testing.T) {
	_, _, _, opts, cleanup := githubFixtureHandler(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer cleanup()
	outputs := filepath.Join(t.TempDir(), "github_output")
	opts.GitHubOutput = outputs
	if err := Run(opts, ModeRelease); err == nil {
		t.Fatal("the release API failure must fail the run")
	}
	if err := os.WriteFile(outputs, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	opts.LookPath = func(string) error { return os.ErrNotExist }
	if err := Run(opts, ModeResume); err == nil {
		t.Fatal("a missing tool must fail the resume")
	}
	data, err := os.ReadFile(outputs)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, string(data), "released=true", "the pushed tags are reported")
	mustNotContain(t, string(data), "released=false", "a resume past the tag check never reports false")
}
