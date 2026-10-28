package manager

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alfpricot/gala/internal/config"
	"github.com/alfpricot/gala/internal/semver"
)

// Extra version files ([[versionFile]]) carry the release version beside the
// manager's manifest: a package.json next to a Cargo.toml, a Python
// __init__.py next to pyproject.toml, a chart's Chart.yaml. JSON and TOML are
// addressed by key, anything else by a regular expression; every edit changes
// only the version's bytes.

// ReadVersionFile returns the version recorded in a version file.
func ReadVersionFile(root string, vf config.VersionFile) (semver.Version, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(vf.Path)))
	if err != nil {
		return semver.Version{}, err
	}
	var raw string
	switch {
	case vf.Pattern != "":
		raw, err = patternValue(data, vf.Pattern)
	case isJSON(vf.Path):
		raw, err = GetJSONString(data, vf.Key)
	default:
		section, key := tomlKey(vf.Key)
		var ok bool
		if raw, ok = GetTomlString(string(data), section, key); !ok {
			err = fmt.Errorf("no string %s in [%s]", key, section)
		}
	}
	if err != nil {
		return semver.Version{}, fmt.Errorf("%s: %w", vf.Path, err)
	}
	v, err := semver.Parse(raw)
	if err != nil {
		return semver.Version{}, fmt.Errorf("%s: %s: %w", vf.Path, Address(vf), err)
	}
	return v, nil
}

// WriteVersionFile writes v into a version file, changing only the value.
func WriteVersionFile(root string, vf config.VersionFile, v semver.Version) error {
	file := filepath.Join(root, filepath.FromSlash(vf.Path))
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var edited []byte
	switch {
	case vf.Pattern != "":
		edited, err = replacePattern(data, vf.Pattern, v.String())
	case isJSON(vf.Path):
		edited, err = SetJSONString(data, vf.Key, v.String())
	default:
		section, key := tomlKey(vf.Key)
		var s string
		s, err = SetTomlValue(string(data), section, key, v.String())
		edited = []byte(s)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", vf.Path, err)
	}
	return os.WriteFile(file, edited, 0o644)
}

// Address names how a version file is addressed, for messages.
func Address(vf config.VersionFile) string {
	if vf.Pattern != "" {
		return "pattern " + vf.Pattern
	}
	return vf.Key
}

// patternValue returns the capture group's value. Every match must agree, so
// a file that mentions the version twice (a constant and a docstring) cannot
// drift.
func patternValue(data []byte, pattern string) (string, error) {
	value, _, err := patternSpans(data, pattern)
	return value, err
}

// replacePattern rewrites the capture group of every match.
func replacePattern(data []byte, pattern, value string) ([]byte, error) {
	_, spans, err := patternSpans(data, pattern)
	if err != nil {
		return nil, err
	}
	var out []byte
	last := 0
	for _, s := range spans {
		out = append(out, data[last:s[0]]...)
		out = append(out, value...)
		last = s[1]
	}
	return append(out, data[last:]...), nil
}

// patternSpans matches pattern against a normalized view of data, with any
// UTF-8 byte-order mark removed and CRLF folded to LF, so `^(\S+)` never
// captures a BOM and `(?m)...$` works on Windows checkouts. It returns the
// agreed capture value and each capture's byte range in the original data.
func patternSpans(data []byte, pattern string) (string, [][2]int, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", nil, err
	}
	view, offsets := normalizeLines(data)
	matches := re.FindAllSubmatchIndex(view, -1)
	if len(matches) == 0 {
		return "", nil, fmt.Errorf("pattern %q matches nothing", pattern)
	}
	var value string
	spans := make([][2]int, 0, len(matches))
	for i, m := range matches {
		if m[2] < 0 {
			return "", nil, fmt.Errorf("pattern %q matched without its capture group", pattern)
		}
		got := string(view[m[2]:m[3]])
		if i == 0 {
			value = got
		} else if got != value {
			return "", nil, fmt.Errorf("pattern %q matches disagree: %q and %q", pattern, value, got)
		}
		// The end comes from the last captured byte, not the byte after it,
		// so a capture just before a dropped CR never swallows that CR.
		end := offsets[m[2]]
		if m[3] > m[2] {
			end = offsets[m[3]-1] + 1
		}
		spans = append(spans, [2]int{offsets[m[2]], end})
	}
	return value, spans, nil
}

