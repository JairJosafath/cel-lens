# cel-lens engine

The engine of [cel-lens](https://github.com/JairJosafath/cel-lens): language
support for CEL in Kubernetes files, built on cel-go and the Kubernetes CEL
libraries so results match the API server. One program, `cel-lens-core`, serves
as a language server (`lsp`) for editors such as VS Code and as a command-line
check (`check`) for terminals and CI. The source lives under `engine/` in the
cel-lens repository.

## License

The engine is licensed under Apache-2.0; see [LICENSE](LICENSE).

## Bugs

Report bugs and ask questions at https://github.com/JairJosafath/cel-lens/issues.

## Build, test and run

You need Go 1.26 or newer from [go.dev](https://go.dev/dl/) and `make`.

```sh
make build           # bin/cel-lens-core, versioned with git describe
make check           # tidy, format, vet, lint, test with -race
go test ./...        # run the tests only
go run . --help      # run it
go run . lsp         # the language server on stdin and stdout
go run . check deploy/ crd.yaml
make dist            # release binaries and checksums.txt in dist/
make help            # every target
```

`make check` also runs golangci-lint and gofumpt; install them as their
documentation describes ([golangci-lint](https://golangci-lint.run/welcome/install/)).

`cel-lens-core --version` prints the version the binary was built with: the
`-ldflags "-X main.version=..."` of the build, else the module version from the
build info (the tag `go install` fetched, or `(devel)` from a checkout), then the Kubernetes
version it matches and the range it supports, such as
`cel-lens-core v0.3.0 (matches Kubernetes 1.37; supports 1.33–1.37)`.

## Install

Download the binary for your platform from the releases of
https://github.com/JairJosafath/cel-lens/releases, or build it with
`make build`. The VS Code extension ships the engine, so editor users install
nothing else.

### Install with Go

```sh
go install github.com/JairJosafath/cel-lens/engine@<version>
```

The public repository tags the engine `engine/vX.Y.Z`; `<version>` is the
`vX.Y.Z` of such a tag (or `latest`). `go install` names the binary after the
last path element, so it writes `engine` (`engine.exe` on Windows) to
`$(go env GOBIN)`, or `$(go env GOPATH)/bin`. Rename it to get the program
under its usual name:

```sh
mv "$(go env GOPATH)/bin/engine" "$(go env GOPATH)/bin/cel-lens-core"
```

Or build with `go build -o cel-lens-core` from a checkout. The release files
below keep their names.

## Kubernetes version

By default cel-lens matches **Kubernetes 1.37**: results come from the 1.37
Kubernetes CEL libraries and cost rules. Clusters on another version can pick
theirs, from 1.33 to 1.37 (`pkg/kubeversion` defines the range and the default):

- command line: `cel-lens-core check --kube-version 1.34 deploy/`; an
  unsupported value fails with exit code 2 and lists the supported versions;
- language server: set `kubernetesVersion` (for example `"1.34"`) in
  `initializationOptions` or in the `settings` of `workspace/didChangeConfiguration`.
  A change re-checks every open document; an empty value returns to the
  default, and an unsupported value is refused with a message and ignored.
  The `initialize` result reports the version in use as the top-level
  `kubernetesVersion`. Under `capabilities.experimental.celLens`,
  `supportedKubernetesVersions` lists the versions the engine supports (newest
  first, written like `kubernetesVersion`) and `kubernetesVersion` is the
  version used when none is picked; for example
  `"supportedKubernetesVersions": ["1.37", "1.36", "1.35", "1.34", "1.33"]`
  and `"kubernetesVersion": "1.37"`.

## Releases

Release binaries are built with `CGO_ENABLED=0` (static, nothing to install)
for six platforms. The assets are bare files, not archives, named

```
cel-lens-core_<version>_<os>_<arch>[.exe]
```

where `<version>` is the tag (`v1.2.3`), `<os>` is `linux`, `darwin` or
`windows`, `<arch>` is `amd64` or `arm64`, and `.exe` ends the Windows files.
`checksums.txt` on the same release lists the SHA-256 of every asset
(`sha256sum -c checksums.txt` verifies them). `--version` prints the tag.

## Language server

`cel-lens-core lsp` serves CEL support for Kubernetes YAML over the Language
Server Protocol on stdin and stdout, for any editor:

- **Diagnostics** for every CEL expression in an open CustomResourceDefinition
  or ValidatingAdmissionPolicy, published on open and on every change, with the
  API server's messages. A document whose YAML does not parse gets an error on
  the line where it breaks (`YAML is broken here: ...`); the other documents of
  the file are still checked.
- **Hover** on a variable, field or function inside an expression: its type and
  a short description.
- **Completion** inside an expression: `self`/`oldSelf` or `object`/`request`/...,
  the fields of the type before a dot, and the Kubernetes CEL functions.

- **Quick fixes** (the light bulb): "Change to `maxReplicas`" for a misspelled
  field, function or macro when exactly one name valid there is within two edits
  of it, and "Add `maxLength: 1000` to `spec.name`" (or `maxItems`,
  `maxProperties`) for a cost error, with the largest round number that brings
  the rule under its budget (a cost error on the CRD's total is solved when the
number also brings the total under its budget). No fix is offered when there is no clear one.

Positions outside CEL expressions get no hover and no completion. Documents are
synchronised in full; the server exits 0 after `shutdown` and `exit`, and 1 when
the client leaves without `shutdown`. `go test -bench Diagnostics ./internal/lsp/`
measures the 300 ms diagnostics budget on a 1,000-line CRD.

The command accepts the flags language clients pass: `--stdio` (the default and
only transport) and `--clientProcessId=<pid>`, which makes the server exit when
that process is gone. `--socket`, `--pipe` and `--node-ipc` are refused with
exit code 2. `lsp --help` lists them.

## Typed `object` and `oldObject`

In a ValidatingAdmissionPolicy whose `matchConstraints.resourceRules` match
built-in kinds, `object` and `oldObject` are typed from that kind's schema, as
the API server does when it type-checks a policy: completion offers the kind's
fields down the tree, hover names the kind (`object: apps/v1 Deployment`) and
gives a field's type and description, and a field the kind lacks gets a
**warning** (not an error) such as `apps/v1 Deployment: undefined field
'additionalContainers'`, the API server's `status.typeChecking` wording.

The kind comes from the `apiGroups`, `apiVersions` and `resources` of the rule,
including the core group `""`; subresources such as `deployments/scale` are
ignored as the API server ignores them. Typed kinds are the built-in resources
of `k8s.io/api`. A `*` in a rule, a custom resource or any resource the engine
has no schema for, leave `object` as `dyn` with no field warning, even when
the other matched kinds are known. The schemas are those of the bundled `k8s.io/api`, not
yet of the `--kube-version` chosen.

When a policy matches several kinds, the API server type-checks it against
each. Completion after `object.` lists the fields every kind has first, then
the others, and each item names the kinds that have the field (`int ·
Deployment, StatefulSet`); hover on `object` lists the kinds and hover on a
field says which have it; a field only some kinds have gets one warning for each
kind that lacks it, such as `apps/v1 Deployment: undefined field 'serviceName'`.

## Kyverno policies

The engine checks the CEL of a Kyverno `ValidatingPolicy`
(`policies.kyverno.io/v1`): `spec.matchConditions[].expression`,
`spec.variables[].expression`, `spec.validations[].expression` and
`messageExpression`, and `spec.auditAnnotations[].valueExpression`, in plain,
quoted and block scalars. Expressions are compiled in Kyverno's environment for
ValidatingPolicy, taken from **Kyverno v1.19.1**: `object`, `oldObject`,
`request`, `namespaceObject`, `variables`, `exceptions` and the libraries
`resource`, `http`, `globalContext`, `user`, `hash`, `math`, `json`, `yaml`,
`random`, `x509`, `time`, `transform`, `gzip` and the rest of that release's
libraries, with
Kyverno's messages and rules (a `messageExpression` must be a string, for
example). A function is only checked, never called, so nothing needs a cluster
or the network. The Kubernetes `--kube-version` does not apply to a Kyverno policy.

`object` and `oldObject` are typed from `spec.matchConstraints.resourceRules` as
for a ValidatingAdmissionPolicy (see above): completion and hover offer the
fields of the matched kinds, a field a kind lacks gets a warning that names the
kind, and `object` stays `dyn` when a matched kind can't be known. Completion and
hover in a ValidatingPolicy offer Kyverno's own variables and functions
(`resource.`, `http.` and the rest, but not `authorizer` or `params`),
and `variables.<name>` completes the policy's own variables.

Not yet supported: Kyverno's other policy kinds (the kind that verifies signatures and attestations,
`MutatingPolicy`, `GeneratingPolicy`, `DeletingPolicy`, the namespaced
variants, `ClusterPolicy` and `Policy`), other apiVersions of `ValidatingPolicy`,
and a ValidatingPolicy with another `evaluation.mode` than `Kubernetes` or
`JSON` (such as `Envoy`), which is not checked.

## Command-line check

`cel-lens-core check [--format text|json] <paths...>` runs the editor's checks
on files and folders (folders are walked for `.yaml` and `.yml` files):

```sh
cel-lens-core check deploy/ crd.yaml
```

Each problem is one line, `file:line:col: error: message` (`warning` for a field warning, which does not change the exit code); `--format json`
prints an array of `{file, start, end, severity, message}` instead (with `fixes`, a list of `{fieldPath, limit, value}`, when a limit such as `maxLength` would bring a rule under its cost budget; the language server sends the same as the diagnostic's `data`). Exit code 0
means no errors, 1 that errors were found, 2 a usage or read error (a missing
path, malformed YAML, which is also printed as a problem at the line where it
breaks, as the language server shows it), 130 that the run was interrupted (Ctrl-C). Unreadable paths do not stop the others being checked.

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
- Tests sit next to the code; fixtures and golden files in `testdata/`.
