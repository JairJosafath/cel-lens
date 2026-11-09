# Contributing

Bug reports and pull requests are welcome. Report bugs and ask for features in the [issues](https://github.com/JairJosafath/cel-lens/issues).

How to build, test and change each part:

- [engine/CONTRIBUTING.md](engine/CONTRIBUTING.md): the engine and command line, in Go.
- [vscode/CONTRIBUTING.md](vscode/CONTRIBUTING.md): the VS Code extension, in TypeScript.

By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Dependency updates

Dependabot opens a pull request here every week when a dependency of the engine, the extension or the workflows has a newer version. Such a pull request is not merged on GitHub: this repository is published from the maintainers' source, where every change is reviewed. A maintainer makes the same bump there, the next published release carries it, and Dependabot then closes its pull request by itself. A security update is released as soon as its checks pass.
