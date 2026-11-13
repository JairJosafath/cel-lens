# Contributing

Bug reports and pull requests are welcome. Report bugs and ask for features in the [issues](https://github.com/JairJosafath/cel-lens/issues).

How to build, test and change each part:

- [engine/CONTRIBUTING.md](engine/CONTRIBUTING.md): the engine and command line, in Go.
- [vscode/CONTRIBUTING.md](vscode/CONTRIBUTING.md): the VS Code extension, in TypeScript.

By taking part you agree to the [Code of Conduct](CODE_OF_CONDUCT.md).

## Dependency updates

Dependabot opens a pull request here every week when a dependency of the engine, the extension or the workflows has a newer version. Such a pull request is not merged on GitHub: this repository is published from the maintainers' source, where every change is reviewed. A maintainer makes the same bump there, the next published release carries it, and Dependabot then closes its pull request by itself. A security update is released as soon as its checks pass.

## Releases

A release of the engine or of the extension is made by the [release.yml](.github/workflows/release.yml) workflow, from the source on `main`:

1. Prepare the version in a change of its own: the heading `## [X.Y.Z]` with its notes in the part's `CHANGELOG.md`, and for the extension the `version` in `vscode/package.json` too.
2. Right after that change is merged, run the workflow by hand from `main` (**Actions**, **Release**, **Run workflow**) with the part (`engine` or `vscode`) and the version. It always releases the head of `main`: if other changes landed after yours, prepare the release again, or release them too.

The workflow does the rest: it builds the files with the part's `make dist`, creates the tags (`engine/vX.Y.Z` and `core-vX.Y.Z`, or `vscode/vX.Y.Z` and `vscode-vX.Y.Z`), publishes the release with the files, their build provenance and the version's changelog section as its notes, and has the files signed. [Verifying a release](SECURITY.md#verifying-a-release) says how to check a file. When a run fails, re-run its failed jobs, or run the workflow again with the same inputs: a new run finishes the release when the tags exist and are both at the commit it releases, and the release is not published. A tag at another commit, only one of the two tags, or a published release stops it, with what to do.
