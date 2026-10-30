# Changelog

Every user-visible change, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `version` in `package.json`.

## [Unreleased]

## [0.22.0] - 2026-10-30

### Changed

- The extension ships `cel-lens-core` v0.20.0 (#89).
- CI fetches the pinned engine source and sets `CEL_LENS_ENGINE_DIR`, so a pull request that links to a file the engine lacks fails the layout test (#65).
- The `releaseDownloadUrl` comment names no engine version, and the README says the `crd-typed-policies` scan runs only while the feature is on (#72).
- `CONTRIBUTING.md` documents `CEL_LENS_ENGINE_DIR` (#66).
- Tests: one GIF frame walker serves `gifFrameCount`, `gifDelaysMs`, `gifDisposals` and `gifColorTables` (#68).
- Tests: the public-repo Markdown link test parses Markdown with a CommonMark library instead of regexes (#79).
- The CI workflow change for #65 is merged as a Standard change (#86).

## [0.21.0] - 2026-10-30

### Changed

- `make release-version` fails when `CHANGELOG.md` has no heading for the tagged version (#63).
- Tests: `describeFailure` is tested directly, so the dual-stack download test no longer depends on the runner's network (#58).
- The public-repo Markdown link test resolves the remaining CommonMark link forms (#60).
- The Makefile change for #63 is merged as a Standard change (#80).

### Fixed

- Near-black pixels no longer vanish in later frames of the GIFs the picture run makes; the README GIFs are retaken with the next picture run (#76).

## [0.20.0] - 2026-10-30

### Changed

- The extension ships `cel-lens-core` v0.19.0 (#70).
- The public-repo layout test builds `engine/` from the checkout named by `CEL_LENS_ENGINE_DIR` when it is set, so a link to a file the engine lacks fails (#16).
- A test checks that each README GIF has one global colour table and no local tables (#51).

## [0.19.0] - 2026-10-30

### Changed

- CONTRIBUTING.md says that Kyverno coloring needs the `kyverno` experimental feature (#41).
- The public-repo Markdown link test also covers reference-style and titled links (#47).

### Fixed

- A failed engine download with no error message names the error code, such as ECONNREFUSED (#43).

## [0.18.0] - 2026-10-30

### Changed

- The public-repo Markdown link test also checks the targets of badge links (#17).
- The public layout test's copy fallback works on unusual layouts (#36).
- CONTRIBUTING.md's Releases section is a numbered checklist: version bump, changelog, pictures, `make check`, tag (#33).

### Fixed

- `npm run pictures` quantizes all frames of a GIF with one palette and compares palette colours, not raw JPEG pixels, so a kept background pixel cannot differ from a rewritten one by a few levels (#31).

## [0.17.0] - 2026-10-29

### Added

- The `cel-lens.experimental` setting: a list of the experimental features to turn on, from `kyverno`, `crd-typed-policies` and `fitting-completion`; empty by default. The extension passes it to the engine at start and when it changes, and the README's "Experimental features" section tells which feature is stable or experimental (#5).

### Changed

- `npm run engine` (the end-to-end step of `make check`) says where it fetches the engine from, and a download that fails, even with no connection, names the URL of the missing file (#27).
- The extension carries the `cel-lens-core` v0.17.2 engine, which reads the experimental list (#5).
- Kyverno `ValidatingPolicy` documents are colored only when `kyverno` is in `cel-lens.experimental` (#5).

## [0.16.0] - 2026-10-29

### Changed

- The extension carries the `cel-lens-core` v0.17.2 engine (#30).
- The release steps in `CONTRIBUTING.md` now say to run `npm run pictures` and commit `images/`, so the store page matches each release (#14).
- The public layout test keeps tooling's temp folders out of the tree it copies (#20), prints the nested run's failures (#23), and links `node_modules` when `TMPDIR` is on another device (#24).
- `colors.gif` and `completion.gif` in the README are retaken: 5.7 s and 4.3 s long, at 10 frames per second, and no completion list shows in `colors.gif`. `npm run pictures` now records with the browser's screencast, encodes after the action with frames that stay on screen (no disposal), and stops if a completion list shows while `colors.gif` records (#13).

## [0.15.2] - 2026-10-29

### Changed

- The README names the status bar menu and the Pick Kubernetes Version and Choose Color Style commands instead of linking to CONTRIBUTING.md (#9).
- The public layout test is lighter on the runner (#11).

## [0.15.1] - 2026-10-29

### Added

- `public-repo/`: the front page of JairJosafath/cel-lens (README, CONTRIBUTING, SECURITY, Code of Conduct, LICENSE, issue forms, and the CI and Scorecard workflows), kept out of the published source by `.gitattributes` and tested by `src/public_repo.test.ts` (#6).
- The pictures of the README, taken from the extension running in a browser-served VS Code: `problems.png`, `hover.png`, `completion.png`, `quick-fix.png` and `colors.png`, and two GIFs, `colors.gif` and `completion.gif` (#4).
- `npm run pictures` retakes them with openvscode-server and Playwright's Chromium, on the fixtures in `test/pictures/`; `CONTRIBUTING.md` says how (#4).
- `SECURITY.md`: report a vulnerability privately through GitHub's private vulnerability reporting; we acknowledge within 7 days and answer within 14 days (#3).

### Changed

- The README is a short store page that leads with what a user sees; the only install route it names is the `.vsix` from the GitHub releases, and it has an Experimental features section (#3).
- How it works, Develop, Releases and Layout moved from the README to `CONTRIBUTING.md`, which says `make check` fails on any warning (#3).

### Fixed

- `npm test` passes in a clone of the published source: the tests that read the maintainers' CI workflow files skip when those files are absent, and a test runs the suite in a copy without them (#2).

## [0.15.0] - 2026-10-12

### Changed

- The extension carries the `cel-lens-core` v0.15.0 engine (#380).
- `package.json` states the Node version contributors need (`engines.node`), and a test keeps it equal to the one in CONTRIBUTING.md (#332).

## [0.14.0] - 2026-10-12

### Changed

- The release workflow test fails with a clear message when its step names change (#286).

## [0.13.1] - 2026-10-12

### Changed

- The extension carries the `cel-lens-core` v0.14.0 engine (#354).
- A check fails when the lockfile's root package does not match `package.json` (#346).
- The store listing test decodes icon pixels with PNG filters (#327).
- The CI temporary-folder cleanup refuses to run against `/tmp` (#289, #357).

### Fixed

- A multi-line plain or quoted rule that starts far above the visible lines is colored on the lines you see (#299).

## [0.13.0] - 2026-10-12

### Changed

- `npm run profile` prints the median time per run, the warm-up and profiled run counts and the `out/profile/` directory (#277).

## [0.12.1] - 2026-10-10

### Changed

- The extension carries the `cel-lens-core` v0.13.1 engine (#348).
- The lockfile licence says Apache-2.0, like `package.json` (#335).
- The "Show Logs" button label lives in `button_labels.ts` with the other labels (#266).

## [0.12.0] - 2026-10-10

### Added

- Every CEL field of ValidatingAdmissionPolicy and MutatingAdmissionPolicy is colored like an `x-kubernetes-validations` rule, in the color style you picked (#317).
- Every CEL field of a Kyverno `ValidatingPolicy` (`policies.kyverno.io/v1`) is colored too, and its errors, hover and completion come from the engine (#340).

### Changed

- The extension carries the `cel-lens-core` v0.13.0 engine (#340). Its typed `object` and `oldObject` in admission policies and its Kyverno `ValidatingPolicy` checks reach the editor through the language server.

## [0.11.1] - 2026-10-10

### Added

- The store page has an icon and keywords (`cel`, `kubernetes`, `crd`, `x-kubernetes-validations`, `validatingadmissionpolicy`), and a test fails if `vsce package` prints a warning (#311).

### Changed

- The extension carries the `cel-lens-core` v0.12.1 engine (#324). Its changes (`go install` of the engine, licence and module path) need nothing from the extension.
- The "engine too old" message names github.com/JairJosafath/cel-lens/releases, and `package.json` points `repository`, `bugs` and `homepage` at the public repository (#308).
- Building the extension downloads the pinned engine from the public GitHub releases by default; `CEL_LENS_CORE_RELEASES_URL` picks another source (#308).
- The extension is licensed under Apache-2.0, and the `.vsix` carries the `LICENSE` file (#307).
- `CONTRIBUTING.md` explains how to build, test, package and run the extension from source, and the README ends with a Contributing section (#309).
- Tooling only: the check and release workflows use the public repository (#315).
- The README is now a store page: pictures, install steps, settings, the supported Kubernetes versions and what is not supported yet (#310).

## [0.11.0] - 2026-10-10

### Changed

- Tooling only, nothing changes for users: the Makefile compile-cache test no longer depends on the caller's environment (#250).

## [0.10.0] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.12.0 engine (#298). Its changes are internal (macro receiver kinds, immutable lookup tables) and need nothing from the extension.
- Tooling only, nothing else changes for users: the CHANGELOG test catches any wording of an engine bump (#263).

### Fixed

- A long block-scalar rule that starts far above the visible lines but runs into them is now colored on its visible lines (#279).

## [0.9.1] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.11.0 engine (#285, #291): bad characters in a UTF-16 YAML stream are reported on the right document, and quick fixes detect CRLF line endings in UTF-16 files. Its other changes need nothing from the extension.
- Tests and tooling only, nothing else changes for users: a test checks that the release workflow keeps `TMPDIR` out of `/tmp` and the checkout (#254), and the check and release workflows clean up the CI temporary folder after each job (#269, #283).

## [0.9.0] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.9.2 engine (#273, #278): hover marks the form of every optional call whose type can be told, including after `optMap`, `optFlatMap`, `or` and `value`, and YAML streams in UTF-16 that hold several documents are checked. Its other changes need nothing from the extension.
- Large CRDs repaint faster: the CEL colors are computed only for the visible lines and a margin around them, and again after a scroll past it.
- Tooling only, nothing changes for users: `npm run profile -- <rules>` CPU-profiles `tokenizeCel` at one rule count in its own process (#213).

## [0.8.1] - 2026-10-09

### Changed

- Tests and tooling only, nothing else changes for users: the CHANGELOG test allows every Keep a Changelog kind and collapses double engine bumps in released sections, the Restart Engine and Open Engine Setting labels share constants, and the check and release workflows were updated (#239, #222, #240, #264).
- The extension carries the `cel-lens-core` v0.9.0 engine (#267): a quote after a verbatim tag with a comma in a flow collection is read as quoted. Its other changes need nothing from the extension.

### Fixed

- Release runs no longer fill the runner's `/tmp`: the workflows stage engine binaries in a temporary folder of the job's own (#253).

## [0.8.0] - 2026-10-09

### Changed

- The README now says which settings files a color style pick changes: your user settings, and the workspace and the open file's folder when they set the key (#245).
- The log line about an engine that reports no Kubernetes versions now says where to download a newer one: the releases page and the `core-vX.Y.Z` tags (#230).

### Fixed

- The start-failure message separates the engine's last lines with `/` and escapes double quotes, so the quoted part is easy to read (#238).

## [0.7.2] - 2026-10-09

### Changed

- Tests and tooling only, nothing changes for users: the check turns Node's compile cache off (`NODE_DISABLE_COMPILE_CACHE=1`), so it no longer fills the CI runner's `/tmp` (#243, #247).

### Fixed

- Picking a color style writes the folder-level `cel-lens.colorStyle` of the open file's workspace folder only, no longer rewriting the other folders' `.vscode/settings.json` (#209).

## [0.7.1] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.8.0 engine (#235): hover skips quoted brackets, completion resolves groups after keyword operators, and merge-key cancellation is checked. Its changes need nothing from the extension.
- Tests and tooling only, nothing changes for users: the test stand-in's document events are checked against the real `vscode` Event types (#228), and its quick pick against `vscode.QuickPick` (#199).
- Tests and tooling only, nothing changes for users: CHANGELOG.md has one Keep a Changelog section per kind (#58).
- Tests and tooling only, nothing changes for users: the tokenizing benchmark's growth line times the 5000 and 10000 rule sizes in alternation, so machine load no longer gives false OVER results (#214).

### Fixed

- The engine start-failure message reads as sentences: it starts with "The cel-lens engine failed to start", puts what the engine printed in double quotes, and gives the advice as its own sentence (#207).

## [0.6.1] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.7.0 engine (#219).
- Tests and tooling only, nothing changes for users: the Show Logs label is one shared constant (#183), a test pins that `ServerController` starts the engine through `spawnEngine` (#194), and the end-to-end docs-link pattern is anchored at a path boundary (#195).

### Fixed

- Open Releases Page opens https://github.com/JairJosafath/cel-lens/releases, where engine releases are tagged `core-vX.Y.Z`, instead of a cluster-internal host (#197).
- The status bar shows the engine as stopped when the connection keeps erroring and the engine is shut down (#179).

## [0.6.0] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.6.1 engine (#202, #211): a broken document's own YAML error is reported when a bad character follows it, UTF-8 errors sit on the offending character, and a quote after an anchor or tag in a flow collection is read as a quoted scalar. Its changes need nothing from the extension.
- Tests and tooling only, nothing changes for users: a profile explains why `tokenizeCel` grows faster than linearly past 5000 rules, with a benchmark budget for it (#170), and the Makefile declares `e2e` phony and lists it in `make help` (#46, #205).

### Fixed

- The status bar actions menu shows the color style of the active editor's folder, as the Color style menu does (#184).

## [0.5.0] - 2026-10-09

### Changed

- Tests only, nothing changes for users: the manifest test checks that command titles start with a verb (#82), the extension and the end-to-end test share their spawn options (#55), an end-to-end test checks that hover links point at known docs hosts (#168), and the `onDidChangeTextDocument` test stand-in is type-checked (#89).

### Fixed

- The Color style menu opens with the current style highlighted, so pressing Enter at once changes nothing (#185).

## [0.4.1] - 2026-10-09

### Changed

- The cel-lens-core releases URL behind Open Releases Page and the engine download is defined once, so it can move to a public host in one place (#42).
- Tests only, nothing changes for users: `verifyDist` is tested on an empty or missing `dist` folder (#160).
- The extension carries the `cel-lens-core` v0.5.1 engine (#181): the hover marks the right form when the receiver or an argument is a call, an optional or a macro's loop variable, hovers on `sum`, `isLessThan`, `orValue`, `size`, `startsWith` and `all` link to their own docs section, quick fixes, hovers and completions work in the documents before a bad character, a block scalar is kept when a comment separates its anchor or tag from the header, and schemas that alias or merge a large `properties` map are handled in full. Its changes need nothing from the extension.

### Fixed

- The notification for an engine that fails to start leads with the real cause: the engine's last few lines, or "the engine file can't be run (permission denied)" with the path. Stream errors such as `write EPIPE` no longer appear in it (#157).
- The actions menu entry reads `Show Logs`, like the error notification buttons (#154).
- When the connection to the engine keeps erroring, the message says "engine" instead of "server" and offers Restart Engine and Show Logs, once (#152).

## [0.4.0] - 2026-10-09

### Changed

- Tests and benchmarks only, nothing changes for users: `verifyDist` is tested on a real folder of `.vsix` files (#137), the 5000-rule `tokenizeCel` unit test is back (#140), an end-to-end test checks that the real engine's hover reaches the client with its doc link (#144), and the cel tokens benchmark is steadied and checks its 100 ms budget (#141).
- The extension carries the `cel-lens-core` v0.5.0 engine (#165): a block scalar keeps its range when its header is split by a tab or line break, line counts match the YAML parser for a lone CR and Unicode line separators, CEL in the documents before a bad character is still checked, and "Invalid Unicode character" errors sit on the offending character. Its changes need nothing from the extension.

### Fixed

- Coloring the CEL of a large CRD takes less than half the time and allocates less, so a CRD with 5000 rules no longer stalls the editor while it repaints (#131).
- In a multi-root workspace, Choose Color Style pre-selects the style of the active editor's folder, the one painted in that editor (#146).

## [0.3.1] - 2026-10-09

### Fixed

- In a remote window (WSL, SSH, a dev container, VS Code in a browser), the Open Engine Setting button opens the Remote settings filtered to `cel-lens.serverPath`, instead of the User tab that says "No Settings Found" (#133).
- When the engine keeps crashing, the message now says "engine" instead of "server" and offers Restart Engine and Show Logs buttons (#135). It still restarts the engine after a crash and gives up after 5 crashes in 3 minutes.
- The `CEL · K8s` status bar item stays on YAML files when the engine failed to start or stopped after crashing (#134). It reads `CEL · Engine down` with an error icon and a tooltip giving the reason, and clicking it opens the actions menu with Restart Engine and Show logs at the top. After a restart it shows the Kubernetes version again.

## [0.3.0] - 2026-10-08

### Changed

- The extension carries the `cel-lens-core` v0.4.1 engine (#139, #143): hovers link to the function's section of the docs, macro completions are offered only on receivers they apply to, the hover marks the matching overload from the argument types too, and a YAML alias of a list in a text field, such as `description: *a`, no longer crashes the engine and gets the wrong-type problem on the alias. Its changes need nothing from the extension.
- The one-time hint to move `celLens.colorStyle` to `cel-lens.colorStyle`, and the Move Setting button, now also cover a value set at workspace-folder scope, read through each folder of a multi-root workspace (#122). `cel-lens.colorStyle` now has resource scope, so VS Code accepts it in workspace and folder settings.
- A benchmark now times the color repaint on a 5000-rule CRD (#88), and the tokenizing benchmark on such a CRD was reworked after it took over 5 s in CI (#121). Nothing changes for users.
- `make dist` now opens every platform `.vsix` it wrote and fails unless each holds exactly one engine binary for its own target and a manifest TargetPlatform that matches its file name (#35). `node src/dist.ts verify` runs the same check on an existing `dist/`.

### Fixed

- Choose Color Style now also writes the pick where the workspace or a folder sets `cel-lens.colorStyle`, and each editor is painted in the style of its own workspace folder (#122).
- The end-to-end engine test reads the engine from `bin/` and makes no network request; `npm run engine` puts it there, and `npm run e2e` runs that first (#54).
- A failed engine start no longer logs the engine's stderr a second time in the summary; the notification still quotes it (#117).
- An engine the system refuses to launch (for example a file that is not executable) now has its cause in the log (#117).

## [0.2.4] - 2026-10-08

### Added

- Whoever still sets `celLens.colorStyle` is told once how to move it to `cel-lens.colorStyle`, with a Move Setting button that writes the new key and removes the old one at the same scope (#103).

### Changed

- Messages call it the engine, not the language server, and every button that opens `cel-lens.serverPath` is labelled Open Engine Setting (#96).
- The extension carries the `cel-lens-core` v0.3.0 engine (#118): diagnostics for control-character, encoding and wrong-typed flow, alias and multi-line scalar errors sit on the exact characters, and costs are no longer reused for untyped schema nodes. Its changes need nothing from the extension.
- `make dist` first compares all six pinned checksums in `core-version.json` with the engine release's `checksums.txt` and fails, naming each asset that differs (#61).
- A test now shows that opening an unrelated document does not repaint other editors (#102). Nothing changes for users.

### Fixed

- Each line the engine writes to stderr now appears once in the cel-lens log, not twice (#53).

## [0.2.3] - 2026-10-08

### Changed

- The extension carries the `cel-lens-core` v0.2.0 engine (#106, #108): richer hovers on CEL functions and macros, faster checks of large CRDs, more accurate diagnostic ranges, and a list of its supported Kubernetes versions in its `initialize` reply, which the version picker already reads, so Pick Kubernetes Version offers them. Its changes need nothing from the extension.

## [0.2.2] - 2026-10-08

### Changed

- The color style setting is now `cel-lens.colorStyle`, like every other cel-lens setting (#85), and the README test expects that name. A value set under the old `celLens.colorStyle` key is still used until `cel-lens.colorStyle` is set.

### Fixed

- The README's Choose Color Style entry names the styles as the quick pick shows them: CEL, Classic and Off (#95).
- An editor whose language mode changes loses or gains its cel-lens colors at once, not at the next repaint (#91).

## [0.2.1] - 2026-10-08

### Changed

- The actions menu names the engine's state (starting, stopped, failed) on the Kubernetes version entry instead of leaving it blank (#76).
- Coloring repaints only the YAML document that changed, not every visible YAML editor, and ignores other documents such as the cel-lens output channel (#73).
- The error button and messages now say **Restart Engine** and "restart the engine", matching the command (#81).
- The actions menu and the Choose Color Style quick pick name the styles the same way: CEL, Classic and Off (#77).

### Fixed

- The README describes the start-failure buttons by source: Open Setting for a `cel-lens.serverPath` binary, Show Logs and a reinstall hint for the bundled engine (#80).
- An editor switched from YAML to another language loses its cel-lens colors at the next repaint (#74).

## [0.2.0] - 2026-10-08

### Added

- A menu of the main actions from the status bar (#65): clicking the item lists Color style, Kubernetes version, Restart Engine and Show logs, the first two showing the current style and version. The tooltip now reads "cel-lens: Kubernetes <version> · click for actions". New commands **cel-lens: Show Actions** and **cel-lens: Show Logs**.
- Color styles for CEL (#64): the parts of each CEL expression in Kubernetes YAML are colored in the `cel` (default), `classic` or `off` style, chosen by theme kind. Set `celLens.colorStyle` or run **cel-lens: Choose Color Style**; open editors recolor at once.

### Changed

- Command and setting names are clearer (#66): **cel-lens: Restart Server** is now **cel-lens: Restart Engine** (same command id); commands that need a YAML file are hidden from the palette without one; settings are ordered with color style first, then Kubernetes version, and every setting has a description and enum values are described.
- Groundwork for coloring the parts of a CEL expression (#63): finding the CEL in a CRD's `x-kubernetes-validations`, a CEL lexer and the colors of the `cel`, `classic` and `off` styles. Nothing is colored yet.
- A `v`-prefixed `cel-lens.kubernetesVersion` such as `v1.36` is matched against the engine's supported versions like `1.36`: no false "cannot match" warning, and the version picker marks the right entry (#40).
- When the engine fails to start, the one message now says to reinstall the extension or clear
  `cel-lens.serverPath` (#36).
- When an engine from `cel-lens.serverPath` fails to start, the message says to fix or clear the setting and its button, Open Setting, goes straight to it; a bundled engine's message says to reinstall and offers Show Logs (#71).

## [0.1.1] - 2026-10-08

### Changed

- The extension carries the `cel-lens-core` v0.1.2 engine (#59): diagnostic spans come from the checked AST, problems sit on YAML aliases, and broken YAML on a blank line never gets an empty range. These need nothing from the extension.

## [0.1.0] - 2026-10-07

The first release: the bundled `cel-lens-core` v0.1.1 engine behind a language client for Kubernetes YAML (#1, #3, #11, #12, #14, #16, #17, #18, #19, #26, #30, #33, #48). The Standard changes to the Makefile (#6, #8, #10, #20, #44, #50) change nothing users see.

### Added

- The extension carries the `cel-lens-core` v0.1.1 engine for its platform, one `.vsix` per platform, so installing it is all a developer does. `core-version.json` pins the engine and its checksums.
- The extension starts `cel-lens-core lsp` for YAML files, for CEL errors, hover and completion in Kubernetes YAML. Broken YAML shows as an error where it breaks. The `cel-lens.serverPath` setting (an advanced override, otherwise the bundled engine is used), the `cel-lens.trace.server` setting, and the Restart Server command.
- A status bar item on YAML files shows the Kubernetes version the results match, such as `CEL · K8s 1.37`. The advanced `cel-lens.kubernetesVersion` setting and the Pick Kubernetes Version command choose another version; clearing the setting or picking Default returns the engine to its default. A setting the engine does not support is logged once and the default is shown, and a `v`-prefixed version such as `v1.34` is read as `1.34`. When the engine reports no version the item is hidden and the log says so.
- Pick Kubernetes Version says why it cannot pick: the engine is still starting, failed to start, is not running, or reports no versions, with buttons to the releases page, to restart the engine and to the engine setting.
- A failure to start the engine shows one message with the error and a button to the logs. An engine that exits while starting is not restarted until the client gives up.

### Removed

- The Say Hello command and the other template leftovers, and the README drawings, which were mock-ups and not real screenshots.

### Fixed

- A Kubernetes version picked while the engine is starting reaches the engine.

[Unreleased]: https://github.com/JairJosafath/cel-lens/compare/v0.22.0...HEAD
[0.22.0]: https://github.com/JairJosafath/cel-lens/compare/v0.21.0...v0.22.0
[0.21.0]: https://github.com/JairJosafath/cel-lens/compare/v0.20.0...v0.21.0
[0.20.0]: https://github.com/JairJosafath/cel-lens/compare/v0.19.0...v0.20.0
[0.19.0]: https://github.com/JairJosafath/cel-lens/compare/v0.18.0...v0.19.0
[0.18.0]: https://github.com/JairJosafath/cel-lens/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/JairJosafath/cel-lens/compare/v0.16.0...v0.17.0
[0.16.0]: https://github.com/JairJosafath/cel-lens/compare/v0.15.2...v0.16.0
[0.15.2]: https://github.com/JairJosafath/cel-lens/compare/v0.15.1...v0.15.2
[0.15.1]: https://github.com/JairJosafath/cel-lens/compare/v0.15.0...v0.15.1
[0.15.0]: https://github.com/JairJosafath/cel-lens/compare/v0.14.0...v0.15.0
[0.14.0]: https://github.com/JairJosafath/cel-lens/compare/v0.13.1...v0.14.0
[0.13.1]: https://github.com/JairJosafath/cel-lens/compare/v0.13.0...v0.13.1
[0.13.0]: https://github.com/JairJosafath/cel-lens/compare/v0.12.1...v0.13.0
[0.12.1]: https://github.com/JairJosafath/cel-lens/compare/v0.12.0...v0.12.1
[0.12.0]: https://github.com/JairJosafath/cel-lens/compare/v0.11.1...v0.12.0
[0.11.1]: https://github.com/JairJosafath/cel-lens/compare/v0.11.0...v0.11.1
[0.11.0]: https://github.com/JairJosafath/cel-lens/compare/v0.10.0...v0.11.0
[0.10.0]: https://github.com/JairJosafath/cel-lens/compare/v0.9.1...v0.10.0
[0.9.1]: https://github.com/JairJosafath/cel-lens/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/JairJosafath/cel-lens/compare/v0.8.1...v0.9.0
[0.8.1]: https://github.com/JairJosafath/cel-lens/compare/v0.8.0...v0.8.1
[0.8.0]: https://github.com/JairJosafath/cel-lens/compare/v0.7.2...v0.8.0
[0.7.2]: https://github.com/JairJosafath/cel-lens/compare/v0.7.1...v0.7.2
[0.7.1]: https://github.com/JairJosafath/cel-lens/compare/v0.6.1...v0.7.1
[0.6.1]: https://github.com/JairJosafath/cel-lens/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/JairJosafath/cel-lens/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/JairJosafath/cel-lens/compare/v0.4.1...v0.5.0
[0.4.1]: https://github.com/JairJosafath/cel-lens/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/JairJosafath/cel-lens/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/JairJosafath/cel-lens/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/JairJosafath/cel-lens/compare/v0.2.4...v0.3.0
[0.2.4]: https://github.com/JairJosafath/cel-lens/compare/v0.2.3...v0.2.4
[0.2.3]: https://github.com/JairJosafath/cel-lens/compare/v0.2.2...v0.2.3
[0.2.2]: https://github.com/JairJosafath/cel-lens/compare/v0.2.1...v0.2.2
[0.2.1]: https://github.com/JairJosafath/cel-lens/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/JairJosafath/cel-lens/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/JairJosafath/cel-lens/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/JairJosafath/cel-lens/releases/tag/v0.1.0
