# cel-lens engine

`cel-lens-core` finds the CEL in Kubernetes YAML files and checks it with
cel-go and the Kubernetes CEL libraries, so the results match the API server,
as a command-line check for terminals and CI and as a language server for
editors.

## What you see

`widget-crd.yaml` is a CustomResourceDefinition whose `spec` has one field,
`replicas`, and a rule that reads another:

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  scope: Namespaced
  names: {plural: widgets, singular: widget, kind: Widget, listKind: WidgetList}
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                replicas: {type: integer}
              x-kubernetes-validations:
                - rule: self.foo > 1
```

```sh
$ cel-lens-core check widget-crd.yaml
widget-crd.yaml:22:30: error: undefined field 'foo'
$ echo $?
1
```

Each problem is one line, `file:line:col: severity: message`, on the part that
is wrong. A YAML document that does not parse is reported at the line where it
breaks, never as clean.

## Install

```sh
go install github.com/JairJosafath/cel-lens/engine@latest
```

`go install` names the program after the last element of the path, so it
writes `engine` (`engine.exe` on Windows) to `$(go env GOBIN)`, or
`$(go env GOPATH)/bin`. Rename it:

```sh
mv "$(go env GOPATH)/bin/engine" "$(go env GOPATH)/bin/cel-lens-core"
```

For a pinned version, download the binary for your platform from the
[releases](https://github.com/JairJosafath/cel-lens/releases). `checksums.txt`
on the release lists the SHA-256 of every file. `cel-lens-core --version` prints
the version, the Kubernetes version it matches and the range it supports.

## Flags and settings

`cel-lens-core check [flags] <paths...>` checks files and folders (folders are
walked for `.yaml` and `.yml` files).

| Flag | Language server setting | What it does |
|---|---|---|
| `--kube-version 1.34` | `kubernetesVersion` | The Kubernetes version the results match; the default is 1.37 |
| `--format text\|json` | | `json` prints an array of `{file, start, end, severity, message}` instead of lines |
| `--experimental name,name` | `experimental` | Turns on experimental features (below) |

Exit code 0 means no errors (warnings do not count), 1 that errors were found,
2 a usage or read error, 130 that the run was interrupted. Results go to
stdout, diagnostics about the run to stderr. The language server takes its
settings in `initializationOptions` and in the `settings` of
`workspace/didChangeConfiguration`; a change re-checks every open document.

## Supported Kubernetes versions

1.33 to 1.37; the default is **1.37**. An unsupported value fails with exit
code 2 and lists the supported versions. A Kyverno policy ignores the setting.
Details are in [docs/engine.md](docs/engine.md).

## Stable and experimental features

Every feature is stable or experimental. A stable feature is always on. An
experimental feature is off until you list its name in `--experimental` or in
the `experimental` setting. A name that is not an experimental feature is
ignored, with one warning that names the known ones.

Stable:

| Feature | What it does |
|---|---|
| Checks of CEL | Compiles the CEL of `x-kubernetes-validations`, ValidatingAdmissionPolicy and MutatingAdmissionPolicy in the Kubernetes CEL environment, with the API server's messages and cost rules |
| Language server | Diagnostics, hover, completion and quick fixes |
| Command-line check | `cel-lens-core check` on files and folders |
| Kubernetes version | Results match the version picked with `--kube-version` or `kubernetesVersion` |
| Typed `object`, `oldObject` and policy variables | Typed from the built-in kinds a policy matches |
| Typed `params` of a built-in kind | Typed from `spec.paramKind` when it names a built-in kind |

Experimental:

| Name | What it does | Why it is experimental |
|---|---|---|
| `kyverno` | Checks, hover and completion for Kyverno `ValidatingPolicy` (`policies.kyverno.io/v1`). Off, these documents are parsed but get no diagnostic, hover or completion | Compiled with Kyverno's compiler but never run against Kyverno's engine; `namespaceObject` is still `dyn` in hover; only `ValidatingPolicy` is read |
| `crd-typed-policies` | Types `params` of a custom resource from a CustomResourceDefinition found in the workspace. Off, it is `dyn` and hover says the feature is off | A CRD in a neighbouring `---` document of the same file still types it (`docs/type-sources.md`), and the workspace scan runs inside `initialize` and blocks the session while it scans |
| `fitting-completion` | After a dot completion offers only what the receiver has, before a dot only the names of this kind of document, and hover says why a value is `dyn`. Off, completion and hover behave as in v0.17.1 | Built after v0.17.1 and not yet proven on real use |

How the types of `object`, `params` and the policy variables are found is in
[docs/type-sources.md](docs/type-sources.md), which also reports how many
expressions of real policies still get `dyn`, and why.

## Other editors

`cel-lens-core lsp` serves the Language Server Protocol over stdio, for any
editor with a language client: diagnostics, hover, completion and quick fixes for
the CEL in Kubernetes YAML. It takes `--stdio` (the default and only transport)
and `--clientProcessId=<pid>`; `--socket`, `--pipe` and `--node-ipc` are
refused with exit code 2. VS Code users install the cel-lens extension, which
ships the engine. See [docs/engine.md](docs/engine.md) for what the server does.

## Not supported yet

- Kyverno policy kinds other than `ValidatingPolicy`, other apiVersions of it,
  and an `evaluation.mode` other than `Kubernetes` or `JSON`.
- `object` and `oldObject` of a custom resource, and of a rule with `*`: they are
  `dyn`, with no field warning.
- Schemas for `object` that follow `--kube-version`: they are those of the
  bundled `k8s.io/api`.
- Transports other than stdio for the language server.

## Contributing

To build, test and send a change, read [CONTRIBUTING.md](CONTRIBUTING.md):
`make build` builds, `make check` runs every check and fails on any warning.
Report a vulnerability as [SECURITY.md](SECURITY.md) says, never in a public
issue. Report bugs and ask questions at
https://github.com/JairJosafath/cel-lens/issues. You need Go from
[go.dev](https://go.dev/dl/). The engine is licensed under Apache-2.0; see
[LICENSE](LICENSE). The source lives under `engine/` of
https://github.com/JairJosafath/cel-lens.
