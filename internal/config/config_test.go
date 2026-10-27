package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, content string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gala.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestDefaults(t *testing.T) {
	cfg, err := load(t, "kind = \"app\"\nmanager = \"go\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Kind != KindApp || cfg.Manager != ManagerGo {
		t.Errorf("kind/manager = %s/%s", cfg.Kind, cfg.Manager)
	}
	if cfg.Publish {
		t.Error("publish must default to false (registry pushes are opt-in)")
	}
	if !cfg.GitHubRelease {
		t.Error("githubRelease must default to true")
	}
	if len(cfg.ReleaseBranches) != 1 || cfg.ReleaseBranches[0] != "main" {
		t.Errorf("releaseBranches = %v, want [main]", cfg.ReleaseBranches)
	}
	if cfg.SyncBranch != "" {
		t.Errorf("syncBranch = %q, want empty (off)", cfg.SyncBranch)
	}
	if cfg.TagPrefix != "v" || cfg.Changelog != "CHANGELOG.md" || cfg.Remote != "origin" {
		t.Errorf("tagPrefix/changelog/remote = %q/%q/%q", cfg.TagPrefix, cfg.Changelog, cfg.Remote)
	}
}

func TestUnknownKeyIsHardError(t *testing.T) {
	_, err := load(t, "kind = \"app\"\nmanager = \"go\"\npubish = true\n")
	if err == nil {
		t.Fatal("an unknown key must be a hard error, not a silently dropped default")
	}
	if !strings.Contains(err.Error(), "unknown key") || !strings.Contains(err.Error(), "pubish") {
		t.Errorf("error should name the typo: %v", err)
	}
}

func TestUnknownKeyInsidePackage(t *testing.T) {
	_, err := load(t, "kind = \"monorepo\"\nmanager = \"npm\"\n\n[[package]]\npath = \"a\"\ntagprefx = \"x/\"\n")
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unknown key inside [[package]] must error, got %v", err)
	}
}

func TestRequiredAndEnumKeys(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"missing kind", "manager = \"go\"\n", "missing required key \"kind\""},
		{"missing manager", "kind = \"app\"\n", "missing required key \"manager\""},
		{"bad kind", "kind = \"library\"\nmanager = \"go\"\n", "invalid kind"},
		{"bad manager", "kind = \"app\"\nmanager = \"yarn\"\n", "invalid manager"},
		{"empty releaseBranches", "kind = \"app\"\nmanager = \"go\"\nreleaseBranches = []\n", "at least one branch"},
		{"app with packages", "kind = \"app\"\nmanager = \"go\"\n\n[[package]]\npath = \"a\"\n", "[[package]] is only valid"},
		{"monorepo without packages", "kind = \"monorepo\"\nmanager = \"npm\"\n", "needs at least one [[package]]"},
		{"monorepo with top tagPrefix", "kind = \"monorepo\"\nmanager = \"npm\"\ntagPrefix = \"v\"\n\n[[package]]\npath = \"a\"\n", "tagPrefix applies"},
		{"duplicate path", "kind = \"monorepo\"\nmanager = \"npm\"\n\n[[package]]\npath = \"a\"\n\n[[package]]\npath = \"a\"\n", "duplicate package path"},
		{"missing path", "kind = \"monorepo\"\nmanager = \"npm\"\n\n[[package]]\nname = \"x\"\n", "missing the required key \"path\""},
		{"escaping path", "kind = \"monorepo\"\nmanager = \"npm\"\n\n[[package]]\npath = \"../outside\"\n", "inside the repository"},
		{"sync in releaseBranches", "kind = \"app\"\nmanager = \"go\"\nreleaseBranches = [\"main\", \"dev\"]\nsyncBranch = \"dev\"\n", "must not appear in releaseBranches"},
		{"escaping changelog", "kind = \"app\"\nmanager = \"go\"\nchangelog = \"../CHANGELOG.md\"\n", "inside the repository"},
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

func TestExplicitFalseStaysFalse(t *testing.T) {
	cfg, err := load(t, "kind = \"app\"\nmanager = \"go\"\ngithubRelease = false\npublish = true\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitHubRelease {
		t.Error("explicit githubRelease = false was overwritten by the default")
	}
	if !cfg.Publish {
		t.Error("explicit publish = true was lost")
	}
}

func TestValidatePackageDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "packages", "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gala.toml")
	content := "kind = \"monorepo\"\nmanager = \"npm\"\n\n[[package]]\npath = \"packages/core\"\n\n[[package]]\npath = \"packages/missing\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.Validate(root)
	if err == nil {
		t.Fatal("a missing package directory must fail validation")
	}
	if !strings.Contains(err.Error(), "packages/missing") {
		t.Errorf("error should name the directory: %v", err)
	}
}

func TestOnReleaseBranch(t *testing.T) {
	cfg, err := load(t, "kind = \"app\"\nmanager = \"go\"\nreleaseBranches = [\"main\", \"release\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OnReleaseBranch("main") || !cfg.OnReleaseBranch("release") {
		t.Error("declared branches must be allowed")
	}
	if cfg.OnReleaseBranch("dev") {
		t.Error("undeclared branches must be refused")
	}
}
