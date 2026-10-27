// Package changelog renders release entries and prepends them to
// CHANGELOG.md, preserving the file's existing line endings.
package changelog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/alfpricot/gala/internal/commit"
)

// Section is one heading and its bullet lines.
type Section struct {
	Title string
	Lines []string
}

// sectionOrder is fixed: breaking changes always come first.
var sectionOrder = []struct {
	kind  commit.Kind
	title string
}{
	{commit.KindBreaking, "Breaking Changes"},
	{commit.KindFeature, "Features"},
	{commit.KindFix, "Bug Fixes"},
	{commit.KindPerf, "Performance"},
	{commit.KindRefactor, "Refactoring"},
	{commit.KindRevert, "Reverts"},
}

// Build groups commits into an entry under the given header line, for example
// "## 1.2.0 (2026-10-27)" or "## core 1.2.0 (2026-10-27)". Skipped and
// hidden commits never render.
func Build(header string, commits []commit.Commit) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")

	for _, so := range sectionOrder {
		var lines []string
		for _, c := range commits {
			if c.Kind() != so.kind {
				continue
			}
			lines = append(lines, bullet(c))
		}
		if len(lines) == 0 {
			continue
		}
		b.WriteString("\n### ")
		b.WriteString(so.title)
		b.WriteString("\n\n")
		for _, l := range lines {
			b.WriteString("* ")
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// bullet renders one commit: bold scope, description, then inline refs.
func bullet(c commit.Commit) string {
	line := c.Description
	if c.Scope != "" {
		line = fmt.Sprintf("**%s:** %s", c.Scope, line)
	}
	if len(c.Refs) > 0 {
		line += " (" + strings.Join(c.Refs, ", ") + ")"
	}
	return line
}

// Prepend inserts entry into the changelog at path, newest first: after the
// title header, before any existing "## " entry. A missing file is created
// with a "# Changelog" title. Line endings are detected from the existing
// content and preserved for the whole file.
func Prepend(path, entry string) error {
	data, err := os.ReadFile(path)
	missing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !missing {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}

	eol := "\n"
	content := ""
	if !missing {
		content = string(data)
		if strings.Contains(content, "\r\n") {
			eol = "\r\n"
		}
	}

	lines := split(content, eol)
	// Drop trailing blanks; the file is rebuilt with exactly one at the end.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	entryLines := split(strings.TrimRight(entry, "\n"), "\n")

	var out []string
	if i := firstH2(lines); i >= 0 {
		head := trimBlank(lines[:i])
		out = append(out, head...)
		if len(head) > 0 {
			out = append(out, "")
		}
		out = append(out, entryLines...)
		out = append(out, "")
		out = append(out, lines[i:]...)
	} else {
		head := lines
		if len(head) == 0 {
			head = []string{"# Changelog"}
		}
		out = append(out, head...)
		out = append(out, "")
		out = append(out, entryLines...)
	}
	out = append(out, "")

	return os.WriteFile(path, []byte(strings.Join(out, eol)), 0o644)
}

// HasEntry reports whether the changelog already documents a version, which
// tests use to assert an entry landed exactly once.
func HasEntry(content, header string) bool {
	return strings.Contains(content, header)
}

func split(s, sep string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, sep)
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func firstH2(lines []string) int {
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") {
			return i
		}
	}
	return -1
}
