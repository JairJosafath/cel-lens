# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Versions before 0.14.0 were released before the public repository existed, and 0.15.1 and 0.17.1 were never tagged on it, so they have no link.

## [Unreleased]

### Added

- The `.cellens` format, for types cel-lens assumes where Kubernetes gives `dyn`: `docs/cellens.md` describes it, and the engine reads it and reports its problems with their line and column.
- The language server reads every `.cellens` of the workspace and uses its types: an `x-kubernetes-int-or-string` value of a CustomResourceDefinition, the one value its rules see as `dyn`, or `params`, `object`, `oldObject` or `variables.<name>` of a ValidatingAdmissionPolicy, gets checks, completion and hover with the type an entry gives it, and `kind:` names a built-in kind or a workspace CRD. A problem only the type shows is a warning naming the `.cellens` file and line; the API server's own problems keep their severity. The problems of a `.cellens`, such as an entry for a file, document or value that does not exist or for a value Kubernetes already types, are diagnostics on it, and opening, editing, saving, creating or deleting one checks the open documents again. `pkg/check` takes the types with `WithAssumedTypes` and says why one does not apply with `AssumedTypeProblems`.

### Fixed

- A `didChangeWatchedFiles` batch that changes the CRDs and creates a folder while a workspace scan runs is published once, when the scan of that folder ends: the end of the scan in progress no longer publishes the state without the folder's CRDs. What a batch changes waits for the end of both the CRD scan and the search for `.cellens` files it starts; what a scan finds is published when it ends, whatever other batches wait for.
- The workspace CRD scan, and a CRD file read again after a change on disk, read only regular files of at most 1 MiB, through the workspace folder: a symbolic link to `/dev/zero`, to a pipe, out of the folder, or to a bigger file is skipped, where before it could hold the server or fill its memory.

## [0.27.0] - 2026-11-04

### Added

- The `initialize` result reports the workspace-scan limits, the file cap and the per-file size in bytes, under `capabilities.experimental.celLens`.

### Changed

- A table test covers the root cases of `skippedCreated`.
- A test checks the changelog intro's list of unlinked versions against the public tag list.

## [0.26.0] - 2026-11-04

### Changed

- Hover says `crd-typed-policies` is off when a workspace CRD would type `object`.
- The language server publishes once per `didChangeWatchedFiles` batch that starts a folder scan.
- A test checks every version number in the Kubernetes version section of `docs/engine.md` against `pkg/kubeversion`.
- The fallback-build test honours the Windows temp directory.
- The unreadable-subfolder scan test of the language server runs as root, by injecting the file system.

### Fixed

- A created-folder notification during a workspace CRD scan no longer restarts it: the folders wait and are scanned together when it ends, so a long clone or unzip lets the scan finish and the first folders' CRDs become known before it ends.
- With `kyverno` and `crd-typed-policies` on, `object` and `oldObject` of a Kyverno ValidatingPolicy that matches a custom resource are typed from its workspace CRD: a wrong field gets a warning naming the kind, and hover names it.

## [0.25.1] - 2026-11-03

### Changed

- A test covers an unreadable subfolder below a readable workspace root in the language server's CRD scan, and `pkg/check`'s `Fill` table test pins the per-folder `Incomplete` flag.
- The README's `crd-typed-policies` row mentions the oversized-file summary line.

### Fixed

- A created folder below a hidden folder or `node_modules` is skipped by the workspace CRD scan.
- A CRD file under a hidden folder or `node_modules` that is edited on disk while closed gives its new schema after a workspace rescan, without waiting for the file to be opened.
- Preparing a release (`make release RELEASE=X.Y.Z`) refreshes `testdata/public-core-tags.txt` from the public repository and rewrites the changelog's link lines from it, and stops before writing when the repository can't be read.

## [0.25.0] - 2026-11-03

### Added

- With `crd-typed-policies` on, `object` and `oldObject` of a ValidatingAdmissionPolicy that matches a custom resource are typed from its CustomResourceDefinition in the workspace: a wrong field gets the API server's warning, and hover names the kind instead of `dyn`.

### Changed

- `make check` guards the `CEL_LENS_BIN` fallback build with a test.
- A test derives the Kubernetes version range and default in `docs/engine.md` from `pkg/kubeversion`.
- The doc comment of `TestPublicSourceTestsPass` says what `-short` does.
- `compileValidations` no longer passes its unused `field` parameter to the `fail` callback.

