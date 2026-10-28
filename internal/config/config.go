// Package config discovers gala.toml or .gala.toml, applies defaults, and
// rejects any key gala does not know. A typo silently dropping its default is
// the failure mode this tool exists to avoid, so unknown keys are a hard error.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
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

// The supported managers. Go is tag-only: the tag is the publish.
const (
	ManagerGo    ManagerName = "go"
	ManagerNpm   ManagerName = "npm"
	ManagerCargo ManagerName = "cargo"
	ManagerPyPI  ManagerName = "pypi"
	ManagerNuGet ManagerName = "nuget"
	// ManagerNone has no manifest and no registry: tags and version files
	// carry the version, for any ecosystem without an adapter.
	ManagerNone ManagerName = "none"
)

// Package is one declared monorepo package. Declaration order is publish
// order: declare dependencies before their dependents. Manager and Publish
// default to the top-level values, so one repository can mix ecosystems.
type Package struct {
	Path         string
	Name         string
	TagPrefix    string
	Manager      ManagerName
	Publish      bool
	VersionFiles []VersionFile
	Hooks
}

// VersionFile is one more file that carries the release version, beyond the
// manager's manifest. Path is repository-relative. Exactly one of Key and
// Pattern addresses the value:
//
//   - Key is a dotted JSON path (metadata.version) for a .json file, or a
//     TOML table path ending in the key (package.version,
//     workspace.package.version) for a .toml file.
//   - Pattern is a regular expression with one capture group around the
//     version, for any other text file (__init__.py, Chart.yaml, VERSION).
type VersionFile struct {
	Path    string
	Key     string
	Pattern string
}

// Command is one configured hook command: the line as written, for messages,
// and its argv. Commands run directly, never through a shell.
type Command struct {
	Line string
	Argv []string
}

// Hooks are the commands one scope runs: the repository (top level, from the
// root) or a monorepo package (from its directory, only when it releases).
// PostBumpPaths are repository-relative.
type Hooks struct {
	PreRelease    []Command
	PostBump      []Command
	PostBumpPaths []string
}

// MatchesPostBumpPath reports whether a repo-relative path is one the postBump
// commands may change: an exact entry or a path.Match pattern.
func (h Hooks) MatchesPostBumpPath(p string) bool {
	for _, pattern := range h.PostBumpPaths {
		if ok, _ := path.Match(pattern, p); ok {
			return true
		}
	}
	return false
}

// Config is a validated config file.
type Config struct {
	Kind            Kind
	Manager         ManagerName // "" in a monorepo whose packages each name one
	Publish         bool
	GitHubRelease   bool
	ReleaseBranches []string
	SyncBranch      string
	TagPrefix       string
	Changelog       string
	Remote          string
	Packages        []Package
	VersionFiles    []VersionFile // app only
	Hooks

	// Source is the config file path, for messages.
	Source string
}

// packageFile mirrors one [[package]] table.
type packageFile struct {
	Path          string            `toml:"path"`
	Name          string            `toml:"name"`
	TagPrefix     string            `toml:"tagPrefix"`
	Manager       string            `toml:"manager"`
	Publish       *bool             `toml:"publish"`
	VersionFiles  []versionFileFile `toml:"versionFile"`
	PreRelease    []string          `toml:"preRelease"`
	PostBump      []string          `toml:"postBump"`
	PostBumpPaths []string          `toml:"postBumpPaths"`
}

// versionFileFile mirrors one [[versionFile]] table.
type versionFileFile struct {
	Path    string `toml:"path"`
	Key     string `toml:"key"`
	Pattern string `toml:"pattern"`
}

