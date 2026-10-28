# Configuration

Every `gala.toml` key with its default, monorepo examples (including one that mixes ecosystems), extra version files, hook commands, Cargo workspaces, Python projects, choosing a layout, and workflow outputs.

Put `gala.toml` or `.gala.toml` at the repository root. You need exactly one of them: if both exist, gala reports an error rather than picking one. `--config` overrides the lookup. A key gala doesn't recognize is a hard error, so a typo can't quietly leave you on the default.

```toml
# single-package repository
kind = "app"          # "app" | "monorepo"  (required)
manager = "go"        # "go" | "npm" | "cargo" | "pypi" | "nuget" | "none"  (required)
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
manager = "npm"          # the default for every package

[[package]]
path = "packages/core"   # required; name defaults to the manifest's package name
# name = "core"          # optional override
# tagPrefix = "core/v"   # default "<name>/v"

[[package]]
path = "packages/cli"    # declared after core because it depends on core
```

```toml
# monorepo with several ecosystems: each package can override the manager,
# the publish setting, and add its own version files and hooks
kind = "monorepo"
manager = "cargo"
publish = true

[[package]]
path = "crates/core"

[[package]]
path = "bindings/node"
manager = "npm"
postBump = ["npm run build"]       # runs in bindings/node, only when it releases
postBumpPaths = ["dist/*.js"]      # relative to bindings/node

[[package.versionFile]]
path = "jsr.json"                  # relative to bindings/node
key = "version"

[[package]]
path = "bindings/python"
manager = "pypi"
publish = false

[[package.versionFile]]
path = "src/widget/__init__.py"
pattern = '__version__ = "([^"]+)"'
```

