# gala architecture

gala is a single Go binary that turns Conventional Commits into a release. It plans the next version from git history, writes the changelog and manifest, tags and pushes, creates a GitHub Release, and publishes to a package registry. `gala plan` stops after planning and never writes anything.

The release steps are ordered so that when something fails, you can see what state you're in and fix it. Every check that can run before the first write does. All the git work finishes before any registry publish. There's no resume command: recovery is manual, and the failure message prints the exact state.

## What gala is not

- It isn't a recovery tool. A failed publish stops and prints the manual commands.
- It isn't a dependency-graph tool. Monorepo packages are declared rather than discovered, and they publish in declaration order.
- It isn't a service. It runs once per invocation, in CI or locally.

## Major components

### [cmd/gala](cmd/gala)

The entry point. It parses flags and the `plan`, `release`, `version`, and `help` subcommands, picks a mode, and calls `internal/release`. Exit codes are `0` for success or nothing due, `1` for failure, and `2` for a usage error.

### [internal/release](internal/release)

Builds the plan and runs the steps: read-only checks, the per-package plan, live preflights, writes, commit, tag, atomic push, branch sync, GitHub Release, and publish. `plan.go` computes bumps and entries, `checks.go` has the checks that run before any write, `exec.go` does the writes and pushes, and `format.go` renders the plan output.

### [internal/config](internal/config)

Finds `gala.toml` or `.gala.toml` at the repository root (neither, or both, is an error), applies defaults, and rejects any key it doesn't know, so a typo can't quietly fall back to a default.

### [internal/commit](internal/commit)

Parses Conventional Commit messages and applies the type policy: which types bump the version, which are hidden from the changelog, and which messages get skipped.

### [internal/semver](internal/semver)

Parsing, ordering, and bumps for the part of semver gala needs. Prerelease ordering is implemented even though planning ignores prerelease tags.

### [internal/git](internal/git)

Wraps the `git` command line behind an injectable runner. Reads put `--end-of-options` ahead of revisions and pathspecs.

### [internal/manager](internal/manager)

One adapter each for Go, npm, Cargo, PyPI (`uv`), and NuGet (`dotnet`). Each one reads the manifest version, writes it back, refreshes the lockfile through the tool that owns it, runs its preflight, and publishes. TOML manifests are edited line by line (`toledit.go`) and .NET project files by element value (`nuget.go`), so comments and ordering survive.

### [internal/changelog](internal/changelog)

Renders release entries and prepends them to `CHANGELOG.md`, keeping the file's existing line endings.

### [internal/github](internal/github)

Creates GitHub Releases through a single REST endpoint using `net/http`, and parses remote URLs into an owner and repository.

### [internal/gittest](internal/gittest)

Test support that builds real git repositories in temporary directories, optionally with a bare file-path remote, so tests exercise real pushes without a network.

## External systems and data flow

- **git.** Every read and write goes through the `git` binary (`internal/git`).
- **GitHub REST API.** Only used to create releases (`internal/github`), with `GITHUB_TOKEN` or `GH_TOKEN`. With no token, or on a non-GitHub remote, that step is skipped with a warning.
- **Package managers.** `npm`, `cargo`, `uv`, and `dotnet` run for lockfile refreshes, preflights, and publishing (`internal/manager`). The Go manager never publishes, because the tag is the publish.

The main path goes `cmd/gala` → `release.Run` → read git history and tags → plan per package → write manifest, lockfile, and changelog → commit and tag → `git push --atomic` → GitHub Release → registry publish.

## Design invariants

- Release commits carry a skip-ci marker in the body, which keeps `release.yml` from triggering itself again.
- Stable-tag selection only looks at reachable tags (`git tag --merged HEAD`) and ignores prerelease tags on purpose.
- Version writes to TOML are section-and-key line edits and never go through a marshaller. A commented fixture test guards this.
- Version writes to .NET project files replace only the `<Version>` and `<PackageVersion>` values, found by byte offset, and never re-serialize the XML. gala checks the version MSBuild evaluates before and after the write, and checks every packed `.nupkg` against the released version before anything is pushed.
- Lockfiles are refreshed through their owning tool (`npm install --package-lock-only`, `cargo update --workspace`, `uv lock`) and never edited by hand.
- Changed-file scoping for a monorepo uses one batched `git log --name-only` over the union range plus one rev-list per package. It never spawns a process per commit.
- The push is atomic (branch and tags land together or not at all), and the `syncBranch` push is never forced.
- Tests run against real repositories in temporary directories with a bare file-path remote. Git is never mocked.

## Build, test, release, and deploy

- **`verify.yml`** runs on pull requests and manual dispatch. It checks formatting, `go vet`, `go mod tidy -diff`, staticcheck, errcheck, govulncheck, `go test -race`, and a build. Then it runs `gala plan` on the repository itself and checks that the npm package manifests agree with `npm/platforms.json`.
- **`release.yml`** runs on pushes to `main` and on manual dispatch. It runs `gala release`, pushing over the `RELEASE_DEPLOY_KEY` deploy key (the ruleset bypass) and creating the GitHub Release with `GITHUB_TOKEN`. gala releases itself: changelog, tag, push, `dev` fast-forward, and GitHub Release, with no registry step in gala's own run (`publish = false` in `.gala.toml`). After a run that tagged a release, the workflow publishes the npm packages from `npm/` to GitHub Packages. That's one `@alfpricot/gala-<os>-<arch>` package per platform in `npm/platforms.json`, then the `@alfpricot/gala` wrapper that lists them as `optionalDependencies`.
- `just check` runs the same gate locally.

## Where things live

| Path | Holds |
| --- | --- |
| `cmd/gala/` | CLI entry point, flag parsing, help text |
| `internal/` | All library packages listed above |
| `.gala.toml` | gala's own release configuration |
| `justfile` | Local build and check recipes |
| `.github/workflows/` | `verify.yml` and `release.yml` |
| `docs/` | Configuration reference and release process |
