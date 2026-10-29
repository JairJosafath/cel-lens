# cel-lens

Language support for CEL in Kubernetes YAML: errors, hover, completion and quick fixes that match the API server.

[![CI](https://github.com/JairJosafath/cel-lens/actions/workflows/ci.yml/badge.svg)](https://github.com/JairJosafath/cel-lens/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/JairJosafath/cel-lens)](https://github.com/JairJosafath/cel-lens/releases)
[![License](https://img.shields.io/github/license/JairJosafath/cel-lens)](LICENSE)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/JairJosafath/cel-lens/badge)](https://scorecard.dev/viewer/?uri=github.com/JairJosafath/cel-lens)
[![Go Reference](https://pkg.go.dev/badge/github.com/JairJosafath/cel-lens/engine.svg)](https://pkg.go.dev/github.com/JairJosafath/cel-lens/engine)

![Colors: a CEL rule colored part by part as it is typed](vscode/images/colors.gif)

![Completion: the list after typing self. and one item picked](vscode/images/completion.gif)

## Install

- **VS Code:** download the `.vsix` for your platform from the [releases](https://github.com/JairJosafath/cel-lens/releases), then run **Extensions: Install from VSIX...** or `code --install-extension <file>.vsix`. The extension carries the engine; nothing else to set up.
- **Command line:** `go install github.com/JairJosafath/cel-lens/engine@<version>` installs the engine; see [engine/](engine/) for the details.

## Features

Stable:

| Feature                           | What it does                                         |
| --------------------------------- | ---------------------------------------------------- |
| Problems                          | CEL errors on the exact part that is wrong           |
| Hover, completion and quick fixes | Types, fields and fixes for CEL in Kubernetes YAML   |
| Colors                            | The parts of a CEL expression, in the style you pick |
| Kubernetes version                | Results match the version you choose                 |

Experimental, off until you list its name in the `cel-lens.experimental` setting:

| Name | What it does                                       |
| ---- | -------------------------------------------------- |
| none | No experimental feature is offered in this release |

## Learn more

- [engine/](engine/): the CEL engine and command line.
- [vscode/](vscode/): the VS Code extension.
- [CONTRIBUTING.md](CONTRIBUTING.md): how to contribute.
- [SECURITY.md](SECURITY.md): how to report a vulnerability.
