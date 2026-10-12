# Contributing to cel-lens for VS Code

cel-lens for VS Code is the VS Code extension of [cel-lens](https://github.com/JairJosafath/cel-lens). It shows CEL errors, hover, completion and quick fixes inside Kubernetes YAML. The extension does not check CEL itself: it starts the cel-lens engine (`cel-lens-core`, a language server written with cel-go and the Kubernetes CEL libraries) as a child process, talks to it over stdio with `vscode-languageclient`, and shows what the engine answers. The engine is a pinned release, listed in `core-version.json`.

You need Node 24, npm and make. Nothing else.

## Set up

```sh
git clone https://github.com/JairJosafath/cel-lens.git
cd cel-lens/vscode
npm ci            # install the exact packages in package-lock.json
npm run engine    # fetch the pinned engine for this machine into bin/
```

`npm run engine` downloads the engine from <https://github.com/JairJosafath/cel-lens/releases> (the `core-v...` release named in `core-version.json`), checks its SHA-256, and does nothing when `bin/` already holds it.

## Build

```sh
npm run build     # bundle src/ into out/extension.js
npm run watch     # the same, again after every save
```

## Test

```sh
make check
```

`make check` is what CI runs, and it must pass before every commit. It installs the packages in `package-lock.json`, then runs Prettier (check), ESLint, `tsc`, the unit tests, the end-to-end test (the real engine behind the extension's start command; no VS Code and no display) and a packaging test. `make fmt` formats; `make help` lists every target.

While you work, `npx vitest` re-runs the unit tests. Put logic in modules that do not import `vscode`, and keep tests next to the code (`foo.test.ts`).

## Package a .vsix

```sh
make dist         # dist/cel-lens-vscode-<platform>-<version>.vsix, one per platform
```

`make dist` needs network access to the releases page above. To install one, run **Extensions: Install from VSIX...** in VS Code and pick the file for your platform.

## Run from source

```sh
npm run build
code --extensionDevelopmentPath="$PWD" path/to/a/kubernetes/file.yaml
```

This opens a VS Code window with the extension loaded from `out/`; rebuild and run **Developer: Reload Window** to see a change. To try another engine build, set `cel-lens.serverPath`. What needs a real editor (colors, status bar, quick fixes) is for you to try there.

## Send a change

One concern per pull request. Write a failing test first, make it pass, and describe the change under "Unreleased" in `CHANGELOG.md` when users can see it. Use Conventional Commit messages (`feat:`, `fix:`, `test:`, `refactor:`, `docs:`).

## Report a bug

Bugs go to <https://github.com/JairJosafath/cel-lens/issues>. Say which Kubernetes version and extension version you use, and include the CEL expression and what the editor and your cluster each said.
