package release

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBatchArgError(t *testing.T) {
	for _, program := range []string{`C:\tools\npm.cmd`, "/x/run.BAT"} {
		for _, arg := range []string{"50%PATH%", "a&b", "a|b", "<in", ">out", "^x", `say "hi"`, "a\nb"} {
			if err := batchArgError(program, []string{"run", arg}); err == nil {
				t.Errorf("%s with %q must be refused", program, arg)
			}
		}
		if err := batchArgError(program, []string{"run", "build", "--label=1.2.3", "a b"}); err != nil {
			t.Errorf("%s with plain arguments: %v", program, err)
		}
	}
	for _, program := range []string{`C:\tools\node.exe`, "/usr/bin/npm", "./gen.sh"} {
		if err := batchArgError(program, []string{"50%PATH%", "a&b"}); err != nil {
			t.Errorf("%s is not a batch file and passes arguments unchanged: %v", program, err)
		}
	}
}

func TestStartableOnWindows(t *testing.T) {
	for _, program := range []string{`C:\r\scripts\gen.ps1`, `C:\r\scripts\gen.sh`, `C:\r\scripts\gen.py`} {
		err := startableOnWindows("windows", program, "")
		if err == nil || !strings.Contains(err.Error(), "cannot be started directly") {
			t.Errorf("%s must be refused on Windows: %v", program, err)
		}
	}
	for _, program := range []string{`C:\r\scripts\gen.cmd`, `C:\r\scripts\gen.EXE`, `C:\r\scripts\gen.bat`} {
		if err := startableOnWindows("windows", program, ".COM;.EXE;.BAT;.CMD"); err != nil {
			t.Errorf("%s starts directly: %v", program, err)
		}
	}
	if err := startableOnWindows("windows", `C:\r\gen.ps1`, ".COM;.EXE;.PS1"); err != nil {
		t.Errorf("an extension the user added to PATHEXT is accepted: %v", err)
	}
	if err := startableOnWindows("linux", "/r/scripts/gen.sh", ""); err != nil {
		t.Errorf("only Windows restricts extensions: %v", err)
	}
}

// TestCheckHookPathsResolvesLikeExec covers path-named hooks: on Windows
// ./scripts/build finds build.cmd through PATHEXT, and elsewhere the script
// must be executable, matching what os/exec does when the hook runs.
func TestCheckHookPathsResolvesLikeExec(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if err := os.WriteFile(filepath.Join(dir, "scripts", "build.cmd"), []byte("@echo ok\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.WriteFile(filepath.Join(dir, "scripts", "build"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "scripts", "plain"), []byte("echo no\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := checkHookPaths([]string{filepath.Join(dir, "scripts", "plain")}); err == nil {
			t.Error("a script without the executable bit cannot run and must fail the preflight")
		}
	}
	if err := checkHookPaths([]string{filepath.Join(dir, "scripts", "build")}); err != nil {
		t.Errorf("./scripts/build must resolve the way the hook will run: %v", err)
	}
	if err := checkHookPaths([]string{filepath.Join(dir, "scripts", "missing")}); err == nil {
		t.Error("a missing script must fail the preflight")
	}
}

// TestBatchHookArgumentsRefusedOnWindows runs the preflight against a real
// batch file, the case where cmd.exe would expand the argument.
func TestBatchHookArgumentsRefusedOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch files run through cmd.exe only on Windows")
	}
	cfg := "kind = \"app\"\nmanager = \"none\"\ngithubRelease = false\n" +
		"postBump = [\"./scripts/gen 50%PATH%\"]\npostBumpPaths = [\"gen\"]\n\n" +
		"[[versionFile]]\npath = \"VERSION\"\npattern = '^(\\S+)'\n"
	r, cleanup := scaffold(t, map[string]string{
		"gala.toml":       cfg,
		"VERSION":         "1.0.0\n",
		"scripts/gen.cmd": "@echo %1\r\n",
	})
	defer cleanup()
	if err := r.CommitFile("src.txt", "x\n", "feat: a change"); err != nil {
		t.Fatal(err)
	}

	opts, _, _ := testOptions(r, &captureRunner{})
	err := Run(opts, ModeDryRun)
	if err == nil || !strings.Contains(err.Error(), "cmd.exe") {
		t.Errorf("a batch hook with %%VAR%% in an argument must fail the preflight: %v", err)
	}
}

// TestReleaseFailureReportsReleasedFalse covers the "released is always
// written" contract: a run that stops before the push, whether at config
// load or at a preflight hook, still tells the workflow released=false.
func TestReleaseFailureReportsReleasedFalse(t *testing.T) {
	t.Run("preflight", func(t *testing.T) {
		r, _, cleanup := multiFileFixture(t, multiFileConfig, nil)
		defer cleanup()
		exec := &captureRunner{respond: func(_, name string, _ []string) (string, error) {
			if name == "make" {
				return "", errors.New("make check failed")
			}
			return "", nil
		}}
		opts, _, _ := testOptions(r, exec)
		outputs := filepath.Join(t.TempDir(), "github_output")
		opts.GitHubOutput = outputs
		if err := Run(opts, ModeRelease); err == nil {
			t.Fatal("a failing preRelease check must fail the release")
		}
		data, err := os.ReadFile(outputs)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "released=false\n" {
			t.Errorf("GITHUB_OUTPUT = %q, want released=false", data)
		}
	})

	t.Run("config", func(t *testing.T) {
		r, cleanup := scaffold(t, map[string]string{"gala.toml": "kind = \"app\"\nmanager = \"go\"\npubish = true\n"})
		defer cleanup()
		opts, _, _ := testOptions(r, &captureRunner{})
		outputs := filepath.Join(t.TempDir(), "github_output")
		opts.GitHubOutput = outputs
		if err := Run(opts, ModeRelease); err == nil {
			t.Fatal("an unknown config key must fail the release")
		}
		data, err := os.ReadFile(outputs)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "released=false\n" {
			t.Errorf("GITHUB_OUTPUT = %q, want released=false", data)
		}
	})

	t.Run("plan writes nothing", func(t *testing.T) {
		r, _, cleanup := multiFileFixture(t, multiFileConfig, nil)
		defer cleanup()
		opts, _, _ := testOptions(r, &captureRunner{})
		outputs := filepath.Join(t.TempDir(), "github_output")
		opts.GitHubOutput = outputs
		if err := Run(opts, ModePlan); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(outputs); !os.IsNotExist(err) {
			t.Errorf("plan must not write GITHUB_OUTPUT: %v", err)
		}
	})
}
