# gala — build recipes.
#
# The gate (just check) is what CI runs; run it before every push.

set shell := if os_family() == "windows" { ["powershell", "-NoProfile", "-Command"] } else { ["sh", "-c"] }

default: check

# Build the binary into ./bin.
build:
    go build -o bin/gala ./cmd/gala

# Format every Go file in place.
fmt:
    gofmt -w cmd internal

# Fail when anything is unformatted.
[windows]
fmt-check:
    $bad = gofmt -l cmd internal; if ($bad) { Write-Error "unformatted files:`n$bad"; exit 1 }

[unix]
fmt-check:
    test -z "$(gofmt -l cmd internal)" || { gofmt -l cmd internal; exit 1; }

# Static analysis.
vet:
    go vet ./...

# go.mod and go.sum must already be tidy (tool directives included).
tidy-check:
    go mod tidy -diff

staticcheck:
    go tool staticcheck ./...

errcheck:
    go tool errcheck -ignoretests ./...

vulncheck:
    go tool govulncheck ./...

# Tests with the race detector, no result caching.
test-race:
    go test -race ./... -count=1

# The whole gate, in the same order CI runs it.
check: fmt-check vet tidy-check staticcheck errcheck vulncheck test-race build self-plan

# gala plans its own next release: the self-hosting smoke test.
self-plan:
    go run ./cmd/gala plan

# Run the CLI from ./bin.
run +ARGS:
    go run ./cmd/gala {{ARGS}}