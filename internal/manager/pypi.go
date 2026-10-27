package manager

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/alfpricot/gala/internal/semver"
)

// pypiManager edits pyproject.toml [project] version with a targeted line
// edit and leaves uv.lock to `uv lock`. Publishing is `uv build` + `uv
// publish`.
type pypiManager struct {
	r Runner
}

func (p *pypiManager) Name() string { return "pypi" }

func (p *pypiManager) Tools() []string { return []string{"uv"} }

func (p *pypiManager) Manifest(dir, root string) string {
	return filepath.ToSlash(filepath.Join(rel(dir, root), "pyproject.toml"))
}

func (p *pypiManager) read(dir string) (string, string, error) {
	path := filepath.Join(dir, "pyproject.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	return string(data), filepath.ToSlash(path), nil
}

// ManifestVersion reads [project] version. A pyproject without a static
// version (dynamic versioning) is a plan-time error, before any write.
func (p *pypiManager) ManifestVersion(dir string) (semver.Version, bool, error) {
	content, file, err := p.read(dir)
	if err != nil {
		return semver.Version{}, false, err
	}
	v, ok := GetTomlString(content, "project", "version")
	if !ok {
		return semver.Version{}, false, fmt.Errorf(
			"%s: no static [project] version — dynamic versioning is not supported, "+
				"declare version = \"...\" under [project]", file)
	}
	parsed, err := semver.Parse(v)
	if err != nil {
		return semver.Version{}, false, fmt.Errorf("%s: [project] version: %w", file, err)
	}
	return parsed, true, nil
}

func (p *pypiManager) PackageName(dir string) (string, error) {
	content, file, err := p.read(dir)
	if err != nil {
		return "", err
	}
	name, ok := GetTomlString(content, "project", "name")
	if !ok || name == "" {
		return "", fmt.Errorf("%s: no [project] name found", file)
	}
	return name, nil
}

func (p *pypiManager) SetVersion(dir string, v semver.Version) error {
	content, file, err := p.read(dir)
	if err != nil {
		return err
	}
	edited, err := SetTomlValue(content, "project", "version", v.String())
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	return os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte(edited), 0o644)
}

func (p *pypiManager) CheckVersion(string, semver.Version) error { return nil }

// RefreshLock runs `uv lock` only when a uv.lock exists; it records the new
// project version and keeps existing pins.
func (p *pypiManager) RefreshLock(dir, root string) ([]string, error) {
	var locks []string
	for _, base := range uniqueDirs(dir, root) {
		fp := filepath.Join(base, "uv.lock")
		if _, err := os.Stat(fp); err == nil {
			locks = append(locks, filepath.ToSlash(rel(fp, root)))
		}
	}
	if len(locks) == 0 {
		return nil, nil
	}
	if _, err := p.r.Run(dir, "uv", "lock"); err != nil {
		return locks, fmt.Errorf("uv lock: %w", err)
	}
	return locks, nil
}

func (p *pypiManager) Preflight(string, bool) error { return nil }

func (p *pypiManager) Publish(dir string) error {
	if _, err := p.r.Run(dir, "uv", "build"); err != nil {
		return fmt.Errorf("uv build: %w", err)
	}
	if _, err := p.r.Run(dir, "uv", "publish"); err != nil {
		return fmt.Errorf("uv publish: %w", err)
	}
	return nil
}

func (p *pypiManager) Recovery(dir, root string) string {
	return fmt.Sprintf("cd %s && uv build && uv publish", rel(dir, root))
}
