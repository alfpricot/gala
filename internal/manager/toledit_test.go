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
	if strings.Contains("", "") == false {
		t.Fatal("sanity")
	}
}
