package release

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alfpricot/gala/internal/gittest"
	"github.com/alfpricot/gala/internal/manager"
)

func TestIndentWriter(t *testing.T) {
	var buf bytes.Buffer
	w := &indentWriter{w: &buf, prefix: "> "}
	// A line split across writes, CRLF endings, blank lines, and a final line
	// with no newline.
	for _, chunk := range []string{"one\r\ntw", "o\n\n   \nthree"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	if got, want := buf.String(), "> one\n> two\n> three\n"; got != want {
		t.Errorf("indented output = %q, want %q", got, want)
	}

	// A progress bar redraws with a bare carriage return: each redraw is a
	// line as soon as it is printed, without waiting for a newline.
	buf.Reset()
	w = &indentWriter{w: &buf, prefix: "> "}
	if _, err := w.Write([]byte("10%\r50%\r")); err != nil {
		t.Fatal(err)
	}
	if got, want := buf.String(), "> 10%\n> 50%\n"; got != want {
		t.Errorf("progress output = %q, want %q before the command exits", got, want)
	}
}

// streamingRunner is a runner that, like the production one, streams hook
// output and takes a timeout.
type streamingRunner struct {
	captureRunner
	specs []manager.HookSpec
}

func (s *streamingRunner) RunHook(h manager.HookSpec) (string, error) {
	s.specs = append(s.specs, h)
	_, _ = h.Out.Write([]byte("line one\npartial"))
	return "", nil
}

// TestHooksStreamThroughTheRunner: a runner that can stream receives the
// configured timeout, and its output reaches the transcript indented as it is
// printed, a trailing partial line included.
func TestHooksStreamThroughTheRunner(t *testing.T) {
	r, _, cleanup := fixture(t, "kind = \"app\"\nmanager = \"go\"\ngithubRelease = false\n"+
		"releaseBranches = [\"main\"]\nhookTimeout = \"90s\"\npreRelease = [\"check all\"]\n")
	defer cleanup()
	if err := r.CommitFile("app.go", "package app\n", "feat: first"); err != nil {
		t.Fatal(err)
	}

	exec := &streamingRunner{}
	opts, out, _ := testOptions(r, exec)
	if err := Run(opts, ModeDryRun); err != nil {
		t.Fatal(err)
	}
	if len(exec.specs) != 1 {
		t.Fatalf("hook runs = %d, want 1", len(exec.specs))
	}
	if got := exec.specs[0].Timeout; got != 90*time.Second {
		t.Errorf("timeout = %s, want 90s from hookTimeout", got)
	}
	if exec.specs[0].Name != "check" || strings.Join(exec.specs[0].Args, " ") != "all" {
		t.Errorf("command = %s %v", exec.specs[0].Name, exec.specs[0].Args)
	}
	mustContain(t, out.String(), "          line one\n", "streamed output is indented into the transcript")
	mustContain(t, out.String(), "          partial\n", "a trailing partial line is flushed")
}

// TestPostBumpOutputThatGitIgnoresIsRefused: a file a postBump command creates
// under a postBumpPaths entry that .gitignore hides would be left out of the
// release commit, so the run stops before committing, naming the rule.
func TestPostBumpOutputThatGitIgnoresIsRefused(t *testing.T) {
	cfg := "kind = \"app\"\nmanager = \"go\"\ngithubRelease = false\nreleaseBranches = [\"main\"]\n" +
		"postBump = [\"gen\"]\npostBumpPaths = [\"dist/*.js\"]\n"
	gen := func(r interface{ WriteFile(string, string) error }) *captureRunner {
		return &captureRunner{respond: func(_, name string, _ []string) (string, error) {
			if name == "gen" {
				return "", r.WriteFile("dist/app.js", "// built\n")
			}
			return "", nil
		}}
	}

	t.Run("ignored directory", func(t *testing.T) {
		r, cleanup := scaffold(t, map[string]string{"gala.toml": cfg, ".gitignore": "dist/\n"})
		defer cleanup()
		if err := r.CommitFile("src.txt", "x\n", "feat: a change"); err != nil {
			t.Fatal(err)
		}
		before, err := r.HeadSHA()
		if err != nil {
			t.Fatal(err)
		}

		opts, _, _ := testOptions(r, gen(r))
		err = Run(opts, ModeRelease)
		if err == nil {
			t.Fatal("a postBump output that git ignores must fail the release")
		}
		mustContain(t, err.Error(), "dist/app.js (ignored by .gitignore:1:dist/)", "the file and the rule that hides it")
		mustContain(t, err.Error(), "nothing was committed, tagged, or pushed", "the recovery state")
		if head, _ := r.HeadSHA(); head != before {
			t.Errorf("a release commit was made: HEAD %s -> %s", before, head)
		}
	})

	t.Run("tracked output is fine", func(t *testing.T) {
		r, cleanup := scaffold(t, map[string]string{"gala.toml": cfg})
		defer cleanup()
		if err := r.CommitFile("src.txt", "x\n", "feat: a change"); err != nil {
			t.Fatal(err)
		}
		opts, out, _ := testOptions(r, gen(r))
		if err := Run(opts, ModeRelease); err != nil {
			t.Fatalf("an unignored postBump output must release: %v\n%s", err, out.String())
		}
		files, err := r.Run("show", "--name-only", "--format=", "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		mustContain(t, files, "dist/app.js", "the generated file joins the release commit")
	})
}

// TestPostBumpIgnoredCheckCountsOnlyWhatTheHooksWrote covers the snapshot: an
// ignored file that was already under a directory entry is the user's, and
// only a file a command created or rewrote stops the release.
func TestPostBumpIgnoredCheckCountsOnlyWhatTheHooksWrote(t *testing.T) {
	cfg := "kind = \"app\"\nmanager = \"go\"\ngithubRelease = false\nreleaseBranches = [\"main\"]\n" +
		"postBump = [\"gen\"]\npostBumpPaths = [\"dist\"]\n"
	setup := func(t *testing.T) *gittest.Repo {
		t.Helper()
		r, cleanup := scaffold(t, map[string]string{"gala.toml": cfg, ".gitignore": "dist/\n"})
		t.Cleanup(cleanup)
		if err := r.CommitFile("src.txt", "x\n", "feat: a change"); err != nil {
			t.Fatal(err)
		}
		// Content a CI step left behind, older than the run.
		if err := r.WriteFile("dist/cache.bin", "old build output\n"); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(filepath.Join(r.Dir, "dist", "cache.bin"), old, old); err != nil {
			t.Fatal(err)
		}
		return r
	}
	genNothing := &captureRunner{}

	t.Run("pre-existing ignored files are left alone", func(t *testing.T) {
		r := setup(t)
		opts, out, _ := testOptions(r, genNothing)
		if err := Run(opts, ModeRelease); err != nil {
			t.Fatalf("ignored content that was already there must not stop the release: %v\n%s", err, out.String())
		}
	})

	t.Run("a rewritten ignored file stops it", func(t *testing.T) {
		r := setup(t)
		rewrite := &captureRunner{respond: func(_, name string, _ []string) (string, error) {
			if name == "gen" {
				return "", r.WriteFile("dist/cache.bin", "rebuilt by the hook, longer\n")
			}
			return "", nil
		}}
		opts, _, _ := testOptions(r, rewrite)
		err := Run(opts, ModeRelease)
		if err == nil {
			t.Fatal("an ignored file the hook rewrote must fail the release")
		}
		mustContain(t, err.Error(), "dist/cache.bin (ignored by .gitignore:1:dist/)", "the file and its rule")
	})

	t.Run("a long list is capped and named", func(t *testing.T) {
		r := setup(t)
		many := &captureRunner{respond: func(_, name string, _ []string) (string, error) {
			if name == "gen" {
				for i := 0; i < 25; i++ {
					if err := r.WriteFile(fmt.Sprintf("dist/out-%02d.js", i), "x\n"); err != nil {
						return "", err
					}
				}
			}
			return "", nil
		}}
		opts, _, _ := testOptions(r, many)
		err := Run(opts, ModeRelease)
		if err == nil {
			t.Fatal("ignored hook output must fail the release")
		}
		mustContain(t, err.Error(), "dist/out-00.js (ignored by", "the first files are named with their rule")
		mustContain(t, err.Error(), "and 5 more", "the rest are counted")
		mustNotContain(t, err.Error(), "dist/out-24.js", "the list is capped")
	})

	t.Run("a name git quotes is still found", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("a double quote cannot appear in a Windows file name")
		}
		r := setup(t)
		quoted := &captureRunner{respond: func(_, name string, _ []string) (string, error) {
			if name == "gen" {
				return "", r.WriteFile("dist/a\"b.js", "x\n")
			}
			return "", nil
		}}
		opts, _, _ := testOptions(r, quoted)
		if err := Run(opts, ModeRelease); err == nil {
			t.Fatal("an ignored file whose name git quotes must still fail the release")
		}
	})
}
