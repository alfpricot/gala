package release

import (
	"io"
)

// Run executes one mode against the repository: load, check, plan, preflight,
// and (unless the mode stops earlier) execute. The returned error is a
// failure (exit 1); "nothing to release" is not an error (exit 0).
//
// A release always reports `released` to GITHUB_OUTPUT: execute writes
// released=true once the tags are pushed, and every other exit (nothing due,
// or a failure before the push) writes released=false here. A write failure
// only warns, so it never changes the exit code.
func Run(opts *Options, mode Mode) error {
	var e *Engine
	err := run(opts, mode, &e)
	if mode == ModeRelease && (e == nil || !e.outputWritten) {
		if werr := appendGitHubOutput(opts.GitHubOutput, false, nil); werr != nil {
			opts.warnf("%v", werr)
		}
	}
	return err
}

func run(opts *Options, mode Mode, engine **Engine) error {
	e, err := Open(opts)
	if err != nil {
		return err
	}
	*engine = e

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
	allowPublish := !opts.NoPublish
	if err := e.preflight(plan, allowPublish); err != nil {
		return err
	}

	if mode == ModeDryRun {
		_, _ = io.WriteString(opts.out(), FormatDryRun(e, plan, allowPublish))
		return nil
	}

	_, _ = io.WriteString(opts.out(), "\n")
	return e.execute(plan, allowPublish)
}
