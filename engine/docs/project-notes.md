# Project notes

## Release check

The check before a release tag is these `make` targets, in this order:

1. `make check` (deps, tidy-check, fmt-check, vet, lint, `go test -race -count=1 -timeout 30m ./...`, dyn-report)
2. `make vet-windows`
3. `make test-windows`
4. `make test-dist`

CI's `full` job of `.gitea/workflows/check.yaml` runs all four on a push to a
`release-check/**` branch.

## v0.17.2: release

v0.17.2 was tagged at the merge of #18 and published on GitHub as `core-v0.17.2` on
2026-10-29. https://github.com/JairJosafath/cel-lens/releases/tag/core-v0.17.2 holds
`checksums.txt` and the six binaries; the source is under `engine/` at the tag
`engine/v0.17.2`.

## v0.17.2: the first full check timed out

Cause: `go test -race ./...` ran past `go test`'s default 10-minute timeout in the
root package (`panic: test timed out after 10m0s` in `TestGoInstallFromProxy`).
Before #26 the e2e tests built the binary with `go build` from `e2e_test.go`, and `TestGoInstallFromProxy`
ran `go install -trimpath` into a fresh module cache; without `-trimpath` in the
first build, the two builds shared no cache entries, so every dependency
(Kubernetes, Kyverno, cel-go) was compiled twice. On a slow machine
`internal/assist` also took almost nine minutes with `-race`.

