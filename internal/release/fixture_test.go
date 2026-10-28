package release

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alfpricot/gala/internal/gittest"
	"github.com/alfpricot/gala/internal/manager"
)

const appConfig = `kind = "app"
manager = "go"
publish = false
githubRelease = false
releaseBranches = ["main"]
syncBranch = "dev"
`

// fixture builds a committed repository on main with a bare origin, ready for
// a release run.
func fixture(t *testing.T, cfg string) (*gittest.Repo, string, func()) {
	t.Helper()
	r, cleanup, err := gittest.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile("gala.toml", cfg); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if err := r.CommitAll("chore: add gala config"); err != nil {
		cleanup()
		t.Fatal(err)
	}
	bare, bareCleanup, err := r.BareRemote("origin", "main")
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	return r, bare, func() {
		bareCleanup()
		cleanup()
	}
}

// call records one captured package-manager invocation.
type call struct {
	dir  string
	name string
	args []string
}

// captureRunner stands in for npm/cargo/uv: argv is asserted, the binary
// never runs. Real git still runs underneath.
type captureRunner struct {
	calls []call
	// respond, when set, answers every call in place of the defaults.
	respond func(dir, name string, args []string) (string, error)
}

func (c *captureRunner) Run(dir, name string, args ...string) (string, error) {
	c.calls = append(c.calls, call{dir: dir, name: name, args: append([]string(nil), args...)})
	if c.respond != nil {
		return c.respond(dir, name, args)
	}
	if len(args) > 0 && args[0] == "whoami" {
		return "fixture-user\n", nil
	}
	return "", nil
}

// testOptions wires a fixture into the pipeline: real git, captured manager
// exec, a fixed clock, and buffers for the prose output.
func testOptions(r *gittest.Repo, exec manager.Runner) (*Options, *bytes.Buffer, *bytes.Buffer) {
	var out, warn bytes.Buffer
	opts := DefaultOptions()
	opts.Cwd = r.Dir
	opts.Stdout = &out
	opts.Stderr = &warn
	opts.Token = ""
	opts.LookPath = func(string) error { return nil }
	opts.Now = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }
	if exec != nil {
		opts.Exec = exec
	}
	return opts, &out, &warn
}

// snapshot captures every file's bytes outside .git, so a dry run can be
// proven byte-identical.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func diffMaps(before, after map[string]string) []string {
	var diffs []string
	for k, v := range before {
		if av, ok := after[k]; !ok {
			diffs = append(diffs, k+": deleted")
		} else if av != v {
			diffs = append(diffs, k+": changed")
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			diffs = append(diffs, k+": added")
		}
	}
	return diffs
}

func tags(t *testing.T, r *gittest.Repo) string {
	t.Helper()
	out, err := r.Run("tag", "--list")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func mustContain(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s should contain %q:\n%s", what, needle, haystack)
	}
}

func mustNotContain(t *testing.T, haystack, needle, what string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("%s should not contain %q:\n%s", what, needle, haystack)
	}
}
