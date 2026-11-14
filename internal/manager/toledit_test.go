package manager

import (
	"strings"
	"testing"
)

func TestSetTomlValue(t *testing.T) {
	cases := []struct {
		name    string
		content string
		section string
		key     string
		value   string
		want    string
		wantErr bool
	}{
		{
			name:    "simple",
			content: "[package]\nname = \"x\"\nversion = \"1.0.0\"\n",
			section: "package", key: "version", value: "1.1.0",
			want: "[package]\nname = \"x\"\nversion = \"1.1.0\"\n",
		},
		{
			name:    "commented value kept",
			content: "[package]\nversion = \"1.0.0\" # trailing\n",
			section: "package", key: "version", value: "1.1.0",
			want: "[package]\nversion = \"1.1.0\" # trailing\n",
		},
		{
			name:    "tight spacing kept",
			content: "[package]\nversion=1.0.0\n",
			section: "package", key: "version", value: "1.1.0",
			want: "[package]\nversion=\"1.1.0\"\n",
		},
		{
			name:    "wide spacing kept",
			content: "[package]\nversion    =    1.0.0\n",
			section: "package", key: "version", value: "1.1.0",
			want: "[package]\nversion    =    \"1.1.0\"\n",
		},
		{
			name:    "commented-out key ignored",
			content: "[package]\n# version = \"9.9.9\"\nversion = \"1.0.0\"\n",
			section: "package", key: "version", value: "2.0.0",
			want: "[package]\n# version = \"9.9.9\"\nversion = \"2.0.0\"\n",
		},
		{
			name:    "dotted key is not the key",
			content: "[package]\nversion.workspace = true\n",
			section: "package", key: "version", value: "2.0.0",
			wantErr: true,
		},
		{
			name:    "only the target section",
			content: "[package]\nversion = \"1.0.0\"\n\n[dependencies]\nversion = \"other\"\n",
			section: "package", key: "version", value: "1.1.0",
			want: "[package]\nversion = \"1.1.0\"\n\n[dependencies]\nversion = \"other\"\n",
		},
		{
			name:    "crlf preserved",
			content: "[package]\r\nversion = \"1.0.0\"\r\n",
			section: "package", key: "version", value: "1.1.0",
			want: "[package]\r\nversion = \"1.1.0\"\r\n",
		},
		{
			name:    "missing section",
			content: "[dependencies]\nversion = \"1\"\n",
			section: "package", key: "version", value: "2",
			wantErr: true,
		},
		{
			name:    "missing key",
			content: "[package]\nname = \"x\"\n",
			section: "package", key: "version", value: "2",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SetTomlValue(tc.content, tc.section, tc.key, tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGetTomlString(t *testing.T) {
	content := "[project]\nname = \"gizmo\"  # the real name\nversion = \"1.0.0\"\nflag = true\n\n[tool.x]\nname = \"other\"\n"

	if v, ok := GetTomlString(content, "project", "name"); !ok || v != "gizmo" {
		t.Errorf("name = %q %v", v, ok)
	}
	if v, ok := GetTomlString(content, "project", "version"); !ok || v != "1.0.0" {
		t.Errorf("version = %q %v", v, ok)
	}
	if v, ok := GetTomlString(content, "project", "flag"); !ok || v != "true" {
		t.Errorf("an unquoted value should read back verbatim: %q %v", v, ok)
	}
	if _, ok := GetTomlString(content, "project", "missing"); ok {
		t.Error("missing key must not be found")
	}
	if _, ok := GetTomlString(content, "missing", "name"); ok {
		t.Error("missing section must not be found")
	}
	if v, ok := GetTomlString(content, "tool.x", "name"); !ok || v != "other" {
		t.Errorf("dotted section = %q %v", v, ok)
	}
}

func TestSectionHasKey(t *testing.T) {
	content := "[package]\nname = \"x\"\nversion.workspace = true\n"
	if !SectionHasKey(content, "package", "version") {
		t.Error("version.workspace should count as the version key existing")
	}
	if !SectionHasKey(content, "package", "version.workspace") {
		t.Error("the dotted key itself must be found")
	}
	if SectionHasKey(content, "dependencies", "version") {
		t.Error("another section must not match")
	}
	if SectionHasKey("[package]\n# version.workspace = true\n", "package", "version.workspace") {
		t.Error("a commented key is not set; it must not count")
	}
}

func TestUnquoteEdge(t *testing.T) {
	v, ok := GetTomlString("[project]\nversion = \"\"\n", "project", "version")
	if !ok || v != "" {
		t.Errorf("empty quoted string = %q %v", v, ok)
	}
	if _, ok := GetTomlString("[project]\nversion = \"1.0.0\n", "project", "version"); ok {
		t.Error("an unterminated string must not parse")
	}
}

// TestSingleQuotedLiteralStrings: a '...' literal string reads without its
// quotes and without escape processing, wherever the key is read, and a write
// replaces it with a basic string while keeping the trailing comment.
func TestSingleQuotedLiteralStrings(t *testing.T) {
	doc := "[project]\nname = 'gizmo'\nversion = '1.2.3' # pinned\n" +
		"path = 'C:\\tools\\a b # not a comment' # real comment\nbroken = '1.0\n"
	for key, want := range map[string]string{
		"name":    "gizmo",
		"version": "1.2.3",
		"path":    `C:\tools\a b # not a comment`,
	} {
		if got, ok := GetTomlString(doc, "project", key); !ok || got != want {
			t.Errorf("%s = %q %v, want %q", key, got, ok, want)
		}
	}
	if _, ok := GetTomlString(doc, "project", "broken"); ok {
		t.Error("an unterminated literal string must not parse")
	}

	out, err := SetTomlValue(doc, "project", "version", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `version = "1.3.0" # pinned`) {
		t.Errorf("the write must replace the literal and keep the comment:\n%s", out)
	}
	out, err = SetTomlValue(doc, "project", "path", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `path = "x" # real comment`) {
		t.Errorf("a literal containing # and spaces must be replaced whole:\n%s", out)
	}
}
