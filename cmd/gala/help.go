package main

const usageText = `gala — a small release engine

usage:
  gala [plan]              compute the next version and entry; never writes
  gala release             run the full pipeline: write, tag, push, publish
  gala release --dry-run   run the checks and preflights; preRelease commands run,
                           nothing is written
  gala release --resume    finish a release whose tags are pushed: create the
                           missing GitHub Releases and publish every package
                           tagged at HEAD (leave out any that already published)
  gala version             print the gala version
  gala help                print this help

flags:
  --config <path>   config file to use instead of discovery at the repo root
  --cwd <dir>       run against another checkout
  --dry-run         checks, preflights and preRelease commands, no writes (release)
  --no-publish      turn publish = true off for this run (release)
  --resume          complete the steps after the push for the tags at HEAD (release)
  --package <name>  with --resume, only this package; repeat for several

config (gala.toml or .gala.toml at the repo root):
  kind, manager, publish, minorOnZero, githubRelease, releaseBranches,
  syncBranch, tagPrefix, changelog, remote, [[versionFile]], preRelease,
  postBump, postBumpPaths, hookTimeout, and [[package]] entries for a monorepo.

exit codes:
  0  success, or no release due
  1  failure
  2  usage error
`
