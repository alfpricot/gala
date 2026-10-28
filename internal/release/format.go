package release

import (
	"fmt"
	"slices"
	"strings"

	"github.com/alfpricot/gala/internal/config"
)

// FormatPlan renders the plan as prose: one block per package, the entry's
// sections indented, skipped commits counted.
func FormatPlan(p *Plan) string {
	var b strings.Builder
	due := len(p.Due())
	total := len(p.Packages)

	switch {
	case total > 1:
		fmt.Fprintf(&b, "gala plan — %d of %d packages due (branch %s)\n", due, total, p.Branch)
	case due > 0:
		fmt.Fprintf(&b, "gala plan — release due (branch %s)\n", p.Branch)
	default:
		fmt.Fprintf(&b, "gala plan — nothing to release (branch %s)\n", p.Branch)
	}

	for _, pkg := range p.Packages {
		b.WriteString("\n")
		if !pkg.Due {
			fmt.Fprintf(&b, "%s → no release: %s\n", pkg.Name, pkg.Reason)
			continue
		}

		from := pkg.Tag
		switch {
		case from != "":
			// anchor tag names the range the plan was computed over
		case pkg.Base.String() == "0.0.0":
			from = "0.0.0 (no tag)"
		default:
			from = pkg.Base.String() + " (manifest)"
		}
		fmt.Fprintf(&b, "%s → %s (%s, from %s)\n", pkg.Name, pkg.NextTag, pkg.Bump, from)

		// The rendered entry minus its "##" header, indented under the block.
		lines := strings.Split(pkg.Entry, "\n")
		for _, l := range lines[1:] {
			switch {
			case strings.TrimSpace(l) == "":
				continue
			case strings.HasPrefix(l, "### "):
				fmt.Fprintf(&b, "  %s\n", l[4:])
			case strings.HasPrefix(l, "* "):
				fmt.Fprintf(&b, "    %s\n", l[2:])
			default:
				fmt.Fprintf(&b, "  %s\n", l)
			}
		}
		if pkg.Skipped > 0 {
			fmt.Fprintf(&b, "  skipped: %d non-conforming commit%s\n", pkg.Skipped, plural(pkg.Skipped))
		}
	}
	return b.String()
}

// FormatDryRun reports exactly what a real run would do, after the preflights
// have passed and before anything is written.
func FormatDryRun(e *Engine, p *Plan, allowPublish bool) string {
	var b strings.Builder
	due := p.Due()

	b.WriteString("\ndry run — preflights passed; nothing was written\n\n")

	fmt.Fprintf(&b, "  would write   %s\n", describeWrites(e, due))
	for _, s := range e.scopes(due) {
		for _, cmd := range s.hooks.PostBump {
			fmt.Fprintf(&b, "  would run     %s%s\n", cmd.Line, e.where(s))
		}
	}

	subject, _ := releaseCommit(e.cfg.Kind == config.KindMonorepo, due)
	fmt.Fprintf(&b, "  would commit  %s (skip-ci marker in body)\n", subject)

	var tags []string
	for _, pkg := range due {
		tags = append(tags, pkg.NextTag)
	}
	fmt.Fprintf(&b, "  would tag     %s\n", strings.Join(tags, ", "))
	fmt.Fprintf(&b, "  would push    %s %s + %d tag%s (atomic)\n", e.cfg.Remote, e.branch, len(tags), plural(len(tags)))

	if e.cfg.SyncBranch != "" {
		fmt.Fprintf(&b, "  would sync    %s:%s (fast-forward only)\n", e.branch, e.cfg.SyncBranch)
	}
	if e.cfg.GitHubRelease {
		fmt.Fprintf(&b, "  would release %d GitHub release%s\n", len(due), plural(len(due)))
	} else {
		b.WriteString("  would release none (githubRelease = false)\n")
	}
	if pubs := publishing(due, allowPublish); len(pubs) > 0 {
		var via []string
		for _, pkg := range pubs {
			if name := pkg.Manager.Name(); !slices.Contains(via, name) {
				via = append(via, name)
			}
		}
		fmt.Fprintf(&b, "  would publish %d package%s via %s\n", len(pubs), plural(len(pubs)), strings.Join(via, ", "))
	} else {
		b.WriteString("  would publish nothing (publish = false)\n")
	}
	return b.String()
}
