package manager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alfpricot/gala/internal/semver"
)

// call records one captured invocation.
type call struct {
	dir  string
	name string
	args []string
}

// captureRunner stands in for a package-manager binary: argv is asserted, the
// binary never runs.
type captureRunner struct {
	calls   []call
	respond func(dir, name string, args []string) (string, error)
}

func (c *captureRunner) Run(dir, name string, args ...string) (string, error) {
	c.calls = append(c.calls, call{dir: dir, name: name, args: append([]string(nil), args...)})
	if c.respond != nil {
		return c.respond(dir, name, args)
	}
	return "", nil
}

func (c *captureRunner) argvs() []string {
	var out []string
	for _, cl := range c.calls {
		out = append(out, cl.name+" "+strings.Join(cl.args, " "))
	}
	return out
}

func tempWith(t *testing.T, rel, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestNpmArgv(t *testing.T) {
	dir := tempWith(t, "package.json", `{"name":"gizmo","version":"1.0.0"}`+"\n")
	r := &captureRunner{respond: func(_, _ string, args []string) (string, error) {
		if args[0] == "whoami" {
			return "fixture-user\n", nil
		}
		return "", nil
	}}
	n, err := New("npm", r)
	if err != nil {
		t.Fatal(err)
	}

	if err := n.SetVersion(dir, semver.MustParse("1.1.0")); err != nil {
		t.Fatal(err)
	}
	if err := n.Publish(dir); err != nil {
		t.Fatal(err)
	}
	if err := n.Preflight(dir, true); err != nil {
		t.Fatal(err)
	}
	// Publishing off means no registry contact at all.
	if err := n.Preflight(dir, false); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"npm version 1.1.0 --no-git-tag-version",
		"npm publish",
		"npm whoami",
	}
	got := r.argvs()
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if r.calls[0].dir != dir {
		t.Errorf("npm version ran in %q, want the package dir", r.calls[0].dir)
	}
}

func TestNpmWhoamiFailure(t *testing.T) {
	dir := tempWith(t, "package.json", `{"name":"gizmo","version":"1.0.0"}`)
	r := &captureRunner{respond: func(string, string, []string) (string, error) {
		return "", errors.New("ENEEDAUTH")
	}}
	n, _ := New("npm", r)
	err := n.Preflight(dir, true)
	if err == nil {
		t.Fatal("an unauthenticated npm must fail the preflight, before the tag exists")
	}
	if !strings.Contains(err.Error(), "npm login") {
		t.Errorf("error should name the fix: %v", err)
	}
}

func TestNpmManifestRead(t *testing.T) {
	dir := tempWith(t, "package.json", `{"name":"gizmo","version":"2.3.4"}`)
	n, _ := New("npm", &captureRunner{})
	v, ok, err := n.ManifestVersion(dir)
	if err != nil || !ok {
		t.Fatalf("ManifestVersion = %v, %v, %v", v, ok, err)
	}
	if v.String() != "2.3.4" {
		t.Errorf("version = %s, want 2.3.4", v)
	}
	name, err := n.PackageName(dir)
	if err != nil || name != "gizmo" {
		t.Errorf("PackageName = %q, %v", name, err)
	}
	if got := n.Manifest(dir, "/repo"); !strings.HasSuffix(got, "package.json") {
		t.Errorf("Manifest = %q", got)
	}
}

func TestNpmManifestWithoutVersion(t *testing.T) {
	dir := tempWith(t, "package.json", `{"name":"gizmo"}`)
	n, _ := New("npm", &captureRunner{})
	if _, _, err := n.ManifestVersion(dir); err == nil {
		t.Fatal("a package.json without a version must be an error")
	}
}

// TestNpmMonorepoLockRefresh covers the plan's monorepo rule: a root
// `npm install --package-lock-only` records the new member versions, and the
// lockfile is staged.
func TestNpmMonorepoLockRefresh(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "packages", "core")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}

	r := &captureRunner{}
	n, _ := New("npm", r)
	locks, err := n.RefreshLock(pkg, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || locks[0] != "package-lock.json" {
		t.Errorf("locks = %v, want [package-lock.json]", locks)
	}
	got := r.argvs()
	if len(got) != 1 || got[0] != "npm install --package-lock-only" {
		t.Errorf("argv = %v", got)
	}
	if r.calls[0].dir != root {
		t.Errorf("the lock refresh must run at the repo root, ran in %q", r.calls[0].dir)
	}
}

