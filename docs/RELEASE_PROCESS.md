# Release process

How gala chooses versions, the order of the release steps, the branch model, authentication, and manual recovery.

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

## The release steps

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
| nuget (dotnet) | `defaultPushSource` in `NuGet.config`, plus an API key from `dotnet nuget setapikey` (user-level config) or a `%NUGET_AUTH_TOKEN%` reference in `<packageSourceCredentials>` | never commit the key; the preflight checks the push source and packability when `publish = true` |
| go | — | the tag *is* the publish |
| GitHub Release | `GITHUB_TOKEN` or `GH_TOKEN` | absent token = warn and skip, never fail after tags are pushed |

If you later add a branch-protection ruleset on `main` or `dev`, the release and sync pushes will need a bypass for the `GITHUB_TOKEN` (or your release identity), or the push in step 8 will be rejected.

## Recovering from a partial release

gala has no resume command — recovery is manual by design, and the failure message prints the exact state: which tag is pushed, which package failed, the tool output, and the command to run.

- **Tag pushed, publish failed:** fix the registry problem, then run the printed command (`npm publish` / `cargo publish` / `uv publish` / `dotnet nuget push`) in the package directory. The tag, changelog, and GitHub Release are already correct.
- **GitHub Release failed:** the tags are pushed; create the release by hand from the existing tag, or fix API access and re-run it.
- **Sync push rejected:** `dev` has commits the release does not. The warning prints the merge command; `dev` was never force-pushed.
- **Never** delete and recreate a tag that has already been published to a registry.
