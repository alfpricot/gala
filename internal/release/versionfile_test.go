package release

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alfpricot/gala/internal/gittest"
)

// multiFileConfig is a repository that records its version in several files:
// cargo owns the manifest, package.json and app.json carry the same version,
// and a generated source file that embeds the version is regenerated after
// the bump.
const multiFileConfig = `kind = "app"
manager = "cargo"
publish = false
githubRelease = false
preRelease = ["make check"]
postBump = ["codegen --write"]
postBumpPaths = ["src/version.c", "src/meta.json"]

[[versionFile]]
path = "package.json"
key = "version"

[[versionFile]]
path = "app.json"
key = "metadata.version"
`

// multiFileFixture commits such a repository at 1.0.0, tags it, adds a
// releasable commit, and gives it a bare origin.
func multiFileFixture(t *testing.T, cfg string, files map[string]string) (*gittest.Repo, string, func()) {
	t.Helper()
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]string{
		"gala.toml":         cfg,
		"Cargo.toml":        "[package]\nname = \"widget\"\nversion = \"1.0.0\"\n",
		"Cargo.lock":        "version = 4\n",
		"package.json":      "{\n  \"name\": \"widget\",\n  \"version\": \"1.0.0\"\n}\n",
		"package-lock.json": "{\n  \"version\": \"1.0.0\"\n}\n",
		"app.json":          "{\r\n  \"targets\": [{ \"name\": \"x\" }],\r\n  \"metadata\": {\r\n    \"version\": \"1.0.0\"\r\n  }\r\n}\r\n",
		"src/version.c":     "/* 1.0.0 */\n",
	}
	for k, v := range files {
		base[k] = v
	}
	for path, content := range base {
		if content == "" {
			continue
		}
		if err := r.WriteFile(path, content); err != nil {
			cleanup()
			t.Fatal(err)
		}
	}
	if err := r.CommitAll("chore: scaffold"); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if err := r.Tag("v1.0.0"); err != nil {
		cleanup()
		t.Fatal(err)
	}
	bare, bareCleanup, err := r.BareRemote("origin", "main")
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	if err := r.CommitFile("spec.txt", "rule\n", "feat: a new rule"); err != nil {
		bareCleanup()
		cleanup()
		t.Fatal(err)
	}
	return r, bare, func() {
		bareCleanup()
		cleanup()
	}
}

// generateRunner stands in for cargo, npm, and a code generator; its
// "codegen --write" rewrites the generated files the way a real generator
// embeds the new version.
func generateRunner(r *gittest.Repo, write map[string]string) *captureRunner {
	return &captureRunner{respond: func(dir, name string, args []string) (string, error) {
		if name == "codegen" {
			for path, content := range write {
				if err := r.WriteFile(path, content); err != nil {
					return "", err
				}
			}
			return "generated\n", nil
		}
		return "", nil
	}}
}

func TestReleaseWritesVersionFilesAndRegenerates(t *testing.T) {
	r, bare, cleanup := multiFileFixture(t, multiFileConfig, nil)
	defer cleanup()

	exec := generateRunner(r, map[string]string{"src/version.c": "/* 1.1.0 */\n"})
	opts, out, warn := testOptions(r, exec)
	outputs := filepath.Join(t.TempDir(), "github_output")
	opts.GitHubOutput = outputs
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out.String())
	}

	var got []string
	for _, c := range exec.calls {
		got = append(got, c.name+" "+strings.Join(c.args, " "))
	}
	want := []string{
		"make check",
		"cargo update --workspace",
		"npm install --package-lock-only",
		"codegen --write",
	}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("argv = %v, want %v", got, want)
	}

	for path, needle := range map[string]string{
		"Cargo.toml":    `version = "1.1.0"`,
		"package.json":  `"version": "1.1.0"`,
		"app.json":      "    \"version\": \"1.1.0\"\r\n",
		"src/version.c": "1.1.0",
	} {
		content, err := r.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, content, needle, path)
	}

	committed, err := r.Run("show", "--name-only", "--format=", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"CHANGELOG.md", "Cargo.toml", "package.json", "app.json", "src/version.c"} {
		mustContain(t, committed, path, "the release commit")
	}
	status, err := r.Run("status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(status) != "" {
		t.Errorf("the release left the tree dirty:\n%s", status)
	}
	if sha, err := gittest.Ref(bare, "refs/tags/v1.1.0"); err != nil || sha == "" {
		t.Errorf("remote tag v1.1.0 missing: %v", err)
	}

	data, err := os.ReadFile(outputs)
	if err != nil {
		t.Fatal(err)
	}
	if want := "released=true\ntag=v1.1.0\nversion=1.1.0\ntags<<GALA_TAGS\nv1.1.0\nGALA_TAGS\n"; string(data) != want {
		t.Errorf("GITHUB_OUTPUT = %q, want %q", data, want)
	}
	mustContain(t, out.String(), "run     codegen --write", "the transcript names the hook")
	if warn.String() != "" {
		t.Errorf("unexpected warnings: %s", warn.String())
	}
}

