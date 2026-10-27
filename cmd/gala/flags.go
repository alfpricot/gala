package main

import "fmt"

// cli is the parsed command line. The parser is hand-rolled on purpose: gala
// has four flags and two things to be strict about (exit 2 on a typo).
type cli struct {
	command   string
	config    string
	cwd       string
	dryRun    bool
	noPublish bool
}

const (
	cmdPlan    = "plan"
	cmdRelease = "release"
	cmdVersion = "version"
	cmdHelp    = "help"
)

// parseArgs parses args, defaulting to `plan` when no command is given.
// Errors are usage errors: exit code 2.
func parseArgs(args []string) (cli, error) {
	var c cli
	var positional []string

	for i := 0; i < len(args); i++ {
		a := args[i]

		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 2 && a[:2] == "--" {
			name, value, hasValue := splitFlag(a)
			switch name {
			case "--config":
				if !hasValue {
					var err error
					value, i, err = needValue(args, i, name)
					if err != nil {
						return c, err
					}
				}
				c.config = value
			case "--cwd":
				if !hasValue {
					var err error
					value, i, err = needValue(args, i, name)
					if err != nil {
						return c, err
					}
				}
				c.cwd = value
			case "--dry-run":
				if hasValue {
					return c, fmt.Errorf("flag %s does not take a value", name)
				}
				c.dryRun = true
			case "--no-publish":
				if hasValue {
					return c, fmt.Errorf("flag %s does not take a value", name)
				}
				c.noPublish = true
			case "--help":
				c.command = cmdHelp
			case "--version":
				c.command = cmdVersion
			default:
				return c, fmt.Errorf("unknown flag %q", name)
			}
			continue
		}

		if a == "-h" {
			c.command = cmdHelp
			continue
		}
		if len(a) > 1 && a[0] == '-' && a != "--" {
			return c, fmt.Errorf("unknown flag %q", a)
		}
		positional = append(positional, a)
	}

	for _, p := range positional {
		switch p {
		case cmdPlan, cmdRelease, cmdVersion, cmdHelp:
			if c.command != "" && c.command != p {
				return c, fmt.Errorf("unexpected argument %q", p)
			}
			c.command = p
		default:
			return c, fmt.Errorf("unknown command %q", p)
		}
	}

	if c.command == "" {
		c.command = cmdPlan
	}

	// --dry-run and --no-publish only mean something on a release.
	if c.command != cmdRelease {
		if c.dryRun {
			return c, fmt.Errorf("--dry-run only applies to `gala release`")
		}
		if c.noPublish {
			return c, fmt.Errorf("--no-publish only applies to `gala release`")
		}
	}

	return c, nil
}

// splitFlag splits --name=value; the name keeps its dashes.
func splitFlag(a string) (name, value string, hasValue bool) {
	for i := 2; i < len(a); i++ {
		if a[i] == '=' {
			return a[:i], a[i+1:], true
		}
	}
	return a, "", false
}

// needValue consumes the value that follows a flag, or reports the usage
// error. It returns the next loop index.
func needValue(args []string, i int, name string) (string, int, error) {
	if i+1 >= len(args) {
		return "", i, fmt.Errorf("flag %s needs a value", name)
	}
	next := args[i+1]
	if len(next) > 1 && next[0] == '-' && next != "-" {
		return "", i, fmt.Errorf("flag %s needs a value", name)
	}
	return next, i + 1, nil
}
