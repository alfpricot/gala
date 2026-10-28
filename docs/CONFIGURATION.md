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

`manager = "nuget"` releases a .NET project through the .NET SDK (`dotnet` 8.0.200 or newer on `PATH` for `gala release`). Each package path — the repository root for an app — holds exactly one `.csproj`, `.fsproj`, or `.vbproj`; none or several is an error.

- The project file declares the version as a literal `<Version>` in a top-level, unconditional `<PropertyGroup>`. A `<PackageVersion>` beside it must match, and both are rewritten. A version inherited from `Directory.Build.props`, built from `<VersionPrefix>`, or set under a `Condition` is refused at plan time.
- The package name defaults to `<PackageId>`, then `<AssemblyName>`, then the project file name, matching MSBuild.
- Before any write, gala asks MSBuild for the `PackageVersion` it evaluates under the Release configuration and refuses a disagreement: something imported after the project file (a `Directory.Build.targets`, a custom SDK, a package's build targets) sets the version, and releasing would tag one version and publish another. An override that happens to equal the current version shows only once the new one is written, so the check runs again after the write and, on a disagreement, restores the project file and stops before anything is tagged. In a monorepo, packages written before that one stay written; `git checkout` them before retrying.
- `packages.lock.json` records no project version, so nothing beyond the project file is staged.
- With `publish = true`, the preflight also refuses a project with `IsPackable` false and a repository with no `defaultPushSource`, both before anything is written.
- Publishing runs `dotnet pack --configuration Release` into a temporary directory, checks that every package it produced carries the released version (a versioning package such as MinVer, or a target that sets `Version` during the build, would otherwise publish a different version than the tag), and runs `dotnet nuget push --skip-duplicate` for each. The feed is `defaultPushSource` in `NuGet.config`, the way npm's registry comes from `.npmrc`. Keep the credential out of the repository: store an API key in the user-level config with `dotnet nuget setapikey <key> --source <feed>`, or, for a feed that takes a username and token, reference an environment variable from `<packageSourceCredentials>` (`<add key="ClearTextPassword" value="%NUGET_AUTH_TOKEN%" />`).
