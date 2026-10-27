package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alfpricot/gala/internal/semver"
)

// goManager is the tag-only adapter: Go manifests carry no version, the tag
// is the truth, and the tag is the publish. The one ecosystem rule gala
// enforces is the module path's /vN suffix for major 2 and up.
type goManager struct{}

func (g *goManager) Name() string { return "go" }

// Tools is empty: a go release runs git only.
func (g *goManager) Tools() []string { return nil }

// Manifest is "": there is nothing to version-bump or stage.
func (g *goManager) Manifest(string, string) string { return "" }

func (g *goManager) ManifestVersion(string) (semver.Version, bool, error) {
	return semver.Version{}, false, nil
}

// PackageName derives the name from the module path: the last element, with a
// /vN major suffix removed so github.com/x/y/v2 is still "y".
func (g *goManager) PackageName(dir string) (string, error) {
	mod, err := ModulePath(dir)
	if err != nil {
		return "", err
	}
	name := pathLast(mod)
	if vSuffixRe.MatchString(name) {
		if i := strings.LastIndexByte(mod, '/'); i >= 0 {
			name = pathLast(mod[:i])
		}
	}
	return name, nil
}

// vSuffixRe matches a Go major-version suffix such as v2 or v10.
var vSuffixRe = regexp.MustCompile(`^v\d+$`)

func (g *goManager) SetVersion(string, semver.Version) error { return nil }

// CheckVersion enforces "fail before the tag": a module path must carry
// /v<N> once the next major is 2 or higher.
func (g *goManager) CheckVersion(dir string, v semver.Version) error {
	if v.Major < 2 {
		return nil
	}
	mod, err := ModulePath(dir)
	if err != nil {
		return err
	}
	want := fmt.Sprintf("/v%d", v.Major)
	if strings.HasSuffix(mod, want) {
		return nil
	}
	return fmt.Errorf("next version is %s but the module path is %q: "+
		"rename the module to end in %s (and update imports) before releasing", v, mod, want)
}

func (g *goManager) RefreshLock(string, string) ([]string, error) { return nil, nil }

func (g *goManager) Preflight(string, bool) error { return nil }

// Publish is a no-op: publishing a Go module is pushing its tag.
func (g *goManager) Publish(string) error { return nil }

func (g *goManager) Recovery(string, string) string { return "" }

// ModulePath reads the `module` line of dir's go.mod.
func ModulePath(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("go.mod: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			mod := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			mod = strings.TrimSuffix(mod, "\r")
			if mod == "" {
				return "", fmt.Errorf("go.mod: empty module directive")
			}
			return mod, nil
		}
	}
	return "", fmt.Errorf("go.mod: no module directive found")
}

// pathLast returns the last element of a slash path.
func pathLast(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// uniqueDirs returns dir and root without duplicates, for lockfile lookup.
func uniqueDirs(dirs ...string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range dirs {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
