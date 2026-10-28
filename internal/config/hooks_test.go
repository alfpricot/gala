package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestVersionFilesAndHooks(t *testing.T) {
	cfg, err := load(t, `kind = "app"
manager = "cargo"
preRelease = ["make check"]
postBump = ["npm run generate", "node 'tools/a b.mjs'"]
postBumpPaths = ["src/version.c", "*/src/*.json"]

[[versionFile]]
path = "./package.json"
key = "version"

[[versionFile]]
path = "app.json"
key = "metadata.version"

[[versionFile]]
path = "handlers/Cargo.toml"
key = "package.version"
`)
	if err != nil {
		t.Fatal(err)
	}
	want := []VersionFile{
		{Path: "package.json", Key: "version"},
		{Path: "app.json", Key: "metadata.version"},
		{Path: "handlers/Cargo.toml", Key: "package.version"},
	}
	if !reflect.DeepEqual(cfg.VersionFiles, want) {
		t.Errorf("versionFiles = %v, want %v", cfg.VersionFiles, want)
	}
	if len(cfg.PreRelease) != 1 || !reflect.DeepEqual(cfg.PreRelease[0].Argv, []string{"make", "check"}) {
		t.Errorf("preRelease = %v", cfg.PreRelease)
	}
	if len(cfg.PostBump) != 2 || !reflect.DeepEqual(cfg.PostBump[1].Argv, []string{"node", "tools/a b.mjs"}) {
		t.Errorf("postBump = %v", cfg.PostBump)
	}
	for p, want := range map[string]bool{
		"src/version.c":       true,
		"b/src/meta.json":     true,
		"src/main.c":          false,
		"b/src/nested/x.json": false,
		"src/version.c/x":     true, // an entry also covers files under it
	} {
		if got := cfg.MatchesPostBumpPath(p); got != want {
			t.Errorf("MatchesPostBumpPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestVersionFileAndHookErrors(t *testing.T) {
	const app = "kind = \"app\"\nmanager = \"cargo\"\n"
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"monorepo", "kind = \"monorepo\"\nmanager = \"cargo\"\n\n[[package]]\npath = \"a\"\n\n[[versionFile]]\npath = \"x.json\"\nkey = \"version\"\n", "only valid for kind = \"app\""},
		{"missing path", app + "\n[[versionFile]]\nkey = \"version\"\n", "missing the required key \"path\""},
		{"missing key", app + "\n[[versionFile]]\npath = \"x.json\"\n", "needs \"key\""},
		{"key and pattern", app + "\n[[versionFile]]\npath = \"x.json\"\nkey = \"version\"\npattern = \"(.*)\"\n", "not both"},
		{"pattern without group", app + "\n[[versionFile]]\npath = \"VERSION\"\npattern = \".*\"\n", "exactly one capture group"},
		{"pattern with two groups", app + "\n[[versionFile]]\npath = \"VERSION\"\npattern = \"(a)(b)\"\n", "exactly one capture group"},
		{"invalid pattern", app + "\n[[versionFile]]\npath = \"VERSION\"\npattern = \"(\"\n", "invalid pattern"},
		{"bad package manager", "kind = \"monorepo\"\nmanager = \"npm\"\n\n[[package]]\npath = \"a\"\nmanager = \"maven\"\n", "invalid manager \"maven\""},
		{"monorepo missing a manager", "kind = \"monorepo\"\n\n[[package]]\npath = \"a\"\nmanager = \"npm\"\n\n[[package]]\npath = \"b\"\n", "missing required key \"manager\""},
		{"shared version file", "kind = \"monorepo\"\nmanager = \"npm\"\n\n[[package]]\npath = \"a\"\n\n[[package.versionFile]]\npath = \"../v.json\"\nkey = \"version\"\n\n[[package]]\npath = \"b\"\n\n[[package.versionFile]]\npath = \"../v.json\"\nkey = \"version\"\n", "listed by both"},
		{"package hook paths without hook", "kind = \"monorepo\"\nmanager = \"npm\"\n\n[[package]]\npath = \"a\"\npostBumpPaths = [\"x\"]\n", "package \"a\" postBumpPaths needs"},
		{"package version file escaping", "kind = \"monorepo\"\nmanager = \"npm\"\n\n[[package]]\npath = \"a\"\n\n[[package.versionFile]]\npath = \"../../x.json\"\nkey = \"version\"\n", "inside the repository"},
		{"escaping", app + "\n[[versionFile]]\npath = \"../x.json\"\nkey = \"version\"\n", "inside the repository"},
		{"duplicate", app + "\n[[versionFile]]\npath = \"x.json\"\nkey = \"version\"\n\n[[versionFile]]\npath = \"./x.json\"\nkey = \"version\"\n", "duplicate versionFile"},
		{"bad extension", app + "\n[[versionFile]]\npath = \"VERSION\"\nkey = \"version\"\n", "use \"pattern\" for any other format"},
		{"toml without table", app + "\n[[versionFile]]\npath = \"a/Cargo.toml\"\nkey = \"version\"\n", "needs its table"},
		{"empty key segment", app + "\n[[versionFile]]\npath = \"x.json\"\nkey = \"metadata..version\"\n", "invalid key"},
		{"unknown versionFile key", app + "\n[[versionFile]]\npath = \"x.json\"\nkey = \"version\"\nformat = \"json\"\n", "unknown key"},
		{"empty command", app + "postBump = [\"  \"]\n", "empty command"},
		{"unterminated quote", app + "preRelease = [\"node 'x\"]\n", "unterminated"},
		{"paths without hook", app + "postBumpPaths = [\"src/version.c\"]\n", "needs a postBump command"},
		{"escaping hook path", app + "postBump = [\"make\"]\npostBumpPaths = [\"../x\"]\n", "inside the repository"},
		{"bad pattern", app + "postBump = [\"make\"]\npostBumpPaths = [\"src/[\"]\n", "not a valid pattern"},
		{"root-relative versionFile", app + "\n[[versionFile]]\npath = \"/VERSION\"\npattern = \"(.*)\"\n", "inside the repository"},
		{"drive versionFile", app + "\n[[versionFile]]\npath = \"C:VERSION\"\npattern = \"(.*)\"\n", "inside the repository"},
		{"root-relative postBumpPaths", app + "postBump = [\"make\"]\npostBumpPaths = [\"/dist/*\"]\n", "inside the repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(t, tc.content)
			if err == nil {
				t.Fatalf("expected an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestSplitCommand(t *testing.T) {
	cases := map[string][]string{
		"npm run generate":           {"npm", "run", "generate"},
		"  a\t b  ":                  {"a", "b"},
		`node "tools/x y.mjs" --a=1`: {"node", "tools/x y.mjs", "--a=1"},
		`echo ''`:                    {"echo", ""},
		`pre"fix x"post`:             {"prefix xpost"},
		"":                           nil,
	}
	for line, want := range cases {
		got, err := SplitCommand(line)
		if err != nil {
			t.Errorf("SplitCommand(%q): %v", line, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("SplitCommand(%q) = %q, want %q", line, got, want)
		}
	}
}

// TestMixedMonorepo covers a repository whose packages use different
// ecosystems: each package can name its manager and publish setting, list its
// own version files and hooks, and have its paths resolved from its directory.
func TestMixedMonorepo(t *testing.T) {
	cfg, err := load(t, `kind = "monorepo"
manager = "cargo"
publish = true
postBump = ["make docs"]

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
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Packages) != 3 {
		t.Fatalf("packages = %d", len(cfg.Packages))
	}
	core, node, py := cfg.Packages[0], cfg.Packages[1], cfg.Packages[2]
	if core.Manager != ManagerCargo || !core.Publish {
		t.Errorf("core inherits the top level: %s publish=%v", core.Manager, core.Publish)
	}
	if node.Manager != ManagerNpm || !node.Publish {
		t.Errorf("node = %s publish=%v", node.Manager, node.Publish)
	}
	if py.Manager != ManagerPyPI || py.Publish {
		t.Errorf("python = %s publish=%v, want pypi and an explicit false", py.Manager, py.Publish)
	}
	if want := []VersionFile{{Path: "bindings/node/jsr.json", Key: "version"}}; !reflect.DeepEqual(node.VersionFiles, want) {
		t.Errorf("node version files = %v", node.VersionFiles)
	}
	if want := "bindings/python/src/widget/__init__.py"; len(py.VersionFiles) != 1 || py.VersionFiles[0].Path != want {
		t.Errorf("python version files = %v", py.VersionFiles)
	}
	if !node.MatchesPostBumpPath("bindings/node/dist/index.js") || node.MatchesPostBumpPath("dist/index.js") {
		t.Error("package postBumpPaths resolve from the package directory")
	}
	if len(cfg.PostBump) != 1 || len(node.PostBump) != 1 {
		t.Errorf("top-level and package hooks stay separate: %v / %v", cfg.PostBump, node.PostBump)
	}

	// With every package naming its manager, the top-level one is optional.
	if _, err := load(t, "kind = \"monorepo\"\n\n[[package]]\npath = \"a\"\nmanager = \"go\"\n"); err != nil {
		t.Errorf("a monorepo whose packages all name a manager needs no top-level one: %v", err)
	}
}
