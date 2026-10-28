# Configuration

Every `gala.toml` key with its default, and a monorepo example.

Put `gala.toml` or `.gala.toml` at the repository root. You need exactly one of them: if both exist, gala reports an error rather than picking one. `--config` overrides the lookup. A key gala doesn't recognize is a hard error, so a typo can't quietly leave you on the default.

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
| `publish` | `false` | off by default: without it, a release stops after the GitHub Release |
| `githubRelease` | `true` | needs `GITHUB_TOKEN` or `GH_TOKEN`; with no token, or on a non-GitHub remote, gala warns and skips it |
| `releaseBranches` | `["main"]` | checked by `release` (and `--dry-run`), not by `plan` |
| `syncBranch` | `""` | after the release push, gala runs a plain `git push <remote> <branch>:<syncBranch>`; it's never forced, and a rejection only warns |
| `tagPrefix` | `"v"` | app only |
| `changelog` | `"CHANGELOG.md"` | path relative to the repo root |
| `remote` | `"origin"` | used for the push and to work out the owner and repo |
| `[[package]]` | none | monorepo only: `path` is required, `name` and `tagPrefix` are optional, and declaration order is publish order |

## NuGet packages

`manager = "nuget"` releases a .NET project through the .NET SDK. `gala release` needs `dotnet` 8.0.200 or newer on `PATH`. Each package path (the repository root, for an app) must hold exactly one `.csproj`, `.fsproj`, or `.vbproj`. Zero or several is an error.

- The project file has to declare the version as a literal `<Version>` in a top-level `<PropertyGroup>` with no condition. A `<PackageVersion>` next to it must match, and gala rewrites both. If the version comes from `Directory.Build.props`, is built from `<VersionPrefix>`, or is set under a `Condition`, gala refuses it at plan time.
- The package name defaults to `<PackageId>`, then `<AssemblyName>`, then the project file name, the same order MSBuild uses.
- Before writing anything, gala asks MSBuild which `PackageVersion` it evaluates under the Release configuration, and it stops if that disagrees with the file. Something imported after the project file (a `Directory.Build.targets`, a custom SDK, a package's build targets) is setting the version, and going ahead would tag one version and publish another. An override that happens to equal the current version only shows up once the new version is written, so gala runs the check again after the write. If it disagrees then, gala restores the project file and stops before tagging. In a monorepo, packages written before that one stay written, so run `git checkout` on them before you retry.
- `packages.lock.json` doesn't record the project's version, so gala stages nothing beyond the project file.
- With `publish = true`, the preflight also refuses a project where `IsPackable` is false and a repository with no `defaultPushSource`. Both checks happen before anything is written.
- Publishing runs `dotnet pack --configuration Release` into a temporary directory, then checks that every package it produced carries the released version. (A versioning package like MinVer, or a target that sets `Version` during the build, could otherwise publish a version that doesn't match the tag.) After that it runs `dotnet nuget push --skip-duplicate` for each package. The feed is `defaultPushSource` in `NuGet.config`, much as npm's registry comes from `.npmrc`. Keep the credential out of the repository. Store an API key in the user-level config with `dotnet nuget setapikey <key> --source <feed>`, or, for a feed that takes a username and token, reference an environment variable from `<packageSourceCredentials>` (`<add key="ClearTextPassword" value="%NUGET_AUTH_TOKEN%" />`).
