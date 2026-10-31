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

`make check` is what CI runs, and it must pass before every commit. It fails on any warning: ESLint runs with `--max-warnings 0`, and the packaging check fails on a `vsce` warning. It installs the packages in `package-lock.json`, then runs Prettier (check), ESLint, `tsc`, the unit tests, the end-to-end test (the real engine behind the extension's start command; no VS Code and no display) and a packaging test. `make fmt` formats; `make help` lists every target.

While you work, `npx vitest` re-runs the unit tests. Put logic in modules that do not import `vscode`, and keep tests next to the code (`foo.test.ts`). `AGENTS.md` and `docs/style/` hold the rules in full.

### Check the layout test against a real engine

Before `make check`, export `CEL_LENS_ENGINE_DIR` to a `cel-lens-core` checkout at the tag named in `core-version.json`:

```sh
git clone --branch "$(node -p "require('./core-version.json').version")" <url of the cel-lens-core repository> ../cel-lens-core
export CEL_LENS_ENGINE_DIR="$PWD/../cel-lens-core"
make check
```

The public-repo layout test (`src/public_repo.test.ts`) builds `engine/` from that checkout, so a link to a file the engine lacks fails on your machine. Without the variable, `engine/` is an empty tree, the test passes on such a link, and only CI reports it.

Some tests read the maintainers' CI workflow files, which are not part of this published source. They skip, with a message saying so, when those files are missing, so `npm ci && npm test` is green in a clone; `make check` runs them where the files exist. `src/public_layout.test.ts` proves it by running `vitest run` in a copy of the tree without those files.

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

## Retake the pictures

```sh
npm run pictures
```

The pictures in the README (`images/*.png` and the two GIFs) are taken by a script, so a release can retake them. `npm run pictures` builds the extension, puts the pinned engine in `bin/` (`npm run engine`), packages the extension and installs it in [openvscode-server](https://github.com/gitpod-io/openvscode-server), a VS Code served to a browser, opens the two fixtures of `test/pictures/` (a CRD with `x-kubernetes-validations` and a ValidatingAdmissionPolicy) in headless Chromium driven by Playwright, and writes `problems.png`, `hover.png`, `completion.png`, `quick-fix.png`, `colors.png`, `colors.gif` and `completion.gif` into `images/`. It needs no display.

It is not part of `make check`, and it needs a network: it downloads openvscode-server (version in `package.json`, under `pictures`) into `node_modules/.cache/` the first time, and Chromium needs `npx playwright install chromium` once (on Linux, `npx playwright install-deps chromium` adds the libraries it needs). Commit the files it wrote; `src/picture_files.test.ts` checks that each is a real PNG or GIF and that each GIF has more than one frame and is under 2 MB. A GIF's clock is the script's pacing, not the wall clock: it plays the sum of the script's holds (`holdFrame`), so it lasts the same on a slow machine as on a fast one. `colors.gif` is planned to play 4.5 seconds and `completion.gif` 3 seconds; the script stops with an error naming the GIF when one is planned to play 8 seconds or more. To change what a picture shows, edit the fixtures or `test/pictures/take.ts`. The `.vsix` carries only `images/icon.png` (`.vscodeignore`).

## Send a change

One concern per pull request. Write a failing test first, make it pass, and describe the change under "Unreleased" in `CHANGELOG.md` when users can see it; a CHANGELOG line names no issue number. Use Conventional Commit messages (`feat:`, `fix:`, `test:`, `refactor:`, `docs:`).

## Report a bug

Bugs go to <https://github.com/JairJosafath/cel-lens/issues>. Say which Kubernetes version and extension version you use, and include the CEL expression and what the editor and your cluster each said.

## How it works

The extension activates on YAML files and starts the bundled cel-lens language
server, `cel-lens-core` v0.21.0 (`cel-lens-core lsp`), which gives CEL errors, hover and completion inside
Kubernetes YAML such as CRDs and admission policies. The status bar shows the
Kubernetes version the server reports; the engine lists the versions it supports, so Pick
Kubernetes Version offers them; or type a version in the
`cel-lens.kubernetesVersion` setting, and the status bar shows the version the
server then reports, which stays the default if it refuses yours. If the engine
fails to start, the extension says so once, with the error. A binary from
`cel-lens.serverPath` that fails gets an **Open Engine Setting** button; the bundled
engine that fails gets a **Show Logs** button and the advice to reinstall the
extension.

It covers YAML files on disk; untitled YAML buffers are not analysed until saved.

### What you see

- **Errors:** a red squiggle under the part of the rule that is wrong.
- **Hover:** a field in a rule shows its type and description.
- **Completion:** typing `self.` offers the fields of the schema.

### Kubernetes version

On YAML files a status bar item such as `CEL · K8s 1.37` shows the Kubernetes
version the results match. Click it (or run **cel-lens: Show Actions**) to open a
menu of the main actions: **Color style** (shows the current style), **Kubernetes
version** (shows the current version), **Restart Engine** and **Show Logs**.
Most people leave the version alone. If your cluster runs
another version, choose **Kubernetes version** in the menu (or run **cel-lens: Pick Kubernetes Version**); the choice is saved in the workspace setting
`cel-lens.kubernetesVersion` and takes effect at once (the extension restarts the engine for you).

### Color styles

cel-lens colors the parts of each CEL expression so a rule stands apart from a plain
`message:`. It colors the `rule` and `messageExpression` of `x-kubernetes-validations`,
and the CEL fields of admission policies (`admissionregistration.k8s.io`):
`spec.validations[].expression`, `spec.validations[].messageExpression`,
`spec.matchConditions[].expression`, `spec.variables[].expression` and
`spec.auditAnnotations[].valueExpression` of a ValidatingAdmissionPolicy, and
`spec.matchConditions[].expression`, `spec.variables[].expression`,
`spec.mutations[].applyConfiguration.expression` and `spec.mutations[].jsonPatch.expression`
of a MutatingAdmissionPolicy. It also colors the same five fields of a Kyverno
ValidatingPolicy (`policies.kyverno.io/v1`), but only when `kyverno` is in the
`cel-lens.experimental` setting: `spec.validations[].expression`,
`spec.validations[].messageExpression`, `spec.matchConditions[].expression`,
`spec.variables[].expression` and `spec.auditAnnotations[].valueExpression`. Kyverno's
other kinds and other versions are not colored, and neither is a field of the same name
in any other kind of resource. Pick a
style with the `cel-lens.colorStyle` setting or the **cel-lens: Choose Color Style**
command (`cel-lens.chooseColorStyle`), which lists the three styles, marks the
current one and saves your choice to your user settings. So the pick takes effect, it
also writes the workspace setting when the workspace sets `cel-lens.colorStyle`, and the
folder setting of the open file's workspace folder when that folder sets it (every
folder that sets it, when no file in a workspace folder is open). The workspace's
`.vscode/settings.json` (or `.code-workspace` file) and the folder's
`.vscode/settings.json` change in those cases. Open editors recolor at once.

- `cel` (the default): plum and lavender, the hues of the cel-lens logo. It is the
  default so CEL is recognisably CEL from the first file, with no setup.
- `classic`: warm browns and ambers, close to how a theme colors a YAML string.
- `off`: no cel-lens colors; the theme alone colors the YAML.

The colors follow the theme kind, so they read in light, dark and high contrast themes:

**cel**

| Part        | Light     | Dark      | High contrast dark | High contrast light |
| ----------- | --------- | --------- | ------------------ | ------------------- |
| variable    | `#7E2A3E` | `#E5B1BD` | `#EBC2CB`          | `#60202F`           |
| field       | `#4F3EA5` | `#B8B0E1` | `#C5BEE6`          | `#3C2F7C`           |
| function    | `#9D274F` | `#E495AF` | `#E9A7BD`          | `#781E3C`           |
| keyword     | `#AD1F3B` | `#EB8C9F` | `#EEA0AF`          | `#84172D`           |
| number      | `#6839D5` | `#AF96E9` | `#BCA7EC`          | `#4E25AF`           |
| string      | `#924B55` | `#C69097` | `#CFA2A8`          | `#6F3940`           |
| operator    | `#635EA8` | `#9390C3` | `#A3A1CC`          | `#4B4785`           |
| punctuation | `#756BA6` | `#8B83B4` | `#9B94BF`          | `#584F84`           |

**classic**

| Part        | Light     | Dark      | High contrast dark | High contrast light |
| ----------- | --------- | --------- | ------------------ | ------------------- |
| variable    | `#703B1C` | `#E6B599` | `#EBC4AD`          | `#542C15`           |
| field       | `#674922` | `#D5B081` | `#DDBF98`          | `#4D361A`           |
| function    | `#804817` | `#E39C5F` | `#E8AE7B`          | `#623711`           |
| keyword     | `#765419` | `#D59C39` | `#DCAD5B`          | `#583F13`           |
| number      | `#75560D` | `#D39B17` | `#E6A819`          | `#5A420A`           |
| string      | `#7C5C3C` | `#BA9570` | `#C5A687`          | `#5F462E`           |
| operator    | `#98563A` | `#C7876B` | `#CF9981`          | `#73412B`           |
| punctuation | `#8C6C54` | `#A48268` | `#B1937C`          | `#69513F`           |

### Commands

- **cel-lens: Show Actions** (`cel-lens.showActions`) opens the status bar menu.
- **cel-lens: Show Logs** (`cel-lens.showLogs`) opens the cel-lens output channel.
- **cel-lens: Pick Kubernetes Version** (`cel-lens.pickKubernetesVersion`) picks
  the Kubernetes version from the versions the engine supports.
- **cel-lens: Choose Color Style** (`cel-lens.chooseColorStyle`) picks the color
  style of CEL: CEL, Classic or Off.
- **cel-lens: Restart Engine** (`cel-lens.restartServer`) restarts the engine.
  Use it after changing `cel-lens.serverPath`.

## Develop

Use a machine with Node 24 and the Go toolchain.

```sh
npm ci               # install the exact tools in package-lock.json
npm run build        # bundle src/ into out/extension.js
npm test             # unit tests with vitest; no VS Code needed
make check           # what CI runs: format, lint, type check, test, e2e, package
npm run e2e          # the engine in bin/ over stdio, started as the extension starts it (fetches it first if bin/ lacks it; no VS Code, no display)
npm run engine       # put the pinned engine for this machine in bin/
make dist            # dist/cel-lens-vscode-<platform>-<version>.vsix, six platforms
make help            # every target
npm run profile -- 5000   # CPU-profile tokenizeCel at one rule count, in its own process
npm run bench        # time one color repaint (10000-rule CRD, 50 visible lines) and the tokenizer (5000 rules)
```

### Bench

`npm run bench` builds every `src/*.bench.ts` and prints the mean time per run. `color_painter.bench.ts` generates a
CRD with 10000 rules, shows 50 lines of it, and times one `ColorPainter` repaint (`planColors` plus the decoration step; budget 100 ms) against the
stand-in in `test/vscode.ts`, so VS Code's own cost of drawing decorations is not counted. Use it to catch
regressions against the 300 ms diagnostics budget; it is not part of `make check`.

Current numbers, on the shared sandbox (Node 24, Linux, 8 cores at a load average of 19 to 23, so noisy; four runs, the mean per run
that `npm run bench` prints):

| Bench                              | Mean per run                      |
| ---------------------------------- | --------------------------------- |
| `ColorPainter` repaint, 5000 rules | 260 ms, 252 ms, 139 ms and 126 ms |
| `tokenizeCel`, 5000 rules          | 80 ms, 92 ms, 82 ms and 58 ms     |

Before the change the same sandbox read 548 ms and 565 ms for the repaint and 135 ms and 280 ms for `tokenizeCel`. The
budgets are 100 ms for `tokenizeCel` and 300 ms for a repaint. A single run under heavy load can still pass them: one run in
a separate batch read 246 ms for `tokenizeCel`, so rerun when the machine is busy. Issue #73 cited about 95 ms for the repaint
on a faster machine; run the bench on your own machine before comparing.

## Releases

`core-version.json` pins the `cel-lens-core` release and the SHA-256 of each of its
six binaries. `make dist` checks all six against the release's `checksums.txt`,
downloads the binaries from the release on GitHub
(`https://github.com/JairJosafath/cel-lens/releases/download/core-v<version>/`), fails on a checksum mismatch, and writes one `.vsix` per platform (linux-x64, linux-arm64,
darwin-x64, darwin-arm64, win32-x64, win32-arm64) into `dist/`, each holding only
its own `bin/cel-lens-core` (`.exe` on Windows). The release workflow attaches
`dist/` to the release of a `v...` tag. To bump the engine, change the
version, names and checksums in `core-version.json` together. A contributor needs no
other access: the default engine source is the public releases page. To fetch from
another releases page (a mirror, say), set `CEL_LENS_CORE_RELEASES_URL` to its
URL; files are then read from `<URL>/download/<version>/` (`src/core_releases.ts`).

To run the stricter layout check locally, set `CEL_LENS_ENGINE_DIR` to a checkout of the
engine's source (the `cel-lens-core` repository at the pinned version). The layout test in
`src/public_repo.test.ts` then builds `engine/` from that checkout, so a link to a file the
engine lacks fails. Without it, `engine/` is an empty tree and those links are not checked.

To release, follow these steps in order:

1. Bump `version` in `package.json` (and `package-lock.json`), and the engine in `core-version.json` if it changes.
2. Move the "Unreleased" entries of `CHANGELOG.md` under a new heading for the version and date, add the version's link line at the end in the `vscode-v` form (`[X.Y.Z]: https://github.com/JairJosafath/cel-lens/compare/vscode-v<previous>...vscode-vX.Y.Z`; versions older than the oldest public tag, `vscode-v0.14.0`, have no link line), and move the `[Unreleased]` link to compare `vscode-vX.Y.Z` to `HEAD`.
3. Retake the pictures so the store page matches the release: run `npm run pictures` (see "Retake the pictures" above) and commit the changed files in `images/`.
4. Run `make check`; it must pass before you tag, and never release a red commit.
5. Tag the commit `vX.Y.Z` (the `version` in `package.json` with a `v` in front; the release workflow refuses any other tag) and push the tag; the release workflow attaches `dist/` to the release.

The **Open Releases Page** button, shown when the engine is too old, opens
<https://github.com/JairJosafath/cel-lens/releases> (`RELEASES_PAGE_URL`). Engine
releases are tagged `core-vX.Y.Z` there, the extension's `vscode-vX.Y.Z`.

## Layout

- `src/extension.ts` activates the extension: it registers commands and
  providers and puts every registration in `context.subscriptions`.
- `src/*.ts` are the extension's modules. Keep logic in modules that don't import
  `vscode`, so plain unit tests cover it; tests sit next to them as `*.test.ts`.
- `test/vscode.ts` stands in for the `vscode` module in unit tests
  (`vitest.config.mts` points `vscode` at it).
- `test/e2e/` is the end-to-end test: `engine.test.ts` takes the engine from `bin/` (failing with "npm run engine" when it is missing), starts it
  with the extension's own command, and as a language client checks a diagnostic, hover,
  completion and broken YAML. The stand-in unit tests do not replace it.
- `test/pictures/` holds the fixtures and `take.ts`, the script behind `npm run pictures`.
- `esbuild.mjs` bundles into `out/`; `.vscodeignore` keeps everything else out of
  the `.vsix`.

Read `AGENTS.md` before changing anything: it is the rulebook, for people and agents.
