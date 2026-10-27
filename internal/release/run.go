package release

import (
	"io"
)

// Run executes one mode against the repository: load, check, plan, preflight,
// and (unless the mode stops earlier) execute. The returned error is a
// failure (exit 1); "nothing to release" is not an error (exit 0).
func Run(opts *Options, mode Mode) error {
	e, err := Open(opts)
	if err != nil {
		return err
	}

	// Step 1: read-only checks. `plan` skips them — it works anywhere.
	if mode != ModePlan {
		if err := e.checks(); err != nil {
			return err
		}
	}

	// Step 2: build the plan.
	plan, err := e.Plan(opts.Now())
	if err != nil {
		return err
	}
	_, _ = io.WriteString(opts.out(), FormatPlan(plan))

	// Nothing due: print why (done above), exit 0.
	if len(plan.Due()) == 0 {
		if mode != ModePlan {
			_, _ = io.WriteString(opts.out(), "\nrelease: nothing to do — no tags, no commits, no writes.\n")
		}
		return nil
	}
	if mode == ModePlan {
		return nil
	}

	// Step 3: live preflights, skipped by plan, run by release and --dry-run.
	publish := e.cfg.Publish && !opts.NoPublish
	if err := e.preflight(plan, publish); err != nil {
		return err
	}

	if mode == ModeDryRun {
		_, _ = io.WriteString(opts.out(), FormatDryRun(e, plan, publish))
		return nil
	}

	_, _ = io.WriteString(opts.out(), "\n")
	return e.execute(plan, publish)
}
