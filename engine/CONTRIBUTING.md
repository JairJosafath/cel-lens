# Contributing

Thank you for helping. The engine, `cel-lens-core`, is open source under
Apache-2.0 and lives under `engine/` of
https://github.com/JairJosafath/cel-lens. Report bugs and ask questions at
https://github.com/JairJosafath/cel-lens/issues; report a vulnerability as
[SECURITY.md](SECURITY.md) says.

## Build

You need Go 1.26 or newer from [go.dev](https://go.dev/dl/) and `make`.

```sh
go build -o cel-lens-core .   # the program, from a checkout
make build                    # bin/cel-lens-core, versioned with git describe
make dist                     # release binaries and checksums.txt in dist/
make help                     # every target
go run . --help               # run it
go run . lsp                  # the language server on stdin and stdout
go run . check deploy/ crd.yaml
```

`cel-lens-core --version` prints the version the binary was built with (the
`-ldflags "-X main.version=..."` of the build, else the module version from the
build info), then the Kubernetes version it matches and the range it supports,
such as `cel-lens-core v0.3.0 (matches Kubernetes 1.37; supports 1.33–1.37)`.

Release binaries are built with `CGO_ENABLED=0` for six platforms and named
`cel-lens-core_<version>_<os>_<arch>[.exe]`, where `<os>` is `linux`, `darwin`
or `windows` and `<arch>` is `amd64` or `arm64`. `checksums.txt` on the same
release lists the SHA-256 of every file (`sha256sum -c checksums.txt`).

## Test

```sh
make check         # tidy, format, vet, lint, test with -race, dyn report
make check-light   # vet, lint and build, no tests
make test          # the tests only: builds bin/cel-lens-core once, then go test -race
```

`go test ./...` passes on its own: when `CEL_LENS_BIN` is unset, the root package
builds the program once and the tests run it. `make test` is the quicker way to
repeat it, since it builds `bin/cel-lens-core` once and hands it to the tests.

`make check` needs [golangci-lint](https://golangci-lint.run/welcome/install/)
and gofumpt. **It fails on any warning**: `golangci-lint run` and `go vet` fail
on every finding, so fix the code rather than silence the finding. A
`//nolint:<linter> // <why>` is for a false positive only: one linter, one
reason. `make fmt` formats. Run `make check` before every commit.

Add a test that fails first, then the change that makes it pass. Tests sit
next to the code; fixtures and golden files are in `testdata/` and a golden
file is rewritten with `make build` and then
`CEL_LENS_BIN=$PWD/bin/cel-lens-core go test ./... -update`, after which you review every
diff. `go test -bench Diagnostics ./internal/lsp/` measures the 300 ms
diagnostics budget on a 1,000-line CRD.

### Tests that skip

A few tests skip in a clone of the public repository and report why. They
check files that only the maintainers' build setup has, or they are too slow to
repeat:

- The tests in `ci_test.go` read the CI workflow files. The published
  source does not carry them, so the tests skip.
- `TestPublicSourceTestsPass` proves that `go test ./...` passes on a copy of
  the tree without those files; it skips on the public copy, where it would
  only repeat the run, and with `-short`.
- `TestDist` and `TestGoInstallFromProxy` skip unless `CEL_LENS_DIST_TEST=1`
  (`make test-dist`): the first cross-compiles the release binaries, the second
  runs `go install` and builds every dependency from a fresh module cache.

## Send a change

1. Fork https://github.com/JairJosafath/cel-lens and create a branch.
2. Make one change for one concern, with its test and the docs a user reads
   (README, `--help` text, doc comments).
3. Add a line under `[Unreleased]` in `CHANGELOG.md`, in the section of its kind
   from [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), in this order:
   `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`. The line
   names no issue number. A release adds a link line for its notes in the
   `core-v` form, `[0.20.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.19.0...core-v0.20.0`,
   and `[Unreleased]` compares the newest `core-v` tag to `HEAD`.
4. Run `make check`, commit with a Conventional Commit message (`feat:`, `fix:`,
   `test:`, `refactor:`, `docs:`) and open a pull request on the public
   repository.

`AGENTS.md` is the rulebook for people and agents: package layout, errors,
context, goroutines, tests and docs. Read it before changing anything, and
`docs/style/` for the style guides.

## Layout

- `main.go` is the command: it parses the command line and calls into
  `internal/`. Its `run` function returns the exit code instead of exiting, so
  `main_test.go` tests the whole program.
- `internal/` holds the packages, one purpose each; `internal/doc.go` explains
  the rules.
- `internal/scan` runs the `check` command over files and folders,
  `internal/lsp` is the language server, `internal/assist` answers its hover and
  completion requests, and `internal/jsonrpc` frames the protocol's messages.
- `pkg/extract` finds the CEL expressions in Kubernetes YAML files, with their
  positions; `pkg/check` compiles and type-checks them with the Kubernetes CEL
  libraries and returns diagnostics positioned in the file. Its fixtures in
  `pkg/check/testdata` note the API server's result in `# api-server:` comments
  and what must be reported in `# want:` lines.

How the engine behaves, in detail, is in [docs/engine.md](docs/engine.md); the
places a YAML document gives an expression its types are in
[docs/type-sources.md](docs/type-sources.md).
