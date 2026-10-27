package git

import (
	"fmt"
	"strings"
)

// Add stages exactly the given repo-relative paths.
func (g *Repo) Add(paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, paths...)
	_, err := g.Run(args...)
	return err
}

// Commit creates a commit from what is already staged: subject first, body as
// a second paragraph. Both are passed with -m, so no editor ever opens.
func (g *Repo) Commit(subject, body string) error {
	args := []string{"commit", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}
	_, err := g.Run(args...)
	return err
}

// TagAnnotated creates an annotated tag with the given message.
func (g *Repo) TagAnnotated(name, message string) error {
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("refusing to create a tag whose name starts with -: %q", name)
	}
	_, err := g.Run("tag", "-a", name, "-m", message)
	return err
}

// PushAtomic pushes refs (branch and tags) in one atomic push: they land
// together or not at all.
func (g *Repo) PushAtomic(remote string, refs ...string) error {
	if err := checkRefs(refs); err != nil {
		return err
	}
	args := append([]string{"push", "--atomic", remote}, refs...)
	_, err := g.Run(args...)
	return err
}

// Push performs a plain push. Non-fast-forward rejection is left to git: the
// caller warns and prints the manual merge command, it never forces.
func (g *Repo) Push(remote, refspec string) error {
	if strings.HasPrefix(refspec, "-") {
		return fmt.Errorf("refusing to push a refspec that starts with -: %q", refspec)
	}
	_, err := g.Run("push", remote, refspec)
	return err
}

func checkRefs(refs []string) error {
	for _, r := range refs {
		if r == "" || strings.HasPrefix(r, "-") {
			return fmt.Errorf("refusing to push invalid ref %q", r)
		}
	}
	return nil
}
