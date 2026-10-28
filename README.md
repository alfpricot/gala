# gala

[![verify](https://github.com/alfpricot/gala/actions/workflows/verify.yml/badge.svg?branch=dev)](https://github.com/alfpricot/gala/actions/workflows/verify.yml)

**A small release engine.** It reads Conventional Commits since the last release tag, computes the next semver version, renders a changelog entry, bumps the manifest, tags, pushes, creates a GitHub Release, and publishes to a package registry — for a single package or a declared monorepo.

The name is a gala apple, and a gala is a celebration: `gala release` reads as throwing a release party.

gala covers the release steps and leaves out everything around them: no recovery tooling, no dependency-graph machinery, no provenance, no notifications. See [What gala does not do](#what-gala-does-not-do).

## Install

In consumer CI, pin to a release tag (reproducible builds):

```sh
go install github.com/alfpricot/gala/cmd/gala@v0.1.1
```

For local use, `@latest` is fine:

```sh
go install github.com/alfpricot/gala/cmd/gala@latest
```

gala is a single Go binary. Its runtime dependency is `github.com/BurntSushi/toml`. Dev tools are pinned as `tool` directives in `go.mod`, so they never end up in the binary.

### Requirements

- Go 1.27.3 or newer to build or install (the version in `go.mod`).
- `git` on `PATH`.
- For `gala release` (including `--dry-run`), the tool for the configured `manager`: `npm`, `cargo`, `uv`, or `dotnet` (the .NET SDK, for `nuget`). gala runs it to write versions and refresh lockfiles, and to publish when `publish = true`. The `go` manager needs nothing extra, because the tag is the publish.

## Usage

```sh
gala            # same as `gala plan`
gala plan       # read-only: what would release, at what version
gala release     # the full release
gala release --dry-run    # checks + preflights, writes nothing
gala release --no-publish # turn publish = true off for this run
gala version
gala help
```

Flags: `--config <path>` (overrides `gala.toml` / `.gala.toml` discovery), `--cwd <dir>` (run against another checkout).

Exit codes: `0` success or no release due, `1` failure, `2` usage error.

`gala plan` is read-only and works on any branch. This output was captured from this repository's own checkout before v0.1.1 was cut, while three merged fixes awaited promotion:

```text
gala plan — release due (branch dev)

gala → v0.1.1 (patch, from v0.1.0)
  Bug Fixes
    require the reference right after a closure keyword (#5)
    read refs from the subject and closure footers only (#4)
    stop repeating refs and tidy the no-release reason (#3)
```

Nothing to release is a success, with a reason — captured from the same checkout right after the `v0.1.1` release:

```text
gala plan — nothing to release (branch dev)

gala → no release: no commits since v0.1.1
```

## Configuration

gala reads `gala.toml` — or the dotfile `.gala.toml`, for repositories that keep tool config in dotfiles — at the repository root. A repository may have one or the other, never both: two config files is an error, because letting a stale one win silently is the failure gala exists to avoid. A key gala does not know is a hard error. See [docs/CONFIGURATION.md](docs/CONFIGURATION.md) for every key, default, and a monorepo example.

## How releases work

How versions are chosen, the order of the release steps, the `dev` → `main` branch model, authentication, and manual recovery from a partial release are in [docs/RELEASE_PROCESS.md](docs/RELEASE_PROCESS.md). The structure of the code is in [ARCHITECTURE.md](ARCHITECTURE.md).

## What gala does not do

- No resume/repair: if a registry publish fails after the tag is pushed, gala stops and prints the exact state plus the manual recovery commands.
- No prerelease channels: prerelease tags (`v2.0.0-rc.1`) are ignored when planning; gala never creates them.
- No internal range rewriting or lockfile regeneration for rewritten ranges: monorepo packages should declare dependencies with ranges or workspace protocols that survive a sibling bump.
- No dependency-ordered publishing: monorepo packages publish in the order they are declared, so declare dependencies before their dependents.
- No branch merging: `syncBranch` only fast-forwards. If commits landed on `dev` during the release, gala warns and prints the manual merge command; it never fails a release over it.
- No command timeouts: do not interrupt `gala release` during the publish step.
- Non-GitHub remotes: tags and changelog still work; the GitHub Release step is skipped with a warning.
- One config file per repository (`gala.toml` or `.gala.toml`, never both), prose output only.

Also out of scope for v1: notifications, commit linting, provenance, workspace glob discovery, JSON output, alternate config formats, `init`/`doctor` commands.

## How to contribute

Branch from `dev`, run `just check` before pushing, and open a pull request against `dev` with a Conventional Commit title. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT, see [LICENSE](LICENSE).