### Fixed

- A created folder is scanned for CRDs in the background, and nested folder events are coalesced into one scan.

## [0.24.2] - 2026-11-03

### Changed

- A test asserts `make test-dist` keeps `-race -count=1` and `-timeout 30m`, and the stdio test client of the language server reports a closed server pipe at once.

### Fixed

- A created folder that is itself hidden or named `node_modules` is skipped, like the folders below it.
- `public-tags.sh` refuses to run when the list file is missing, and reports an unreadable remote as such.
- A workspace scan that skips many files for their size sends one summary log line, with the count, the limit and the first three names, instead of one line per file; a single file still gets its own line.

## [0.24.1] - 2026-11-03

### Changed

- `public-tags.sh --append` adds newly published tags to `testdata/public-core-tags.txt`, which now lists `core-v0.24.0`.
- A test pins the README's experimental feature names against `check --help`, another asserts `make test-dist` still sets `CEL_LENS_DIST_TEST=1`, and the stdio test client has a read deadline.
- A truth case with a divergence note fails on a malformed recorded line.

### Fixed

- On `didChangeWatchedFiles`, the CRDs below a created or renamed-in folder are read.
- A workspace scan skips the files below a folder forgotten while it ran when its result is filled in.
- A workspace folder that is missing or cannot be read no longer stops a rescan from forgetting deleted CRDs under the other folders; the CRDs known under the folder that could not be read are kept, and the log names it as incomplete.
- A workspace scan that read a CRD file before its folder was deleted no longer brings the CRD back when it ends.
- A CRD file under a hidden folder or `node_modules` that the server learned from a document is kept across a workspace rescan while the file is on disk, and forgotten once it is deleted.

## [0.24.0] - 2026-11-02

### Added

- The language server tells the client when the workspace scan for CRDs stops at its file cap (one warning) or skips a file over the size limit (a log message naming the file and the limit), so a params type that stays `dyn` has a visible reason.

### Changed

- A divergence note pins the engine's current answer too, not only the API server's.
- The release check compares `testdata/public-core-tags.txt` with the tags of the public repository.
- A test pins the README's flag table and exit-code paragraph against `check --help`.
- The truth set of recorded API-server answers now holds 13 cases, the owner's nine other `status.typeChecking` recordings added to the four it had. A case with no line is `typeChecking: []`, and an answer with several errors for one kind keeps them in one string.

### Fixed

- A rescan of the workspace forgets the CRDs of files it no longer finds below the scanned folders, so a CRD file deleted while `crd-typed-policies` was off stops typing policy params on a client without file watching. Files past the workspace file cap are left alone.

## [0.23.0] - 2026-11-02

### Changed

- The workspace scan for CRDs that `crd-typed-policies` starts runs in the background, so the language server answers requests while it runs, and checks the open documents again when it ends.
- A test runs `check` on the embedded CRD and pins the README's sample output.
- A test derives the README's Kubernetes version range from `pkg/kubeversion`, so the two cannot differ.

### Fixed

- The link of version 0.21.1 is back in this file, and 0.22.0 compares from it, so each compare link opens on the public repository.
- Deleting or renaming a folder makes the language server forget the CRDs read from files below it, so a policy no longer keeps a warning from a CRD that is gone. The server now also asks the client to report deletions of folders.

## [0.22.0] - 2026-10-31

### Changed

- Every section of this file, not only the newest two, is in Keep a Changelog order with standard headings, and a test checks it.
- A test asserts that `make test-dist` keeps running both gated tests.
- The non-root test guard catches factory paths that are split or joined.
- The dist tests stay out of the public-copy inner run.

### Fixed

- The version links of this file name only core tags that are on the public repository, checked against a list of them.
- `trackedFiles` errors include git's own message.
- `make check` builds and tests two packages at a time by default (`GO_PARALLEL`, raise it on a big machine), so its peak memory is lower.

## [0.21.1] - 2026-10-30

### Fixed

- The params cases of the truth set now hold the policy the owner sent to the API server, and no case carries a divergence note.
- The version links of this file open only for versions the public repository has tags for.

## [0.21.0] - 2026-10-30

### Changed