// file mirrors the config file — gala.toml and .gala.toml share one schema.
// Pointers distinguish "absent" from an explicit false, because two defaults
// (githubRelease, syncBranch) are not the zero value.
type file struct {
	Kind            string            `toml:"kind"`
	Manager         string            `toml:"manager"`
	Publish         *bool             `toml:"publish"`
	GitHubRelease   *bool             `toml:"githubRelease"`
	ReleaseBranches []string          `toml:"releaseBranches"`
	SyncBranch      *string           `toml:"syncBranch"`
	TagPrefix       *string           `toml:"tagPrefix"`
	Changelog       string            `toml:"changelog"`
	Remote          string            `toml:"remote"`
	Packages        []packageFile     `toml:"package"`
	VersionFiles    []versionFileFile `toml:"versionFile"`
	PreRelease      []string          `toml:"preRelease"`
	PostBump        []string          `toml:"postBump"`
	PostBumpPaths   []string          `toml:"postBumpPaths"`
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

	switch {
	case f.Manager != "":
		m, err := managerName(f.Manager)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		cfg.Manager = m
	case cfg.Kind == KindMonorepo && everyPackageNamesManager(f.Packages):
		// each package names its own manager
	default:
		return nil, fmt.Errorf("%s: missing required key \"manager\" (%s)", path, managerChoices)
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

	if cfg.Kind == KindApp {
		if cfg.VersionFiles, err = cfg.versionFiles(path, ".", f.VersionFiles); err != nil {
			return nil, err
		}
	} else if len(f.VersionFiles) > 0 {
		return nil, fmt.Errorf("%s: top-level [[versionFile]] is only valid for kind = \"app\"; "+
			"a monorepo package lists its own as [[package.versionFile]]", path)
	}
	if cfg.Hooks, err = hooks(path, "", ".", f.PreRelease, f.PostBump, f.PostBumpPaths); err != nil {
		return nil, err
	}
	// A file can carry only one version, so two packages cannot share one.
	owner := map[string]string{}
	for _, p := range f.Packages {
		pkg, err := cfg.loadPackage(path, p)
		if err != nil {
			return nil, err
		}
		for _, vf := range pkg.VersionFiles {
			if other, dup := owner[vf.Path]; dup {
				return nil, fmt.Errorf("%s: versionFile %q is listed by both package %q and package %q",
					path, vf.Path, other, pkg.Path)
			}
			owner[vf.Path] = pkg.Path
		}
		cfg.Packages = append(cfg.Packages, pkg)
	}
	return cfg, nil
}

// loadPackage resolves one [[package]]: its manager and publish setting fall
// back to the top level, and its version files and postBumpPaths are given
// relative to the package directory.
func (c *Config) loadPackage(source string, p packageFile) (Package, error) {
	pkg := Package{Path: p.Path, Name: p.Name, TagPrefix: p.TagPrefix, Manager: c.Manager, Publish: c.Publish}
	if p.Manager != "" {
		m, err := managerName(p.Manager)
		if err != nil {
			return pkg, fmt.Errorf("%s: package %q: %w", source, p.Path, err)
		}
		pkg.Manager = m
	}
	if p.Publish != nil {
		pkg.Publish = *p.Publish
	}
	var err error
	if pkg.VersionFiles, err = c.versionFiles(source, p.Path, p.VersionFiles); err != nil {
		return pkg, err
	}
	scope := fmt.Sprintf("package %q ", p.Path)
	if pkg.Hooks, err = hooks(source, scope, p.Path, p.PreRelease, p.PostBump, p.PostBumpPaths); err != nil {
		return pkg, err
	}
	return pkg, nil
}

const managerChoices = `"go", "npm", "cargo", "pypi", "nuget", or "none"`

func managerName(s string) (ManagerName, error) {
	switch ManagerName(s) {
	case ManagerGo, ManagerNpm, ManagerCargo, ManagerPyPI, ManagerNuGet, ManagerNone:
		return ManagerName(s), nil
	}
	return "", fmt.Errorf("invalid manager %q (want %s)", s, managerChoices)
}

func everyPackageNamesManager(pkgs []packageFile) bool {
	for _, p := range pkgs {
		if p.Manager == "" {
			return false
		}
	}
	return len(pkgs) > 0
}

// versionFiles validates [[versionFile]] entries whose paths are relative to
// base ("." for the repository root, or a package path) and returns them with
// repository-relative paths.
func (c *Config) versionFiles(source, base string, in []versionFileFile) ([]VersionFile, error) {
	var out []VersionFile
	seen := map[string]bool{}
	for i, vf := range in {
		if strings.TrimSpace(vf.Path) == "" {
			return nil, fmt.Errorf("%s: [[versionFile]] #%d is missing the required key \"path\"", source, i+1)
		}
		clean, ok := repoPath(path.Join(base, filepath.ToSlash(vf.Path)))
		if !ok || filepath.IsAbs(vf.Path) {
			return nil, fmt.Errorf("%s: versionFile path %q must be a relative path inside the repository", source, vf.Path)
		}
		if seen[clean] {
			return nil, fmt.Errorf("%s: duplicate versionFile path %q", source, clean)
		}
		seen[clean] = true
		if clean == c.Changelog {
			return nil, fmt.Errorf("%s: versionFile path %q is the changelog", source, clean)
		}
		if err := checkVersionFileAddress(clean, vf.Key, vf.Pattern); err != nil {
			return nil, fmt.Errorf("%s: versionFile %q: %w", source, clean, err)
		}
		out = append(out, VersionFile{Path: clean, Key: vf.Key, Pattern: vf.Pattern})
	}
	return out, nil
}

// checkVersionFileAddress validates that exactly one of key and pattern is
// set, and that it fits the file.
func checkVersionFileAddress(file, key, pattern string) error {
	switch {
	case key != "" && pattern != "":
		return errors.New("set key or pattern, not both")
	case pattern != "":
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("invalid pattern: %w", err)
		}
		if re.NumSubexp() != 1 {
			return fmt.Errorf("pattern %q must have exactly one capture group, around the version", pattern)
		}
		return nil
	case strings.TrimSpace(key) == "":
		return errors.New(`needs "key" (a JSON or TOML path such as "version" or "metadata.version") ` +
			`or "pattern" (a regular expression with one capture group)`)
	}
	for _, part := range strings.Split(key, ".") {
		if part == "" {
			return fmt.Errorf("invalid key %q", key)
		}
	}
	switch strings.ToLower(path.Ext(file)) {
	case ".json":
	case ".toml":
		if !strings.Contains(key, ".") {
			return errors.New(`a TOML key needs its table, such as "package.version"`)
		}
	default:
		return errors.New(`key works for .json and .toml files; use "pattern" for any other format`)
	}
	return nil
}

