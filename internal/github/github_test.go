package github

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateRelease(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotCT, gotAccept string
	var gotPayload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotAccept = r.Header.Get("Accept")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotPayload); err != nil {
			t.Errorf("payload is not JSON: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id": 1}`)
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), Base: srv.URL, Token: "tok123"}
	if err := c.CreateRelease("alfpricot", "gala", "v1.2.0", "v1.2.0", "## 1.2.0 (2026-10-27)"); err != nil {
		t.Fatal(err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s", gotMethod)
	}
	if gotPath != "/repos/alfpricot/gala/releases" {
		t.Errorf("path = %s", gotPath)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if !strings.Contains(gotAccept, "application/vnd.github+json") {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gotPayload["tag_name"] != "v1.2.0" || gotPayload["name"] != "v1.2.0" {
		t.Errorf("payload tag/name = %v/%v", gotPayload["tag_name"], gotPayload["name"])
	}
	if gotPayload["body"] != "## 1.2.0 (2026-10-27)" {
		t.Errorf("payload body = %v", gotPayload["body"])
	}
	if gotPayload["draft"] != false || gotPayload["prerelease"] != false {
		t.Errorf("draft/prerelease must be false: %v/%v", gotPayload["draft"], gotPayload["prerelease"])
	}
}

func TestCreateReleaseErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"Validation Failed","errors":[{"code":"already_exists"}]}`)
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), Base: srv.URL, Token: "tok"}
	err := c.CreateRelease("a", "r", "v1.0.0", "v1.0.0", "body")
	if err == nil {
		t.Fatal("a non-2xx response must be an error")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error should carry the status: %v", err)
	}
	if !strings.Contains(err.Error(), "Validation Failed") {
		t.Errorf("error should carry the API body: %v", err)
	}
	if !strings.Contains(err.Error(), "v1.0.0") {
		t.Errorf("error should name the tag: %v", err)
	}
}

func TestParseRemoteURL(t *testing.T) {
	cases := []struct {
		in    string
		host  string
		owner string
		repo  string
		ok    bool
	}{
		{in: "https://github.com/alfpricot/gala.git", host: "github.com", owner: "alfpricot", repo: "gala", ok: true},
		{in: "https://github.com/alfpricot/gala", host: "github.com", owner: "alfpricot", repo: "gala", ok: true},
		{in: "https://github.com/alfpricot/gala/", host: "github.com", owner: "alfpricot", repo: "gala", ok: true},
		{in: "git@github.com:alfpricot/gala.git", host: "github.com", owner: "alfpricot", repo: "gala", ok: true},
		{in: "git@github.com:alfpricot/gala", host: "github.com", owner: "alfpricot", repo: "gala", ok: true},
		{in: "ssh://git@github.com/alfpricot/gala.git", host: "github.com", owner: "alfpricot", repo: "gala", ok: true},
		{in: "ssh://git@github.com:22/alfpricot/gala.git", host: "github.com", owner: "alfpricot", repo: "gala", ok: true},
		{in: "git://github.com/alfpricot/gala.git", host: "github.com", owner: "alfpricot", repo: "gala", ok: true},
		{in: "https://gitlab.com/someone/project.git", host: "gitlab.com", owner: "someone", repo: "project", ok: true},
		{in: "https://github.com/only-org", ok: false},
		{in: "not a url", ok: false},
		{in: "", ok: false},
		{in: "https://github.com", ok: false},
	}
	for _, tc := range cases {
		got, err := ParseRemoteURL(tc.in)
		if tc.ok {
			if err != nil {
				t.Errorf("ParseRemoteURL(%q) error: %v", tc.in, err)
				continue
			}
			if got.Host != tc.host || got.Owner != tc.owner || got.Repo != tc.repo {
				t.Errorf("ParseRemoteURL(%q) = %+v, want %s/%s on %s", tc.in, got, tc.owner, tc.repo, tc.host)
			}
		} else if err == nil {
			t.Errorf("ParseRemoteURL(%q) = %+v, want an error", tc.in, got)
		}
	}
}

func TestIsGitHub(t *testing.T) {
	if !(Remote{Host: "github.com"}).IsGitHub() {
		t.Error("github.com should be a GitHub remote")
	}
	if !(Remote{Host: "GitHub.com"}).IsGitHub() {
		t.Error("host comparison should be case-insensitive")
	}
	if (Remote{Host: "gitlab.com"}).IsGitHub() {
		t.Error("gitlab.com is not GitHub")
	}
	if (Remote{Owner: "a", Repo: "b"}).String() != "a/b" {
		t.Error("String should be owner/repo")
	}
}
