package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alfpricot/gala/internal/gittest"
)

// TestPlanTagSelection covers the version-policy core: reachability (a tag on
// a sibling branch must not anchor a plan computed on main) and prerelease
// filtering (rc tags are ignored entirely).
func TestPlanTagSelection(t *testing.T) {
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if err := r.WriteFile("gala.toml", appConfig); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("app.go", "package app\n", "feat: first feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag("v1.0.0"); err != nil {
		t.Fatal(err)
	}
	// A prerelease on main, reachable but never an anchor.
	if err := r.CommitFile("app2.go", "package app\n", "feat: second feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag("v2.0.0-rc.1"); err != nil {
		t.Fatal(err)
	}
	// A stable tag on a sibling branch: unreachable from HEAD, so it must be
	// ignored even though 9.0.0 > 1.0.0.
	if err := r.Checkout("other"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("other.go", "package other\n", "feat: on a branch"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag("v9.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("app3.go", "package app\n", "fix: third feature"); err != nil {
		t.Fatal(err)
	}

	opts, out, _ := testOptions(r, nil)
	if err := Run(opts, ModePlan); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	mustContain(t, got, "gala plan", "plan output")
	mustContain(t, got, "v1.1.0", "a patch on top of the reachable stable tag")
	mustContain(t, got, "from v1.0.0", "the anchor is the reachable stable tag")
	mustNotContain(t, got, "v9.0.0", "an unreachable tag")
	mustNotContain(t, got, "v2.0.0", "a prerelease tag")
}

// TestPlanNothingDue: a run with nothing to release is a success, not a
// failure, and it explains itself.
func TestPlanNothingDue(t *testing.T) {
	r, _, cleanup := fixture(t, appConfig)
	defer cleanup()

	if err := r.CommitFile("app.go", "package app\n", "feat: first feature"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag("v0.1.0"); err != nil {
		t.Fatal(err)
	}
	// Only hidden and non-conforming commits after the tag.
	if err := r.CommitFile("doc.md", "docs\n", "docs: explain the plan"); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitFile("doc2.md", "docs2\n", "Merge branch 'dev' into main"); err != nil {
		t.Fatal(err)
	}

	opts, out, _ := testOptions(r, nil)
	if err := Run(opts, ModePlan); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	mustContain(t, got, "nothing to release", "plan output")
	mustContain(t, got, "no releasable commits", "the reason")
	mustContain(t, got, "1 skipped", "the promotion merge commit is counted")
}

// TestDriftIsHardError: a manifest that disagrees with the latest reachable
// tag is reconciled by hand, never guessed around.
func TestDriftIsHardError(t *testing.T) {
	cfg := `kind = "app"
manager = "npm"
githubRelease = false
releaseBranches = ["main"]
syncBranch = "dev"
`
	r, _, cleanup := fixture(t, cfg)
	defer cleanup()

	if err := r.CommitFile("package.json", "{\"name\":\"gizmo\",\"version\":\"1.0.0\"}\n", "feat: first"); err != nil {
		t.Fatal(err)
	}
	if err := r.Tag("v1.0.0"); err != nil {
		t.Fatal(err)
	}
	// The manifest moved without a tag: hand-edit drift.
	if err := r.CommitFile("package.json", "{\"name\":\"gizmo\",\"version\":\"1.0.5\"}\n", "chore: bump by hand"); err != nil {
		t.Fatal(err)
	}

	opts, _, _ := testOptions(r, nil)
	err := Run(opts, ModePlan)
	if err == nil {
		t.Fatal("manifest/tag drift must be a hard error")
	}
	mustContain(t, err.Error(), "does not match the latest reachable tag", "drift message")
	mustContain(t, err.Error(), "syncBranch", "the message names the sync as the likely fix")
}

// TestGoVNextRule: a 2.0.0 release from a module without /v2 fails at plan
// time, before any write.
func TestGoVNextRule(t *testing.T) {
	setup := func(module string) (*gittest.Repo, func()) {
		r, cleanup, err := gittest.New()
		if err != nil {
			t.Fatal(err)
		}
		if err := r.WriteFile("gala.toml", appConfig); err != nil {
			cleanup()
			t.Fatal(err)
		}
		if err := r.WriteFile("go.mod", "module "+module+"\n\ngo 1.27\n"); err != nil {
			cleanup()
			t.Fatal(err)
		}
		if err := r.CommitAll("chore: bootstrap"); err != nil {
			cleanup()
			t.Fatal(err)
		}
		if err := r.CommitFile("app.go", "package app\n", "feat: start"); err != nil {
			cleanup()
			t.Fatal(err)
		}
		if err := r.Tag("v1.0.0"); err != nil {
			cleanup()
			t.Fatal(err)
		}
		if err := r.CommitFile("app.go", "package app2\n", "feat!: break the API"); err != nil {
			cleanup()
			t.Fatal(err)
		}
		return r, cleanup
	}

	t.Run("missing suffix", func(t *testing.T) {
		r, cleanup := setup("example.com/alfpricot/tool")
		defer cleanup()
		opts, _, _ := testOptions(r, nil)
		err := Run(opts, ModePlan)
		if err == nil {
			t.Fatal("a 2.0.0 release from a module without /v2 must fail at plan time")
		}
		mustContain(t, err.Error(), "/v2", "the error names the suffix")
		mustContain(t, err.Error(), "example.com/alfpricot/tool", "the error names the module path")
	})

	t.Run("suffix present", func(t *testing.T) {
		r, cleanup := setup("example.com/alfpricot/tool/v2")
		defer cleanup()
		opts, out, _ := testOptions(r, nil)
		if err := Run(opts, ModePlan); err != nil {
			t.Fatal(err)
		}
		mustContain(t, out.String(), "v2.0.0", "the plan proceeds")
	})
}

// TestChecks: release refuses a dirty tree and a non-release branch; plan
// works anywhere.
func TestChecks(t *testing.T) {
	t.Run("dirty tree", func(t *testing.T) {
		r, _, cleanup := fixture(t, appConfig)
		defer cleanup()
		if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
			t.Fatal(err)
		}
		if err := r.WriteFile("scratch.txt", "half-written\n"); err != nil {
			t.Fatal(err)
		}

		// plan works anyway (read-only, no checks)
		opts, _, _ := testOptions(r, nil)
		if err := Run(opts, ModePlan); err != nil {
			t.Errorf("plan must work on a dirty tree: %v", err)
		}

		opts2, _, _ := testOptions(r, nil)
		err := Run(opts2, ModeRelease)
		if err == nil {
			t.Fatal("release must refuse a dirty tree")
		}
		mustContain(t, err.Error(), "working tree is not clean", "message")
		mustContain(t, err.Error(), "scratch.txt", "the message lists the files")
	})

	t.Run("non-release branch", func(t *testing.T) {
		r, _, cleanup := fixture(t, appConfig)
		defer cleanup()
		if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
			t.Fatal(err)
		}
		if err := r.Checkout("feature"); err != nil {
			t.Fatal(err)
		}

		opts, _, _ := testOptions(r, nil)
		err := Run(opts, ModeRelease)
		if err == nil {
			t.Fatal("release must refuse a branch outside releaseBranches")
		}
		mustContain(t, err.Error(), "releaseBranches", "message names the config")

		// plan still works on that branch
		opts2, _, _ := testOptions(r, nil)
		if err := Run(opts2, ModePlan); err != nil {
			t.Errorf("plan must work anywhere: %v", err)
		}
	})

	t.Run("detached head names the fix", func(t *testing.T) {
		r, _, cleanup := fixture(t, appConfig)
		defer cleanup()
		if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
			t.Fatal(err)
		}
		sha, err := r.HeadSHA()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Run("checkout", "-q", "--detach", sha); err != nil {
			t.Fatal(err)
		}

		opts, _, _ := testOptions(r, nil)
		err = Run(opts, ModeRelease)
		if err == nil {
			t.Fatal("a detached HEAD must be an error")
		}
		mustContain(t, err.Error(), "git switch", "the error names the fix")
	})
}

