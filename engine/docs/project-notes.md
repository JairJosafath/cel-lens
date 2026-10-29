# Project notes

## Release check

The check before a release tag is these `make` targets, in this order:

1. `make check` (deps, tidy-check, fmt-check, vet, lint, `go test -race -count=1 -timeout 30m ./...`, dyn-report)
2. `make vet-windows`
3. `make test-windows`
4. `make test-dist`

CI's `full` job of `.gitea/workflows/check.yaml` gates its last three steps
(vet-windows, test-windows, test-dist) on `steps.changes.outputs.code`, but the
job has no `changes` step, so they are skipped there. Until the Standard fixes
the workflow, run those three targets by hand before every release.

## v0.17.2: the full check timed out

Cause: `go test -race ./...` ran past `go test`'s default 10-minute timeout in the
root package (`panic: test timed out after 10m0s` in `TestGoInstallFromProxy`).
The e2e tests build the binary with `go build`, and `TestGoInstallFromProxy` then
ran `go install -trimpath` into a fresh module cache; without `-trimpath` in the
first build, the two builds shared no cache entries, so every dependency
(Kubernetes, Kyverno, cel-go) was compiled twice. On a slow machine
`internal/assist` also took almost nine minutes with `-race`.

Fix: the e2e build (`build` in `e2e_test.go`) uses `-trimpath`, so the install
reuses its build cache; the `test` target passes `-timeout 30m` (a Makefile
change, proposed as a Standard change). Not user-visible, so no CHANGELOG line.

Budget: on the 8-core sandbox, `go test -race -count=1 -timeout 30m ./...` takes
about 4.5 minutes (root package 94 to 164 s, `internal/assist` 261 s, `pkg/check`
82 s, `internal/lsp` 51 s); the slow machine took roughly twice as long
for `internal/assist`. Keep the whole run well under the 30-minute timeout.

`make test-dist` builds the release binaries with `CGO_ENABLED=0 -ldflags '-s -w'`,
which shares no build cache with the tests, so it compiles every dependency again:
about 7 minutes on the sandbox (it passed the default 10-minute limit only just, and
failed once on a cold cache). The target now also passes `-timeout 30m`.
`make vet-windows` and `make test-windows` pass in under a minute.

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
  v0.17.2` exists, but the repository has no tags, so I could not tell whether
  0.17.2 was published. What is gated is the code of b90b4ca (#419), 2661148
  (#420: `oldSelf` is not offered on the uncorrelatable portion of a schema) and
  51d8812 (#415). #417 changed no code, so it is not gated.
