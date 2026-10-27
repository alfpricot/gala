# gala — build recipes.
#
# The gate (just check) is what CI runs; run it before every push.
#
# Every recipe is POSIX sh. just picks its shell from PATH — on this machine
# that is Git's sh even on Windows — so a single dialect keeps `just check`
# identical everywhere it runs. CI does not use just; verify.yml spells the
# same steps out as workflow run steps.

set shell := ["sh", "-c"]

default: check

# Build the binary into ./bin.
build:
    go build -o bin/gala ./cmd/gala

# Format every Go file in place.
fmt:
    gofmt -w cmd internal

# Fail when anything is unformatted.
fmt-check:
    bad="$(gofmt -l cmd internal)"; if [ -n "$bad" ]; then echo "unformatted files:"; echo "$bad"; exit 1; fi

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

# Run the CLI.
run +ARGS:
    go run ./cmd/gala {{ARGS}}