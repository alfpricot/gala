# Configuration

Every `gala.toml` key, its default, and a monorepo example.

`gala.toml` or `.gala.toml` at the repository root — exactly one of the two; both present is an error, and gala will not pick between them. `--config` overrides discovery. **A key gala does not know is a hard error** — a typo silently dropping its default is the failure mode this tool exists to avoid.

```toml
# single-package repository
kind = "app"          # "app" | "monorepo"  (required)
manager = "go"        # "go" | "npm" | "cargo" | "pypi" | "nuget"  (required)
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
| `manager` | required | one of `go`, `npm`, `cargo`, `pypi`, `nuget`; applies to every package in a monorepo |
| `publish` | `false` | safe-by-default: without it a release stops after the GitHub Release |
| `githubRelease` | `true` | needs `GITHUB_TOKEN` or `GH_TOKEN`; absent token or non-GitHub remote = warn and skip |
| `releaseBranches` | `["main"]` | checked by `release` (and `--dry-run`), not by `plan` |
| `syncBranch` | `""` | plain `git push <remote> <branch>:<syncBranch>` after the release push; never forced, rejection warns |
| `tagPrefix` | `"v"` | app only |
| `changelog` | `"CHANGELOG.md"` | path relative to the repo root |
| `remote` | `"origin"` | used for push and for parsing owner/repo |
| `[[package]]` | — | monorepo only: `path` required; `name`, `tagPrefix` optional; declaration order is publish order |

## NuGet packages

`manager = "nuget"` releases a .NET project through the .NET SDK (`dotnet` on `PATH` for `gala release`). Each package path — the repository root for an app — holds exactly one `.csproj`, `.fsproj`, or `.vbproj`; none or several is an error.

- The project file declares the version as a literal `<Version>` in a top-level, unconditional `<PropertyGroup>`. A `<PackageVersion>` beside it must match, and both are rewritten. A version inherited from `Directory.Build.props`, built from `<VersionPrefix>`, or set under a `Condition` is refused at plan time.
- The package name defaults to `<PackageId>`, then `<AssemblyName>`, then the project file name, matching MSBuild.
- Before any write, and again after the version is written, gala asks MSBuild for the `PackageVersion` it evaluates under the Release configuration. A disagreement means something outside the project file sets the version, so the release stops (restoring the project file) before anything is tagged.
- `packages.lock.json` records no project version, so nothing beyond the project file is staged.
- Publishing runs `dotnet pack --configuration Release` into a temporary directory and `dotnet nuget push --skip-duplicate` for each package it produced. The feed and its credentials come from the repository's `NuGet.config` (`defaultPushSource`), the way npm's registry comes from `.npmrc`.
