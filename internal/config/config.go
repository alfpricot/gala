// Package config discovers gala.toml or .gala.toml, applies defaults, and
// rejects any key gala does not know. A typo silently dropping its default is
// the failure mode this tool exists to avoid, so unknown keys are a hard error.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Kind is what is being released.
type Kind string

const (
	// KindApp is a single-package repository.
	KindApp Kind = "app"
	// KindMonorepo releases declared packages independently.
	KindMonorepo Kind = "monorepo"
)

// ManagerName selects the package manager adapter.
type ManagerName string

// The four supported managers. Go is tag-only: the tag is the publish.
const (
	ManagerGo    ManagerName = "go"
	ManagerNpm   ManagerName = "npm"
	ManagerCargo ManagerName = "cargo"
	ManagerPyPI  ManagerName = "pypi"
)

// Package is one declared monorepo package. Declaration order is publish
// order: declare dependencies before their dependents.
type Package struct {
	Path      string `toml:"path"`
	Name      string `toml:"name"`
	TagPrefix string `toml:"tagPrefix"`
}

// Config is a validated config file.
type Config struct {
	Kind            Kind
	Manager         ManagerName
	Publish         bool
	GitHubRelease   bool
	ReleaseBranches []string
	SyncBranch      string
	TagPrefix       string
	Changelog       string
	Remote          string
	Packages        []Package

	// Source is the config file path, for messages.
	Source string
}

// file mirrors the config file — gala.toml and .gala.toml share one schema.
// Pointers distinguish "absent" from an explicit false, because two defaults
// (githubRelease, syncBranch) are not the zero value.
type file struct {
	Kind            string    `toml:"kind"`
	Manager         string    `toml:"manager"`
	Publish         *bool     `toml:"publish"`
	GitHubRelease   *bool     `toml:"githubRelease"`
	ReleaseBranches []string  `toml:"releaseBranches"`
	SyncBranch      *string   `toml:"syncBranch"`
	TagPrefix       *string   `toml:"tagPrefix"`
	Changelog       string    `toml:"changelog"`
	Remote          string    `toml:"remote"`
	Packages        []Package `toml:"package"`
}

