VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

.PHONY: install
install:
	go install -ldflags "$(LDFLAGS)" ./cmd/nem

# Ephemeral local OCI registry + catalog for development.
# DIR overrides the catalog directory (default: sibling nem-official-catalog checkout).
DIR ?=

.PHONY: local-catalog-up local-catalog-publish local-catalog-down local-catalog-status
local-catalog-up:
	hack/local-catalog.sh up $(DIR)

local-catalog-publish:
	hack/local-catalog.sh publish $(DIR)

local-catalog-down:
	hack/local-catalog.sh down

local-catalog-status:
	hack/local-catalog.sh status

# Point git at the tracked hooks so commit subjects are checked locally
# instead of failing in CI. One-time, per clone.
.PHONY: hooks
hooks:
	git config core.hooksPath .githooks

.PHONY: check
check:
	env -u GOROOT go vet ./...
	env -u GOROOT golangci-lint run ./...
	env -u GOROOT go test -race -shuffle=on ./...

# Snapshot builds have no Sigstore identity available and are never
# published, so signing is skipped rather than blocking on OIDC login.
.PHONY: snapshot
snapshot:
	env -u GOROOT goreleaser release --snapshot --clean --skip=sign

.PHONY: test-install
# Run install script unit tests
test-install:
	bats test/install.bats
