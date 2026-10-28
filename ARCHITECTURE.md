# gala architecture

gala is a single Go binary that turns Conventional Commits into a release: it plans the next version from git history, writes the changelog and manifest, tags and pushes, creates a GitHub Release, and publishes to a package registry. `gala plan` stops after planning and never writes.

The main design choice is that the release is ordered so a failure leaves a state that is visible and fixable. Every check that can run before the first write does, all git work finishes before any registry publish, and there is no resume command: recovery is manual, and the failure message prints the exact state.

## What gala is not

- Not a recovery tool: a failed publish stops and prints the manual commands.
- Not a dependency-graph tool: monorepo packages are declared, not discovered, and publish in declaration order.
- Not a service: it runs once per invocation, in CI or locally.

## Major components

### [cmd/gala](cmd/gala)

The entry point. Parses flags and the `plan`, `release`, `version`, and `help` subcommands, picks a mode, and calls `internal/release`. Exit codes are `0` success or nothing due, `1` failure, `2` usage error.

### [internal/release](internal/release)

Builds the plan and runs the steps: read-only checks, the per-package plan, live preflights, writes, commit, tag, atomic push, branch sync, GitHub Release, and publish. `plan.go` computes bumps and entries, `checks.go` holds the pre-write checks, `exec.go` performs the writes and pushes, and `format.go` renders plan output.

### [internal/config](internal/config)

Loads `gala.toml`, applies defaults, and rejects any key it does not know, so a typo cannot silently fall back to a default.

### [internal/commit](internal/commit)

Parses Conventional Commit messages and applies the type policy: which types bump, which are hidden from the changelog, and which messages are skipped.

### [internal/semver](internal/semver)

Parsing, ordering, and bumps for the subset of semver gala needs. Prerelease ordering is implemented even though planning ignores prerelease tags.

### [internal/git](internal/git)

Wraps the `git` command line behind an injectable runner. Reads put `--end-of-options` ahead of revisions and pathspecs.

### [internal/manager](internal/manager)

One adapter each for Go, npm, Cargo, and PyPI (`uv`): read the manifest version, write it back, refresh the lockfile through its owning tool, preflight, and publish. TOML manifests are edited by line (`toledit.go`) to preserve comments and ordering.

### [internal/changelog](internal/changelog)

Renders release entries and prepends them to `CHANGELOG.md`, keeping the file's existing line endings.

### [internal/github](internal/github)

Creates GitHub Releases through one REST endpoint with `net/http`, and parses remote URLs into owner and repository.

### [internal/gittest](internal/gittest)

Test support that builds real git repositories in temporary directories, optionally with a bare file-path remote, so tests exercise real pushes with no network.

## External systems and data flow

- **git.** Every read and write goes through the `git` binary (`internal/git`).
- **GitHub REST API.** Used only to create releases (`internal/github`), with `GITHUB_TOKEN` or `GH_TOKEN`. Without a token, or on a non-GitHub remote, that step is skipped with a warning.
- **Package managers.** `npm`, `cargo`, and `uv` run for lockfile refreshes and publishing (`internal/manager`). The Go manager runs no publish, because the tag is the publish.

The main path: `cmd/gala` → `release.Run` → read git history and tags → plan per package → write manifest, lockfile, and changelog → commit and tag → `git push --atomic` → GitHub Release → registry publish.

## Design invariants

- Release commits carry a skip-ci marker in the body, which stops `release.yml` from re-waking itself.
- Stable-tag selection is reachable-only (`git tag --merged HEAD`) and ignores prerelease tags on purpose.
- Version writes to TOML are section-and-key line edits and never go through a marshaller, guarded by a commented fixture test.
- Lockfiles are refreshed through their owning tool (`npm install --package-lock-only`, `cargo update --workspace`, `uv lock`), never edited by hand.
- Changed-file scoping for a monorepo uses one batched `git log --name-only` over the union range plus one rev-list per package, with no per-commit spawn.
- The push is atomic (branch and tags together or not at all), and the `syncBranch` push is never forced.
- Tests run against real repositories in temporary directories with a bare file-path remote, with no mocked git.

## Build, test, release, and deploy

- **`verify.yml`** runs on pull requests and manual dispatch. It checks formatting, `go vet`, `go mod tidy -diff`, staticcheck, errcheck, govulncheck, `go test -race`, and a build, then runs `gala plan` on its own repository.
- **`release.yml`** runs on pushes to `main` and manual dispatch, and runs `gala release` with `GITHUB_TOKEN`. gala releases itself: changelog, tag, push, `dev` fast-forward, and GitHub Release, with no registry step (`publish = false` in `gala.toml`).
- `just check` runs the same gate locally.

## Where things live

| Path | Holds |
| --- | --- |
| `cmd/gala/` | CLI entry point, flag parsing, help text |
| `internal/` | All library packages listed above |
| `gala.toml` | gala's own release configuration |
| `justfile` | Local build and check recipes |
| `.github/workflows/` | `verify.yml` and `release.yml` |
| `docs/` | Configuration reference and release process |
