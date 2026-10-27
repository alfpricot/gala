// Package commit parses Conventional Commit messages and applies gala's type
// policy: which types produce a bump, which are hidden from the changelog,
// and which messages are skipped outright.
package commit

import (
	"regexp"
	"strings"

	"github.com/alfpricot/gala/internal/semver"
)

// Kind classifies a parsed commit for changelog rendering.
type Kind int

const (
	// KindNone renders nothing (hidden or skipped).
	KindNone Kind = iota
	// KindBreaking renders under Breaking Changes, first.
	KindBreaking
	// KindFeature renders under Features.
	KindFeature
	// KindFix renders under Bug Fixes.
	KindFix
	// KindPerf renders under Performance.
	KindPerf
	// KindRefactor renders under Refactoring.
	KindRefactor
	// KindRevert renders under Reverts.
	KindRevert
)

// Commit is one parsed commit.
type Commit struct {
	Hash        string
	Subject     string
	Body        string
	Type        string
	Scope       string
	Description string
	Breaking    bool
	Refs        []string
	// Conforming is true when the header parses as a Conventional Commit with
	// a type gala knows. Merge commits and other free-form messages are not
	// conforming: they are counted and reported as skipped, never rendered.
	Conforming bool
}

var (
	// header: type(scope)?!?: description
	headerRe = regexp.MustCompile(`^([A-Za-z]+)(\(([^)]*)\))?(!)?:\s(.*)$`)
	// footer: BREAKING CHANGE: text or BREAKING-CHANGE: text
	breakingRe = regexp.MustCompile(`(?m)^BREAKING[- ]CHANGE:\s?(.*)$`)
	// issue-style references in the subject, and in a body line that starts
	// with a closure keyword (Closes #7, fixes: #8) — a number mentioned in
	// prose is not a reference.
	refRe = regexp.MustCompile(`#\d+`)
	// footerRef matches the keyword that introduces references in a body line.
	footerRef = regexp.MustCompile(`(?i)^(?:closes?|fixed?|fixes|resolves?|refs?|references|implements)(?:\s*:\s*|\s+)(.*)$`)
)

// knownTypes is the full policy table: anything not listed is skipped.
var knownTypes = map[string]bool{
	"feat": true, "fix": true, "perf": true, "refactor": true,
	"revert": true, "docs": true, "test": true, "style": true,
	"build": true, "ci": true, "chore": true,
}

// hiddenTypes produce no changelog section but are still well-formed commits.
var hiddenTypes = map[string]bool{
	"docs": true, "test": true, "style": true, "build": true,
	"ci": true, "chore": true,
}

// Parse parses subject and body of one commit. The header alone decides
// conformance; breaking-ness comes from the "!" suffix or a breaking footer.
func Parse(hash, subject, body string) Commit {
	c := Commit{
		Hash:    hash,
		Subject: subject,
		Body:    body,
	}

	m := headerRe.FindStringSubmatch(subject)
	if m == nil || !knownTypes[strings.ToLower(m[1])] {
		// Non-conforming, or a type gala has no policy for: skipped.
		c.Breaking = false
		c.Refs = collectRefs(subject, body)
		return c
	}

	c.Conforming = true
	c.Type = strings.ToLower(m[1])
	c.Scope = strings.TrimSpace(m[3])
	c.Breaking = m[4] == "!"
	c.Description = strings.TrimSpace(m[5])
	if !c.Breaking {
		c.Breaking = breakingRe.MatchString(body)
	}
	c.Refs = collectRefs(subject, body)
	return c
}

func collectRefs(parts ...string) []string {
	var refs []string
	seen := make(map[string]bool)
	add := func(text string) {
		for _, r := range refRe.FindAllString(text, -1) {
			if !seen[r] {
				seen[r] = true
				refs = append(refs, r)
			}
		}
	}

	// Subject: any #N counts — that is where a changelog bullet points.
	add(parts[0])

	// Body: only a line that starts with a closure keyword contributes. The
	// same line can carry several references ("closes #7 and #42").
	if len(parts) > 1 {
		for _, line := range strings.Split(parts[1], "\n") {
			if m := footerRef.FindStringSubmatch(line); m != nil {
				add(m[1])
			}
		}
	}
	return refs
}

// Bump maps the commit to its version bump. A breaking marker overrides the
// type and asks for a major bump; hidden types and skipped messages ask for
// none.
func (c Commit) Bump() semver.Bump {
	if !c.Conforming {
		return semver.None
	}
	if c.Breaking {
		return semver.Major
	}
	switch c.Type {
	case "feat":
		return semver.Minor
	case "fix", "perf", "refactor", "revert":
		return semver.Patch
	default: // docs, test, style, build, chore: hidden, no bump
		return semver.None
	}
}

// Skipped reports whether the commit is counted as skipped rather than merely
// hidden: non-conforming messages (including promotion merge commits) and
// types outside the policy table.
func (c Commit) Skipped() bool {
	return !c.Conforming
}

// Hidden reports whether the commit renders no changelog section despite
// being well-formed.
func (c Commit) Hidden() bool {
	return c.Conforming && hiddenTypes[c.Type]
}

// Kind selects the changelog section. Breaking commits go to Breaking
// Changes only; the bump overrides the type there too.
func (c Commit) Kind() Kind {
	if !c.Conforming {
		return KindNone
	}
	if c.Breaking {
		return KindBreaking
	}
	switch c.Type {
	case "feat":
		return KindFeature
	case "fix":
		return KindFix
	case "perf":
		return KindPerf
	case "refactor":
		return KindRefactor
	case "revert":
		return KindRevert
	default:
		return KindNone
	}
}