// TestMissingTool: preflight degrades to a clear error naming the tool, before
// anything is written.
func TestMissingTool(t *testing.T) {
	cfg := strings.Replace(appConfig, `manager = "go"`, `manager = "npm"`, 1)
	r, _, cleanup := fixture(t, cfg)
	defer cleanup()

	if err := r.CommitFile("package.json", "{\"name\":\"gizmo\",\"version\":\"1.0.0\"}\n", "feat: first"); err != nil {
		t.Fatal(err)
	}

	opts, _, _ := testOptions(r, nil)
	opts.LookPath = func(name string) error {
		return os.ErrNotExist
	}
	err := Run(opts, ModeDryRun)
	if err == nil {
		t.Fatal("a missing npm must fail the preflight")
	}
	mustContain(t, err.Error(), "npm", "the error names the tool")
	mustContain(t, err.Error(), "PATH", "and where to put it")

	// Nothing was written.
	status, err := r.Run("status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(status) != "" {
		t.Errorf("a failed preflight must write nothing, status: %s", status)
	}
	if got := tags(t, r); got != "" {
		t.Errorf("a failed preflight must not tag, tags: %s", got)
	}
}

// TestMissingConfig: discovery failure is a clear error, not a panic.
func TestMissingConfig(t *testing.T) {
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := r.CommitFile("x.txt", "x\n", "chore: init"); err != nil {
		t.Fatal(err)
	}
	opts, _, _ := testOptions(r, nil)
	err = Run(opts, ModePlan)
	if err == nil {
		t.Fatal("a repo without gala.toml must error")
	}
	mustContain(t, err.Error(), "gala.toml", "the error names the file")
}

// TestConfigPathOverride: --config wins over discovery.
func TestConfigPathOverride(t *testing.T) {
	r, _, cleanup := fixture(t, appConfig)
	defer cleanup()
	if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
		t.Fatal(err)
	}
	alt := filepath.Join(r.Dir, "other.toml")
	if err := os.WriteFile(alt, []byte("kind = \"app\"\nmanager = \"go\"\nchangelog = \"NOTES.md\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts, _, _ := testOptions(r, nil)
	opts.ConfigPath = alt
	if err := Run(opts, ModePlan); err != nil {
		t.Fatalf("--config must override discovery: %v", err)
	}
}
