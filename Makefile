# Plumb – build, test and lint. See docs/ARCHITECTURE.md §9.
# Needs: Go (version in go.mod). The web targets need Node (T-017).

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_BUILD     = CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
# Pinned linter. Change the version here only.
STATICCHECK  = honnef.co/go/tools/cmd/staticcheck@v0.8.2
# npm packages can ship Go files. The go command skips web/node_modules
# through the "ignore" line in go.mod; gofmt needs its own file list.
GO_FILES     = $(shell find . -name '*.go' -not -path '*/node_modules/*')

.PHONY: build build-web test test-go test-web lint lint-go lint-web test-guest test-pve clean

## build: web UI and both static binaries → bin/
build: build-web
	$(GO_BUILD) -o bin/plumb ./cmd/plumb
	$(GO_BUILD) -o bin/plumb-agent ./cmd/plumb-agent

## build-web: build the web UI into web/dist (stub; T-017 fills it)
build-web:
	@echo "build-web: stub, filled by T-017"

## test: all tests that need no Proxmox host and no root
test: test-go test-web

test-go:
	go test ./...

## test-web: unit tests of the web UI (stub; T-017 fills it)
test-web:
	@echo "test-web: stub, filled by T-017"

## lint: all linters
lint: lint-go lint-web

lint-go:
	@files="$$(gofmt -l $(GO_FILES))"; if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi
	go vet ./...
	go run $(STATICCHECK) ./...
	go mod tidy -diff

## lint-web: lint the web UI (stub; T-017 fills it)
lint-web:
	@echo "lint-web: stub, filled by T-017"

## test-guest: resource tests in privileged Debian/Ubuntu containers (images kept for cache)
test-guest:
	bash test/guest/run.sh

## test-pve: tests against a real Proxmox host (stub; T-012 and T-013 fill it)
test-pve:
	@echo "test-pve: not implemented yet (T-012, T-013)"; exit 1

clean:
	rm -f bin/plumb bin/plumb-agent