| Key | Default | Notes |
| --- | --- | --- |
| `kind` | required | `app` or `monorepo` |
| `manager` | required | one of `go`, `npm`, `cargo`, `pypi`, `nuget`, `none` (see [Other ecosystems](#other-ecosystems)); in a monorepo it's the default for every package, and it can be left out when every `[[package]]` sets its own |
| `publish` | `false` | off by default: without it, a release stops after the GitHub Release; in a monorepo it's the default for every package |
| `githubRelease` | `true` | needs `GITHUB_TOKEN` or `GH_TOKEN`; with no token, or on a non-GitHub remote, gala warns and skips it |
| `releaseBranches` | `["main"]` | checked by `release` (and `--dry-run`), not by `plan` |
| `syncBranch` | `""` | after the release push, gala runs a plain `git push <remote> <branch>:<syncBranch>`; it's never forced, and a rejection only warns |
| `tagPrefix` | `"v"` | app only |
| `changelog` | `"CHANGELOG.md"` | path relative to the repo root |
| `remote` | `"origin"` | used for the push and to work out the owner and repo |
| `[[package]]` | none | monorepo only: `path` is required; `name`, `tagPrefix`, `manager`, `publish`, `preRelease`, `postBump`, `postBumpPaths`, and `[[package.versionFile]]` are optional; declaration order is publish order |
| `[[versionFile]]` | none | app only (a monorepo package uses `[[package.versionFile]]`): more files that carry the release version; see [Extra version files](#extra-version-files) |
| `preRelease` | `[]` | commands run after the preflights and before any write; see [Hook commands](#hook-commands) |
| `postBump` | `[]` | commands run after the version writes and before staging |
| `postBumpPaths` | `[]` | files, directories (everything under them), or `path.Match` patterns the `postBump` commands may change; needs `postBump` |

## Extra version files

A package that records its version in more than one file lists the others as version files: `[[versionFile]]` for an app, `[[package.versionFile]]` for a monorepo package. Each one is written with that package's new version. Some common cases:

```toml
# a JSON file, addressed by key
[[versionFile]]
path = "app.json"
key = "metadata.version"

# a TOML file: the table, then the key
[[versionFile]]
path = "crates/helper/Cargo.toml"
key = "package.version"

# any other text file, addressed by a regular expression
[[versionFile]]
path = "src/widget/__init__.py"
pattern = '__version__ = "([^"]+)"'

[[versionFile]]
path = "charts/widget/Chart.yaml"
pattern = '(?m)^version: (\S+)$'

[[versionFile]]
path = "VERSION"
pattern = '^(\S+)'
```

- `path` is relative to the repository root for `[[versionFile]]` and to the package directory for `[[package.versionFile]]`. Don't list a manifest that a manager already writes (the package's own or another package's), and don't list one file under two packages: a file can carry only one version.
- Set exactly one of `key` and `pattern`.
- `key` works for `.json` and `.toml` files. For JSON it's a dotted path through objects (`version`, `metadata.version`). For TOML it's the table followed by the key (`package.version`, `workspace.package.version`, `project.version`).
- `pattern` is a [Go regular expression](https://pkg.go.dev/regexp/syntax) with exactly one capture group around the version. Every match must capture the same value, and gala rewrites all of them, so a file that states the version twice stays consistent. Use `(?m)` with `^` and `$` to anchor to a line; gala matches against the file with a byte-order mark removed and CRLF line endings read as LF, so the same pattern works on a Windows checkout. Make the pattern specific enough not to catch other values (a YAML `appVersion`, a dependency's version).
- gala changes only the version's bytes: indentation, key order, comments, line endings, and a byte-order mark all stay as they were.
- Each file must already record the package's current version. For `manager = "go"` and `manager = "none"`, which have no manifest version, the first version file stands in for the manifest: it supplies the starting version when there is no tag yet, and must agree with the latest tag when there is one. If any file disagrees, `plan` fails instead of tagging a release whose files publish another version.
- When a version file is itself a manifest, gala refreshes its lockfile through the owning tool and adds the lockfile to the release commit:

  | Version file | Key | Lockfile | Command |
  | --- | --- | --- | --- |
  | `package.json` | `version` | `package-lock.json` | `npm install --package-lock-only` |
  | `Cargo.toml` | `package.version` or `workspace.package.version` | `Cargo.lock` | `cargo update --workspace` |
  | `pyproject.toml` | `project.version` or `tool.poetry.version` | `uv.lock` | `uv lock` |

  The command runs where the lockfile is: in the file's own directory, and at the repository root when the root has one (a workspace records every member there). Each lockfile is refreshed once, and the tool must be on `PATH`.

## Hook commands

`preRelease` and `postBump` are lists of commands. Top-level commands run from the repository root. A `[[package]]`'s own commands run from its directory, and only when that package releases. Package commands run first, in declaration order, then the top-level ones, and gala stops at the first failure.

Commands run directly, not through a shell: words split on spaces, quotes group a word with spaces in it, and nothing is expanded. Use a package script (`npm run …`, `make …`, `just …`) or a checked-in script for anything that needs a pipe, `cd`, or a variable. The preflight checks that each command's program is on `PATH` (or, for a path like `./tools/gen.sh`, that the file exists relative to where the command runs).

```toml
preRelease = ["make check"]
postBump = ["npm run generate"]
postBumpPaths = ["src/version.c", "src/*.json"]
```

Each command gets these environment variables:

| Variable | Value |
| --- | --- |
| `GALA_TAGS` | every tag this release creates, separated by spaces |
| `GALA_PACKAGE` | the package's name |
| `GALA_VERSION` | the package's new version, such as `1.3.0` |
| `GALA_PREVIOUS_VERSION` | the version it releases from |
| `GALA_TAG` | the package's new tag |

`GALA_TAGS` is always set. The other four are set for a package's own commands, and for top-level commands when exactly one package releases (always, for an app).

- `preRelease` runs during `release` and `--dry-run`, after every other preflight. These commands are checks: they must not change any tracked or unignored file, and the release stops if one does.
- `postBump` runs during `release` only, after the manifests, version files, lockfiles, and changelog are written and before anything is staged. Use it to regenerate files that embed the version, such as generated source code. `--dry-run` lists the commands without running them.
- Whatever a `postBump` command changes or creates must match the `postBumpPaths` of some scope that ran (an exact file, a directory entry such as `dist` covering everything under it, or a pattern), and those changes join the release commit. A change anywhere else is an error, the same as any other unexpected change. A package's `postBumpPaths` are relative to its directory.
- If a `postBump` command fails, gala prints its output and stops. Nothing has been committed, tagged, or pushed at that point; discard the local writes with `git restore` (and remove any new files the command created) and release again.

## Cargo workspaces

For `manager = "cargo"`, gala reads and writes `[package] version`. When the file has no `[package]` (a virtual workspace) or its `[package]` inherits with `version.workspace = true`, gala uses `[workspace.package] version` in the same file instead. That releases every member that inherits the shared version in lockstep, as one `kind = "app"` with one tag and one changelog entry:

```toml
# Cargo.toml at the root
[workspace]
members = ["core", "cli"]

[workspace.package]
version = "0.1.1"

# core/Cargo.toml and cli/Cargo.toml
[package]
version.workspace = true
```

The same holds for a monorepo package whose path is a workspace root. A monorepo package whose `Cargo.toml` inherits its version from a workspace root elsewhere is refused, because it can't be versioned on its own.

A package whose version lives in `[workspace.package]` needs `publish = false`: `cargo publish` at a workspace root can't publish the members as one package, so the preflight refuses `publish = true` before anything is tagged. Publish the member crates from a workflow that reads the [workflow outputs](#workflow-outputs).

A workspace member that isn't part of the release (it isn't a released manifest and isn't listed as a version file) is left alone: gala never bumps or tags it. To have it follow a package's version, list its `Cargo.toml` as a version file of that package with `key = "package.version"`. To version it independently, declare it as its own `[[package]]` in a monorepo config. Otherwise, bump it by hand in its own commit; `cargo update --workspace` in the next release records whatever version it carries.

## Python projects

For `manager = "pypi"`, gala reads and writes `[project] version` in `pyproject.toml`, or `[tool.poetry] version` for a Poetry project that keeps its metadata there. A version with no static value (`dynamic = ["version"]` and no `[tool.poetry] version`), or one owned by an enabled `[tool.poetry-dynamic-versioning]` plugin, is refused at plan time; declare a static version and list any module that repeats it (`__init__.py`, `_version.py`) as a version file with a `pattern`. `uv.lock` is refreshed with `uv lock` when it exists; `poetry.lock` doesn't record the project's own version, so it needs no refresh.

## Other ecosystems

`manager = "none"` releases a project gala has no adapter for: Maven or Gradle, a Ruby gem, a PHP or Swift package, a Dart package, a Helm chart, a documentation site. There is no manifest to write and nothing to publish. The version lives in the tags and in the version files you list, usually with a `pattern`:

```toml
kind = "app"
manager = "none"

[[versionFile]]
path = "gradle.properties"
pattern = '(?m)^version=(\S+)$'
```

- The first version file supplies the starting version when the repository has no tag yet.
- `publish = true` is refused at preflight, because gala has nothing to publish with. Publish from a workflow that reads the [workflow outputs](#workflow-outputs), or from a `postBump` command if the build needs the new version first.
- In a monorepo, a `manager = "none"` package needs an explicit `name`, since there is no manifest to read one from.

## Choosing a layout

- **One version, one tag:** use `kind = "app"`. List every other file that carries the version as a `[[versionFile]]`, whatever its ecosystem; a Cargo virtual workspace keeps the shared version in `[workspace.package]`.
- **Independent versions and tags per package:** use `kind = "monorepo"` with one `[[package]]` each, setting `manager` per package when the ecosystems differ. A package's commits are the ones that touch its directory.
- **Generated files that embed the version:** regenerate them with `postBump` and list them in `postBumpPaths`, at the top level or on the package that owns them.

## Workflow outputs

When `GITHUB_OUTPUT` is set, `gala release` appends step outputs for the job:

| Output | When | Value |
| --- | --- | --- |
| `released` | always | `true` once the tags are pushed, `false` when nothing was due |
| `tag` | exactly one tag pushed | that tag, such as `v1.2.3` |
| `version` | `kind = "app"` released | the new version, such as `1.2.3` |
| `tags` | a release happened | every pushed tag, one per line |

The outputs are written as soon as the atomic push succeeds, so a later failure (the GitHub Release or a publish) still reports the tags that exist. `plan` and `--dry-run` write nothing.

```yaml
- id: gala
  run: gala release
- if: steps.gala.outputs.released == 'true'
  env:
    TAG: ${{ steps.gala.outputs.tag }}
  run: gh workflow run release.yml --ref "$TAG" -f "tag=$TAG"
```

## NuGet packages

`manager = "nuget"` releases a .NET project through the .NET SDK. `gala release` needs `dotnet` 8.0.200 or newer on `PATH`. Each package path (the repository root, for an app) must hold exactly one `.csproj`, `.fsproj`, or `.vbproj`. Zero or several is an error.

- The project file has to declare the version as a literal `<Version>` in a top-level `<PropertyGroup>` with no condition. A `<PackageVersion>` next to it must match, and gala rewrites both. If the version comes from `Directory.Build.props`, is built from `<VersionPrefix>`, or is set under a `Condition`, gala refuses it at plan time.
- The package name defaults to `<PackageId>`, then `<AssemblyName>`, then the project file name, the same order MSBuild uses.
- Before writing anything, gala asks MSBuild which `PackageVersion` it evaluates under the Release configuration, and it stops if that disagrees with the file. Something imported after the project file (a `Directory.Build.targets`, a custom SDK, a package's build targets) is setting the version, and going ahead would tag one version and publish another. An override that happens to equal the current version only shows up once the new version is written, so gala runs the check again after the write. If it disagrees then, gala restores the project file and stops before tagging. In a monorepo, packages written before that one stay written, so run `git checkout` on them before you retry.
- `packages.lock.json` doesn't record the project's version, so gala stages nothing beyond the project file.
- With `publish = true`, the preflight also refuses a project where `IsPackable` is false and a repository with no `defaultPushSource`. Both checks happen before anything is written.
- Publishing runs `dotnet pack --configuration Release` into a temporary directory, then checks that every package it produced carries the released version. (A versioning package like MinVer, or a target that sets `Version` during the build, could otherwise publish a version that doesn't match the tag.) After that it runs `dotnet nuget push --skip-duplicate` for each package. The feed is `defaultPushSource` in `NuGet.config`, much as npm's registry comes from `.npmrc`. Keep the credential out of the repository. Store an API key in the user-level config with `dotnet nuget setapikey <key> --source <feed>`, or, for a feed that takes a username and token, reference an environment variable from `<packageSourceCredentials>` (`<add key="ClearTextPassword" value="%NUGET_AUTH_TOKEN%" />`).
