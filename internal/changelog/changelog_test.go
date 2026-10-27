package changelog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alfpricot/gala/internal/commit"
)

func TestBuildSections(t *testing.T) {
	commits := []commit.Commit{
		commit.Parse("a1", "feat(ui): add the plan view", "refs #12"),
		commit.Parse("a2", "fix: correct tag ordering", ""),
		commit.Parse("a3", "feat!: drop the old flag", ""),
		commit.Parse("a4", "perf: cache the log", ""),
		commit.Parse("a5", "refactor: split the pipeline", ""),
		commit.Parse("a6", "revert: undo the flag", ""),
		commit.Parse("a7", "chore: bump deps", ""),
		commit.Parse("a8", "Merge branch 'dev' into main", ""),
	}
	got := Build("## 1.2.0 (2026-10-27)", commits)

	want := strings.Join([]string{
		"## 1.2.0 (2026-10-27)",
		"",
		"### Breaking Changes",
		"",
		"* drop the old flag",
		"",
		"### Features",
		"",
		"* **ui:** add the plan view (#12)",
		"",
		"### Bug Fixes",
		"",
		"* correct tag ordering",
		"",
		"### Performance",
		"",
		"* cache the log",
		"",
		"### Refactoring",
		"",
		"* split the pipeline",
		"",
		"### Reverts",
		"",
		"* undo the flag",
	}, "\n")

	if got != want {
		t.Errorf("Build mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestBuildEmpty(t *testing.T) {
	got := Build("## 0.1.0 (2026-10-27)", nil)
	if got != "## 0.1.0 (2026-10-27)" {
		t.Errorf("empty entry = %q", got)
	}
}

// TestBuildDoesNotRepeatRefs: squash-merge subjects already end in (#42), and
// the ref extraction finds that same number.
func TestBuildDoesNotRepeatRefs(t *testing.T) {
	commits := []commit.Commit{
		commit.Parse("a", "fix: give the release workflow a git identity (#1)", ""),
		commit.Parse("b", "fix(core): repair the edge case (#2)", ""),
	}
	got := Build("## 0.1.1 (2026-10-27)", commits)

	if strings.Count(got, "(#1)") != 1 {
		t.Errorf("ref #1 must appear exactly once:\n%s", got)
	}
	// A body ref the subject does not carry is still appended, next to the
	// one the squash title already had.
	mixed := Build("## 0.1.1 (2026-10-27)", []commit.Commit{
		commit.Parse("c", "fix: two things (#1)", "closes #7"),
	})
	if strings.Count(mixed, "(#1)") != 1 || !strings.Contains(mixed, "two things (#1) (#7)") {
		t.Errorf("mixed refs should render both without repeating #1:\n%s", mixed)
	}
}

func TestBuildBreakingFirstRegardlessOfOrder(t *testing.T) {
	commits := []commit.Commit{
		commit.Parse("a", "feat: later", ""),
		commit.Parse("b", "fix!: earlier breaking", ""),
	}
	got := Build("## 2.0.0 (2026-10-27)", commits)
	if strings.Index(got, "### Breaking Changes") > strings.Index(got, "### Features") {
		t.Errorf("breaking changes must be listed first:\n%s", got)
	}
}

func TestPrependMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	if err := Prepend(path, "## 1.0.0 (2026-10-27)\n\n### Features\n\n* first"); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	want := "# Changelog\n\n## 1.0.0 (2026-10-27)\n\n### Features\n\n* first\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestPrependHeaderOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	write(t, path, "# Changelog\n")
	if err := Prepend(path, "## 1.0.0 (2026-10-27)"); err != nil {
		t.Fatal(err)
	}
	want := "# Changelog\n\n## 1.0.0 (2026-10-27)\n"
	if got := read(t, path); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrependExistingNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	write(t, path, "# Changelog\n\n## 1.0.0 (2026-01-01)\n\n### Features\n\n* old\n")
	if err := Prepend(path, "## 1.1.0 (2026-10-27)\n\n### Bug Fixes\n\n* new"); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !HasEntry(got, "## 1.1.0 (2026-10-27)") || !HasEntry(got, "## 1.0.0 (2026-01-01)") {
		t.Fatalf("both entries must be present:\n%s", got)
	}
	if strings.Index(got, "1.1.0") > strings.Index(got, "1.0.0") {
		t.Errorf("newest entry must come first:\n%s", got)
	}
	// The title stays on top.
	if strings.SplitN(got, "\n", 2)[0] != "# Changelog" {
		t.Errorf("title moved:\n%s", got)
	}
}

func TestPrependPreservesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	write(t, path, "# Changelog\r\n\r\n## 1.0.0 (2026-01-01)\r\n\r\n* old\r\n")
	if err := Prepend(path, "## 1.1.0 (2026-10-27)"); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if strings.Contains(got, "\r\n") == false {
		t.Fatalf("CRLF was lost:\n%q", got)
	}
	if strings.Contains(got, "\n\n") && strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		// after normalizing, no lone LF should remain
		normalized := strings.ReplaceAll(got, "\r\n", "")
		if strings.Contains(normalized, "\n") {
			t.Errorf("found a bare LF in a CRLF file:\n%q", got)
		}
	}
	if !HasEntry(got, "## 1.1.0") || !HasEntry(got, "## 1.0.0") {
		t.Errorf("entries missing:\n%q", got)
	}
}

func TestPrependKeepsCommentsAndExistingBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CHANGELOG.md")
	original := "# Changelog\n\n<!-- maintained by hand -->\n\n## 0.1.0 (2026-01-01)\n\n* first\n"
	write(t, path, original)
	if err := Prepend(path, "## 0.2.0 (2026-02-01)"); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, "<!-- maintained by hand -->") {
		t.Errorf("comment lost:\n%s", got)
	}
	if strings.Index(got, "## 0.2.0") > strings.Index(got, "## 0.1.0") {
		t.Errorf("ordering wrong:\n%s", got)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