// hooks validates one scope's commands; postBumpPaths are relative to base and
// returned repository-relative.
func hooks(source, scope, base string, pre, post, paths []string) (Hooks, error) {
	var h Hooks
	var err error
	if h.PreRelease, err = commands(source, scope+"preRelease", pre); err != nil {
		return h, err
	}
	if h.PostBump, err = commands(source, scope+"postBump", post); err != nil {
		return h, err
	}
	if len(paths) > 0 && len(h.PostBump) == 0 {
		return h, fmt.Errorf("%s: %spostBumpPaths needs a postBump command to apply to", source, scope)
	}
	for _, p := range paths {
		clean, ok := repoPath(path.Join(base, filepath.ToSlash(p)))
		if !ok || filepath.IsAbs(p) {
			return h, fmt.Errorf("%s: %spostBumpPaths entry %q must be a relative path inside the repository", source, scope, p)
		}
		if _, err := path.Match(clean, ""); err != nil {
			return h, fmt.Errorf("%s: %spostBumpPaths entry %q is not a valid pattern: %w", source, scope, p, err)
		}
		h.PostBumpPaths = append(h.PostBumpPaths, clean)
	}
	return h, nil
}

func commands(source, key string, lines []string) ([]Command, error) {
	out := make([]Command, 0, len(lines))
	for _, line := range lines {
		argv, err := SplitCommand(line)
		if err != nil {
			return nil, fmt.Errorf("%s: %s command %q: %w", source, key, line, err)
		}
		if len(argv) == 0 {
			return nil, fmt.Errorf("%s: %s contains an empty command", source, key)
		}
		out = append(out, Command{Line: line, Argv: argv})
	}
	return out, nil
}

// SplitCommand splits a hook command into argv: whitespace separates words,
// and single or double quotes group a word that contains spaces. There is no
// shell, so no expansion, piping, or escapes beyond the quotes.
func SplitCommand(line string) ([]string, error) {
	var argv []string
	var word strings.Builder
	inWord := false
	var quote rune
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inWord = true
		case r == ' ' || r == '\t':
			if inWord {
				argv = append(argv, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inWord {
		argv = append(argv, word.String())
	}
	return argv, nil
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
		candidate := filepath.Join(root, name)
		// Lstat reports the name itself, so a dangling symlink still counts
		// as present and the two-name guard stays honest. Any other
		// inspection failure is reported, never read as absence.
		if _, err := os.Lstat(candidate); err == nil {
			found = append(found, name)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s: cannot inspect config file: %w", candidate, err)
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

// repoPath cleans a configured path to slash form and reports whether it
// stays inside the repository.
func repoPath(p string) (string, bool) {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "-") || escapesRoot(p) {
		return "", false
	}
	return filepath.ToSlash(filepath.Clean(p)), true
}

func escapesRoot(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean == ".." || strings.HasPrefix(clean, "../")
}
