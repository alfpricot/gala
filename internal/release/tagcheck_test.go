package release

import (
	"testing"

	"github.com/alfpricot/gala/internal/config"
	"github.com/alfpricot/gala/internal/gittest"
)

// sideTag puts tag on a commit that main cannot reach and returns to main, so
// the plan (which reads tags merged into HEAD) never sees it.
func sideTag(t *testing.T, r *gittest.Repo, tag string) {
	t.Helper()
	if err := r.Checkout("side"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("side.txt", "side\n", "chore: side work"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag(tag); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
}

// TestPreflightRefusesAnExistingNextTag: a next tag that exists locally or on
// the remote but is invisible to the plan must fail preflight, before any
// write, instead of failing the tag or the push after the release commit.
func TestPreflightRefusesAnExistingNextTag(t *testing.T) {
	t.Run("local tag on an unreachable commit", func(t *testing.T) {
		r, _, cleanup := fixture(t, appConfig)
		defer cleanup()
		if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
			t.Fatal(err)
		}
		sideTag(t, r, "v0.1.0")
		before, err := r.HeadSHA()
		if err != nil {
			t.Fatal(err)
		}

		opts, _, _ := testOptions(r, nil)
		err = Run(opts, ModeRelease)
		if err == nil {
			t.Fatal("release must refuse when the next tag already exists locally")
		}
		mustContain(t, err.Error(), "tag v0.1.0 already exists locally", "the error names the tag")
		after, err := r.HeadSHA()
		if err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Errorf("preflight failed after a release commit was made: HEAD %s -> %s", before, after)
		}
	})

	t.Run("remote tag that was never fetched", func(t *testing.T) {
		r, bare, cleanup := fixture(t, appConfig)
		defer cleanup()
		if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
			t.Fatal(err)
		}
		sideTag(t, r, "v0.1.0")
		if _, err := r.Run("push", "-q", "origin", "refs/tags/v0.1.0"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Run("tag", "-d", "v0.1.0"); err != nil {
			t.Fatal(err)
		}

		opts, _, _ := testOptions(r, nil)
		err := Run(opts, ModeRelease)
		if err == nil {
			t.Fatal("release must refuse when the next tag already exists on the remote")
		}
		mustContain(t, err.Error(), "tag v0.1.0 already exists on remote", "the error names the tag")
		if sha, err := gittest.RemoteRef(bare, "main"); err != nil || sha == "" {
			t.Errorf("remote main unreadable: %v", err)
		}
		if got := tags(t, r); got != "" {
			t.Errorf("no tag may be created locally, got %q", got)
		}
	})

	t.Run("dry run runs the same check", func(t *testing.T) {
		r, _, cleanup := fixture(t, appConfig)
		defer cleanup()
		if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
			t.Fatal(err)
		}
		sideTag(t, r, "v0.1.0")

		opts, _, _ := testOptions(r, nil)
		if err := Run(opts, ModeDryRun); err == nil {
			t.Fatal("a dry run must report the existing tag too")
		}
	})
}

// TestMonorepoTagPrefixes covers the default prefixes and the collisions
// specs() rejects.
func TestMonorepoTagPrefixes(t *testing.T) {
	run := func(t *testing.T, cfg string, files map[string]string) (string, error) {
		t.Helper()
		r, cleanup, err := gittest.New()
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		if err := r.WriteFile("gala.toml", cfg); err != nil {
			t.Fatal(err)
		}
		for path, content := range files {
			if err := r.WriteFile(path, content); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.CommitAll("feat: declare the packages"); err != nil {
			t.Fatal(err)
		}
		opts, out, _ := testOptions(r, nil)
		err = Run(opts, ModePlan)
		return out.String(), err
	}

	t.Run("nested go module defaults to its directory", func(t *testing.T) {
		out, err := run(t, `kind = "monorepo"
manager = "go"
publish = false
githubRelease = false
releaseBranches = ["main"]

[[package]]
path = "libs/foo"
`, map[string]string{"libs/foo/go.mod": "module example.com/org/foo\n\ngo 1.22\n"})
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, out, "libs/foo/v0.1.0", "go resolves nested modules from <dir>/vX.Y.Z tags")
	})

	t.Run("identical prefixes", func(t *testing.T) {
		_, err := run(t, `kind = "monorepo"
manager = "go"
publish = false
githubRelease = false
releaseBranches = ["main"]

[[package]]
path = "a"
tagPrefix = "shared/v"

[[package]]
path = "b"
tagPrefix = "shared/v"
`, map[string]string{
			"a/go.mod": "module example.com/org/a\n\ngo 1.22\n",
			"b/go.mod": "module example.com/org/b\n\ngo 1.22\n",
		})
		if err == nil {
			t.Fatal("two packages with one prefix must be rejected")
		}
		mustContain(t, err.Error(), `both use tag prefix "shared/v"`, "the error names the prefix")
	})

	t.Run("a prefix another one extends with a digit", func(t *testing.T) {
		_, err := run(t, `kind = "monorepo"
manager = "go"
publish = false
githubRelease = false
releaseBranches = ["main"]

[[package]]
path = "a"
tagPrefix = "v"

[[package]]
path = "b"
tagPrefix = "v1."
`, map[string]string{
			"a/go.mod": "module example.com/org/a\n\ngo 1.22\n",
			"b/go.mod": "module example.com/org/b\n\ngo 1.22\n",
		})
		if err == nil {
			t.Fatal("an overlapping prefix must be rejected")
		}
		mustContain(t, err.Error(), "overlapping tag prefixes", "the error names the overlap")
	})

	t.Run("a prefix extended past a separator is fine", func(t *testing.T) {
		if err := checkTagPrefixes([]spec{
			{relDir: "a", prefix: "app/v"},
			{relDir: "b", prefix: "app/vx/v"},
			{relDir: "c", prefix: "app-"},
		}); err != nil {
			t.Errorf("non-digit extensions cannot parse as a version: %v", err)
		}
	})
}

func TestDefaultTagPrefix(t *testing.T) {
	cases := []struct {
		mgr           config.ManagerName
		dir, name, in string
		want          string
	}{
		{config.ManagerGo, "libs/foo", "foo", "", "libs/foo/v"},
		{config.ManagerGo, ".", "root", "", "v"},
		{config.ManagerNpm, "packages/core", "core", "", "core/v"},
	}
	for _, c := range cases {
		if got := defaultTagPrefix(c.mgr, c.dir, c.name); got != c.want {
			t.Errorf("defaultTagPrefix(%s, %q, %q) = %q, want %q", c.mgr, c.dir, c.name, got, c.want)
		}
	}
}