func TestVersionFileMismatchFailsAtPlan(t *testing.T) {
	r, _, cleanup := multiFileFixture(t, multiFileConfig, map[string]string{
		"package.json": "{\n  \"name\": \"widget\",\n  \"version\": \"0.9.0\"\n}\n",
	})
	defer cleanup()

	opts, _, _ := testOptions(r, &captureRunner{})
	err := Run(opts, ModePlan)
	if err == nil {
		t.Fatal("a version file behind the manifest must fail the plan")
	}
	for _, want := range []string{"package.json", "0.9.0", "Cargo.toml", "1.0.0"} {
		mustContain(t, err.Error(), want, "the mismatch error")
	}
}

func TestPostBumpFailureWritesNothingRemote(t *testing.T) {
	r, bare, cleanup := multiFileFixture(t, multiFileConfig, nil)
	defer cleanup()
	before, err := gittest.RemoteRef(bare, "main")
	if err != nil {
		t.Fatal(err)
	}

	exec := &captureRunner{respond: func(_, name string, _ []string) (string, error) {
		if name == "codegen" {
			return "", errors.New("codegen --write failed: spec.txt: syntax error")
		}
		return "", nil
	}}
	opts, _, _ := testOptions(r, exec)
	err = Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("a failing postBump command must fail the release")
	}
	mustContain(t, err.Error(), "spec.txt: syntax error", "the hook output")
	mustContain(t, err.Error(), "nothing was committed, tagged, or pushed", "the recovery state")
	if got := tags(t, r); got != "v1.0.0" {
		t.Errorf("tags = %q, want only v1.0.0", got)
	}
	if after, err := gittest.RemoteRef(bare, "main"); err != nil || after != before {
		t.Errorf("remote main moved: %s → %s (%v)", before, after, err)
	}
}

func TestPostBumpChangeOutsidePathsIsAnError(t *testing.T) {
	r, _, cleanup := multiFileFixture(t, multiFileConfig, nil)
	defer cleanup()

	exec := generateRunner(r, map[string]string{
		"src/version.c": "/* 1.1.0 */\n",
		"src/scanner.c": "/* unexpected */\n",
	})
	opts, _, _ := testOptions(r, exec)
	err := Run(opts, ModeRelease)
	if err == nil {
		t.Fatal("a postBump change outside postBumpPaths must fail the release")
	}
	mustContain(t, err.Error(), "src/scanner.c", "the unexpected path")
	mustNotContain(t, err.Error(), "src/version.c", "an allowed path")
}

func TestPreReleaseMustLeaveTreeClean(t *testing.T) {
	r, _, cleanup := multiFileFixture(t, multiFileConfig, nil)
	defer cleanup()

	exec := &captureRunner{respond: func(_, name string, args []string) (string, error) {
		if name == "make" && len(args) > 0 && args[0] == "check" {
			return "", r.WriteFile("Cargo.toml", "[package]\nname = \"changed\"\nversion = \"1.0.0\"\n")
		}
		return "", nil
	}}
	opts, _, _ := testOptions(r, exec)
	err := Run(opts, ModeDryRun)
	if err == nil {
		t.Fatal("a preRelease command that edits a tracked file must fail")
	}
	mustContain(t, err.Error(), "Cargo.toml", "the changed path")
}

