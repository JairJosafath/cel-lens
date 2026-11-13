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
| `--format text\|json` | | `json` prints an array of `{file, start, end, severity, message}` instead of lines; the default is text |
| `--experimental name,name` | `experimental` | Turns on experimental features (below) |
| | `inlineInfo` | The line above each CEL expression (code lenses): its subject, whether it compiles, and `Give it a type` for a `dyn` value; `false` turns it off; the default is `true` |
| | `exampleResults` | What a CRD's rules and a ValidatingAdmissionPolicy's validations, match conditions and audit annotations do to the workspace's examples, above each, in its hover and as warnings, and the schema's problems on the examples; `false` turns it off and evaluates nothing; the default is `true` |

Exit code 0 means no errors (warnings do not count), 1 that errors were found,
2 a usage or read error, 130 that the run was interrupted. Results go to
stdout, diagnostics about the run to stderr. The language server takes its
settings in `initializationOptions` and in the `settings` of
`workspace/didChangeConfiguration`; a change of `kubernetesVersion` or
`experimental` re-checks every open document, one of `inlineInfo` shows or
hides the lines, and one of `exampleResults` what the examples show: at once in a client that refreshes code lenses when asked
(`workspace/codeLens/refresh`, as VS Code does), else when it next asks for them.

### Run the examples in CI

`cel-lens-core examples [flags] [paths...]` runs every rule of the
CustomResourceDefinitions and ValidatingAdmissionPolicies in the files and
folders given (the current folder when there are none) on the examples it finds,
and exits 1 when a verdict is not what its example expects, so a repository's
examples are a test. The results are those the editor shows above each rule
(the `exampleResults` setting; [docs/engine.md](docs/engine.md) says what an
example is and how it says what it expects): the API server's own validation
for the Kubernetes version picked, over the workspace the paths make up. A
folder is walked as the language server walks its workspace, with the same
limits; a file given alone is the whole workspace, but for the folders given.

```sh
$ cel-lens-core examples ./config
config/crd/widget.yaml:22: self.size <= 10  (CustomResourceDefinition widgets.example.com v1 spec)
  wrong config/samples/huge.yaml:1: refused, expected to be allowed: spec: Invalid value: at most 10
config/policy/replicas.yaml:9: object.spec.replicas <= 3  (ValidatingAdmissionPolicy replicas, validation)
  wrong config/samples/deploy.yaml:6: refused under binding "replicas", expected to be allowed: at most 3 replicas
3 rules, 5 examples: 2 wrong, 3 not run
$ echo $?
1
```

The text lists each rule with a wrong verdict, or a rule that ended in an error,
and the examples it is wrong for, with the API server's message; paths are
relative to the working folder, and the last line says what the run found. When
all is well that line is all it prints. The flags are:

- `--format text|json`: `json` prints one object, the answer of
  `cel-lens.exampleResults` for every file with rules (`kubernetesVersion` and
  `rules`) and the `problems` of the examples; the default is text.
- `--kube-version 1.34` (or `--kubernetes-version 1.34`): the Kubernetes version
  the results match, as for `check`; the default is 1.37.
- `--fail-on wrong|error|none`: what fails the run. `wrong`, the default, is a
  verdict that is not what its example expects, and a problem of an example (the
  schema refuses an example that expects to be allowed, a `# cel-lens:` comment
  that says nothing it knows, an example that expects to be refused and that
  nothing refuses, a CRD, policy or binding the API server cannot read), and a rule that stopped with an error on an example that
  expects to be allowed, which the API server refuses as it refuses a verdict;
  `error` also a rule that stopped with an error on an example that expects to
  be refused; `none` never. A rule that did not run never fails the run.
- `--verbose`: list every result, not only the wrong ones and the errors.

Exit code 0 means nothing failed under `--fail-on`, 1 that a verdict did, 2 a
usage or read error (a missing path, a file that is not `.yaml` or `.yml`), 130
that the run was interrupted. Results go to stdout. The command runs every
example it finds, but it does not read files over 1 MiB or past the first
20000 YAML files: it says so on stderr, and the last line says how many files
were left out (`3 rules, 5 examples: 0 wrong · 1 file left out at the limits`),
and the exit code is not changed by it. The command is new in v0.31.0; in GitHub
Actions, with Go on the runner:

```yaml
- name: Run the examples
  run: |
    go install github.com/JairJosafath/cel-lens/engine@v0.31.0
    mv "$(go env GOPATH)/bin/engine" "$(go env GOPATH)/bin/cel-lens-core"
    "$(go env GOPATH)/bin/cel-lens-core" examples ./config
```

## Supported Kubernetes versions