// Load reads, decodes, and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config: %w", err)
	}

	var f file
	meta, err := toml.Decode(string(data), &f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if und := meta.Undecoded(); len(und) > 0 {
		keys := make([]string, 0, len(und))
		for _, k := range und {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("%s: unknown key %q — gala only reads keys it knows; "+
			"a typo must not silently drop a default", path, keys[0])
	}

	cfg := &Config{Source: path}

	switch f.Kind {
	case string(KindApp):
		cfg.Kind = KindApp
	case string(KindMonorepo):
		cfg.Kind = KindMonorepo
	case "":
		return nil, fmt.Errorf("%s: missing required key \"kind\" (\"app\" or \"monorepo\")", path)
	default:
		return nil, fmt.Errorf("%s: invalid kind %q (want \"app\" or \"monorepo\")", path, f.Kind)
	}

	switch f.Manager {
	case string(ManagerGo), string(ManagerNpm), string(ManagerCargo), string(ManagerPyPI):
		cfg.Manager = ManagerName(f.Manager)
	case "":
		return nil, fmt.Errorf("%s: missing required key \"manager\" (\"go\", \"npm\", \"cargo\", or \"pypi\")", path)
	default:
		return nil, fmt.Errorf("%s: invalid manager %q (want \"go\", \"npm\", \"cargo\", or \"pypi\")", path, f.Manager)
	}

	cfg.Publish = f.Publish != nil && *f.Publish

	cfg.GitHubRelease = true
	if f.GitHubRelease != nil {
		cfg.GitHubRelease = *f.GitHubRelease
	}

	cfg.ReleaseBranches = []string{"main"}
	if f.ReleaseBranches != nil {
		if len(f.ReleaseBranches) == 0 {
			return nil, fmt.Errorf("%s: releaseBranches must list at least one branch", path)
		}
		cfg.ReleaseBranches = append([]string(nil), f.ReleaseBranches...)
		for _, b := range cfg.ReleaseBranches {
			if strings.TrimSpace(b) == "" || strings.HasPrefix(b, "-") {
				return nil, fmt.Errorf("%s: releaseBranches contains invalid branch %q", path, b)
			}
		}
	}

	if f.SyncBranch != nil {
		cfg.SyncBranch = *f.SyncBranch
	}
	if cfg.SyncBranch != "" {
		if strings.HasPrefix(cfg.SyncBranch, "-") {
			return nil, fmt.Errorf("%s: syncBranch %q is not a valid branch name", path, cfg.SyncBranch)
		}
		for _, b := range cfg.ReleaseBranches {
			if b == cfg.SyncBranch {
				return nil, fmt.Errorf("%s: syncBranch %q must not appear in releaseBranches — "+
					"the release branch cannot be synced to itself", path, cfg.SyncBranch)
			}
		}
	}

	switch cfg.Kind {
	case KindApp:
		cfg.TagPrefix = "v"
		if f.TagPrefix != nil {
			cfg.TagPrefix = *f.TagPrefix
			if cfg.TagPrefix == "" || strings.HasPrefix(cfg.TagPrefix, "-") {
				return nil, fmt.Errorf("%s: tagPrefix %q is not a valid tag prefix", path, cfg.TagPrefix)
			}
		}
		if len(f.Packages) > 0 {
			return nil, fmt.Errorf("%s: [[package]] is only valid for kind = \"monorepo\"", path)
		}
	case KindMonorepo:
		if f.TagPrefix != nil {
			return nil, fmt.Errorf("%s: tagPrefix applies to kind = \"app\" only; "+
				"set tagPrefix on each [[package]] instead", path)
		}
		if len(f.Packages) == 0 {
			return nil, fmt.Errorf("%s: kind = \"monorepo\" needs at least one [[package]]", path)
		}
		seen := make(map[string]bool)
		for i, p := range f.Packages {
			if strings.TrimSpace(p.Path) == "" {
				return nil, fmt.Errorf("%s: [[package]] #%d is missing the required key \"path\"", path, i+1)
			}
			if filepath.IsAbs(p.Path) || strings.HasPrefix(p.Path, "-") || escapesRoot(p.Path) {
				return nil, fmt.Errorf("%s: package path %q must be a relative path inside the repository", path, p.Path)
			}
			clean := filepath.ToSlash(filepath.Clean(p.Path))
			if seen[clean] {
				return nil, fmt.Errorf("%s: duplicate package path %q", path, clean)
			}
			seen[clean] = true
			f.Packages[i].Path = clean
			if strings.HasPrefix(p.TagPrefix, "-") {
				return nil, fmt.Errorf("%s: package %q has invalid tagPrefix %q", path, p.Path, p.TagPrefix)
			}
		}
		cfg.Packages = append([]Package(nil), f.Packages...)
	}

	cfg.Changelog = "CHANGELOG.md"
	if f.Changelog != "" {
		cfg.Changelog = f.Changelog
	}
	if cfg.Changelog == "" || filepath.IsAbs(cfg.Changelog) || escapesRoot(cfg.Changelog) {
		return nil, fmt.Errorf("%s: changelog %q must be a relative path inside the repository", path, cfg.Changelog)
	}
	cfg.Changelog = filepath.ToSlash(filepath.Clean(cfg.Changelog))

	cfg.Remote = "origin"
	if f.Remote != "" {
		cfg.Remote = f.Remote
	}
	if cfg.Remote == "" || strings.HasPrefix(cfg.Remote, "-") {
		return nil, fmt.Errorf("%s: remote %q is not a valid remote name", path, cfg.Remote)
	}

	return cfg, nil
}

// Validate checks the filesystem facts that need the repository root: every
// declared package must point at a real directory.
func (c *Config) Validate(root string) error {
	for _, p := range c.Packages {
		dir := filepath.Join(root, filepath.FromSlash(p.Path))
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("%s: package path %q: %w", c.Source, p.Path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s: package path %q is not a directory", c.Source, p.Path)
		}
	}
	return nil
}

// OnReleaseBranch reports whether branch is allowed to release.
func (c *Config) OnReleaseBranch(branch string) bool {
	for _, b := range c.ReleaseBranches {
		if b == branch {
			return true
		}
	}
	return false
}

// ReleaseBranchesList is the config value for messages.
func (c *Config) ReleaseBranchesList() string {
	return strings.Join(c.ReleaseBranches, ", ")
}

// The two config file names gala knows. Both live at the repository root;
// a repository uses one or the other, never both.
const (
	// ConfigName is the plain name, the default choice.
	ConfigName = "gala.toml"
	// DotConfigName is the dotfile alternative, for repositories that keep
	// tool configuration in dotfiles alongside .editorconfig and friends.
	DotConfigName = ".gala.toml"
)

// Find returns the config file path at root. Exactly one of the known names
// may be there: none means there is nothing to read, and two means a stale
// file could win silently — the choice gala refuses to make for you.
func Find(root string) (string, error) {
	found := make([]string, 0, 2)
	for _, name := range []string{ConfigName, DotConfigName} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			found = append(found, name)
		}
	}
	switch len(found) {
	case 1:
		return filepath.Join(root, found[0]), nil
	case 0:
		return "", fmt.Errorf("%s: no config file — gala reads %s or %s at the "+
			"repository root (--config overrides discovery)", root, ConfigName, DotConfigName)
	default:
		return "", fmt.Errorf("%s: both %s and %s exist — gala reads exactly one config "+
			"file, so delete the one you are not using", root, found[0], found[1])
	}
}

func escapesRoot(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean == ".." || strings.HasPrefix(clean, "../")
}
