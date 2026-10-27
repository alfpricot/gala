package github

import (
	"fmt"
	"strings"
)

// Remote is the owner/repo a git remote URL points at.
type Remote struct {
	Host  string
	Owner string
	Repo  string
}

// IsGitHub reports whether releases can be created for this remote.
func (r Remote) IsGitHub() bool { return strings.EqualFold(r.Host, "github.com") }

// String is owner/repo, for messages.
func (r Remote) String() string { return r.Owner + "/" + r.Repo }

// ParseRemoteURL understands the three shapes git remotes actually use:
// https, ssh://, and scp-style git@host:owner/repo, with or without the .git
// suffix. Anything it cannot parse is an error, so a malformed remote is
// reported instead of silently skipping the release step.
func ParseRemoteURL(raw string) (Remote, error) {
	var r Remote
	s := strings.TrimSpace(raw)
	if s == "" {
		return r, fmt.Errorf("empty remote URL")
	}

	// scp-style: [user@]host:path — no scheme, and the colon is not a port.
	if !strings.Contains(s, "://") {
		if i := strings.IndexByte(s, ':'); i > 0 && !strings.Contains(s[:i], "/") {
			cred := s[:i]
			if j := strings.IndexByte(cred, '@'); j >= 0 {
				cred = cred[j+1:]
			}
			r.Host = cred
			return finish(&r, s[i+1:])
		}
		return r, fmt.Errorf("cannot parse remote URL %q", raw)
	}

	rest := s[strings.Index(s, "://")+3:]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		host := rest[:i]
		if j := strings.IndexByte(host, '@'); j >= 0 {
			host = host[j+1:]
		}
		if k := strings.IndexByte(host, ':'); k >= 0 {
			host = host[:k] // ssh://host:port/path
		}
		if host == "" {
			return r, fmt.Errorf("cannot parse remote URL %q: no host", raw)
		}
		r.Host = host
		return finish(&r, rest[i+1:])
	}
	return r, fmt.Errorf("cannot parse remote URL %q: no path", raw)
}

func finish(r *Remote, path string) (Remote, error) {
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return *r, fmt.Errorf("cannot parse remote URL: no owner/repo in path %q", path)
	}
	// Nested groups (gitlab "group/sub/repo") keep the last two elements.
	r.Owner = parts[len(parts)-2]
	r.Repo = parts[len(parts)-1]
	if r.Owner == "" || r.Repo == "" {
		return *r, fmt.Errorf("cannot parse remote URL: empty owner or repo")
	}
	return *r, nil
}
