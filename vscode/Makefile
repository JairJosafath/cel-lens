# The quality contract of this repository. CI runs `make check` on every pull
# request; agents and people run it before every commit. `make help` lists the
# targets.

NAME := cel-lens-vscode
BIN := node_modules/.bin
# Node's compile cache would fill the runner's memory-backed /tmp with files
# that are never reused. Every Node process the check starts inherits this.
export NODE_DISABLE_COMPILE_CACHE := 1
# The extension's version is the one in package.json. A release passes its tag,
# which must match: make dist VERSION=v1.2.3
PACKAGE_VERSION := $(shell node -p "require('./package.json').version")
VERSION ?= v$(PACKAGE_VERSION)
# vsce builds the bundle with package.json's vscode:prepublish script. No
# dependencies to add (esbuild bundled them), no licence or repository prompts.
# A release with a dist script in package.json builds one .vsix per platform with it.
DIST_SCRIPT := $(shell node -p "require('./package.json').scripts?.dist ?? ''")
VSCE := $(BIN)/vsce package --no-dependencies --skip-license --allow-missing-repository

.DEFAULT_GOAL := help
.PHONY: help check deps fmt fmt-check lint typecheck test e2e build package-check dist release-version clean

help: ## List the targets.
	@awk 'BEGIN {FS = ":.*## "} /^[a-z0-9-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

check: deps fmt-check lint typecheck test e2e package-check ## Everything CI checks.

deps: ## Install the exact packages in package-lock.json.
	npm ci --no-audit --no-fund

fmt: ## Format every file with Prettier.
	$(BIN)/prettier --write .

fmt-check: ## Fail when a file is not formatted.
	$(BIN)/prettier --check .

lint: ## Lint with eslint.config.mjs, failing on any finding.
	$(BIN)/eslint --max-warnings 0 .

typecheck: ## Type-check with tsconfig.json.
	$(BIN)/tsc --noEmit

test: ## Run the unit tests.
	$(BIN)/vitest run

# The end-to-end test starts the real engine over stdio: no VS Code and no display.
# `npm run e2e --if-present` does nothing until the end-to-end test lands.
e2e: ## Run the end-to-end test: the real engine behind the extension's start command.
	npm run e2e --if-present

build: ## Bundle the extension into out/, minified.
	node esbuild.mjs --production

package-check: ## Fail when the extension does not package.
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
		$(VSCE) --out "$$tmp/$(NAME).vsix"

dist: release-version deps ## Package the release .vsix files into dist/ (one per platform when package.json has a dist script).
	rm -rf dist
	mkdir -p dist
ifneq ($(DIST_SCRIPT),)
	npm run dist
else
	$(VSCE) --out dist/$(NAME)-$(PACKAGE_VERSION).vsix
endif

release-version: # Fail when VERSION is not package.json's version.
	@if [ "$(VERSION)" != "v$(PACKAGE_VERSION)" ]; then \
		echo "Tag $(VERSION) does not match version $(PACKAGE_VERSION) in package.json." >&2; \
		echo "Set \"version\": \"$(VERSION:v%=%)\" there, add its CHANGELOG.md entry, then tag again." >&2; \
		exit 1; \
	fi

clean: ## Remove build output.
	rm -rf out dist