1.33 to 1.37; the default is **1.37**. An unsupported value fails with exit
code 2 and lists the supported versions. A Kyverno policy ignores the setting.
Details are in [docs/engine.md](docs/engine.md).

## Stable and experimental features

Every feature is stable or experimental. A stable feature is always on. An
experimental feature is off until you list its name in `--experimental` or in
the `experimental` setting. A name that is not an experimental feature is
ignored, with one warning that names the known ones. The warning goes to the
command's stderr and to the language server's log (`window/logMessage`), which
VS Code shows in the cel-lens output channel; it is not a popup.

Stable:

| Feature | What it does |
|---|---|
| Checks of CEL | Compiles the CEL of `x-kubernetes-validations`, ValidatingAdmissionPolicy and MutatingAdmissionPolicy in the Kubernetes CEL environment, with the API server's messages and cost rules |
| Language server | Diagnostics, hover, completion, quick fixes and inline info (a line above each CEL expression) |
| Command-line check | `cel-lens-core check` on files and folders |
| Command-line examples | `cel-lens-core examples` runs the rules of CRDs and admission policies on their examples and fails on a wrong verdict |
| Kubernetes version | Results match the version picked with `--kube-version` or `kubernetesVersion` |
| Typed `object`, `oldObject` and policy variables | Typed from the built-in kinds a policy matches |
| Typed `params` of a built-in kind | Typed from `spec.paramKind` when it names a built-in kind |
| Types from `.cellens` | The language server types the `dyn` values a `.cellens` file names, writes its entries for the editor through `workspace/applyEdit`, and guides its editing with completion, hover and problems as it is typed ([docs/cellens.md](docs/cellens.md)) |

Experimental:

| Name | What it does | Why it is experimental |
|---|---|---|
| `kyverno` | Checks, hover and completion for Kyverno `ValidatingPolicy` (`policies.kyverno.io/v1`). Off, these documents are parsed but get no diagnostic, hover or completion | Compiled with Kyverno's compiler but never run against Kyverno's engine; `namespaceObject` is still `dyn` in hover; only `ValidatingPolicy` is read |
| `crd-typed-policies` | Types `params` of a custom resource, and `object` and `oldObject` of a policy that matches one, from a CustomResourceDefinition found in the workspace. Off, they are `dyn` and hover says the feature is off | A CRD in a neighbouring `---` document of the same file still types it (`docs/type-sources.md`). The workspace scan runs only while the feature is on: at `initialize`, or when `didChangeConfiguration` turns it on, and it runs in the background, so the session stays responsive; the open documents are checked again when it ends. The scan reads at most 20000 YAML files and skips files over 1 MiB; if it stops at the file cap the client gets one warning (`window/showMessage`), and one file skipped for its size is named in its own `window/logMessage` line, while many are reported in one summary line with their count, the limit and the first three names |
| `fitting-completion` | After a dot completion offers only what the receiver has, before a dot only the names of this kind of document, and hover says why a value is `dyn`. Off, completion and hover behave as in v0.17.1 | Built after v0.17.1 and not yet proven on real use |

How the types of `object`, `params` and the policy variables are found is in
[docs/type-sources.md](docs/type-sources.md), which also reports how many
expressions of real policies still get `dyn`, and why.
Where a value stays `dyn`, a `.cellens` file can say what it looks like, as a
CEL type, a Kubernetes kind, an OpenAPI schema written as a CRD's, or a Go type
of the repository ([docs/cellens.md](docs/cellens.md)); the language server then
checks, completes and explains it with that type, and what only that type shows
is a warning that names the `.cellens` line.

## Other editors

`cel-lens-core lsp` serves the Language Server Protocol over stdio, for any
editor with a language client: diagnostics, hover, completion, quick fixes and
inline info (code lenses) for the CEL in Kubernetes YAML. It takes `--stdio` (the default and only transport)
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
- `.cellens` types in `cel-lens-core check`: only the language server reads them.
- Dry runs of a MutatingAdmissionPolicy or a Kyverno policy: the language
  server and `cel-lens-core examples` run what a CRD's rules and a
  ValidatingAdmissionPolicy do to the workspace's examples, and no other kind.

## Contributing

To build, test and send a change, read [CONTRIBUTING.md](CONTRIBUTING.md):
`make build` builds, `make check` runs every check and fails on any warning.
Report a vulnerability as [SECURITY.md](SECURITY.md) says, never in a public
issue. Report bugs and ask questions at
https://github.com/JairJosafath/cel-lens/issues. You need Go from
[go.dev](https://go.dev/dl/). The engine is licensed under Apache-2.0; see
[LICENSE](LICENSE). The source lives under `engine/` of
https://github.com/JairJosafath/cel-lens.