func TestDryRunListsVersionFilesAndHooks(t *testing.T) {
	r, _, cleanup := multiFileFixture(t, multiFileConfig, nil)
	defer cleanup()

	before := snapshot(t, r.Dir)
	opts, out, _ := testOptions(r, &captureRunner{})
	if err := Run(opts, ModeDryRun); err != nil {
		t.Fatal(err)
	}
	if diffs := diffMaps(before, snapshot(t, r.Dir)); len(diffs) > 0 {
		t.Errorf("dry run changed the working tree: %v", diffs)
	}
	got := out.String()
	mustContain(t, got, "package.json → 1.1.0, app.json → 1.1.0", "would write")
	mustContain(t, got, "would run     codegen --write", "would run")
	mustContain(t, got, "check   make check", "the preRelease check ran")
}

// TestVirtualWorkspaceRelease covers a Cargo virtual workspace whose members
// inherit one [workspace.package] version: one bump, one tag, one entry.
func TestVirtualWorkspaceRelease(t *testing.T) {
	cfg := "kind = \"app\"\nmanager = \"cargo\"\npublish = false\ngithubRelease = false\n\n" +
		"[[versionFile]]\npath = \"a/app.json\"\nkey = \"metadata.version\"\n"
	r, _, cleanup := multiFileFixture(t, cfg, map[string]string{
		"Cargo.toml":   "[workspace]\nresolver = \"3\"\nmembers = [\"a\", \"b\"]\n\n[workspace.package]\nversion = \"1.0.0\"\n",
		"a/Cargo.toml": "[package]\nname = \"widget-a\"\nversion.workspace = true\n",
		"b/Cargo.toml": "[package]\nname = \"widget-b\"\nversion.workspace = true\n",
		"a/app.json":   "{\"metadata\": {\"version\": \"1.0.0\"}}\n",
	})
	defer cleanup()

	opts, out, _ := testOptions(r, &captureRunner{})
	outputs := filepath.Join(t.TempDir(), "github_output")
	opts.GitHubOutput = outputs
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out.String())
	}
	if got := tags(t, r); got != "v1.0.0\nv1.1.0" {
		t.Errorf("tags = %q, want v1.0.0 and v1.1.0", got)
	}
	root, err := r.ReadFile("Cargo.toml")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, root, "[workspace.package]\nversion = \"1.1.0\"", "the shared version")
	sitter, err := r.ReadFile("a/app.json")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, sitter, `"version": "1.1.0"`, "the member metadata")

	// A second run has nothing due and says so to the workflow.
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outputs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "GALA_TAGS\nreleased=false\n") {
		t.Errorf("GITHUB_OUTPUT after a no-op run = %q", data)
	}
}

