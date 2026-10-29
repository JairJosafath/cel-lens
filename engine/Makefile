# The quality contract of this repository. CI runs `make check` on every pull
# request; agents and people run it before every commit. `make help` lists the
# targets.

BINARY := cel-lens-core
# The version stamped into binaries: the tag at HEAD, else a describe such as
# v1.2.0-3-gabc1234 (-dirty with uncommitted changes). Releases pass the tag:
# make dist VERSION=v1.2.3
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
# Release platforms, as GOOS/GOARCH.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.DEFAULT_GOAL := help
.PHONY: help check check-light deps tidy-check fmt fmt-check vet vet-windows lint test dyn-report test-windows test-dist build dist clean

help: ## List the targets.
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-11s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

check: deps tidy-check fmt-check vet lint test dyn-report ## Everything CI checks.

check-light: deps vet lint build ## The fast part of check: vet, lint and build, no tests.

deps: ## Download the modules in go.mod.
	go mod download

tidy-check: ## Fail when go mod tidy would change go.mod or go.sum.
	go mod tidy -diff

fmt: ## Format the code (gofumpt, goimports).
	golangci-lint fmt

fmt-check: ## Fail when the code is not formatted, showing the diff.
	golangci-lint fmt --diff

vet: ## Run go vet.
	go vet ./...

vet-windows: ## Run go vet for Windows, which also vets the *_windows.go files.
	GOOS=windows go vet ./...

lint: ## Lint with .golangci.yml, failing on any finding.
	golangci-lint run

test: ## Run the tests with the race detector, never from the cache.
	go test -race -count=1 -timeout 30m ./...

dyn-report: ## Print how many expressions of real policies meet dyn, and why.
	@[ ! -f internal/assist/dynreport_test.go ] || (cd internal/assist && go test -count=1 -run '^TestDynReport$$')

test-windows: ## Compile the test binaries of the packages with Windows-only files.
	@set -e; for dir in $$(find . -name '*_windows*.go' -exec dirname {} \; | sort -u); do \
		echo "GOOS=windows go test -c $$dir"; \
		GOOS=windows go test -c -o /dev/null $$dir; \
	done

test-dist: ## Run the release-build test (TestDist), which go test skips.
	CEL_LENS_DIST_TEST=1 go test -race -count=1 -timeout 30m -run '^TestDist$$' .

build: ## Build bin/cel-lens-core for this machine.
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

dist: ## Cross-compile the release binaries and checksums.txt into dist/.
	rm -rf dist
	mkdir -p dist
	@set -e; for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		out=dist/$(BINARY)_$(VERSION)_$${os}_$${arch}$$ext; \
		echo "go build $$out ($(VERSION))"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags '-s -w $(LDFLAGS)' -o $$out .; \
	done
	cd dist && sha256sum $(BINARY)_* > checksums.txt

clean: ## Remove build output.
	rm -rf bin dist