Fix: before #26, the e2e build used `-trimpath`, so the install reused its build
cache (#26 later replaced that build); the `test` target passes `-timeout 30m` (a Makefile
change, merged as Standard change #6). Not user-visible, so no CHANGELOG line.

Budget: on the 8-core sandbox, `go test -race -count=1 -timeout 30m ./...` takes
about 4.5 minutes (root package 94 to 164 s, `internal/assist` 261 s, `pkg/check`
82 s, `internal/lsp` 51 s); the slow machine took roughly twice as long
for `internal/assist`. Keep the whole run well under the 30-minute timeout.

`make test-dist` builds the release binaries with `CGO_ENABLED=0 -ldflags '-s -w'`,
which shares no build cache with the tests, so it compiles every dependency again:
about 7 minutes on the sandbox (it passed the default 10-minute limit only just, and
failed once on a cold cache). The target now also passes `-timeout 30m`.
`make vet-windows` and `make test-windows` pass in under a minute.

## v0.17.2: the second full check failed (run 41)

In the `full` job, `make check` passed `go mod tidy`, fmt, vet and lint. Then
`go test -race -count=1 -timeout 30m ./...` printed `go build: signal: killed` and
`FAIL github.com/JairJosafath/cel-lens/engine 602.672s`; every other package passed.

Cause: the e2e tests build the engine inside the tests, under their build deadline
and under `-race`, while the other packages' tests ran on the same runner.
cel-lens-core#26 moved that build into `make test`, so the tests used one prebuilt
binary. Run 140 (see "Runner timings") shows it working on the runner.

### Why the tests build the program when `CEL_LENS_BIN` is unset

#26 made `buildBinary` fail without `CEL_LENS_BIN`, which broke the public CI
(job `engine`) and every contributor who runs plain `go test ./...`. Now the
tests use `CEL_LENS_BIN` when set and otherwise build the program once per test
process (`go build -trimpath`, lazily, into a temporary directory that `TestMain`
removes), with no deadline of its own. `make test` and the factory's check still
build once and set `CEL_LENS_BIN`; `TestTestTargetBuildsOnce` guards that.

## Experimental features (#4): where the issue's list disagreed with the evidence

The issue described `kyverno`, `crd-typed-policies` and `fitting-completion`. I
checked the list against `internal/assist/kyverno.go`, `pkg/check/isolation_test.go`,
`internal/lsp/workspace.go`, `internal/assist/kyverno_test.go` and the CHANGELOG,
and followed the evidence where they differed.

- `kyverno`: "libraries such as `resource`, `http`, `image` are not completed by
  name" is wrong. `TestKyvernoComplete` expects `resource`, `http`, `globalContext`,
  `image` and `isImage` in the completion at the top level, and `namespaces()` in
  `internal/assist/assist.go` completes them. The README table does not say it.
  True: `namespaceObject` is `dyn` (`reasonOpenSchema` in `kyverno.go`) and only
  ValidatingPolicy is read (CHANGELOG 0.13). `variables` is typed from
  `spec.variables`, so only its members may be `dyn`.
- `kyverno`, "lists no CEL ranges": the engine has no request that lists CEL
  ranges. The VS Code extension finds them itself (`src/cel_ranges.ts`,
  `src/color_painter.ts` in cel-lens-vscode), so the engine cannot switch the
  colours off. Done: nothing in the engine; follow-up for the extension, which
  should not colour Kyverno documents unless `kyverno` is on.
- `crd-typed-policies`: only `params` is ever typed from a CRD (`paramsVariable`
  in `internal/assist/scope.go`, `pkg/check/typed.go`). `object` and `oldObject` of
  a custom resource are `dyn` (`reasonUnknownKind`) with or without a CRD. So the
  feature gates `params` and the policy variables typed from it, and the README
  says so. True: the workspace scan runs inside `initialize` and blocks the
  session (`scanWorkspace` in `internal/lsp/workspace.go`). It still runs with the
  feature off, because the set of CRDs is kept; only its use is gated.
- `crd-typed-policies`, the neighbouring `---` document: commit 1ed89fe (#417)
  added tests only. `pkg/check/isolation_test.go` has no test for a CRD that types
  `params`, and `scan.go` and `lsp.go` still add the file's own CRDs. Reproduced
  with `cel-lens-core check --experimental crd-typed-policies` on one file (CRD,
  `---`, policy with `paramKind`): the field warning appears. So the gap is real,
  and the CHANGELOG line for #417 is true only for types other than CRDs.
- `fitting-completion`, "built after v0.17.1, never released": the CHANGELOG puts
  #415, #417, #419 and #420 under `[0.17.2] - 2026-10-13` and `aa2ee33 Prepare
  v0.17.2` exists; v0.17.2 was published later (see "v0.17.2: release"). What is gated is the code of b90b4ca (#419), 2661148
  (#420: `oldSelf` is not offered on the uncorrelatable portion of a schema) and
  51d8812 (#415). #417 changed no code, so it is not gated.

## The public-layout test no longer reruns every package (#16)

`TestPublicSourceTestsPass` copied the tree without the factory's files and ran
`go test ./...` in it, so every package ran a second time, and in the root package
`TestGoInstallFromProxy` built every dependency again. With `-race` in `make check`
the root package took 581 s (it was 94 to 164 s before #3).

Options measured on the 8-core sandbox: the cost is the inner run, not the copy.
Only the root package reads the factory's files (`ci_test.go`, `changes_test.go`,
`license_test.go`; `internal/assist` names `AGENTS.md` in a comment only), so the
copy now runs `go test -short .`: the root package alone, and without the install
test, which the outer run already covers. Compiling the test binaries first was not
needed. A test that reads `.gitea/` without skipping still fails in the copy (tried
with a temporary test: `stat .gitea: no such file or directory`), and the test still
skips itself in the copy (no `.gitea`) and under `-short`.

Result: `go test -race -count=1 -v .` on the root package takes 143 s on a quiet
machine (`TestPublicSourceTestsPass` 46 s, `TestGoInstallFromProxy` 41 s), within
twice the 164 s upper figure. A full `make check` on a busy sandbox showed the root
package at 1263 s while other work was running, so timings here vary a lot with load;
the 143 s figure is the one to compare against.

## Current root package budget (#67)

This replaces the root package figures above (94 to 164 s, 143 s on a quiet
machine), which predate #26 and #53. Measured on the 8-core sandbox on 2026-10-30:
`go test -race -count=1 -timeout 30m .`, the root package's part of `make check`,
takes about 36 to 43 s (36 to 38 s in the review of #53; 43 s on a re-run on
2026-10-30 with a warm build cache, 58 s with a cold one). Inside a full
`make check` on 2026-10-30, where every package runs in parallel, the root package
took 65 s (`internal/assist` 248 s, `pkg/check` 119 s). Compare future runs against
36 to 43 s alone and about 65 s inside `make check`.

`go install` (`TestGoInstallFromProxy`) now runs only in `make test-dist`, not in
`make check`; `TestDist` runs there too. Both are skipped by `go test` unless
`CEL_LENS_DIST_TEST=1` is set.

## Runner timings

### `make check` on a busy machine (cel-lens-core#119, resolving #118)

Run 254, the full check of v0.21.1, was cancelled at the runner's 1-hour limit while
`go build` (the first step of `make test`) waited on swap; nothing was wrong in the
code. The Makefile now takes the number of packages Go builds and tests at once from
`GO_PARALLEL` (default 2, passed as `-p`), where Go defaulted to the CPU count. The
public-copy test's inner `go test` takes the same `-p` (it reads `GO_PARALLEL`, 2
when unset) and `-trimpath`, so the outer run's cached packages serve it. `-race`,
`-count=1`, `-short` on the inner run and every timeout are unchanged. I kept the
root package inside the one `go test`; no separate step was needed.

Why 2: with `-p 2` at most two race-instrumented test binaries are built and linked
at once, instead of eight here and more on the runner. I did not measure 1, 3 or 4
separately: the sandbox was itself loaded (swap full, 3 GB of it in use), so the wall
times below are noisy, and the largest single process does not depend on `-p`.

`/usr/bin/time` is not installed in the sandbox; I measured with Python's
`resource.getrusage(RUSAGE_CHILDREN).ru_maxrss`, the largest resident set of any
descendant, which is the figure `time -v` prints as Maximum resident set size. 8-core
sandbox, 2026-10-30, full `make check`:

| | `GO_PARALLEL` | wall time | largest process (max RSS) |
|---|---|---|---|
| before (`-p` = 8 CPUs) | none | 1314 s | 866,824 kB |
| after | 2 | 392 s | 856,640 kB |

Round 2 (#118): `make check GO_PARALLEL=8` failed. `make` exports `GO_PARALLEL`, and
`TestMakefileParallelKnob/default` inherited it and expected `-p 2`. Removing only the
environment variable was not enough: make also passes the override to children inside
`MAKEFLAGS` (`-- GO_PARALLEL=8`). The test now removes `GO_PARALLEL`, `MAKEFLAGS`,
`MFLAGS` and `MAKELEVEL` from the environment of its `make -n` calls. I still did not
measure 1, 3 or 4: a cold-cache `make test GO_PARALLEL=1` ran over 20 minutes in the
loaded sandbox, so the default stays 2.

The wall time fell, but both runs shared a loaded machine, so do not read it as a
speedup. The largest process is one linker or test binary and did not change; what
`-p 2` lowers is how many of them are alive together, which this figure does not
show. The runner's figure comes from the next release check.

From run 140 (cel-lens-core#71), the `full` job of the push `release-check/v0.19.0`
at `5b4c2e4`, green, 2026-10-30 03:55 to 04:08Z. It is the first green full check
after #26. The owner read the figures on the runner. The tests used the binary that
`make` built once (`go build ... -o bin/cel-lens-core`) and passed as `CEL_LENS_BIN`,
so #26 works on the runner. Against the 8-core sandbox figures above (inside a full
`make check`, where every package runs in parallel):

- Root package: `ok  github.com/JairJosafath/cel-lens/engine 82.562s`, against about
  65 s on the sandbox.
- `internal/assist`: 204.844 s, against 248 s on the sandbox.
- `pkg/check`: 72.672 s, against 119 s on the sandbox.
- `go test -race ./...` took 4m17s in all; `make check` took 5m47s (03:55:36 to
  04:01:23Z).

Run 41, the earlier failed run (v0.17.2, before #26), for comparison only: root
package `FAIL ... 602.672s`, `internal/assist` 749 s, `pkg/check` 384 s.

- A cold `-trimpath` build of the engine takes 5 min 15 s on an idle 8-core sandbox.
