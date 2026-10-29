package git

import (
	"errors"
	"fmt"
	"strings"
)

// RawCommit is one commit as git reported it: hash, subject, body, and the
// files it touched (empty for merge commits, which git does not diff by
// default).
type RawCommit struct {
	Hash    string
	Subject string
	Body    string
	Files   []string
}

const (
	recordSep = "\x1e"
	fieldSep  = "\x1f"
	// logFormat opens and closes each commit record with a record separator
	// and separates subject from body with a field separator, so the file
	// names git appends afterwards land in their own chunk when the output is
	// split on the record separator.
	logFormat = "%x1e%H%x1f%s%x1f%b%x1e"
)

// Status returns the paths of everything git considers changed: modified
// tracked files, staged changes, and untracked files. It reads porcelain -z,
// so paths are never C-quoted and need no unescaping, and lists every
// untracked file rather than collapsing a new directory to dir/, so a file a
// hook creates can be matched and staged by its own path.
func (g *Repo) Status() ([]string, error) {
	out, err := g.Run("status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var paths []string
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) < 4 {
			continue
		}
		xy, path := entry[:2], entry[3:]
		// A rename or copy record carries its source path in the next field.
		if strings.ContainsAny(xy, "RC") {
			i++
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}

// CurrentBranch returns the checked-out branch name. It returns ErrDetached
// when HEAD is not on a branch.
func (g *Repo) CurrentBranch() (string, error) {
	out, err := g.Run("symbolic-ref", "--short", "--quiet", "HEAD")
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) && ce.Code == 1 && ce.Stderr == "" {
			return "", ErrDetached
		}
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "", ErrDetached
	}
	return branch, nil
}

// RemoteURL returns the fetch URL of a configured remote.
func (g *Repo) RemoteURL(name string) (string, error) {
	out, err := g.Run("remote", "get-url", name)
	if err != nil {
		return "", fmt.Errorf("remote %q: %w", name, err)
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", fmt.Errorf("remote %q has no URL", name)
	}
	return url, nil
}

// Tags lists every tag reachable from HEAD. Filtering to stable versions of a
// known prefix happens in the caller: the reachability rule matters first,
// because a tag on a main-only commit must not anchor a plan computed on dev.
func (g *Repo) Tags() ([]string, error) {
	out, err := g.Run("tag", "--list", "--merged", "HEAD")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSuffix(l, "\r")
		if l != "" {
			tags = append(tags, l)
		}
	}
	return tags, nil
}

// Log returns the commits in the given ranges (newest first) together with
// the files each commit touched. Ranges are passed to a single batched
// `git log --name-only`; the caller filters paths in process rather than
// spawning git per commit.
//
// With no pathspec, every range argument is a positive-negative range such as
// `v1.0.0..HEAD`; git unions the positives and subtracts every negative, so
// several ranges in one call give exactly the union of the per-package
// ranges.
func (g *Repo) Log(ranges []string, paths []string) ([]RawCommit, error) {
	args := []string{"log", "--name-only", "--format=" + logFormat}
	if len(ranges) > 0 {
		args = append(args, "--end-of-options")
		args = append(args, ranges...)
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := g.Run(args...)
	if err != nil {
		return nil, err
	}
	return parseLog(out)
}

// parseLog splits the batched output. Chunks carrying a field separator are
// commit records (hash, subject, body); the chunks between them are the file
// name lists git printed for the preceding record. Merge commits contribute
// no file list, which the classifier tolerates because file chunks can never
// contain a field separator.
func parseLog(out string) ([]RawCommit, error) {
	chunks := strings.Split(out, recordSep)
	var commits []RawCommit
	for _, chunk := range chunks {
		if chunk == "" {
			continue
		}
		if strings.Contains(chunk, fieldSep) {
			parts := strings.SplitN(chunk, fieldSep, 3)
			if len(parts) != 3 {
				return nil, fmt.Errorf("malformed git log record: %q", chunk)
			}
			commits = append(commits, RawCommit{
				Hash:    strings.TrimSpace(parts[0]),
				Subject: strings.TrimSuffix(parts[1], "\n"),
				Body:    strings.TrimSuffix(strings.TrimSuffix(parts[2], "\n"), "\r"),
			})
			continue
		}
		if len(commits) == 0 {
			return nil, fmt.Errorf("malformed git log output: file list before any commit")
		}
		last := &commits[len(commits)-1]
		for _, f := range strings.Split(chunk, "\n") {
			f = strings.TrimSuffix(f, "\r")
			if strings.TrimSpace(f) != "" {
				last.Files = append(last.Files, f)
			}
		}
	}
	return commits, nil
}

// RevList returns the commit hashes in the given ranges, newest first. It is
// the cheap per-package membership test that pairs with the batched Log: one
// spawn per package, never per commit.
func (g *Repo) RevList(ranges []string) ([]string, error) {
	args := append([]string{"rev-list", "--end-of-options"}, ranges...)
	out, err := g.Run(args...)
	if err != nil {
		return nil, err
	}
	var hashes []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			hashes = append(hashes, l)
		}
	}
	return hashes, nil
}

// HeadSHA is the hash HEAD resolves to.
func (g *Repo) HeadSHA() (string, error) {
	out, err := g.Run("rev-parse", "--verify", "--end-of-options", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// LsRemote returns the hash the remote has for a ref, or "" when the remote
// does not have it yet.
func (g *Repo) LsRemote(remote, ref string) (string, error) {
	out, err := g.Run("ls-remote", remote, ref)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return "", nil
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

// HasObject reports whether the object store already has a commit hash (for
// example one an ls-remote just revealed).
func (g *Repo) HasObject(hash string) bool {
	if !isHash(hash) {
		return false
	}
	_, err := g.Run("cat-file", "-e", hash+"^{commit}")
	return err == nil
}

// IsAncestor reports whether ancestor is reachable from head.
func (g *Repo) IsAncestor(ancestor, head string) (bool, error) {
	if !isHash(ancestor) || !isHash(head) {
		return false, fmt.Errorf("refusing to compare non-hash values %q and %q", ancestor, head)
	}
	_, err := g.Run("merge-base", "--is-ancestor", "--end-of-options", ancestor, head)
	if err == nil {
		return true, nil
	}
	var ce *CommandError
	if errors.As(err, &ce) && ce.Code == 1 {
		return false, nil
	}
	return false, err
}

// MergeBase returns a common ancestor of every ref, which is how the batched
// monorepo log builds its union range: the intersection of the anchors'
// ancestor sets is exactly merge-base's, so `mb..HEAD` is a superset of every
// per-package range (membership is then decided per package with RevList).
//
// --octopus is what makes that hold for three or more refs: plain
// `git merge-base A B C` answers for A against a hypothetical merge of B and
// C, which can be a descendant of C and so cut C's commits out of the range.
func (g *Repo) MergeBase(refs ...string) (string, error) {
	if len(refs) == 0 {
		return "", fmt.Errorf("merge-base needs at least one ref")
	}
	args := append([]string{"merge-base", "--octopus", "--end-of-options"}, refs...)
	out, err := g.Run(args...)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return "", fmt.Errorf("merge-base: no common ancestor for %s", strings.Join(refs, " "))
	}
	// With several bases git prints several lines; any common ancestor works,
	// they are all ancestors of every ref.
	return strings.SplitN(line, "\n", 2)[0], nil
}

func isHash(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
