VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

.PHONY: install
install:
	go install -ldflags "$(LDFLAGS)" ./cmd/nem
