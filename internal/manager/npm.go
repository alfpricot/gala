package manager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alfpricot/gala/internal/semver"
)

// npmManager drives npm: package.json is written by `npm version` (npm's own
// tool, so comments and formatting npm understands stay npm's business), and
// publishing is `npm publish`.
type npmManager struct {
	r Runner
}

type packageJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (n *npmManager) Name() string { return "npm" }

func (n *npmManager) Tools() []string { return []string{"npm"} }

func (n *npmManager) Manifest(dir, root string) string {
	return filepath.ToSlash(filepath.Join(rel(dir, root), "package.json"))
}

func (n *npmManager) read(dir string) (packageJSON, error) {
	var pkg packageJSON
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return pkg, err
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return pkg, fmt.Errorf("package.json: %w", err)
	}
	return pkg, nil
}

func (n *npmManager) ManifestVersion(dir string) (semver.Version, bool, error) {
	pkg, err := n.read(dir)
	if err != nil {
		return semver.Version{}, false, err
	}
	if pkg.Version == "" {
		return semver.Version{}, false, fmt.Errorf("%s: package.json has no version field", dir)
	}
	v, err := semver.Parse(pkg.Version)
	if err != nil {
		return semver.Version{}, false, fmt.Errorf("%s: package.json version: %w", dir, err)
	}
	return v, true, nil
}

func (n *npmManager) PackageName(dir string) (string, error) {
	pkg, err := n.read(dir)
	if err != nil {
		return "", err
	}
	if pkg.Name == "" {
		return "", fmt.Errorf("%s: package.json has no name field", dir)
	}
	return pkg.Name, nil
}

func (n *npmManager) SetVersion(dir string, v semver.Version) error {
	_, err := n.r.Run(dir, "npm", "version", v.String(), "--no-git-tag-version")
	if err != nil {
		return fmt.Errorf("npm version: %w", err)
	}
	return nil
}

func (n *npmManager) CheckVersion(string, semver.Version) error { return nil }

// RefreshLock leaves a single package's lock to `npm version` (it keeps
// package-lock.json in step) and, for a monorepo, asks npm to re-record the
// member versions at the root.
func (n *npmManager) RefreshLock(dir, root string) ([]string, error) {
	var locks []string
	seen := map[string]bool{}
	add := func(base string) {
		p := filepath.Join(base, "package-lock.json")
		if _, err := os.Stat(p); err != nil {
			return
		}
		r := filepath.ToSlash(rel(p, root))
		if !seen[r] {
			seen[r] = true
			locks = append(locks, r)
		}
	}
	add(dir)
	if dir != root {
		add(root)
		if len(locks) > 0 {
			if _, err := n.r.Run(root, "npm", "install", "--package-lock-only"); err != nil {
				return locks, fmt.Errorf("npm install --package-lock-only: %w", err)
			}
		}
	}
	return locks, nil
}

func (n *npmManager) Preflight(dir string, publish bool) error {
	if !publish {
		return nil
	}
	out, err := n.r.Run(dir, "npm", "whoami")
	if err != nil {
		return fmt.Errorf("npm whoami failed — log in with `npm login` or set NODE_AUTH_TOKEN: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("npm whoami returned no username — log in with `npm login`")
	}
	return nil
}

func (n *npmManager) Publish(dir string) error {
	if _, err := n.r.Run(dir, "npm", "publish"); err != nil {
		return err
	}
	return nil
}

func (n *npmManager) Recovery(dir, root string) string {
	return fmt.Sprintf("cd %s && npm publish", rel(dir, root))
}
