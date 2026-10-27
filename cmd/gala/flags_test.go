package main

import (
	"strings"
	"testing"
)

func TestParseArgsDefaultsToPlan(t *testing.T) {
	c, err := parseArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.command != cmdPlan {
		t.Errorf("command = %q, want plan", c.command)
	}
}

func TestParseArgsCommands(t *testing.T) {
	c, err := parseArgs([]string{"release", "--dry-run", "--no-publish"})
	if err != nil {
		t.Fatal(err)
	}
	if c.command != cmdRelease || !c.dryRun || !c.noPublish {
		t.Errorf("got %+v", c)
	}

	c, err = parseArgs([]string{"help"})
	if err != nil || c.command != cmdHelp {
		t.Errorf("help: %+v %v", c, err)
	}
	c, err = parseArgs([]string{"version"})
	if err != nil || c.command != cmdVersion {
		t.Errorf("version: %+v %v", c, err)
	}
	c, err = parseArgs([]string{"--help"})
	if err != nil || c.command != cmdHelp {
		t.Errorf("--help: %+v %v", c, err)
	}
}

func TestParseArgsValueFlags(t *testing.T) {
	c, err := parseArgs([]string{"release", "--config", "path/to/gala.toml", "--cwd", "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	if c.config != "path/to/gala.toml" || c.cwd != "elsewhere" {
		t.Errorf("got %+v", c)
	}

	c, err = parseArgs([]string{"--config=x.toml", "--cwd=/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if c.config != "x.toml" || c.cwd != "/tmp/repo" {
		t.Errorf("--flag=value form: got %+v", c)
	}
}

func TestParseArgsUsageErrors(t *testing.T) {
	cases := [][]string{
		{"nonsense"},                         // unknown command
		{"plan", "release"},                  // two commands
		{"--bogus"},                          // unknown flag
		{"-x"},                               // short unknown flag
		{"release", "--config"},              // missing value
		{"release", "--cwd"},                 // missing value at end
		{"release", "--config", "--dry-run"}, // value looks like a flag
		{"plan", "--dry-run"},                // release-only flag
		{"plan", "--no-publish"},             // release-only flag
		{"release", "--dry-run=yes"},         // boolean flag with value
	}
	for _, args := range cases {
		if _, err := parseArgs(args); err == nil {
			t.Errorf("parseArgs(%v) should be a usage error", args)
		}
	}
}

func TestRunExitCodes(t *testing.T) {
	if code := run([]string{"help"}); code != exitOK {
		t.Errorf("help exit = %d, want 0", code)
	}
	if code := run([]string{"version"}); code != exitOK {
		t.Errorf("version exit = %d, want 0", code)
	}
	if code := run([]string{"nonsense"}); code != exitUsage {
		t.Errorf("unknown command exit = %d, want 2", code)
	}
	if code := run([]string{"--bogus"}); code != exitUsage {
		t.Errorf("unknown flag exit = %d, want 2", code)
	}
}

func TestUsageText(t *testing.T) {
	for _, want := range []string{"gala [plan]", "gala release", "--dry-run", "--no-publish", "--config", "--cwd", "exit codes"} {
		if !strings.Contains(usageText, want) {
			t.Errorf("usage text is missing %q", want)
		}
	}
}

func TestVersionFallsBackToDev(t *testing.T) {
	// Unset in tests: the ldflags value is empty and there is no module
	// version baked into a test binary, so "dev" is the honest answer.
	if v := version(); v == "" {
		t.Error("version must never be empty")
	}
}
