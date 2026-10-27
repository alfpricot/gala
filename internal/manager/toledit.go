package manager

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Errors returned by the section-and-key text editor. Adapters wrap them with
// the file name so the message names the file to fix.
var (
	// ErrSection means the TOML table is not in the file.
	ErrSection = errors.New("section not found")
	// ErrKey means the table exists but the key does not: dynamic versioning
	// or version.workspace = true land here for the other adapters to explain.
	ErrKey = errors.New("key not found")
)

// SetTomlValue rewrites exactly one line: the key inside the named section.
// Every other byte of the file — comments, ordering, spacing, line endings —
// is preserved. TOML is never marshalled.
//
// Sections are matched by their literal header ([project], [package]); the
// search stops at the next table header. A commented-out key is not a key.
func SetTomlValue(content, section, key, value string) (string, error) {
	start, end, err := findSection(content, section)
	if err != nil {
		return "", err
	}

	lines := strings.Split(content, "\n")
	keyRe, err := keyLineRe(key)
	if err != nil {
		return "", err
	}

	for i := start + 1; i < end; i++ {
		m := keyRe.FindStringSubmatchIndex(lines[i])
		if m == nil {
			continue
		}
		// group 1 is "<key> <ws>= <ws>"; group 2 is the rest of the line.
		prefix := lines[i][m[2]:m[3]]
		rest := lines[i][m[4]:m[5]]
		suffix, err := valueSuffix(rest)
		if err != nil {
			return "", err
		}
		lines[i] = prefix + strconv.Quote(value) + suffix
		return strings.Join(lines, "\n"), nil
	}
	return "", fmt.Errorf("%s in [%s]: %w", key, section, ErrKey)
}

// GetTomlString reads a quoted string value out of a section. It returns
// ("", false) when the section or the key is absent.
func GetTomlString(content, section, key string) (string, bool) {
	start, end, err := findSection(content, section)
	if err != nil {
		return "", false
	}
	lines := strings.Split(content, "\n")
	keyRe, err := keyLineRe(key)
	if err != nil {
		return "", false
	}
	for i := start + 1; i < end; i++ {
		m := keyRe.FindStringSubmatchIndex(lines[i])
		if m == nil {
			continue
		}
		rest := strings.TrimLeft(lines[i][m[4]:m[5]], " \t")
		var raw string
		if strings.HasPrefix(rest, `"`) {
			closed := false
			j := 1
			for ; j < len(rest); j++ {
				if rest[j] == '\\' {
					j++
					continue
				}
				if rest[j] == '"' {
					j++
					closed = true
					break
				}
			}
			if !closed {
				return "", false
			}
			raw = rest[:j]
		} else {
			j := strings.IndexAny(rest, " \t\r#")
			if j < 0 {
				j = len(rest)
			}
			raw = rest[:j]
		}
		v, ok := unquote(raw)
		if !ok {
			return "", false
		}
		return v, true
	}
	return "", false
}

// SectionHasKey reports whether the key (or a dotted form of it, such as
// version.workspace) appears inside the section.
func SectionHasKey(content, section, key string) bool {
	start, end, err := findSection(content, section)
	if err != nil {
		return false
	}
	lines := strings.Split(content, "\n")
	re, err := regexp.Compile(`^[ \t]*` + regexp.QuoteMeta(key) + `(\.[A-Za-z0-9_-]+)*[ \t]*=`)
	if err != nil {
		return false
	}
	for i := start + 1; i < end; i++ {
		if re.MatchString(lines[i]) {
			return true
		}
	}
	return false
}

// SectionLines returns the raw lines of a table (header included).
func SectionLines(content, section string) ([]string, error) {
	start, end, err := findSection(content, section)
	if err != nil {
		return nil, err
	}
	return strings.Split(content, "\n")[start:end], nil
}

func findSection(content, section string) (start, end int, err error) {
	lines := strings.Split(content, "\n")
	header := regexp.MustCompile(`^[ \t]*\[` + regexp.QuoteMeta(section) + `[ \t]*][ \t]*(#.*)?\r?$`)
	anyHeader := regexp.MustCompile(`^[ \t]*\[`)
	for i, l := range lines {
		if header.MatchString(l) {
			start = i
			for j := i + 1; j < len(lines); j++ {
				if anyHeader.MatchString(lines[j]) {
					return start, j, nil
				}
			}
			return start, len(lines), nil
		}
	}
	return 0, 0, fmt.Errorf("[%s]: %w", section, ErrSection)
}

func keyLineRe(key string) (*regexp.Regexp, error) {
	// The key must be followed by optional space and "=", so version.workspace
	// never matches a lookup of "version".
	return regexp.Compile(`^([ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=[ \t]*)(.*)$`)
}

// valueSuffix returns everything after the value on the line: a trailing
// comment and, for CRLF files, the carriage return.
func valueSuffix(rest string) (string, error) {
	if strings.HasPrefix(rest, `"`) {
		for i := 1; i < len(rest); i++ {
			if rest[i] == '\\' {
				i++
				continue
			}
			if rest[i] == '"' {
				return rest[i+1:], nil
			}
		}
		return "", errors.New("unterminated string value")
	}
	// Unquoted value: it ends at whitespace or a comment.
	for i := 0; i < len(rest); i++ {
		if rest[i] == ' ' || rest[i] == '\t' || rest[i] == '\r' || rest[i] == '#' {
			return rest[i:], nil
		}
	}
	return "", nil
}

func unquote(s string) (string, bool) {
	if len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		v, err := strconv.Unquote(s)
		if err != nil {
			return s[1 : len(s)-1], true
		}
		return v, true
	}
	return s, s != ""
}
