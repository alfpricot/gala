package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alfpricot/gala/internal/semver"
)

// cargoManager edits Cargo.toml [package] version (or [workspace.package]
// version for a workspace root) with a targeted line edit and leaves
// Cargo.lock to `cargo update --workspace`.
type cargoManager struct {
	r Runner
}

func (c *cargoManager) Name() string { return "cargo" }

func (c *cargoManager) Tools() []string { return []string{"cargo"} }

func (c *cargoManager) Manifest(dir, root string) string {
	return filepath.ToSlash(filepath.Join(rel(dir, root), "Cargo.toml"))
}

func (c *cargoManager) read(dir string) (string, string, error) {
	path := filepath.Join(dir, "Cargo.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	return string(data), filepath.ToSlash(path), nil
}

// ManifestVersion reads [package] version. A virtual workspace (no
// [package]) or a root package that inherits its version reads
// [workspace.package] version from the same file instead: the members that
// inherit it release in lockstep under one version. A member that inherits
// from a workspace root elsewhere is refused by name: independent versioning
// needs an explicit version.
func (c *cargoManager) ManifestVersion(dir string) (semver.Version, bool, error) {
	content, file, err := c.read(dir)
	if err != nil {
		return semver.Version{}, false, err
	}
	section, ok := versionSection(content)
	if ok {
		v, _ := GetTomlString(content, section, "version")
		parsed, err := semver.Parse(v)
		if err != nil {
			return semver.Version{}, false, fmt.Errorf("%s: [%s] version: %w", file, section, err)
		}
		return parsed, true, nil
	}
	// Both spellings inherit: version.workspace = true and the inline table
	// version = { workspace = true }.
	if SectionHasKey(content, "package", "version.workspace") || inheritsVersion(content) {
		return semver.Version{}, false, fmt.Errorf(
			"%s: [package] sets version.workspace = true and this file has no [workspace.package] version; "+
				"gala releases packages independently, so the manifest needs an explicit version", file)
	}
	return semver.Version{}, false, fmt.Errorf("%s: no [package] or [workspace.package] version found", file)
}

// versionSection names the table whose version key gala reads and writes:
// [package] when it carries an explicit version, otherwise [workspace.package].
func versionSection(content string) (string, bool) {
	if v, ok := GetTomlString(content, "package", "version"); ok && !strings.HasPrefix(v, "{") {
		return "package", true
	}
	if _, ok := GetTomlString(content, "workspace.package", "version"); ok {
		return "workspace.package", true
	}
	return "", false
}

func (c *cargoManager) PackageName(dir string) (string, error) {
	content, file, err := c.read(dir)
	if err != nil {
		return "", err
	}
	name, ok := GetTomlString(content, "package", "name")
	if !ok || name == "" {
		return "", fmt.Errorf("%s: no [package] name found", file)
	}
	return name, nil
}

func (c *cargoManager) SetVersion(dir string, v semver.Version) error {
	content, file, err := c.read(dir)
	if err != nil {
		return err
	}
	section, ok := versionSection(content)
	if !ok {
		return fmt.Errorf("%s: no [package] or [workspace.package] version to write", file)
	}
	edited, err := SetTomlValue(content, section, "version", v.String())
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte(edited), 0o644)
}

func (c *cargoManager) CheckVersion(string, semver.Version) error { return nil }

// RefreshLock runs `cargo update --workspace` only when a Cargo.lock exists;
// it rewrites workspace members' entries and leaves dependency pins alone.
func (c *cargoManager) RefreshLock(dir, root string) ([]string, error) {
	var locks []string
	for _, base := range uniqueDirs(dir, root) {
		p := filepath.Join(base, "Cargo.lock")
		if _, err := os.Stat(p); err == nil {
			locks = append(locks, filepath.ToSlash(rel(p, root)))
		}
	}
	if len(locks) == 0 {
		return nil, nil
	}
	if _, err := c.r.Run(dir, "cargo", "update", "--workspace"); err != nil {
		return locks, fmt.Errorf("cargo update --workspace: %w", err)
	}
	return locks, nil
}

func (c *cargoManager) Preflight(string, bool) error { return nil }

func (c *cargoManager) Publish(dir string) error {
	_, err := c.r.Run(dir, "cargo", "publish")
	return err
}

func (c *cargoManager) Recovery(dir, root string) string {
	return fmt.Sprintf("cd %s && cargo publish", rel(dir, root))
}

// inlineWorkspaceRe matches `version = { workspace = true }` in any spacing.
var inlineWorkspaceRe = regexp.MustCompile(`^[ \t]*version[ \t]*=[ \t]*\{[^}]*\bworkspace[ \t]*=[ \t]*true\b`)

// inheritsVersion reports whether [package] inherits its version through the
// inline-table spelling.
func inheritsVersion(content string) bool {
	lines, err := SectionLines(content, "package")
	if err != nil {
		return false
	}
	for _, l := range lines[1:] {
		if inlineWorkspaceRe.MatchString(l) {
			return true
		}
	}
	return false
}
