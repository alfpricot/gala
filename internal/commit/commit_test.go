package commit

import (
	"reflect"
	"testing"

	"github.com/alfpricot/gala/internal/semver"
)

func TestParseTypes(t *testing.T) {
	cases := []struct {
		subject    string
		body       string
		typ        string
		scope      string
		desc       string
		conforming bool
		bump       semver.Bump
		kind       Kind
		skipped    bool
	}{
		{subject: "feat: add plan command", typ: "feat", desc: "add plan command",
			conforming: true, bump: semver.Minor, kind: KindFeature},
		{subject: "fix(core): correct the tag order", typ: "fix", scope: "core", desc: "correct the tag order",
			conforming: true, bump: semver.Patch, kind: KindFix},
		{subject: "perf: faster log parsing", typ: "perf", desc: "faster log parsing",
			conforming: true, bump: semver.Patch, kind: KindPerf},
		{subject: "refactor: split the pipeline", typ: "refactor", desc: "split the pipeline",
			conforming: true, bump: semver.Patch, kind: KindRefactor},
		{subject: "revert: undo the flag", typ: "revert", desc: "undo the flag",
			conforming: true, bump: semver.Patch, kind: KindRevert},
		{subject: "chore(release): v1.2.0", typ: "chore", scope: "release", desc: "v1.2.0",
			conforming: true, bump: semver.None, kind: KindNone},
		{subject: "docs: explain the config", typ: "docs", desc: "explain the config",
			conforming: true, bump: semver.None, kind: KindNone},
		{subject: "ci: run the gate", typ: "ci", desc: "run the gate",
			conforming: true, bump: semver.None, kind: KindNone},
		// Non-conforming: promotion merge commits and free-form messages are
		// counted and reported as skipped, never rendered.
		{subject: "Merge branch 'dev' into main", conforming: false, bump: semver.None,
			kind: KindNone, skipped: true},
		{subject: "fixed a bug", conforming: false, bump: semver.None, kind: KindNone, skipped: true},
		// A type outside the policy table is not conforming either.
		{subject: "wip: half done", conforming: false, bump: semver.None, kind: KindNone, skipped: true},
	}

	for _, tc := range cases {
		c := Parse("abc1234", tc.subject, tc.body)
		if c.Conforming != tc.conforming {
			t.Errorf("%q: Conforming = %v, want %v", tc.subject, c.Conforming, tc.conforming)
		}
		if c.Type != tc.typ {
			t.Errorf("%q: Type = %q, want %q", tc.subject, c.Type, tc.typ)
		}
		if c.Scope != tc.scope {
			t.Errorf("%q: Scope = %q, want %q", tc.subject, c.Scope, tc.scope)
		}
		if c.Description != tc.desc {
			t.Errorf("%q: Description = %q, want %q", tc.subject, c.Description, tc.desc)
		}
		if c.Bump() != tc.bump {
			t.Errorf("%q: Bump = %s, want %s", tc.subject, c.Bump(), tc.bump)
		}
		if c.Kind() != tc.kind {
			t.Errorf("%q: Kind = %v, want %v", tc.subject, c.Kind(), tc.kind)
		}
		if c.Skipped() != tc.skipped {
			t.Errorf("%q: Skipped = %v, want %v", tc.subject, c.Skipped(), tc.skipped)
		}
	}
}

func TestBreaking(t *testing.T) {
	cases := []struct {
		subject string
		body    string
		want    bool
		bump    semver.Bump
	}{
		{"feat!: drop the old flag", "", true, semver.Major},
		{"fix(core)!: repair the wire format", "", true, semver.Major},
		{"feat: rename the option", "BREAKING CHANGE: --foo is now --bar", true, semver.Major},
		{"feat: rename the option", "BREAKING-CHANGE: --foo is now --bar", true, semver.Major},
		{"feat: rename the option", "notes\n\nBREAKING CHANGE: gone", true, semver.Major},
		{"feat: rename the option", "this mentions breaking changes but not as a footer", false, semver.Minor},
		{"feat: plain", "", false, semver.Minor},
	}
	for _, tc := range cases {
		c := Parse("abc", tc.subject, tc.body)
		if c.Breaking != tc.want {
			t.Errorf("%q/%q: Breaking = %v, want %v", tc.subject, tc.body, c.Breaking, tc.want)
		}
		if c.Bump() != tc.bump {
			t.Errorf("%q/%q: Bump = %s, want %s", tc.subject, tc.body, c.Bump(), tc.bump)
		}
		if tc.want && c.Kind() != KindBreaking {
			t.Errorf("%q: breaking commits must render under Breaking Changes", tc.subject)
		}
	}
}

func TestRefs(t *testing.T) {
	c := Parse("abc", "fix: wrong pin (#42)", "closes #7 and #42")
	want := []string{"#42", "#7"}
	if !reflect.DeepEqual(c.Refs, want) {
		t.Errorf("Refs = %v, want %v", c.Refs, want)
	}
	if none := Parse("abc", "fix: nothing referenced", ""); len(none.Refs) != 0 {
		t.Errorf("Refs = %v, want none", none.Refs)
	}
	// Non-conforming commits still surface their subject refs so a skipped
	// message can be traced back.
	m := Parse("abc", "addressed #99 in passing", "")
	if !reflect.DeepEqual(m.Refs, []string{"#99"}) {
		t.Errorf("Refs = %v, want [#99]", m.Refs)
	}
	// A closure footer contributes; prose that merely mentions a number or an
	// example like "(#42)" does not.
	f := Parse("abc", "fix: honor the config file",
		"the renderer used to append (#42) verbatim, see the example.\n\nCloses #7")
	if !reflect.DeepEqual(f.Refs, []string{"#7"}) {
		t.Errorf("Refs = %v, want [#7] — prose numbers must not count", f.Refs)
	}
	// A footer keyword only counts at the start of a line, followed directly
	// by the reference: mid-line keywords and keyword-led prose are not refs.
	mid := Parse("abc", "fix: subject", "the phrase closes #8 appears mid-line")
	if len(mid.Refs) != 0 {
		t.Errorf("Refs = %v, want none — a mid-line keyword is prose", mid.Refs)
	}
	prose := Parse("abc", "fix: subject",
		"Ref extraction scanned the whole message, so an illustrative (#42) in body prose\nbecame a changelog reference.")
	if len(prose.Refs) != 0 {
		t.Errorf("Refs = %v, want none — a keyword-led sentence is prose", prose.Refs)
	}
}

func TestHidden(t *testing.T) {
	if !Parse("abc", "chore: bump deps", "").Hidden() {
		t.Error("chore should be hidden")
	}
	if Parse("abc", "feat: x", "").Hidden() {
		t.Error("feat should not be hidden")
	}
}
