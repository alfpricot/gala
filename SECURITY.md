# Security policy

## Reporting a vulnerability

Do not open a public issue. Use [private vulnerability reporting](https://github.com/alfpricot/gala/security/advisories/new): open the repository's Security tab and choose "Report a vulnerability". Include what you found, how to reproduce it, and the impact you expect.

## Scope

The `gala` binary and the workflows in this repository.

## Supported versions

Only the latest release receives fixes.

## Practices

- gala reads `GITHUB_TOKEN` or `GH_TOKEN` from the environment; never commit a token or put one in `gala.toml`.
- Keep Go dependencies current; `govulncheck` runs on every pull request.
