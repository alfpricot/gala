# Contributing to gala

gala is a small release engine written in Go. These are the notes for working on it.

## Before you start

1. Branch from `dev`. `main` is the production branch, and it gets updated from `dev` through a release pull request.
2. Skim [ARCHITECTURE.md](ARCHITECTURE.md) and [docs/RELEASE_PROCESS.md](docs/RELEASE_PROCESS.md).

## Local setup

```bash
go build ./...
just run plan
```

You'll need Go 1.27.3 or newer (see `go.mod`), `git`, [`just`](https://github.com/casey/just), and Node 18 or newer for the npm package checks. The dev tools (staticcheck, errcheck, govulncheck) are pinned in `go.mod` and run through `go tool`.

## Checking your work

Before you open a pull request, run:

```bash
just check
```

If it fails on formatting, `just fmt` rewrites every Go file in place. `just build` puts the binary at `./bin/gala`.

`just check` runs gofmt, `go vet`, `go mod tidy -diff`, staticcheck, errcheck, govulncheck, `go test -race`, a build, a self-plan, and the npm package manifest checks (`node --test` in `npm/`). These are the same steps as `.github/workflows/verify.yml`, which runs on every pull request.

## Pull requests

- Open them against `dev`. Feature pull requests squash into it.
- Give the pull request a Conventional Commit title (`feat:`, `fix:`, `docs:`, `chore:`, `test:`, and so on). gala plans its own releases from these, so the type decides the next version.
- Keep each pull request to one change, and link the issue it resolves in the body.

## Documentation

| Document | Covers |
| --- | --- |
| [docs/CONFIGURATION.md](docs/CONFIGURATION.md) | Every `gala.toml` key and default |
| [docs/RELEASE_PROCESS.md](docs/RELEASE_PROCESS.md) | Version policy, release steps, branch model, authentication, recovery |
