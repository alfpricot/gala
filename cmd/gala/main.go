package main

import (
	"fmt"
	"os"

	"github.com/alfpricot/gala/internal/release"
)

// Exit codes: 0 success or no release due, 1 failure, 2 usage error.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	c, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gala: %s\n\n%s", err, usageText)
		return exitUsage
	}

	switch c.command {
	case cmdHelp:
		_, _ = fmt.Print(usageText)
		return exitOK
	case cmdVersion:
		_, _ = fmt.Println(version())
		return exitOK
	}

	opts := release.DefaultOptions()
	if c.cwd != "" {
		opts.Cwd = c.cwd
	}
	if c.config != "" {
		opts.ConfigPath = c.config
	}
	opts.NoPublish = c.noPublish

	mode := release.ModePlan
	if c.command == cmdRelease {
		mode = release.ModeRelease
		if c.dryRun {
			mode = release.ModeDryRun
		}
	}

	if err := release.Run(opts, mode); err != nil {
		fmt.Fprintf(os.Stderr, "gala: %s\n", err)
		return exitFailure
	}
	return exitOK
}