func TestNpmSinglePackageLeavesLockToNpm(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package-lock.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &captureRunner{}
	n, _ := New("npm", r)
	locks, err := n.RefreshLock(root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 {
		t.Errorf("the single-package lock is still staged: %v", locks)
	}
	if len(r.calls) != 0 {
		t.Errorf("`npm version` keeps the single-package lock in step; no extra spawn wanted: %v", r.argvs())
	}
}

func TestCargoArgv(t *testing.T) {
	dir := tempWith(t, "Cargo.toml", "[package]\nname = \"scarf\"\nversion = \"0.1.0\"\n")
	if err := os.WriteFile(filepath.Join(dir, "Cargo.lock"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &captureRunner{}
	c, _ := New("cargo", r)
	locks, err := c.RefreshLock(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || locks[0] != "Cargo.lock" {
		t.Errorf("locks = %v", locks)
	}
	if got := r.argvs(); len(got) != 1 || got[0] != "cargo update --workspace" {
		t.Errorf("argv = %v", got)
	}

	// Without a lockfile nothing runs and nothing is staged.
	idle := &captureRunner{}
	c2, _ := New("cargo", idle)
	emptyDir := tempWith(t, "Cargo.toml", "[package]\nname = \"s\"\nversion = \"0.1.0\"\n")
	locks, err = c2.RefreshLock(emptyDir, emptyDir)
	if err != nil || len(locks) != 0 || len(idle.calls) != 0 {
		t.Errorf("no lockfile: locks=%v calls=%v err=%v", locks, idle.calls, err)
	}
}

func TestCargoSetVersionPreservesBytes(t *testing.T) {
	dir := tempWith(t, "Cargo.toml", strings.Join([]string{
		"# hand-maintained manifest",
		"[package]",
		"name = \"scarf\"",
		"version = \"0.1.0\"     # keep this comment",
		"edition = \"2021\"",
		"",
		"[dependencies]",
		"# pinned deliberately",
		"serde = \"1\"",
		"",
	}, "\n"))
	c, _ := New("cargo", &captureRunner{})
	if err := c.SetVersion(dir, semver.MustParse("0.2.0")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"# hand-maintained manifest",
		"[package]",
		"name = \"scarf\"",
		"version = \"0.2.0\"     # keep this comment",
		"edition = \"2021\"",
		"",
		"[dependencies]",
		"# pinned deliberately",
		"serde = \"1\"",
		"",
	}, "\n")
	if string(data) != want {
		t.Errorf("byte-exact write failed:\n--- got ---\n%s\n--- want ---\n%s", data, want)
	}
}

func TestCargoVersionWorkspaceRefused(t *testing.T) {
	root := tempWith(t, "workspace/Cargo.toml", "[package]\nname = \"scarf\"\nversion.workspace = true\n")
	dir := filepath.Join(root, "workspace")
	c, _ := New("cargo", &captureRunner{})
	_, _, err := c.ManifestVersion(dir)
	if err == nil {
		t.Fatal("version.workspace = true must be refused before any write")
	}
	if !strings.Contains(err.Error(), "version.workspace") || !strings.Contains(err.Error(), "Cargo.toml") {
		t.Errorf("error must name the file and the key: %v", err)
	}
}

func TestPypiArgvAndTextEdit(t *testing.T) {
	dir := tempWith(t, "pyproject.toml", strings.Join([]string{
		"[project]",
		"name = \"gizmo\"",
		"version = \"1.0.0\"",
		"dynamic = [\"classifiers\"]",
		"",
		"[tool.uv]",
		"package = true",
		"",
	}, "\n"))
	if err := os.WriteFile(filepath.Join(dir, "uv.lock"), []byte("version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &captureRunner{}
	p, _ := New("pypi", r)

	v, ok, err := p.ManifestVersion(dir)
	if err != nil || !ok || v.String() != "1.0.0" {
		t.Fatalf("ManifestVersion = %v %v %v", v, ok, err)
	}
	if err := p.SetVersion(dir, semver.MustParse("1.1.0")); err != nil {
		t.Fatal(err)
	}
	locks, err := p.RefreshLock(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Publish(dir); err != nil {
		t.Fatal(err)
	}

	got := r.argvs()
	want := []string{"uv lock", "uv build", "uv publish"}
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("argv = %v, want %v", got, want)
	}
	if len(locks) != 1 || locks[0] != "uv.lock" {
		t.Errorf("locks = %v", locks)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "pyproject.toml"))
	if !strings.Contains(string(data), `version = "1.1.0"`) {
		t.Errorf("version not written:\n%s", data)
	}
	if !strings.Contains(string(data), `dynamic = ["classifiers"]`) {
		t.Errorf("an unrelated key changed:\n%s", data)
	}
}

func TestPypiDynamicVersionRefused(t *testing.T) {
	dir := tempWith(t, "pyproject.toml", "[project]\nname = \"gizmo\"\ndynamic = [\"version\"]\n")
	p, _ := New("pypi", &captureRunner{})
	_, _, err := p.ManifestVersion(dir)
	if err == nil {
		t.Fatal("dynamic versioning must fail before the tag")
	}
	if !strings.Contains(err.Error(), "no static [project] version") || !strings.Contains(err.Error(), "pyproject.toml") {
		t.Errorf("error should explain and name the file: %v", err)
	}
}

func TestGoManager(t *testing.T) {
	dir := tempWith(t, "go.mod", "module example.com/alfpricot/gala\n\ngo 1.27\n")
	g, _ := New("go", &captureRunner{})

	if _, ok, err := g.ManifestVersion(dir); ok || err != nil {
		t.Errorf("go manifests carry no version: ok=%v err=%v", ok, err)
	}
	if m := g.Manifest(dir, dir); m != "" {
		t.Errorf("Manifest = %q, want empty (nothing to stage)", m)
	}
	if len(g.Tools()) != 0 {
		t.Errorf("a go release runs git only, tools = %v", g.Tools())
	}
	if err := g.Publish(dir); err != nil {
		t.Errorf("publishing a go module is pushing the tag: %v", err)
	}
	name, err := g.PackageName(dir)
	if err != nil || name != "gala" {
		t.Errorf("PackageName = %q, %v", name, err)
	}

	// /vN rule: major >= 2 needs the suffix in the module path.
	if err := g.CheckVersion(dir, semver.MustParse("1.9.0")); err != nil {
		t.Errorf("major 1 needs no suffix: %v", err)
	}
	err = g.CheckVersion(dir, semver.MustParse("2.0.0"))
	if err == nil {
		t.Fatal("module path without /v2 must be refused for a 2.0.0 release")
	}
	if !strings.Contains(err.Error(), "/v2") {
		t.Errorf("error must name the suffix: %v", err)
	}

	dirV2 := tempWith(t, "go.mod", "module example.com/alfpricot/gala/v2\n\ngo 1.27\n")
	if err := g.CheckVersion(dirV2, semver.MustParse("2.0.0")); err != nil {
		t.Errorf("/v2 module should release 2.0.0: %v", err)
	}
	name, err = g.PackageName(dirV2)
	if err != nil || name != "gala" {
		t.Errorf("PackageName for /v2 module = %q, %v (want the name without the suffix)", name, err)
	}
}

func TestRecoveryCommands(t *testing.T) {
	n, _ := New("npm", &captureRunner{})
	if got := n.Recovery("/repo/packages/core", "/repo"); got != "cd packages/core && npm publish" {
		t.Errorf("npm recovery = %q", got)
	}
	c, _ := New("cargo", &captureRunner{})
	if got := c.Recovery("/repo", "/repo"); got != "cd . && cargo publish" {
		t.Errorf("cargo recovery = %q", got)
	}
	g, _ := New("go", &captureRunner{})
	if got := g.Recovery("/repo", "/repo"); got != "" {
		t.Errorf("go has nothing to recover: %q", got)
	}
}
