# cel-lens

cel-lens checks the CEL expressions in Kubernetes YAML as the API server does, and shows
the results in your editor or terminal. It is a Go engine (a language server and a
command-line check) and a VS Code extension that runs it.

## What's in this repository

- [`engine/`](engine/README.md): the engine, `cel-lens-core`. It serves as a language
  server (`cel-lens-core lsp`) for any editor, and as a command-line check
  (`cel-lens-core check`) for terminals and CI.
- [`vscode/`](vscode/README.md): the VS Code extension. It carries the engine for your
  platform, so there is nothing else to install.

## Install the VS Code extension

Download the `.vsix` for your platform from
[Releases](https://github.com/JairJosafath/cel-lens/releases) (the `vscode-v...` release):
`cel-lens-vscode-<platform>-<version>.vsix`, where `<platform>` is `linux-x64`,
`linux-arm64`, `darwin-x64`, `darwin-arm64`, `win32-x64` or `win32-arm64`. Then run

```sh
code --install-extension cel-lens-vscode-linux-x64-0.14.0.vsix
```

or **Extensions: Install from VSIX...** in the Command Palette.

## Get the engine

With Go 1.26 or newer:

```sh
go install github.com/JairJosafath/cel-lens/engine@v0.14.0
```

`go install` names the binary after the last part of the path, so this gives `engine`
(`engine.exe` on Windows) in `$(go env GOPATH)/bin`; rename it to `cel-lens-core`.
Or download the binary for your platform from the `core-v...` release on
[Releases](https://github.com/JairJosafath/cel-lens/releases):
`cel-lens-core_<version>_<os>_<arch>` (`.exe` on Windows), with `checksums.txt` to check
it (`sha256sum -c checksums.txt`).

## What works today

- CEL in CustomResourceDefinition `x-kubernetes-validations` rules, in
  ValidatingAdmissionPolicy, and in Kyverno `ValidatingPolicy` (`policies.kyverno.io/v1`).
- Diagnostics built on cel-go and the Kubernetes CEL libraries, with the API server's
  messages, for Kubernetes 1.33 to 1.37 (1.37 by default).
- Hover on a variable, field or function (its type and a short description), and
  completion of `self`, `object`, `request` and the rest, the fields of the type before a
  dot, and the Kubernetes CEL functions.
- Quick fixes for a misspelled name, and for a rule over its cost budget (add a
  `maxLength`, `maxItems` or `maxProperties`).
- In VS Code, colors for the parts of each CEL expression, also in MutatingAdmissionPolicy.

Details and limits are in [engine/README.md](engine/README.md) and
[vscode/README.md](vscode/README.md).

## Bugs and ideas

Open an issue at <https://github.com/JairJosafath/cel-lens/issues>.

## License

Apache-2.0; see [LICENSE](LICENSE).
