# Plumb – build, test and lint. See docs/ARCHITECTURE.md §9.
# Needs: Go (version in go.mod) and Node >= 24 with npm for the web targets.

VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GO_BUILD     = CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
# Pinned linter. Change the version here only.
STATICCHECK  = honnef.co/go/tools/cmd/staticcheck@v0.8.2
# npm packages can ship Go files. The go command skips web/node_modules
# through the "ignore" line in go.mod; gofmt needs its own file list.
GO_FILES     = $(shell find . -name '*.go' -not -path '*/node_modules/*')
# npm ci runs only when the lock file changed; npm writes this file on every install.
WEB_DEPS     = web/node_modules/.package-lock.json

.PHONY: build check-embed build-web test test-go test-race test-web test-e2e lint lint-go lint-web test-guest test-pve clean

## build: web UI and both static binaries → bin/
build: build-web
	$(GO_BUILD) -o bin/plumb-agent ./cmd/plumb-agent
	cp bin/plumb-agent cmd/plumb/agentbin/plumb-agent
	$(GO_BUILD) -o bin/plumb ./cmd/plumb

## check-embed: the built plumb serves the UI and the agent binary of this build
check-embed:
	scripts/check-embed.sh bin/plumb bin/plumb-agent

$(WEB_DEPS): web/package.json web/package-lock.json
	cd web && npm ci --ignore-scripts
	@touch $@

## build-web: generate API types and build the web UI into web/dist (embedded by package web)
build-web: $(WEB_DEPS)
	cd web && npm run build

## test: all tests that need no Proxmox host and no root
test: test-go test-web

test-go:
	go test ./...

## test-race: Go tests with the race detector (needs cgo and a C compiler; CI runs it)
test-race:
	CGO_ENABLED=1 go test -race -count=1 ./...

## test-web: unit tests of the web UI (vitest)
test-web: $(WEB_DEPS)
	cd web && npm test

## test-e2e: Playwright test of the UI against plumb serve --dev --dev-demo
## (needs `make build` and a Chromium: cd web && npx playwright install --with-deps chromium)
test-e2e: build
	cd web && npx playwright test

## lint: all linters
lint: lint-go lint-web

lint-go:
	@files="$$(gofmt -l $(GO_FILES))"; if [ -n "$$files" ]; then echo "gofmt needed:"; echo "$$files"; exit 1; fi
	go vet ./...
	go run $(STATICCHECK) ./...
	go mod tidy -diff

## lint-web: OpenAPI check, eslint, prettier --check, svelte-check
lint-web: $(WEB_DEPS)
	cd web && npm run lint

## test-guest: resource tests in privileged Debian/Ubuntu containers (tags removed; layer cache kept)
test-guest:
	bash test/guest/run.sh

## test-pve: tests against a real Proxmox host (stub; T-012 and T-013 fill it)
test-pve:
	@echo "test-pve: not implemented yet (T-012, T-013)"; exit 1

clean:
	rm -f bin/plumb bin/plumb-agent
