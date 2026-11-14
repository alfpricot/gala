# Release process

How gala chooses versions, the order of the release steps, the branch model, authentication, and how to recover by hand.

## Version policy

gala reads commits from the highest stable tag reachable from `HEAD` up to `HEAD`. If no tag is reachable, it reads the whole history. Reachability matters under the `dev` → `main` model: a tag on a commit that only exists on `main` shouldn't anchor a plan computed on `dev`. Prerelease tags are ignored entirely, and gala never creates them.

The base version is the latest reachable stable tag, or the manifest's current version if there isn't one. Go manifests carry no version, so with no tag the base is `0.0.0`. If the manifest disagrees with the latest reachable tag, gala stops with an error. Drift like that gets reconciled by hand, not guessed around.

| Commit | Bump | Changelog section |
| --- | --- | --- |
| `feat` | minor | Features |
| `fix` | patch | Bug Fixes |
| `perf` | patch | Performance |
| `refactor` | patch | Refactoring |
| `revert` (also git's default `Revert "<subject>"`) | patch | Reverts |
| `!` suffix or `BREAKING CHANGE:`/`BREAKING-CHANGE:` footer | major (overrides the type) | Breaking Changes (listed first) |
| `docs` `test` `style` `build` `ci` `chore` | none | hidden |
| non-conforming message (includes promotion merge commits) | none | counted and reported as skipped |

It's plain semver arithmetic with no special treatment for 0.x: `0.7.0` plus a major bump is `1.0.0`. A manifest that is at a prerelease finalizes it when the bump does not outrank it: `1.2.0-rc.1` with a patch or minor bump releases `1.2.0`, `1.2.1-rc.1` with a patch bump releases `1.2.1`, while `1.2.1-rc.1` with a minor bump releases `1.3.0`.

Commit subjects render into the changelog and the GitHub Release as written, except that an `@handle` is wrapped in a code span so it never notifies the person, and markdown characters in a scope are escaped.

## The release steps

1. Read-only checks: the working tree is clean, the current branch is in `releaseBranches`, and the manifest and tag agree.
2. Build the plan. For each package that means tags, commits, bump, next version, and the rendered entry. If nothing is due, gala prints why and exits 0. For `manager = "go"`, the module path must end in `/vN` once the next major is 2 or higher. Every version file must already record its package's current version, or the plan fails.
3. Live preflight. `plan` skips this; `release` and `--dry-run` run it. It checks that the manager binaries and the hook commands are on `PATH`, that `npm whoami` works when npm publishing is on, that `.gitignore` doesn't hide the changelog, a manifest, or a version file (staging takes what `git status` reports, so an ignored file would silently miss the release commit; ignored lockfiles are fine), that the remote branch hasn't moved past local `HEAD`, and that no tag gala is about to create already exists locally or on the remote (the plan reads only tags reachable from `HEAD`, so such a tag would otherwise fail the tag or the push after the release commit). For nuget it always checks the version MSBuild evaluates, and when publishing is on it also checks packability and `defaultPushSource`. Last, it runs the `preRelease` commands, which must pass and leave the working tree clean.
4. Write the manifest bumps (each package through its own manager) and the version files, refresh the lockfiles through each lockfile's owning tool (including the lockfiles of version files that are themselves manifests), write the changelog entry, then run the `postBump` commands: each released package's own, from its directory, then the top-level ones.
5. Stage exactly what changed, plus whatever the `postBump` commands changed or created inside `postBumpPaths`. If anything else is dirty, that's an error, and gala won't quietly stage it too.
6. Commit as `chore(release): v1.2.0` (`chore(release): N packages` for a monorepo), with the skip-ci marker in the body.
7. Create annotated tags, with the entry's header lines as the message.
8. Push once, atomically: `git push --atomic <remote> <branch> <tag>...`. The branch and tags land together or not at all.
9. Fast-forward `syncBranch`, if one is set.
10. Create the GitHub Release(s) through the REST API, but only after the tags exist on the remote.
11. Publish each package, in declaration order.

Two rules shape this order. Every check that can run before step 4 does, and all the git work finishes before any registry publish. A release that's tagged but not published is easy to see and fix; one that's published but not tagged is neither. `plan` and `--dry-run` never write anything.

## Branch model

Work flows `dev` → `main`. Feature PRs squash into `dev`. The promotion PR from `dev` to `main` (a merge commit) cuts the release, and the release then fast-forwards `dev` to the release commit through `syncBranch`.

gala ships two workflows:

- **verify.yml** runs on `pull_request` and manual dispatch. It's the full gate, ending with a self-plan and the npm package manifest checks. Local `just check` covers pushes.
- **release.yml** runs on `push` to `main` and manual dispatch. It runs `gala release`, pushing over the `RELEASE_DEPLOY_KEY` deploy key and creating the GitHub Release with `GITHUB_TOKEN`. gala releases itself with `kind = "app"`, `manager = "go"`, `publish = false`, and `syncBranch = "dev"`, so its own run does the changelog, tag, push, `dev` fast-forward, and GitHub Release, with no registry step.

The npm publishing is a separate step in `release.yml`. After the release run, the workflow publishes the latest release tag reachable from `HEAD` to GitHub Packages as npm packages, using `GITHUB_TOKEN`. `npm/build.sh` cross-compiles one binary for each entry in `npm/platforms.json` into its own `@alfpricot/gala-<os>-<arch>` package, which `os` and `cpu` restrict to that platform, and stamps the version into the `@alfpricot/gala` wrapper. The wrapper lists every platform package as an `optionalDependency`, so npm installs only the one that matches the consumer's machine, and the wrapper's launcher runs that binary.

The platform packages publish before the wrapper. That means a published wrapper tells you the whole version is out, and the step stops there. If the wrapper isn't published, the step builds from the tag itself and skips each package already in the registry.

If a publish fails after the tag is pushed, the next run on `main` finishes it. Dispatch `release.yml` there (re-running the failed job replays the original push and stops at gala's remote check), or, if the next promotion releases nothing, let that promotion do it. Only the latest release tag is retried, so finish a failed publish before the next release.

To add a platform, add its entry to `npm/platforms.json`, its manifest under `npm/packages/`, and its name to the wrapper's `optionalDependencies`. `just npm-test` (which `verify.yml` runs) fails until all three agree.

Pushes with a deploy key do trigger workflows. What keeps the release commit from re-running `release.yml` is the skip-ci marker in its body. No workflow runs on a tag push or a push to `dev`.

A promotion that releases nothing still leaves `dev` one merge commit behind `main`. That's harmless, since no tag moved, and the next release corrects it.

## Authentication

| Manager | What the publish needs | Notes |
| --- | --- | --- |
| npm | `npm login` or `NODE_AUTH_TOKEN` | `npm whoami` runs as a preflight when `publish = true` |
| cargo | `CARGO_REGISTRY_TOKEN` | `cargo publish` checks it itself |
| pypi (uv) | `UV_PUBLISH_TOKEN` | `uv build`, then `uv publish` |
| nuget (dotnet) | `defaultPushSource` in `NuGet.config`, plus an API key from `dotnet nuget setapikey` (user-level config) or a `%NUGET_AUTH_TOKEN%` reference in `<packageSourceCredentials>` | never commit the key; when `publish = true`, the preflight checks the push source and packability |
| go | none | the tag is the publish |
| GitHub Release | `GITHUB_TOKEN` or `GH_TOKEN` | with no token, gala warns and skips; it never fails after the tags are pushed |

This repository's rulesets block direct pushes to `main` and `dev`, and block creating `v*` tags. The release and sync pushes therefore need a bypass actor, or the push in step 8 is rejected. On a personal-account repository, GitHub Actions can't be a bypass actor, so the rulesets exempt deploy keys instead. `release.yml` checks out with a write-access deploy key whose private half is the `RELEASE_DEPLOY_KEY` secret. If that secret is removed or the key loses write access, the atomic push in step 8 fails after the local commit and tag have been written, and nothing reaches the remote; gala then removes that local commit and tag again.

## Recovering from a partial release

gala has no resume command. Recovery is manual on purpose, and the failure message prints the exact state: which tag was pushed, which package failed, the tool's output, and the command to run.

- **A `postBump` command failed.** Nothing was committed, tagged, or pushed. The working tree holds the version writes: review them with `git status`, discard them with `git restore`, fix the command, and release again.
- **The tag or the atomic push failed.** Nothing reached the remote. gala moves the branch back to the commit it started from with `git reset --keep`, deletes the tags it created, and prints the cause, so the release can be run again once that is fixed. The keep-style reset never discards an edit you made to a tracked file while gala was running: edits to other files stay in place, and an edit to a file the release commit changed makes git refuse, in which case gala leaves the commit and tags together and prints the `git reset --keep` and `git tag -d` commands to finish by hand. gala does the same when it cannot ask the remote what it has.
- **The push failed after the remote accepted it.** gala asks the remote, and when it already holds the release commit it leaves the local commit and tags alone, writes the workflow outputs (`released=true`, the tags), and prints what never ran: the branch sync, one `gh release create` command per missing release, and the publish commands.
- **Tag pushed, publish failed.** Fix the registry problem, then run the printed commands (`npm publish`, `cargo publish`, `uv publish`, or `dotnet nuget push`) in the package directories, in order: the failing package and every package after it. The tag, changelog, and GitHub Release are already correct.
- **GitHub Release failed.** The tags are pushed, so running gala again finds nothing due. The failure message prints one `gh release create <tag> --verify-tag` command for each release still missing, followed by the publish commands for the packages that were going to publish. A tag that already has a release counts as done, so creating a release by hand and then running the printed publish commands is safe.
- **Sync push rejected.** `dev` has commits the release doesn't. The warning prints the merge command, and `dev` was never force-pushed.
- **Never** delete and recreate a tag that's already been published to a registry.
