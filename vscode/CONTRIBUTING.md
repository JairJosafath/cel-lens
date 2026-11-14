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

Before `make check`, export `CEL_LENS_ENGINE_DIR` to the `engine/` folder of a `cel-lens-core` checkout at the tag named in `core-version.json`. The engine lives in the public monorepo, under `engine/`, at the tag `core-v<version>`. A sparse clone fetches only that folder; run this from this repository's root:

```sh
git clone --depth 1 --filter=blob:none --sparse \
  --branch "core-$(node -p "require('./core-version.json').version")" \
  https://github.com/JairJosafath/cel-lens.git ../cel-lens-engine
git -C ../cel-lens-engine sparse-checkout set engine
export CEL_LENS_ENGINE_DIR="$PWD/../cel-lens-engine/engine"
make check
```

(A full clone works too; point the variable at its `engine/` folder.)

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

The pictures in the README (`images/*.png` and the three GIFs) are taken by a script, so a release can retake them. `npm run pictures` builds the extension, puts the pinned engine in `bin/` (`npm run engine`), packages the extension and installs it in [openvscode-server](https://github.com/gitpod-io/openvscode-server), a VS Code served to a browser, opens the fixtures of `test/pictures/` (a CRD with `x-kubernetes-validations`, a ValidatingAdmissionPolicy, and a ValidatingAdmissionPolicy whose `params` is a custom kind, with a sample of that kind that goes in the workspace as `config/samples/limit.yaml`) in headless Chromium driven by Playwright, and writes `inline-info.png`, `problems.png`, `hover.png`, `completion.png`, `quick-fix.png`, `colors.png`, `colors.gif`, `completion.gif`, `give-type.gif`, `actions-menu.png` (the status bar menu open over a CRD) and `editor-menu.png` (the editor's _cel-lens_ submenu open at a rule) into `images/`. `npm run pictures -- menus` takes only the last two. It needs no display. Each picture starts from a clean editor: the script closes every editor (discarding what is unsaved), opens the picture's file and checks one tab, nothing unsaved, the problems the picture should show in the status bar and no notification, and stops naming the picture otherwise. The browser's screencast gives PNG frames, so a GIF is encoded from exact pixels, with the flat colors of the page (`src/gif_frames.ts`) as palette entries of their own.

It is not part of `make check`, and it needs a network: it downloads openvscode-server (version in `package.json`, under `pictures`) into `node_modules/.cache/` the first time, and Chromium needs `npx playwright install chromium` once (on Linux, `npx playwright install-deps chromium` adds the libraries it needs). Commit the files it wrote; `src/picture_files.test.ts` checks that each is a real PNG or GIF and that each GIF has more than one frame and is under 2 MB. A GIF's clock is the script's pacing, not the wall clock: it plays the sum of the script's holds (`holdFrame`), so it lasts the same on a slow machine as on a fast one. `colors.gif` is planned to play 4.5 seconds, `completion.gif` 3 seconds and `give-type.gif` 6 seconds; the script stops with an error naming the GIF when one is planned to play 8 seconds or more. To change what a picture shows, edit the fixtures or `test/pictures/take.ts`. The `.vsix` carries only `images/icon.png` (`.vscodeignore`).

`npm run pictures -- themes` takes six scenes (a CRD with its colors and lines, a problem with its hover, the rule view with a rule in each state, the status bar menu, the editor's _cel-lens_ submenu and the walkthrough's first step) in Default Light Modern, Default Dark Modern, Default High Contrast and Default High Contrast Light, and writes them to `out/pictures/themes/<scene>-<theme>.png`, never to `images/`. They are for a person to look at, to check that everything reads in each theme and that nothing is told by color alone; they are not committed.

## Send a change

One concern per pull request. Write a failing test first, make it pass, and describe the change under "Unreleased" in `CHANGELOG.md` when users can see it; a CHANGELOG line names no issue number. Use Conventional Commit messages (`feat:`, `fix:`, `test:`, `refactor:`, `docs:`).

## Report a bug

Bugs go to <https://github.com/JairJosafath/cel-lens/issues>. Say which Kubernetes version and extension version you use, and include the CEL expression and what the editor and your cluster each said.

## How it works

The extension activates on YAML files and starts the bundled cel-lens language
server, `cel-lens-core` v0.33.0 (`cel-lens-core lsp`), which gives CEL errors, hover and completion inside
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
- **Inline info:** a line above each rule says what `self` is, whether the rule
  compiles, and the values it reads as `dyn`. Its first part opens the rule's first
  problem in the problems peek (or the hover there, when VS Code's problem order would
  open another problem first), or the rule's hover when it has no problem; **+N more**
  lists the values the line leaves out, and picking one runs its action. The engine
  names these parts' commands, `cel-lens.showRule`, `cel-lens.listValues`,
  `cel-lens.giveType`, `cel-lens.editType` and `cel-lens.removeType`, so they are not
  in the Command Palette.
- **Example results:** above a CRD's rule with examples, a last part of the line says
  what the rule does to them (`3 examples pass`, or the wrong verdicts first). The engine
  names its command, `cel-lens.showExampleResults`, and gives it the rule's location, so
  it is not in the Command Palette: it asks the engine's `cel-lens.exampleResults` for
  that rule and opens a picker of each example's verdict, the wrong ones first, each with
  its file and line and the API server's message. The picker opens at once, saying the
  results are being computed, and Escape cancels the engine's work; picking an example
  opens it at its line beside the rule. With `cel-lens.exampleResults` off for the file,
  the command says so and has a button that turns it on. The engine has one switch for the
  window, on while any workspace folder has the setting on; the client hides the part in a
  folder that turns it off, as it hides the whole line for `cel-lens.inlineInfo`.
- **Types for `dyn` values:** **Give it a type** and **Edit** open a picker:
  **Write a schema in .cellens…**, **Use a Go type…** (a second picker of the
  Go types of the `.cellens`'s workspace folder, with a Back button) and **Write a
  short type in .cellens…**, then the kinds there are; a CRD's value gets `int` and
  `string` only. The bundled engine has all of this; an engine of `cel-lens.serverPath`
  that lists no Go types, or refuses `snippet`, is older than the extension needs, and
  the flow says so, with a button to that setting.
  **Remove** removes the entry at once. The extension asks, and the engine writes
  `.cellens`: it runs the engine's commands `cel-lens.typeChoices` (cancelled once the
  flow ends, as the Go walk may still run), `cel-lens.setType` and
  `cel-lens.removeType` with `workspace/executeCommand`, and the engine sends the edit
  back through `workspace/applyEdit`, so it lands in the editor and Undo takes it
  back. A Go type, a kind, `int` or `string` is set at once; the two lines that write
  in `.cellens` send `setType` a `snippet` instead, and the engine answers where the
  cursor goes once its edit is applied, at a new entry or at the one the value has,
  so the extension opens `.cellens` there beside the rule
  (`window.showTextDocument` with `ViewColumn.Beside`, or the group that already
  shows it). The language client would register each command the engine lists as a
  VS Code command, and the engine's `cel-lens.removeType` has the name of the line's
  part; a second registration throws, so the client registers none of them
  (`CelLensClient.registerFeature`) and the extension sends the requests itself. The
  flow is in `src/type_flow.ts` and `src/value_types.ts`, which do not import
  `vscode`, and `src/type_commands.ts` shows it.
- **Types from examples:** `cel-lens.typeChoices` gets the value's argument, so it
  also lists the documents of the workspace of its kind. A document picked, or the
  text of the editor `Paste an example…` opens, goes to `cel-lens.inferType` with
  the value (`value`, the line's argument) and its `path`: an engine that knows the
  value's kinds (`valueKinds` of `typeChoices`) takes a pasted example of another
  kind as the value itself, with a note. With them not known, or an engine older
  than `valueKinds`, a paste gets the `path` only when `apiVersion` and `kind` start
  its lines (or JSON's root), and is sent again without it when no example has a
  value there; nothing in the extension parses YAML. Its schema is set with
  `cel-lens.setType`, and a second `setType` with a `snippet`, which the engine
  answers `unchanged`, gives the place of the entry to open. The engine's notes are
  placed at their keys of `.cellens` by `src/example_schema.ts` (no `vscode`) and
  shown as information diagnostics by `ExampleNotes` in `src/example_editor.ts`,
  where `ExampleEditors` keeps the pasted examples' editors and their code lens.
  Their files live in memory (`ExampleFiles`, a file system provider of the scheme
  `cel-lens-example`, which the `when` clauses of `cel-lens.useExample` name and
  `onFileSystem:cel-lens-example` activates on). A change of the active example is
  saved there in the same turn with **Save without Formatting**, never with
  `TextDocument.save()`, whose save participants would trim or format what is
  typed; one changed while another editor is active is saved when it is active
  again, and an editor closed with a change not yet saved is reverted, so none asks
  to save. `cel-lens.showRule` with a location in a `.cellens` (a value whose type
  an entry gives) opens it beside the rule (`src/cellens_editor.ts`, shared with
  Give it a type).

### Kubernetes version

On YAML files a status bar item such as `CEL · K8s 1.37` shows the Kubernetes
version the results match. Click it (or run **cel-lens: Show Actions**) to open a
menu of every setting and toggle and the engine's actions, each with its value for
the open file: **Kubernetes version** and **Color style**; under _In the editor_,
**Colors**, **Inline info**, **Example results** and, while `cellens-types` is on,
**Value types**, each `on` or `off`; under _Engine_, **Show Logs** and **Restart Engine**,
which come first while the engine has failed or crashed.
Most people leave the version alone. If your cluster runs
another version, choose **Kubernetes version** in the menu (or run **cel-lens: Pick Kubernetes Version**); the choice is saved in the workspace setting
`cel-lens.kubernetesVersion` (in your user settings in a window with no folder or workspace file open) and takes effect at once (the extension restarts the engine for you).

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
- **cel-lens: Toggle Colors** (`cel-lens.toggleColors`) turns the colors of CEL off,
  by writing `off` to `cel-lens.colorStyle` at the scope that sets it for the open file,
  or in your user settings, and back to the style it turned off (CEL when it turned none).
- **cel-lens: Toggle Inline Info** (`cel-lens.toggleInlineInfo`) turns the line
  above each CEL rule off or on, by flipping `cel-lens.inlineInfo` at the scope that
  sets it for the open file, or in your user settings.
- **cel-lens: Toggle Example Results** (`cel-lens.toggleExampleResults`) turns what each
  CRD rule does to its examples off or on (the part of the line, the hover, the warnings),
  by flipping `cel-lens.exampleResults` at the scope that sets it for the open file, or in
  your user settings.
- **cel-lens: Toggle Value Types** (`cel-lens.toggleValueTypes`) hides the values a rule
  reads from the line above it, and shows them again, by flipping `cel-lens.valueTypes`
  at the scope that sets it for the open file, or in your user settings; the command is
  in the palette while `cellens-types` is on.
- **cel-lens: Reveal in Rule View** (`cel-lens.rules.reveal`) selects the rule at the
  cursor in the view, else its document, and opens the view. The editor's **cel-lens**
  submenu (`cel-lens.editor`, in the context menu and the title's overflow) shows in
  YAML the engine lists rules for: the extension sets `cel-lens.editorHasRules` and
  `cel-lens.ruleAtCursor` from the list the view holds, with no message from the engine.
- **cel-lens: Give a Value a Type…** (`cel-lens.giveValueType`) lists the values the
  line above each rule of the open YAML file names, from its code lenses
  (`vscode.executeCodeLensProvider`), and gives the one picked a type, or edits it.
  With the line off for the file, it says so.

### Keyboard shortcut

The extension contributes one keybinding: `Ctrl+K L` (`Cmd+K L` on macOS) runs **cel-lens: Toggle Inline Info** while
you type in a YAML file. A chord after `Ctrl+K`, VS Code's own chord prefix, types no character on any layout, unlike
`Alt+...` on macOS (Option) or `Ctrl+Alt+...` on Windows (AltGr), so do not move it to one of those.

A shortcut must not take a key that VS Code or a common extension uses. `npm run keys` (`src/taken_keys.ts`; it needs the
network, so `make check` does not run it) fetches VS Code's default keybindings for Linux, Windows and macOS from
[codebling/vs-code-default-keybindings](https://github.com/codebling/vs-code-default-keybindings) and the `package.json` of
each extension in `EXTENSIONS` in `src/taken_keys.ts` (the common ones that bind keys in an editor, from GitLens to Helm
Intellisense), and writes their keys, normalised, with each source's version, to
`src/taken_keys.json`. Commit the file when you rerun it.
`src/keybindings.test.ts` fails, naming the source, the key and its command, on a contributed key that equals a taken one,
whose first chord part is a taken single key, or that is a single key starting a taken chord. Bindings that apply only with
`terminalFocus` or in a `focusedView`, or whose `when` is `false`, never compete with an editor's binding and are skipped.
The one clash the test accepts is Vim's: it takes `Ctrl+K` in the editor while `vim.use<C-k>`, as it does for every
`Ctrl+K` chord of VS Code's own; a person gives the key back to VS Code with Vim's `vim.handleKeys`.

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
npm run keys         # fetch the keys VS Code and common extensions take into src/taken_keys.json (needs the network)
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
A download that cannot connect, or whose response is cut off, is tried once more after half a second; a checksum mismatch or an HTTP error is not. A failure names the URL.

To run the stricter layout check locally, see "Check the layout test against a real engine" above for `CEL_LENS_ENGINE_DIR`.

To release, follow these steps in order:

1. Bump `version` in `package.json` (and `package-lock.json`), and the engine in `core-version.json` if it changes.
2. Move the "Unreleased" entries of `CHANGELOG.md` under a new heading for the version and date, add the version's link line at the end in the `vscode-v` form (`[X.Y.Z]: https://github.com/JairJosafath/cel-lens/compare/vscode-v<previous>...vscode-vX.Y.Z`; versions older than the oldest public tag, `vscode-v0.14.0`, have no link line), and move the `[Unreleased]` link to compare `vscode-vX.Y.Z` to `HEAD`.
3. Retake the pictures so the store page matches the release: run `npm run pictures` (see "Retake the pictures" above) and commit the changed files in `images/`.
4. Run `make check`; it must pass before you tag, and never release a red commit.
5. Run `npm run links`: it fetches every link in `CHANGELOG.md` and `README.md` and exits non-zero on any response that is not 2xx or 3xx. Fix a dead link before you tag. It needs a network, so `make check` does not run it.
6. Tag the commit `vX.Y.Z` (the `version` in `package.json` with a `v` in front; the release workflow refuses any other tag) and push the tag; the release workflow attaches `dist/` to the release.

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
  completion and broken YAML. The stand-in unit tests do not replace it. No case skips: the engine in
  `bin/` is the pinned one, and a case fails when it lacks what the extension needs. To try an engine that is not
  pinned yet, set `CEL_LENS_SERVER_PATH` to a `cel-lens-core` binary built from the engine's source (as
  `cel-lens.serverPath` would name it) to run the whole file on that engine instead of the one in `bin/`.
- `test/pictures/` holds the fixtures and `take.ts`, the script behind `npm run pictures`.
- `esbuild.mjs` bundles into `out/`; `.vscodeignore` keeps everything else out of
  the `.vsix`.

Read `AGENTS.md` before changing anything: it is the rulebook, for people and agents.
