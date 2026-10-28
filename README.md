# gala

[![verify](https://github.com/alfpricot/gala/actions/workflows/verify.yml/badge.svg?branch=dev)](https://github.com/alfpricot/gala/actions/workflows/verify.yml)

gala is a small release engine. It reads the Conventional Commits since your last release tag, works out the next semver version, writes a changelog entry, bumps the manifest, tags, pushes, creates a GitHub Release, and publishes to a package registry. It works for a single package or for a monorepo where you list the packages yourself.

It's named after the apple.

It sticks to the release itself. There's no recovery tooling, no dependency-graph handling, no provenance, and no notifications. See [What gala does not do](#what-gala-does-not-do).

## Installation

In CI, pin to a release tag so builds are reproducible:

```sh
go install github.com/alfpricot/gala/cmd/gala@v0.1.1
```

For local use, `@latest` is fine:

```sh
go install github.com/alfpricot/gala/cmd/gala@latest
```

Every release is also published to GitHub Packages as the npm package `@alfpricot/gala`, so a Node project can use gala without a Go toolchain. The same install works on Linux, macOS, and Windows, on x64 and arm64. Each platform's prebuilt binary is its own `@alfpricot/gala-<os>-<arch>` package, listed as an optional dependency, and npm installs only the one that matches the machine. If you install with `--omit=optional`, you won't get a binary.

GitHub Packages wants authentication even for a public package. Point the scope at the registry and put a token with `read:packages` in `NODE_AUTH_TOKEN`, which the snippet below reads. Locally that's a personal access token. In CI it's the workflow's `GITHUB_TOKEN` with `packages: read`, set on the install step as `env: NODE_AUTH_TOKEN: ${{ github.token }}`.

Access is granted per package, so give the consumer repository read access to the wrapper and to each platform package its runners use. If the token can't read a platform package, npm skips it like any optional dependency. The install still passes, and `gala` reports the missing binary when you run it.

```ini
# .npmrc
@alfpricot:registry=https://npm.pkg.github.com
//npm.pkg.github.com/:_authToken=${NODE_AUTH_TOKEN}
```

```sh
npm install --save-dev @alfpricot/gala
npx gala plan
```

gala is a single Go binary with one runtime dependency, `github.com/BurntSushi/toml`. Dev tools are pinned as `tool` directives in `go.mod`, so they stay out of the binary.

### Requirements

- Go 1.27.3 or newer to build or install (the version in `go.mod`).
- `git` on `PATH`.
- For `gala release` (including `--dry-run`), the tool for each configured `manager` (a monorepo can set one per package): `npm`, `cargo`, `uv`, or `dotnet` (the .NET SDK, for `nuget`). gala runs it to write versions and refresh lockfiles, and to publish when `publish = true`. The `go` and `none` managers need nothing extra, since the tag is the publish.

## Usage

```sh
gala            # same as `gala plan`
gala plan       # read-only: what would release, at what version
gala release     # the full release
gala release --dry-run    # checks and preflights, writes nothing
gala release --no-publish # turn publish = true off for this run
gala version
gala help
```

Flags: `--config <path>` (overrides `gala.toml` / `.gala.toml` discovery), `--cwd <dir>` (run against another checkout).

Exit codes: `0` success or no release due, `1` failure, `2` usage error.

`gala plan` is read-only and works on any branch. This output came from this repository's own checkout just before v0.1.1 was cut, while three merged fixes were waiting to be promoted:

```text
gala plan — release due (branch dev)

gala → v0.1.1 (patch, from v0.1.0)
  Bug Fixes
    require the reference right after a closure keyword (#5)
    read refs from the subject and closure footers only (#4)
    stop repeating refs and tidy the no-release reason (#3)
```

Having nothing to release counts as success, and gala tells you why. This is from the same checkout right after the `v0.1.1` release:

```text
gala plan — nothing to release (branch dev)

gala → no release: no commits since v0.1.1
```

### Configuration

gala reads `gala.toml` at the repository root, or `.gala.toml` if you prefer to keep tool config in dotfiles. Use one or the other. Having both is an error, because quietly letting a stale file win is exactly the kind of mistake gala is meant to prevent. A key gala doesn't recognize is also an error. [docs/CONFIGURATION.md](docs/CONFIGURATION.md) lists every key and default and has a monorepo example.

### How releases work

[docs/RELEASE_PROCESS.md](docs/RELEASE_PROCESS.md) covers how versions are chosen, the order of the release steps, the `dev` → `main` branch model, authentication, and how to recover by hand from a partial release. For how the code is laid out, see [ARCHITECTURE.md](ARCHITECTURE.md).

### What gala does not do

- **Resume or repair.** If a registry publish fails after the tag is pushed, gala stops and prints the exact state along with the commands to recover by hand.
- **Prerelease channels.** Prerelease tags like `v2.0.0-rc.1` are ignored when planning, and gala never creates them.
- **Range rewriting between packages.** gala doesn't rewrite internal dependency ranges or regenerate lockfiles for them, so monorepo packages should use ranges or workspace protocols that survive a sibling bump.
- **Dependency-ordered publishing.** Monorepo packages publish in the order you declare them, so list dependencies before the packages that use them.
- **Branch merging.** `syncBranch` only fast-forwards. If commits landed on `dev` during the release, gala warns and prints the merge command, but it won't fail the release over it.
- **Command timeouts.** Don't interrupt `gala release` during the publish step.
- **Non-GitHub remotes.** Tags and the changelog still work, but the GitHub Release step is skipped with a warning.
- **Other config setups.** There's one config file per repository, and output is plain prose.

Also out of scope for v1: notifications, commit linting, provenance, workspace glob discovery, JSON output, other config formats, and `init`/`doctor` commands.

## Contributing

Branch from `dev`, run `just check` before you push, and open a pull request against `dev` with a Conventional Commit title. More in [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT, see [LICENSE](LICENSE).