- The API server's recorded `status.typeChecking` answers are pinned as a test (`TestTruthSet`), so a type-check warning that differs from them fails `make check`.
- The version links point at the public `core-v` tags on github.com/JairJosafath/cel-lens, and the issue references are gone from the lines.
- `docs/project-notes.md` has a refreshed root package timing budget and the runner timings of run 140, the first green full check.

### Fixed

- A type-check warning is one line per matched kind naming that kind again (`apps/v1 Deployment: unexpected failed resolution of 'kubernetes.authorization.Authorizer'`), as the API server writes it; this takes back the v0.20.0 change that showed it once with no kind.

## [0.20.1] - 2026-10-30

### Fixed

- `go test ./...` passes without `make test`, since the tests build the program once when `CEL_LENS_BIN` is unset.
- A wrong `params` field is warned about once per matched kind, naming that kind (`apps/v1 Deployment: undefined field 'dataa'`), as the API server's `status.typeChecking` does; this takes back the change of v0.19.0.

## [0.20.0] - 2026-10-30

### Changed

- While `crd-typed-policies` is off, `workspace/didChangeWatchedFiles` reads no file and publishes no diagnostics; deletions are still forgotten, and turning the feature on scans the whole workspace.
- A type-check warning that depends on neither `object` nor `params`, such as an `authorizer` problem, is shown once with no kind prefix, not under the `paramKind` or a matched kind.
- A test guards that only the root package reads the factory's files, one tracked-tree copy helper is shared by `TestPublicSourceTestsPass` and `TestDist`, and a test asserts the inner `go test` timeout stays below the Makefile's test timeout.
- `docs/project-notes.md` no longer carries a stale e2e build reference, and the `[Unreleased]` CHANGELOG is tidied before v0.19.0.

## [0.19.0] - 2026-10-30

### Added

- `CONTRIBUTING.md` says how to build, test and send a change, that `make check` fails on any warning, and which tests skip in the published source and why.
- `SECURITY.md` asks for private reports through GitHub's private vulnerability reporting, with an answer within 7 days and a verdict within 14.
- `docs/engine.md` holds what the README no longer carries: the language server, typed variables and `params`, Kyverno policies and the command line in detail.

### Changed

- The language server scans the workspace for CRDs only while `crd-typed-policies` is on, so `initialize` no longer blocks on it by default; turning the feature on through `workspace/didChangeConfiguration` scans and re-checks the open documents.
- The README is short and direct, for the command line and other editors: what it does, what `check` prints, install with `go install github.com/JairJosafath/cel-lens/engine@latest`, flags, versions, features, other editors and what is not supported yet.
- The public-layout test copies only tracked files and bounds its inner `go test` by the test's context, which makes `make check` cheaper.
- `make test` builds the engine once instead of inside the end-to-end tests, and `TestGoInstallFromProxy` no longer compiles the engine inside `go test`.
- The check workflow's full job runs `vet-windows`, `test-windows` and `test-dist`, and uses the same `GOTMPDIR` setting as the light job.
- `docs/project-notes.md` is refreshed after the v0.17.2 tag and run 41, and its Standard-change wording is current.

### Fixed

- A warning about a wrong `params` field names the kind of `spec.paramKind` (`example.com/v1 Widget: undefined field 'minReplica'`), not the kind the policy matches, and is shown once per policy; `object` and `oldObject` warnings keep the matched kind.
- The warning for an unknown name in the `experimental` list is written to the language server's log (`window/logMessage`) once for the whole list, not shown as a notification.

## [0.17.2] - 2026-10-29

### Added

- Hover says why a value is `dyn`, such as a field the schema leaves open or a kind that can't be known.
- A report counts how many expressions in real policies from Kyverno's policy library and the Kubernetes docs still get `dyn`, and why.
- A test proves that `cel-lens-core check` works on a folder given as a symlink.
- Every feature is stable or experimental. An experimental feature is off until it is listed: `cel-lens-core check --experimental name,name`, and `experimental: ["name", ...]` in the language server's `initializationOptions` and in the `settings` of `workspace/didChangeConfiguration` (a change re-checks the open documents). The `initialize` result lists the names under `capabilities.experimental.celLens.experimentalFeatures`. An unknown name is ignored with one warning that names the known ones.
- The experimental features are `kyverno`, `crd-typed-policies` and `fitting-completion`; the README has a table of stable and experimental features.

### Changed