// TestMixedMonorepoRelease releases a repository whose packages use three
// ecosystems: each package bumps through its own manager, writes its own
// version files, runs its own hooks from its directory with its version in
// the environment, and publishes (or not) by its own setting.
func TestMixedMonorepoRelease(t *testing.T) {
	cfg := `kind = "monorepo"
manager = "cargo"
publish = true
githubRelease = false
postBump = ["make docs"]
postBumpPaths = ["docs/api.md"]

[[package]]
path = "crates/core"

[[package]]
path = "bindings/node"
manager = "npm"
postBump = ["npm run build"]
postBumpPaths = ["dist/*.js"]

[[package.versionFile]]
path = "jsr.json"
key = "version"

[[package]]
path = "bindings/python"
manager = "pypi"
publish = false

[[package.versionFile]]
path = "src/widget/__init__.py"
pattern = '__version__ = "([^"]+)"'
`
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for path, content := range map[string]string{
		"gala.toml":                              cfg,
		"Cargo.lock":                             "version = 4\n",
		"crates/core/Cargo.toml":                 "[package]\nname = \"core\"\nversion = \"1.0.0\"\n",
		"bindings/node/package.json":             "{\n  \"name\": \"widget-node\",\n  \"version\": \"2.0.0\"\n}\n",
		"bindings/node/jsr.json":                 "{\n  \"name\": \"@x/widget\",\n  \"version\": \"2.0.0\"\n}\n",
		"bindings/python/pyproject.toml":         "[project]\nname = \"widget-py\"\nversion = \"0.5.0\"\n",
		"bindings/python/uv.lock":                "version = 1\n",
		"bindings/python/src/widget/__init__.py": "__version__ = \"0.5.0\"\n",
	} {
		if err := r.WriteFile(path, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.CommitAll("chore: scaffold"); err != nil {
		t.Fatal(err)
	}
	bare, bareCleanup, err := r.BareRemote("origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer bareCleanup()
	for path, msg := range map[string]string{
		"crates/core/src/lib.rs":            "feat: core api",
		"bindings/node/index.js":            "fix: node binding",
		"bindings/python/src/widget/api.py": "feat: python api",
	} {
		if err := r.CommitFile(path, "x\n", msg); err != nil {
			t.Fatal(err)
		}
	}

	exec := &captureRunner{respond: func(dir, name string, args []string) (string, error) {
		switch {
		case name == "npm" && len(args) > 0 && args[0] == "whoami":
			return "fixture-user\n", nil
		case name == "npm" && len(args) > 0 && args[0] == "run":
			return "", r.WriteFile("bindings/node/dist/index.js", "built\n")
		case name == "make":
			return "", r.WriteFile("docs/api.md", "docs\n")
		}
		return "", nil
	}}
	opts, out, _ := testOptions(r, exec)
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out.String())
	}

	if got := tags(t, r); got != "core/v1.1.0\nwidget-node/v2.0.1\nwidget-py/v0.6.0" {
		t.Errorf("tags = %q", got)
	}
	if sha, err := gittest.Ref(bare, "refs/tags/widget-py/v0.6.0"); err != nil || sha == "" {
		t.Errorf("remote tag missing: %v", err)
	}

	// gala resolves the root through git, which can spell a Windows temp
	// directory differently from the fixture (long name versus 8.3 short name).
	top, err := r.Run("rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.FromSlash(strings.TrimSpace(top))
	var argv []string
	var nodeBuild, docs *call
	for i, c := range exec.calls {
		rel, _ := filepath.Rel(root, c.dir)
		argv = append(argv, filepath.ToSlash(rel)+": "+c.name+" "+strings.Join(c.args, " "))
		switch c.name {
		case "make":
			docs = &exec.calls[i]
		case "npm":
			if len(c.args) > 0 && c.args[0] == "run" {
				nodeBuild = &exec.calls[i]
			}
		}
	}
	want := []string{
		"bindings/node: npm whoami",
		"bindings/node: npm version 2.0.1 --no-git-tag-version",
		"crates/core: cargo update --workspace",
		"bindings/python: uv lock",
		"bindings/node: npm run build",
		".: make docs",
		"crates/core: cargo publish",
		"bindings/node: npm publish",
	}
	if strings.Join(argv, "\n") != strings.Join(want, "\n") {
		t.Errorf("argv =\n%s\nwant\n%s", strings.Join(argv, "\n"), strings.Join(want, "\n"))
	}
	if nodeBuild == nil || !slicesContain(nodeBuild.env, "GALA_VERSION=2.0.1") || !slicesContain(nodeBuild.env, "GALA_PACKAGE=widget-node") {
		t.Errorf("a package hook gets its own version: %+v", nodeBuild)
	}
	if docs == nil || !slicesContain(docs.env, "GALA_TAGS=core/v1.1.0 widget-node/v2.0.1 widget-py/v0.6.0") ||
		slicesContain(docs.env, "GALA_VERSION=1.1.0") {
		t.Errorf("a top-level hook in a multi-package release gets every tag and no single version: %+v", docs)
	}

	for path, needle := range map[string]string{
		"crates/core/Cargo.toml":                 `version = "1.1.0"`,
		"bindings/node/jsr.json":                 `"version": "2.0.1"`,
		"bindings/python/pyproject.toml":         `version = "0.6.0"`,
		"bindings/python/src/widget/__init__.py": `__version__ = "0.6.0"`,
	} {
		content, err := r.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, content, needle, path)
	}
	committed, err := r.Run("show", "--name-only", "--format=", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"bindings/node/jsr.json", "bindings/node/dist/index.js", "docs/api.md", "bindings/python/src/widget/__init__.py"} {
		mustContain(t, committed, path, "the release commit")
	}
}

func slicesContain(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestManagerNoneRelease covers an ecosystem gala has no adapter for: the
// version lives only in version files, the first of which supplies the
// starting version when there is no tag yet.
func TestManagerNoneRelease(t *testing.T) {
	cfg := `kind = "app"
manager = "none"
githubRelease = false

[[versionFile]]
path = "VERSION"
pattern = '^(\S+)'

[[versionFile]]
path = "chart/Chart.yaml"
pattern = '(?m)^version: (\S+)$'
`
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for path, content := range map[string]string{
		"gala.toml":        cfg,
		"VERSION":          "1.4.0\n",
		"chart/Chart.yaml": "apiVersion: v2\nname: widget\nversion: 1.4.0\nappVersion: \"3.1\"\n",
	} {
		if err := r.WriteFile(path, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.CommitAll("chore: scaffold"); err != nil {
		t.Fatal(err)
	}
	_, bareCleanup, err := r.BareRemote("origin", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer bareCleanup()
	if err := r.CommitFile("src/Widget.java", "class Widget {}\n", "feat: widgets"); err != nil {
		t.Fatal(err)
	}

	exec := &captureRunner{}
	opts, out, _ := testOptions(r, exec)
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out.String())
	}
	if got := tags(t, r); got != "v1.5.0" {
		t.Errorf("tags = %q, want v1.5.0 (adopted from VERSION, not 0.0.0)", got)
	}
	if len(exec.calls) != 0 {
		t.Errorf("manager none runs no tools: %v", exec.calls)
	}
	chart, err := r.ReadFile("chart/Chart.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if chart != "apiVersion: v2\nname: widget\nversion: 1.5.0\nappVersion: \"3.1\"\n" {
		t.Errorf("Chart.yaml = %q", chart)
	}
	if v, err := r.ReadFile("VERSION"); err != nil || v != "1.5.0\n" {
		t.Errorf("VERSION = %q, %v", v, err)
	}

	// publish = true has nothing to publish with, so the preflight refuses it.
	opts.NoPublish = false
	if err := r.WriteFile("gala.toml", strings.Replace(cfg, `githubRelease = false`, "githubRelease = false\npublish = true", 1)); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitAll("feat: publish"); err != nil {
		t.Fatal(err)
	}
	err = Run(opts, ModeDryRun)
	if err == nil || !strings.Contains(err.Error(), "cannot publish") {
		t.Errorf("manager none with publish = true must fail the preflight: %v", err)
	}
}

// scaffold commits files on main with a bare origin and returns the repo.
func scaffold(t *testing.T, files map[string]string) (*gittest.Repo, func()) {
	t.Helper()
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range files {
		if err := r.WriteFile(path, content); err != nil {
			cleanup()
			t.Fatal(err)
		}
	}
	if err := r.CommitAll("chore: scaffold"); err != nil {
		cleanup()
		t.Fatal(err)
	}
	_, bareCleanup, err := r.BareRemote("origin", "main")
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	return r, func() {
		bareCleanup()
		cleanup()
	}
}

func TestVersionFileThatIsAnotherPackagesManifestFails(t *testing.T) {
	cfg := "kind = \"monorepo\"\nmanager = \"cargo\"\ngithubRelease = false\n\n" +
		"[[package]]\npath = \"core\"\n\n[[package.versionFile]]\npath = \"../node/package.json\"\nkey = \"version\"\n\n" +
		"[[package]]\npath = \"node\"\nmanager = \"npm\"\n"
	r, cleanup := scaffold(t, map[string]string{
		"gala.toml":         cfg,
		"core/Cargo.toml":   "[package]\nname = \"core\"\nversion = \"1.0.0\"\n",
		"node/package.json": "{\n  \"name\": \"node\",\n  \"version\": \"1.0.0\"\n}\n",
	})
	defer cleanup()

	opts, _, _ := testOptions(r, &captureRunner{})
	err := Run(opts, ModePlan)
	if err == nil {
		t.Fatal("a version file that is another package's manifest must fail the plan")
	}
	mustContain(t, err.Error(), "is the manifest of package \"node\"", "the overlap error")
}

func TestAdoptedVersionFileMismatchNamesTheFile(t *testing.T) {
	cfg := "kind = \"app\"\nmanager = \"none\"\ngithubRelease = false\n\n" +
		"[[versionFile]]\npath = \"VERSION\"\npattern = '^(\\S+)'\n"
	r, cleanup := scaffold(t, map[string]string{"gala.toml": cfg, "VERSION": "1.1.0\n"})
	defer cleanup()
	if err := r.Tag("v1.2.0"); err != nil {
		t.Fatal(err)
	}

	opts, _, _ := testOptions(r, &captureRunner{})
	err := Run(opts, ModePlan)
	if err == nil {
		t.Fatal("a version file behind the latest tag must fail the plan")
	}
	mustContain(t, err.Error(), "versionFile VERSION records 1.1.0", "the version-file error")
	mustContain(t, err.Error(), "set it to 1.2.0", "the fix")
	mustNotContain(t, err.Error(), "manifest version", "the manifest drift wording")
}

func TestNothingDueWarnsOnUnwritableGitHubOutput(t *testing.T) {
	cfg := "kind = \"app\"\nmanager = \"none\"\ngithubRelease = false\n\n" +
		"[[versionFile]]\npath = \"VERSION\"\npattern = '^(\\S+)'\n"
	r, cleanup := scaffold(t, map[string]string{"gala.toml": cfg, "VERSION": "1.0.0\n"})
	defer cleanup()
	if err := r.Tag("v1.0.0"); err != nil {
		t.Fatal(err)
	}

	opts, _, warn := testOptions(r, &captureRunner{})
	opts.GitHubOutput = filepath.Join(t.TempDir(), "missing", "dir", "output")
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("nothing to release must stay exit 0: %v", err)
	}
	mustContain(t, warn.String(), "GITHUB_OUTPUT", "the warning")
}

// TestVersionFileRootLockRefreshedBesideNestedProject covers a root uv.lock
// that belongs to a different project from a package's own uv.lock: the
// package's `uv lock` rewrites only its own lock, so the root version file's
// lock still gets its own refresh.
func TestVersionFileRootLockRefreshedBesideNestedProject(t *testing.T) {
	cfg := "kind = \"monorepo\"\nmanager = \"pypi\"\ngithubRelease = false\n\n" +
		"[[package]]\npath = \"packages/a\"\n\n" +
		"[[package]]\npath = \"tools\"\nname = \"tools\"\nmanager = \"none\"\n\n" +
		"[[package.versionFile]]\npath = \"../pyproject.toml\"\nkey = \"project.version\"\n"
	r, cleanup := scaffold(t, map[string]string{
		"gala.toml":                 cfg,
		"pyproject.toml":            "[project]\nname = \"root\"\nversion = \"3.0.0\"\n",
		"uv.lock":                   "version = 1\n",
		"packages/a/pyproject.toml": "[project]\nname = \"a\"\nversion = \"1.0.0\"\n",
		"packages/a/uv.lock":        "version = 1\n",
		"tools/README.md":           "tools\n",
	})
	defer cleanup()
	for path, msg := range map[string]string{
		"packages/a/x.py": "feat: a",
		"tools/run.sh":    "feat: tools",
	} {
		if err := r.CommitFile(path, "x\n", msg); err != nil {
			t.Fatal(err)
		}
	}

	exec := &captureRunner{}
	opts, out, _ := testOptions(r, exec)
	if err := Run(opts, ModeRelease); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out.String())
	}
	top, err := r.Run("rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.FromSlash(strings.TrimSpace(top))
	var uvDirs []string
	for _, c := range exec.calls {
		if c.name == "uv" && len(c.args) > 0 && c.args[0] == "lock" {
			rel, _ := filepath.Rel(root, c.dir)
			uvDirs = append(uvDirs, filepath.ToSlash(rel))
		}
	}
	if strings.Join(uvDirs, ",") != "packages/a,." {
		t.Errorf("uv lock ran in %v, want packages/a then the root", uvDirs)
	}
}
