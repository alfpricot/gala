# Contributing to gala

gala is a small release engine written in Go.

## Before you start

1. Branch from **`dev`**. `main` is the production branch, promoted from `dev` by a release pull request.
2. Read [ARCHITECTURE.md](ARCHITECTURE.md) and [docs/RELEASE_PROCESS.md](docs/RELEASE_PROCESS.md).

## Local setup

```bash
go build ./...
just run plan
```

You need Go 1.27.3 or newer (see `go.mod`), `git`, and [`just`](https://github.com/casey/just). Dev tools (staticcheck, errcheck, govulncheck) are pinned in `go.mod` and run through `go tool`.

## Validation before a pull request

```bash
just check
```

`just check` runs gofmt, `go vet`, `go mod tidy -diff`, staticcheck, errcheck, govulncheck, `go test -race`, a build, and a self-plan. It matches the steps in `.github/workflows/verify.yml`, which runs on every pull request.

## Pull requests

- Open pull requests against **`dev`**; feature pull requests squash into it.
- Use a Conventional Commit title (`feat:`, `fix:`, `docs:`, `chore:`, `test:`, …). gala plans its own releases from these, so the type decides the next version.
- Keep each pull request to one change and link the issue it resolves in the body.

## Documentation

| Document | Covers |
| --- | --- |
| [docs/CONFIGURATION.md](docs/CONFIGURATION.md) | Every `gala.toml` key and default |
| [docs/RELEASE_PROCESS.md](docs/RELEASE_PROCESS.md) | Version policy, release steps, branch model, authentication, recovery |