- Checks, hover and completion for Kyverno `ValidatingPolicy` need `kyverno`. Off, those documents get nothing.
- Typing `params` of a custom resource from a CRD needs `crd-typed-policies`. Off, `params` is `dyn` and hover says the feature is off.
- Completion that fits the receiver or the document, and hover that says why a value is `dyn`, need `fitting-completion`. Off, they behave as in v0.17.1.

### Fixed

- A type comes from the YAML document the expression sits in, never from another document of the same file.
- Completion after a dot offers only what the receiver has.
- Completion before a dot offers only the names this kind of document can use.
- Checking continues after a list item of the wrong type in a policy, so later expressions are still checked.
- An unreadable subfolder is named once in the walk error.
- `go test ./...` passes in the published source, where the tests that read the factory's `.gitea/` skip.
- The full `make check` passes on `main`; the `test` target allows `go test` 30 minutes, through a Standard change to the Makefile.

## [0.17.1] - 2026-10-13

### Added

- `docs/type-sources.md` lists every place a YAML document tells a CEL expression its types.
- A `make check-light` target runs lint, vet and the build without the tests, and the Makefile is updated by the Standard change for it.

### Fixed

- Hover over a method call marks the overload the type checker picked with "// this call" wherever the receiver's type is known, such as `check` in `authorizer.group('apps').resource('deployments').check('update')`, and lists only the overloads that fit that type; with a `dyn` receiver it lists every overload as before.
- Hover and completion work on every line of a CEL rule written as a plain, "double-quoted" or 'single-quoted' YAML scalar over several lines, or with escapes or a doubled `''`, as on one line. The cursor and the hover range are mapped between the file and the folded rule with the character map. A completion now carries an edit range over the name already typed, and a quote in the inserted text is escaped for the scalar (`\"` in double quotes, `''` in single quotes), so the YAML stays valid; rules in a CRD, a ValidatingAdmissionPolicy and a Kyverno ValidatingPolicy alike.

## [0.15.1] - 2026-10-12

### Added

- In a ValidatingAdmissionPolicy and a MutatingAdmissionPolicy, a `messageExpression` that does not give a string, a `matchConditions` or `validations` expression that does not give a bool, and an `auditAnnotations` `valueExpression` that gives neither a string nor null are reported on that expression in the API server's words (`must evaluate to bool but got int`), at every supported Kubernetes version. Like the API server, an expression of type `dyn`, such as `object.spec.replicas`, is not accepted either.

### Changed

- The test that no package holds mutable state also catches a package variable set by a call.
- `TestChangelogHeadings` also checks the link definitions at the end of this file.
- A test pins that no cost problem is reported on any expression of a ValidatingAdmissionPolicy or MutatingAdmissionPolicy at Kubernetes 1.33 to 1.37, in `cel-lens-core check` and the language server alike, as the API server makes no static cost check on policies; CRD rule cost checks are unchanged. No policy expression got a cost problem before, so nothing needed fixing.

### Fixed

