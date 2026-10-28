package manager

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alfpricot/gala/internal/config"
	"github.com/alfpricot/gala/internal/semver"
)

// A nested shape: the version sits under metadata, after an array of objects
// that also carry a "version" string, so a naive search would stop at the
// wrong key.
const nestedJSON = "{\r\n" +
	"  \"targets\": [\r\n" +
	"    { \"name\": \"x\", \"version\": \"9.9.9\", \"file-types\": [\"x\", \"y\\\"z\"] }\r\n" +
	"  ],\r\n" +
	"  \"metadata\" :  {\r\n" +
	"    \"license\": \"MIT\",\r\n" +
	"    \"version\":\"0.1.1\" ,\r\n" +
	"    \"n\": 1.5e3, \"t\": true, \"z\": null\r\n" +
	"  }\r\n" +
	"}\r\n"

func TestSetJSONStringPreservesBytes(t *testing.T) {
	got, err := SetJSONString([]byte(nestedJSON), "metadata.version", "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(nestedJSON, `"version":"0.1.1"`, `"version":"0.2.0"`, 1)
	if string(got) != want {
		t.Errorf("byte-exact write failed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	v, err := GetJSONString(got, "metadata.version")
	if err != nil || v != "0.2.0" {
		t.Errorf("read back = %q, %v", v, err)
	}
}

func TestSetJSONStringTopLevelWithBOM(t *testing.T) {
	in := "\xef\xbb\xbf{\n\t\"name\": \"a\",\n\t\"version\": \"1.0.0\"\n}"
	got, err := SetJSONString([]byte(in), "version", "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(in, "1.0.0", "1.1.0", 1); string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestJSONStringErrors(t *testing.T) {
	cases := []struct {
		content, key, want string
	}{
		{`{"metadata": {}}`, "metadata.version", "key not found"},
		{`{"version": 1}`, "version", "not a string"},
		{`{"metadata": "x"}`, "metadata.version", "not an object"},
		{`{"version": "1.0.0",}`, "version", "not valid JSON"},
	}
	for _, tc := range cases {
		_, err := SetJSONString([]byte(tc.content), tc.key, "2.0.0")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s in %s: error = %v, want %q", tc.key, tc.content, err, tc.want)
		}
	}
	if _, err := GetJSONString([]byte(`{"a": {}}`), "a.version"); !errors.Is(err, ErrKey) {
		t.Errorf("a missing key must wrap ErrKey: %v", err)
	}
}

func TestVersionFileReadWrite(t *testing.T) {
	root := tempWith(t, "app.json", nestedJSON)
	cargo := "[package]\r\nname = \"handlers\" # x\r\nversion = \"0.1.1\" # kept\r\n\r\n[dependencies]\r\nversion = \"9\"\r\n"
	if err := os.MkdirAll(filepath.Join(root, "handlers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "handlers", "Cargo.toml"), []byte(cargo), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []config.VersionFile{
		{Path: "app.json", Key: "metadata.version"},
		{Path: "handlers/Cargo.toml", Key: "package.version"},
	}
	next := semver.Version{Major: 0, Minor: 2, Patch: 0}
	for _, vf := range files {
		v, err := ReadVersionFile(root, vf)
		if err != nil || v.String() != "0.1.1" {
			t.Fatalf("%s: read %v, %v", vf.Path, v, err)
		}
		if err := WriteVersionFile(root, vf, next); err != nil {
			t.Fatal(err)
		}
		if v, err := ReadVersionFile(root, vf); err != nil || v.String() != "0.2.0" {
			t.Errorf("%s: read back %v, %v", vf.Path, v, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "handlers", "Cargo.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(cargo, `"0.1.1" # kept`, `"0.2.0" # kept`, 1); string(data) != want {
		t.Errorf("TOML write not byte-exact:\n%q\nwant\n%q", data, want)
	}

	if _, err := ReadVersionFile(root, config.VersionFile{Path: "handlers/Cargo.toml", Key: "lib.version"}); err == nil {
		t.Error("a missing TOML table must be an error")
	}
}

func TestVersionFileLocks(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"package.json", "package-lock.json", // npm workspace root
		"a/package.json", "a/package-lock.json", // member with its own lock too
		"b/package.json",                    // member recorded only at the root
		"Cargo.lock", "crates/x/Cargo.toml", // Cargo workspace member
		"py/pyproject.toml", "py/uv.lock", // uv project with its own lock
		"c/package.json", // a pattern-addressed package.json is not a manifest edit
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files := []config.VersionFile{
		{Path: "a/package.json", Key: "version"},
		{Path: "b/package.json", Key: "version"},
		{Path: "crates/x/Cargo.toml", Key: "package.version"},
		{Path: "py/pyproject.toml", Key: "project.version"},
		{Path: "c/package.json", Pattern: `"version": "([^"]+)"`},
		{Path: "app.json", Key: "metadata.version"},
	}
	cmds := VersionFileLocks(root, files)
	var got []string
	for _, c := range cmds {
		got = append(got, c.Dir+": "+c.Name+" "+strings.Join(c.Args, " ")+" → "+c.Lock)
	}
	want := []string{
		"a: npm install --package-lock-only → a/package-lock.json",
		".: npm install --package-lock-only → package-lock.json",
		".: cargo update --workspace → Cargo.lock",
		"py: uv lock → py/uv.lock",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lock commands =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	r := &captureRunner{}
	locks, err := RunLockCommands(r, root, cmds, map[string]bool{"Cargo.lock": true})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a/package-lock.json", "package-lock.json", "Cargo.lock", "py/uv.lock"}; !reflect.DeepEqual(locks, want) {
		t.Errorf("locks to stage = %v, want %v", locks, want)
	}
	if want := []string{"npm install --package-lock-only", "npm install --package-lock-only", "uv lock"}; !reflect.DeepEqual(r.argvs(), want) {
		t.Errorf("a lockfile already refreshed must not run again: %v", r.argvs())
	}
}

func TestPatternVersionFile(t *testing.T) {
	const py = "\"\"\"Widget 1.4.0.\"\"\"\r\n__version__ = \"1.4.0\"\r\nVERSION_INFO = (1, 4, 0)\r\n"
	root := tempWith(t, "src/widget/__init__.py", py)
	vf := config.VersionFile{Path: "src/widget/__init__.py", Pattern: `(\d+\.\d+\.\d+)`}
	// Two string mentions agree; the tuple does not match the pattern.
	if v, err := ReadVersionFile(root, vf); err != nil || v.String() != "1.4.0" {
		t.Fatalf("read = %v, %v", v, err)
	}
	if err := WriteVersionFile(root, vf, semver.Version{Major: 1, Minor: 5}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "src", "widget", "__init__.py"))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.ReplaceAll(py, "1.4.0", "1.5.0"); string(data) != want {
		t.Errorf("pattern write:\n%q\nwant\n%q", data, want)
	}

	chart := tempWith(t, "Chart.yaml", "apiVersion: v2\nversion: 0.3.0\nappVersion: \"0.9.1\"\n")
	if _, err := ReadVersionFile(chart, config.VersionFile{Path: "Chart.yaml", Pattern: `(?i)version: (\S+)`}); err == nil ||
		!strings.Contains(err.Error(), "disagree") {
		t.Errorf("matches that disagree must be an error, not a guess: %v", err)
	}
	if v, err := ReadVersionFile(chart, config.VersionFile{Path: "Chart.yaml", Pattern: `(?m)^version: (\S+)$`}); err != nil || v.String() != "0.3.0" {
		t.Errorf("anchored pattern = %v, %v", v, err)
	}
	if _, err := ReadVersionFile(chart, config.VersionFile{Path: "Chart.yaml", Pattern: `release: (\S+)`}); err == nil ||
		!strings.Contains(err.Error(), "matches nothing") {
		t.Errorf("a pattern that matches nothing must be an error: %v", err)
	}
}

func TestPypiPoetryVersion(t *testing.T) {
	content := "[tool.poetry]\nname = \"widget\"\nversion = \"2.0.0\"\n\n[tool.poetry.dependencies]\npython = \"^3.12\"\n"
	root := tempWith(t, "pyproject.toml", content)
	p, _ := New("pypi", &captureRunner{})
	v, ok, err := p.ManifestVersion(root)
	if err != nil || !ok || v.String() != "2.0.0" {
		t.Fatalf("ManifestVersion = %v, %v, %v", v, ok, err)
	}
	if name, err := p.PackageName(root); err != nil || name != "widget" {
		t.Errorf("PackageName = %q, %v", name, err)
	}
	if err := p.SetVersion(root, semver.Version{Major: 2, Minor: 1}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "pyproject.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(content, "2.0.0", "2.1.0", 1); string(data) != want {
		t.Errorf("poetry write:\n%s\nwant\n%s", data, want)
	}
}

func TestCargoWorkspacePackageVersion(t *testing.T) {
	cases := map[string]string{
		"virtual workspace": "[workspace]\nresolver = \"3\"\nmembers = [\"a\", \"b\"]\n\n[workspace.package]\nversion = \"0.1.1\" # shared\nedition = \"2024\"\n",
		"inheriting root":   "[package]\nname = \"root\"\nversion.workspace = true\n\n[workspace]\nmembers = [\"a\"]\n\n[workspace.package]\nversion = \"0.1.1\" # shared\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			root := tempWith(t, "Cargo.toml", content)
			c, _ := New("cargo", &captureRunner{})
			v, ok, err := c.ManifestVersion(root)
			if err != nil || !ok || v.String() != "0.1.1" {
				t.Fatalf("ManifestVersion = %v, %v, %v", v, ok, err)
			}
			if err := c.SetVersion(root, semver.Version{Minor: 2}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.Replace(content, `"0.1.1" # shared`, `"0.2.0" # shared`, 1); string(data) != want {
				t.Errorf("workspace write not byte-exact:\n%s\nwant\n%s", data, want)
			}
		})
	}

	root := tempWith(t, "Cargo.toml", "[workspace]\nmembers = [\"a\"]\n")
	c, _ := New("cargo", &captureRunner{})
	if _, _, err := c.ManifestVersion(root); err == nil || !strings.Contains(err.Error(), "[workspace.package]") {
		t.Errorf("a virtual workspace without a shared version must say where gala looked: %v", err)
	}
}
