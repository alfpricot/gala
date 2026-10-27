# gala

**A small release engine.** It reads Conventional Commits since the last release tag, computes the next semver version, renders a changelog entry, bumps the manifest, tags, pushes, creates a GitHub Release, and publishes to a package registry — for a single package or a declared monorepo.

The name is a gala apple, and a gala is a celebration: `gala release` reads as throwing a release party. It continues the fruit-handle line: `meloncholic` → `alfpricot`.

gala keeps the release pipeline and leaves out everything around it: no recovery tooling, no dependency-graph machinery, no provenance, no notifications. See [What gala does not do](#what-gala-does-not-do).

## Install

In consumer CI, pin to a release tag (reproducible builds):

```sh
go install github.com/alfpricot/gala/cmd/gala@v0.1.0
```

For local use, `@latest` is fine:

```sh
go install github.com/alfpricot/gala/cmd/gala@latest
```

gala is a single Go binary with one runtime dependency (`github.com/BurntSushi/toml`). Dev tools are pinned as `tool` directives in `go.mod`, so they never end up in the binary.

## Usage

```sh
gala            # same as `gala plan`
gala plan       # read-only: what would release, at what version
gala release     # the full pipeline
gala release --dry-run    # checks + preflights, writes nothing
gala release --no-publish # turn publish = true off for this run
gala version
gala help
```

Flags: `--config <path>` (overrides `gala.toml` discovery), `--cwd <dir>` (run against another checkout).

Exit codes: `0` success or no release due, `1` failure, `2` usage error.

`gala plan` is read-only and works on any branch. This is the live output from this repository's own checkout, where three merged fixes await the next promotion:

```text
gala plan — release due (branch dev)

gala → v0.1.1 (patch, from v0.1.0)
  Bug Fixes
    require the reference right after a closure keyword (#5)
    read refs from the subject and closure footers only (#4)
    stop repeating refs and tidy the no-release reason (#3)
```

Nothing to release is a success, with a reason — captured from the same checkout immediately after its own `v0.1.0` release:

```text
gala plan — nothing to release (branch dev)

gala → no release: no commits since v0.1.0
```

## Configuration

`gala.toml` at the repository root (`--config` overrides discovery). **A key gala does not know is a hard error** — a typo silently dropping its default is the failure mode this tool exists to avoid.

```toml
# single-package repository
kind = "app"          # "app" | "monorepo"  (required)
manager = "go"        # "go" | "npm" | "cargo" | "pypi"  (required)
publish = true        # push to the registry after tagging (default false)
githubRelease = true  # create a GitHub Release per released tag (default true)
releaseBranches = ["main"]  # release refuses other branches; plan works anywhere
syncBranch = "dev"    # fast-forward this branch to the release commit (default "") 
tagPrefix = "v"       # app only
changelog = "CHANGELOG.md"
remote = "origin"
```

```toml
# monorepo: packages are declared, not discovered
kind = "monorepo"
manager = "npm"

[[package]]
path = "packages/core"   # required; name defaults to the manifest's package name
# name = "core"          # optional override
# tagPrefix = "core/v"   # default "<name>/v"

[[package]]
path = "packages/cli"    # declared after core because it depends on core
```

| Key | Default | Notes |
| --- | --- | --- |
| `kind` | required | `app` or `monorepo` |
| `manager` | required | one of the four; applies to every package in a monorepo |
| `publish` | `false` | safe-by-default: without it a release stops after the GitHub Release |
| `githubRelease` | `true` | needs `GITHUB_TOKEN` or `GH_TOKEN`; absent token or non-GitHub remote = warn and skip |
| `releaseBranches` | `["main"]` | checked by `release` (and `--dry-run`), not by `plan` |
| `syncBranch` | `""` | plain `git push <remote> <branch>:<syncBranch>` after the release push; never forced, rejection warns |
| `tagPrefix` | `"v"` | app only |
| `changelog` | `"CHANGELOG.md"` | path relative to the repo root |
| `remote` | `"origin"` | used for push and for parsing owner/repo |
| `[[package]]` | — | monorepo only: `path` required; `name`, `tagPrefix` optional; declaration order is publish order |

## Version policy

Commits are read from the highest **stable** tag **reachable from `HEAD`** to `HEAD`; with no reachable tag, the whole history. Reachability matters under the `dev` → `main` model: a tag on a `main`-only commit must not anchor a plan computed on `dev`. Prerelease tags are ignored entirely — gala never creates them.

Base version: the latest reachable stable tag, else the manifest's current version (Go manifests carry none, so no tag means base `0.0.0`). A manifest that disagrees with the latest reachable tag is a **hard error**: drift is reconciled by hand, never guessed around.

| Commit | Bump | Changelog section |
| --- | --- | --- |
| `feat` | minor | Features |
| `fix` | patch | Bug Fixes |
| `perf` | patch | Performance |
| `refactor` | patch | Refactoring |
| `revert` | patch | Reverts |
| `!` suffix or `BREAKING CHANGE:`/`BREAKING-CHANGE:` footer | major (overrides the type) | Breaking Changes (listed first) |
| `docs` `test` `style` `build` `ci` `chore` | none | hidden |
| non-conforming message (includes promotion merge commits) | none | counted and reported as skipped |

Plain semver arithmetic, no 0.x special-casing: `0.7.0` + major = `1.0.0`.

## The release pipeline

1. Read-only checks: working tree clean; current branch in `releaseBranches`; manifest/tag consistency.
2. Build the plan (per package: tags, commits, bump, next version, rendered entry). Nothing due → print why, exit 0. For `manager = "go"`, the module path must carry `/vN` once the next major is ≥ 2.
3. Live preflight (skipped by `plan`, run by `release` and `--dry-run`): manager binaries on PATH, `npm whoami` when npm publishing is on, and the remote branch has not moved past local `HEAD`.
4. Write: manifest bumps, lockfile refreshes (through each lockfile's owning tool), the changelog entry.
5. Stage exactly what changed — anything else dirty is an error, not a silent extra stage.
6. Commit `chore(release): v1.2.0` (`chore(release): N packages` for a monorepo) with the skip-ci marker in the body.
7. Annotated tag(s), message = the entry's header lines.
8. One atomic push: `git push --atomic <remote> <branch> <tag>...` — branch and tags land together or not at all.
9. Fast-forward `syncBranch`, if set.
10. GitHub Release(s) via REST, only after the tags exist remotely.
11. Publish(es) per package, in declaration order.

Invariants: every check that can run before step 4 does; all of git completes before any registry publish — tagged-but-unpublished is visible and fixable, published-but-untagged is neither; `plan` and `--dry-run` never write.

## Branch model

`dev` → `main`. Feature PRs squash into `dev`; the promotion PR `dev` → `main` (merge commit) cuts the release; the release then fast-forwards `dev` to the release commit via `syncBranch`.

gala ships two workflows:

- **verify.yml** — `pull_request` (+ manual dispatch). The full gate, ending with a self-plan. Local `just check` covers pushes.
- **release.yml** — `push` to `main` (+ manual dispatch). Runs `gala release` with `GITHUB_TOKEN`. gala releases itself: `kind = "app"`, `manager = "go"`, `publish = false`, `syncBranch = "dev"`, so a release is changelog + tag + push + `dev` fast-forward + GitHub Release with no registry step. Pushes made with `GITHUB_TOKEN` do not trigger further workflow runs, so the release commit and the `dev` sync cannot loop.

A promotion that releases nothing still leaves `dev` one merge commit behind `main` — harmless (no tag moved) and corrected on the next release.

## Authentication

| Manager | What the publish needs | Notes |
| --- | --- | --- |
| npm | `npm login` or `NODE_AUTH_TOKEN` | `npm whoami` runs as a preflight when `publish = true` |
| cargo | `CARGO_REGISTRY_TOKEN` | checked by `cargo publish` itself |
| pypi (uv) | `UV_PUBLISH_TOKEN` | `uv build` then `uv publish` |
| go | — | the tag *is* the publish |
| GitHub Release | `GITHUB_TOKEN` or `GH_TOKEN` | absent token = warn and skip, never fail after tags are pushed |

If you later add a branch-protection ruleset on `main` or `dev`, the release and sync pushes will need a bypass for the `GITHUB_TOKEN` (or your release identity), or the push in step 8 will be rejected.

## Recovering from a partial release

gala has no resume command — recovery is manual by design, and the failure message prints the exact state: which tag is pushed, which package failed, the tool output, and the command to run.

- **Tag pushed, publish failed:** fix the registry problem, then run the printed command (`npm publish` / `cargo publish` / `uv publish`) in the package directory. The tag, changelog, and GitHub Release are already correct.
- **GitHub Release failed:** the tags are pushed; create the release by hand from the existing tag, or fix API access and re-run it.
- **Sync push rejected:** `dev` has commits the release does not. The warning prints the merge command; `dev` was never force-pushed.
- **Never** delete and recreate a tag that has already been published to a registry.

## What gala does not do

- No resume/repair: if a registry publish fails after the tag is pushed, gala stops and prints the exact state plus the manual recovery commands.
- No prerelease channels: prerelease tags (`v2.0.0-rc.1`) are ignored when planning; gala never creates them.
- No internal range rewriting or lockfile regeneration for rewritten ranges: monorepo packages should declare dependencies with ranges or workspace protocols that survive a sibling bump.
- No dependency-ordered publishing: monorepo packages publish in the order they are declared, so declare dependencies before their dependents.
- No branch merging: `syncBranch` only fast-forwards. If commits landed on `dev` during the release, gala warns and prints the manual merge command; it never fails a release over it.
- No command timeouts: do not interrupt `gala release` during the publish step.
- Non-GitHub remotes: tags and changelog still work; the GitHub Release step is skipped with a warning.
- One config file name (`gala.toml`), prose output only.

Also out of scope for v1: notifications, commit linting, provenance, workspace glob discovery, JSON output, alternate config formats, `init`/`doctor` commands.

## Design notes

The contributor-facing invariants, since the design doc itself stays untracked:

- Release commits carry a skip-ci marker in the body — intentional, it stops `release.yml` from re-waking itself.
- Git reads keep `--end-of-options` ahead of revisions and pathspecs.
- Stable-tag selection is reachable-only (`git tag --merged HEAD`) and ignores prerelease tags on purpose.
- Never marshal TOML for version writes: they are section-and-key line edits that preserve comments, ordering, and every other byte, guarded by a commented fixture test.
- Lockfiles are refreshed through their owning tool (`npm install --package-lock-only`, `cargo update --workspace`, `uv lock`), never edited by hand.
- Changelog writes detect and preserve the file's line endings (CRLF stays CRLF).
- Packages are declared, not discovered, and publish in declaration order.
- Changed-file scoping for a monorepo uses one batched `git log --name-only` over the union range plus one rev-list per package — never a per-commit spawn.
- The push is atomic (branch + tags together or not at all); the `syncBranch` push is never forced.
- No resume command exists: recovery is manual by design.
- Tests run against real repositories in temp dirs with a bare file-path remote — no mocked git.

## Development

```sh
just check    # fmt, vet, tidy-check, staticcheck, errcheck, vulncheck, test -race, build, self-plan
just build    # ./bin/gala
just fmt      # gofmt -w
just self-plan
```

## License

MIT © 2026 M. Reifschneider