- A quick fix in a broken UTF-16 file uses the file's own line ending.
- `Documents` stays under 300 ms when a bad character is near the end of the file.
- A problem in a CEL rule written as a plain, "double-quoted" or 'single-quoted' YAML scalar over several lines, or with escapes (`\"`, `\\`, `\t`, `\u00e9`, a `\` at the line end) or a doubled `''`, is underlined where the API server points, such as `maxSiz` on the second line, not from the first character of the rule over all its lines. Each character of the folded rule is mapped back to its own line and column in the file, in `cel-lens-core check` and the language server alike; single-line rules and `|` and `>` block scalars keep their positions.
- A file with a next line (U+0085), line separator (U+2028) or paragraph separator (U+2029) line end gets its problems underlined in the right place below it, as with a line feed, and a `---` between such line ends no longer costs a document one of its problems. Documents are cut where they were.

## [0.15.0] - 2026-10-12

### Added

- In a ValidatingAdmissionPolicy and a MutatingAdmissionPolicy, `variables.<name>` is typed by the expression of `spec.variables[]` that defines it, as the API server types it. Completion after `variables.` offers the variables defined so far with their types and, after `variables.<name>.`, the fields or methods of that type; hover gives the type and the defining expression; a name the policy lacks or defines later gets the API server's `undefined field` error, and a variable whose expression does not compile is `dyn` without a second error on each use.
- In a ValidatingAdmissionPolicy, `params` is typed from `spec.paramKind`: from the schema of a built-in kind, or of a custom resource whose CustomResourceDefinition is in the workspace (language server) or among the files checked (`cel-lens-core check`). Completion, hover and field warnings follow, the policy's results follow a CRD that changes without a restart, and `params` stays `dyn` when the kind is not found. Its `apiVersion`, `kind` and full `metadata` (`labels`, `annotations`, `namespace` and the rest) are typed for a custom resource too, as the API server publishes them.
- A MutatingAdmissionPolicy is read: its match conditions, variables and the `applyConfiguration` and `jsonPatch` expression of each mutation are checked with the API server's admission compiler and get the typed variables below.
- In a ValidatingAdmissionPolicy and a MutatingAdmissionPolicy, `request`, `namespaceObject` and `authorizer` are typed as the API server declares them: completion and hover offer their fields and methods, including each step of an `authorizer` call chain and `authorizer.requestResource`, and `oldObject` is documented and tested as typed like `object`.

### Changed

- CI skips the Go build, lint and test steps on a change that touches only files none of them reads, and still reports success; a change that adds a version heading to this file (a Prepare change) always runs them. The rule is `scripts/changes.sh`, through a Standard change to `.gitea/workflows/check.yaml`.

## [0.14.0] - 2026-10-10

### Added

- A benchmark measures `Documents` on large multi-document streams.

## [0.13.1] - 2026-10-10

### Changed

- CI compiles and tests Windows-only code, through the Standard change to `.gitea/workflows/check.yaml` and `Makefile`.
- The `TestBadCharacter` subtests are independent of each other.

### Fixed

- A problem in a good document of a UTF-16 file is reported at its own line and column, not at the start of the rule or not at all, when a later document has a broken character.
- Document separators are found at NEL, LS and PS line ends as well.

## [0.13.0] - 2026-10-10

### Added

- `object` and `oldObject` of a ValidatingAdmissionPolicy that matches one built-in kind are typed from its schema: completion, hover and a warning for a field the kind lacks.
- A policy that matches several built-in kinds is read easily: completion after `object.` lists the fields every kind has first and names the kinds of each field, hover says which kinds have a field, and a field only some kinds have gets one warning for each kind that lacks it. A wildcard or unknown resource keeps `object` untyped.
- Kyverno `ValidatingPolicy` (`policies.kyverno.io/v1`) is checked: its `variables`, `validations`, `matchConditions` and `auditAnnotations` expressions are compiled in Kyverno v1.19.1's CEL environment, so unknown variables and functions called with the wrong arguments are reported as Kyverno reports them. Kyverno's other policy kinds are not yet supported.
- A Kyverno `ValidatingPolicy` gets completion and hover with Kyverno's own variables and functions (`resource.`, `http.`, `image(` and the rest, and not `authorizer` or `params`), `variables.<name>` completes the policy's variables, and `object` and `oldObject` are typed from `spec.matchConstraints.resourceRules` as for a ValidatingAdmissionPolicy: fields down the tree, shared fields first for several kinds, `dyn` when a kind can't be known, and a warning naming the kind for a field it lacks.

## [0.12.1] - 2026-10-10

### Changed

- The engine is licensed under Apache-2.0 and its module path is `github.com/JairJosafath/cel-lens/engine`; the README is the engine's page for contributors.

### Fixed

- `go install` of the engine from the public repository works.

## [0.12.0] - 2026-10-09

### Changed

- `internal/assist` lookup tables are immutable.
- The macro catalog keeps macro receiver kinds.

## [0.11.0] - 2026-10-09

### Changed

- Each CEL expression is parsed once for diagnostic spans.
- `internal/assist` shares one string-literal scanner.
- The unreadable-subfolder test runs as root too.

### Fixed

- Quick fixes detect CRLF line endings in UTF-16 files.

## [0.10.0] - 2026-10-09

### Changed

- Marked overloads for namespaced functions are tested beyond `optional`.
- The LSP test helpers fail the running subtest.

### Fixed

- UTF-16 bad characters are reported on the right document of a multi-document stream.
- `procwatch` treats access-denied as alive on Windows.
- The e2e binary directory is created with `os.MkdirTemp`.

## [0.9.2] - 2026-10-09

### Changed

- `firstBroken` is tested against every bad-character reason in `pkg/extract`.

### Fixed

- UTF-16 YAML streams that hold several documents are checked.

## [0.9.1] - 2026-10-09

### Changed

- `TestComplete` covers group receivers.
- The language server test asserts the column of bad-character diagnostics.
- The assist test covers bracket matching with stray closers and escaped quotes.

### Fixed

- Hover marks the form of every optional call whose type can be told, including after `optMap`, `optFlatMap`, `or` and `value`, and for `optional.of` and `optional.none`.

## [0.9.0] - 2026-10-09

### Changed

- The release workflow is updated by a Standard change.
- `.gitea/workflows/check.yaml` and `.gitea/workflows/release.yaml` are updated by a Standard change.
- Every yaml.v3 bad-character reason that `badChar` relies on is pinned.
- `TestCheckMalformedYAML` asserts the message and column of every YAML diagnostic.
- A fixture covers an aliased message in an `x-kubernetes-validation`.
- The language server assist test pins the broken-YAML diagnostic.

### Fixed

- CI and the release workflow keep Go's temporary build files in `.gotmp` in the job's workspace, not in the shared `/tmp`.
- A quote after a verbatim tag with a comma in a flow collection is read as quoted.

## [0.8.0] - 2026-10-09

### Changed

- A file with a broken later document is tested end to end through the language server for assists.
- A bad character is tested end to end through the language server.
- `line.go` helpers share one string-literal scanner.

### Fixed

- Hover skips quoted brackets when it finds the receiver to type.
- Completion resolves groups after keyword operators.
- `procwatch` lint findings on Windows are fixed.
- `entry` checks context cancellation while following merge keys.

## [0.7.0] - 2026-10-09

### Changed

- Fixtures cover aliases in `fieldPath`, `message` and flow-style validations.
- Untyped-node costs are tested through `Check` with a whole CRD.
- The other optional-value functions link to their cel-go sections.

## [0.6.1] - 2026-10-09

### Fixed

- A broken document's own YAML error is reported when a bad character follows it.
- 'invalid length of a UTF-8 sequence' errors are placed on the offending character.
- A quote after an anchor or tag in a flow collection is read as a quoted scalar.

## [0.6.0] - 2026-10-09

### Changed

- Quick fixes from `codeAction` are tested against a different problem after `didChange`.
- `Serve` is tested to stop on cancellation when a message is also ready.
- The version-level root error texts are pinned.
- `yamlfield.Tree.Remove` is unit-tested for alias values.
- CHANGELOG headings carry release dates.

### Fixed

- Completion resolves a parenthesised receiver and a literal-aware receiver.
- The doc comment of `yamlfield.Field.Node` is accurate.

## [0.5.1] - 2026-10-09

### Fixed

- `cel-lens-core check` prints a bad character's problem once, at its own line and column, instead of also naming the line where its document starts.
- The hover on a function marks the form the call uses when the receiver or an argument is a call such as `quantity(...)`, an optional from `?.`, or a macro's loop variable.
- The hovers on `sum`, `isLessThan`, `orValue`, `size`, `startsWith` and `all` link to their own section of the docs.
- Quick fixes, hovers and completions work in the documents before a document with a bad character, as in a clean file.
- A block scalar is no longer dropped when a comment separates its anchor or tag from the header.
- Schemas that alias or merge a large `properties` map are handled in full.

## [0.5.0] - 2026-10-09

### Changed

- Later documents are checked after a wrong-typed field.

### Fixed

- A block scalar keeps its range when the anchor or tag and the header are split by a tab or a line break.
- Lines are counted the same way as the YAML parser for a lone CR and for Unicode line separators.
- CEL in the documents before a bad character is still checked.
- An apostrophe inside a plain flow scalar keeps the wrong-type range at the closing bracket.
- "Invalid Unicode character" errors are placed on the offending character.

## [0.4.1] - 2026-10-08

### Fixed

- A YAML alias of a list in a text field, such as `description: *a`, no longer crashes the engine; it gets the wrong-type problem on the alias.

## [0.4.0] - 2026-10-08

### Added

- Hovers link to the function's section of the docs.
- Macro completions are offered only on receivers they apply to.
- The hover marks the matching overload from the argument types too.

## [0.3.0] - 2026-10-08

### Fixed

- Control-character and encoding errors are placed on the offending character.
- A wrong-typed flow list or mapping is underlined from its opening to its closing bracket, an alias by its name, and a multi-line plain scalar by its first line, no longer to the end of the line.
- Remembered costs are no longer reused for schema nodes without type information.

## [0.2.0] - 2026-10-08

### Added

- Hovers on CEL functions and macros show more.

### Changed

- Checking a large CRD with rules over their cost budget is about three times faster:
  costs are remembered without converting the schema again, sizing stops at the
  smallest number when it is already too big, and the background cost work only
  starts when a size is unlimited.
- The fix and the advice on every over-budget rule are sized so the CRD total is
  solved too, extending the size fixes of 0.1.3.

### Fixed

- The parser blaming an empty line no longer sends an empty range.
- The wrong-type diagnostic is placed on the value, not on the whole line.
- A rule on a schema whose `type` is the wrong type is no longer checked against
  the type the engine guessed, so only the wrong type is reported.

## [0.1.3] - 2026-10-08

### Changed

- codeAction answers from the diagnostics of the last check of the document
  version instead of checking it again.
- Validation and cost explanation share one schema compile.
- Expanding YAML merge keys checks for context cancellation.
- Schema errors are reported in CRDs that have no CEL rules.
- `initialize` reports the supported Kubernetes versions and the default.
- The internal-name test covers member and namespaced completion.

### Fixed

- Completion excludes all CEL reserved words.
- The fix and the advice on the diagnostic that reports a CRD's total cost are
  sized so the total is under budget too.
- The diagnostic for a CRD whose total cost is over budget explains why when no
  single rule is.

## [0.1.2] - 2026-10-08

### Changed

- Diagnostic spans are computed from the checked AST.

### Fixed

- Problems are placed on YAML aliases, not on their anchors.
- Block-scalar indicators after an anchor or tag are honoured.
- The end-to-end binary tests no longer time out under full-suite load.
- Schema errors are placed correctly for property names that contain brackets
 .
- Broken YAML on a blank-only line never gets an empty range.
- Decode errors point at the right YAML field, fast and cancellable.

## [0.1.1] - 2026-10-07

### Added

- The release job downloads every published asset and `checksums.txt` over the
  documented URLs, verifies the checksums and runs `--version`.
- The language server caches parsed documents and reuses the base CEL
  environment across documents.
- Finding CEL in CRD schemas follows YAML merge keys.

### Changed

- `check` has clearer error messages and exits with the interrupt exit code
  when interrupted.
- `check` accepts flags after paths and keeps walking past unreadable
  subfolders.
- `lsp` stops promptly on interrupt, rejects values on `--stdio` and
  reports every bad `--clientProcessId`, and `Serve` stops on its own
  when the client process is gone.
- The release-build test stays out of the fast test loop.
- Standard changes to the check and release workflows and the Makefile.
- Fixture checks are tighter.

### Fixed

- The JSON-RPC layer ignores client responses and bounds header size.

## [0.1.0] - 2026-10-07

### Added

- The language server reports broken YAML as a problem at the place where it
  breaks, so a file whose YAML is broken never looks clean.

[Unreleased]: https://github.com/JairJosafath/cel-lens/compare/core-v0.27.0...HEAD
[0.27.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.26.0...core-v0.27.0
[0.26.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.25.1...core-v0.26.0
[0.25.1]: https://github.com/JairJosafath/cel-lens/compare/core-v0.25.0...core-v0.25.1
[0.25.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.24.2...core-v0.25.0
[0.24.2]: https://github.com/JairJosafath/cel-lens/compare/core-v0.24.1...core-v0.24.2
[0.24.1]: https://github.com/JairJosafath/cel-lens/compare/core-v0.24.0...core-v0.24.1
[0.24.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.23.0...core-v0.24.0
[0.23.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.22.0...core-v0.23.0
[0.22.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.21.1...core-v0.22.0
[0.21.1]: https://github.com/JairJosafath/cel-lens/compare/core-v0.21.0...core-v0.21.1
[0.21.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.20.1...core-v0.21.0
[0.20.1]: https://github.com/JairJosafath/cel-lens/compare/core-v0.20.0...core-v0.20.1
[0.20.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.19.0...core-v0.20.0
[0.19.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.17.2...core-v0.19.0
[0.17.2]: https://github.com/JairJosafath/cel-lens/compare/core-v0.15.0...core-v0.17.2
[0.15.0]: https://github.com/JairJosafath/cel-lens/compare/core-v0.14.0...core-v0.15.0
[0.14.0]: https://github.com/JairJosafath/cel-lens/releases/tag/core-v0.14.0
