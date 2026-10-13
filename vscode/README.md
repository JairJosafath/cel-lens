# cel-lens for VS Code

Shows CEL errors, hover, completion, quick fixes and colors inside Kubernetes YAML.

## What it looks like

![Problems: a red squiggle under the part of a CEL rule that is wrong, with the reason](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/problems.png)

![Hover: the type and description of a field in a CEL rule](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/hover.png)

![Completion: the fields of the schema offered after typing self.](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/completion.png)

![Quick fix: a fix offered on a problem in a CEL rule](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/quick-fix.png)

![Colors: the parts of a CEL expression in different colors](https://raw.githubusercontent.com/JairJosafath/cel-lens/main/vscode/images/colors.png)

## Install

Download the `.vsix` for your platform from <https://github.com/JairJosafath/cel-lens/releases>, then run
**Extensions: Install from VSIX...** in the Command Palette, or `code --install-extension <file>.vsix`. The
extension isn't on the VS Code Marketplace or Open VSX yet.

The extension carries the cel-lens engine for your platform, so there is nothing else to install or set up.

## Settings

| Setting                      | Default | What it does                                                                                                                                                                                                                               |
| ---------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `cel-lens.colorStyle`        | `cel`   | `cel`, `classic` or `off`: the color style of the parts of a CEL expression. See Color styles. Resource scope: user, workspace or workspace folder.                                                                                        |
| `cel-lens.kubernetesVersion` | `""`    | Advanced: the Kubernetes version the results match, such as `1.36`. Empty means the engine's default. The status bar shows the version the engine uses; one it refuses is logged and the engine keeps its default. May restart the engine. |
| `cel-lens.trace.server`      | `off`   | `off`, `messages` or `verbose`: traces the protocol in the **cel-lens** output channel.                                                                                                                                                    |
| `cel-lens.serverPath`        | `""`    | Advanced: a `cel-lens-core` binary to use instead of the bundled engine. Empty (the default) uses the bundled one. Machine scope: in a remote window (WSL, SSH, a dev container) set it on the Remote tab of Settings.                     |

## Supported Kubernetes versions

The bundled engine matches the API server's CEL environment of these Kubernetes versions:
`1.33`, `1.34`, `1.35`, `1.36` and `1.37`. The default is `1.37`. See Kubernetes version below to pick another.

## Not supported yet

- Dry runs of CEL against sample objects.
- Template tests for CEL.
- Helm templates: YAML with template syntax is not analysed.
- Kubebuilder markers in Go files (`+kubebuilder:validation:XValidation`): only YAML files are covered.
- Untitled YAML buffers: they are not analysed until saved.

## How it works

The extension activates on YAML files and starts the bundled cel-lens language
server, `cel-lens-core` v0.15.0 (`cel-lens-core lsp`), which gives CEL errors, hover and completion inside
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
ValidatingPolicy (`policies.kyverno.io/v1`): `spec.validations[].expression`,
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

You need Node 24, npm and make; [CONTRIBUTING.md](CONTRIBUTING.md) has the details.

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
another releases page, set `CEL_LENS_CORE_RELEASES_URL` to its
URL; files are then read from `<URL>/download/<version>/` (`src/core_releases.ts`).

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
- `esbuild.mjs` bundles into `out/`; `.vscodeignore` keeps everything else out of
  the `.vsix`.

## License

cel-lens for VS Code is licensed under Apache-2.0. The full text is in `LICENSE`, which the `.vsix` also carries.

## Contributing

Bug reports and pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) shows how to build, test and run the extension from source. Report bugs at <https://github.com/JairJosafath/cel-lens/issues>.