// normalizeLines drops a leading UTF-8 BOM and the CR of every CRLF. offsets
// maps each index of the view (and one past its end) to the original data.
func normalizeLines(data []byte) ([]byte, []int) {
	start := 0
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		start = 3
	}
	view := make([]byte, 0, len(data)-start)
	offsets := make([]int, 0, len(data)-start+1)
	for i := start; i < len(data); i++ {
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			continue
		}
		view = append(view, data[i])
		offsets = append(offsets, i)
	}
	return view, append(offsets, len(data))
}

// LockCommand is one lockfile refresh a version file needs: run Name Args in
// Dir (repository-relative) so the tool re-records Lock.
type LockCommand struct {
	Dir  string
	Lock string
	Name string
	Args []string
}

// lockRule ties a manifest file name to the lockfile its tool owns. Only a
// version file addressing that manifest's own version key triggers a refresh.
type lockRule struct {
	lock string
	keys []string
	argv []string
}

var lockRules = map[string]lockRule{
	"package.json":   {lock: "package-lock.json", keys: []string{"version"}, argv: []string{"npm", "install", "--package-lock-only"}},
	"Cargo.toml":     {lock: "Cargo.lock", keys: []string{"package.version", "workspace.package.version"}, argv: []string{"cargo", "update", "--workspace"}},
	"pyproject.toml": {lock: "uv.lock", keys: []string{"project.version", "tool.poetry.version"}, argv: []string{"uv", "lock"}},
}

// VersionFileLocks plans the lockfile refreshes for version files that are
// themselves manifests. Each refresh runs where its lockfile is: in the
// file's own directory, or at the root, where an npm, Cargo, or uv workspace
// records every member. Each lockfile is planned once.
func VersionFileLocks(root string, files []config.VersionFile) []LockCommand {
	var out []LockCommand
	seen := map[string]bool{}
	for _, vf := range files {
		rule, ok := lockRules[path.Base(vf.Path)]
		if !ok || vf.Pattern != "" || !contains(rule.keys, vf.Key) {
			continue
		}
		for _, dir := range uniqueDirs(path.Dir(vf.Path), ".") {
			lock := path.Join(dir, rule.lock)
			if seen[lock] {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(lock))); err != nil {
				continue
			}
			seen[lock] = true
			out = append(out, LockCommand{Dir: dir, Lock: lock, Name: rule.argv[0], Args: rule.argv[1:]})
		}
	}
	return out
}

// RunLockCommands runs planned refreshes, skipping lockfiles in done (already
// refreshed after the version writes), and returns the lockfiles to stage.
func RunLockCommands(r Runner, root string, cmds []LockCommand, done map[string]bool) ([]string, error) {
	var locks []string
	for _, c := range cmds {
		locks = append(locks, c.Lock)
		if done[c.Lock] {
			continue
		}
		if _, err := r.Run(filepath.Join(root, filepath.FromSlash(c.Dir)), c.Name, c.Args...); err != nil {
			return locks, fmt.Errorf("%s %s in %s: %w", c.Name, strings.Join(c.Args, " "), c.Dir, err)
		}
		done[c.Lock] = true
	}
	return locks, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func isJSON(p string) bool {
	return strings.EqualFold(path.Ext(p), ".json")
}

// tomlKey splits package.version into the table "package" and the key
// "version"; workspace.package.version into "workspace.package" and "version".
func tomlKey(keyPath string) (section, key string) {
	i := strings.LastIndex(keyPath, ".")
	return keyPath[:i], keyPath[i+1:]
}
