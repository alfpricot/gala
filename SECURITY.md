# Security policy

## Reporting a vulnerability

Please don't open a public issue. Use [private vulnerability reporting](https://github.com/alfpricot/gala/security/advisories/new) instead: go to the repository's Security tab and choose "Report a vulnerability". Include what you found, how to reproduce it, and what impact you expect.

## Scope

The `gala` binary and the workflows in this repository.

## Supported versions

Only the latest release gets fixes.

## Practices

- gala reads `GITHUB_TOKEN` or `GH_TOKEN` from the environment. Never commit a token or put one in `gala.toml`.
- Go dependencies are kept current, and `govulncheck` runs on every pull request.
