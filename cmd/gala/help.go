package main

const usageText = `gala — a small release engine

usage:
  gala [plan]              compute the next version and entry; never writes
  gala release             run the full pipeline: write, tag, push, publish
  gala release --dry-run   run the checks and preflights, write nothing
  gala version             print the gala version
  gala help                print this help

flags:
  --config <path>   config file to use instead of gala.toml at the repo root
  --cwd <dir>       run against another checkout
  --dry-run         preflights only, no writes (release)
  --no-publish      turn publish = true off for this run (release)

config (gala.toml):
  kind, manager, publish, githubRelease, releaseBranches, syncBranch,
  tagPrefix, changelog, remote, and [[package]] entries for a monorepo.

exit codes:
  0  success, or no release due
  1  failure
  2  usage error
`
