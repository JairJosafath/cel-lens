# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.14.0] - 2026-10-10

### Added

- A benchmark measures `Documents` on large multi-document streams (#293).

## [0.13.1] - 2026-10-10

### Fixed

- A problem in a good document of a UTF-16 file is reported at its own line and column, not at the start of the rule or not at all, when a later document has a broken character (#332).
- Document separators are found at NEL, LS and PS line ends as well (#294).

### Changed

- CI compiles and tests Windows-only code, through the Standard change to `.gitea/workflows/check.yaml` and `Makefile` (#291, #342).
- The `TestBadCharacter` subtests are independent of each other (#292).

## [0.13.0] - 2026-10-10

### Added

- `object` and `oldObject` of a ValidatingAdmissionPolicy that matches one built-in kind are typed from its schema: completion, hover and a warning for a field the kind lacks (#320).
- A policy that matches several built-in kinds is read easily: completion after `object.` lists the fields every kind has first and names the kinds of each field, hover says which kinds have a field, and a field only some kinds have gets one warning for each kind that lacks it. A wildcard or unknown resource keeps `object` untyped (#321).
- Kyverno `ValidatingPolicy` (`policies.kyverno.io/v1`) is checked: its `variables`, `validations`, `matchConditions` and `auditAnnotations` expressions are compiled in Kyverno v1.19.1's CEL environment, so unknown variables and functions called with the wrong arguments are reported as Kyverno reports them. Kyverno's other policy kinds are not yet supported (#322).
- A Kyverno `ValidatingPolicy` gets completion and hover with Kyverno's own variables and functions (`resource.`, `http.`, `image(` and the rest, and not `authorizer` or `params`), `variables.<name>` completes the policy's variables, and `object` and `oldObject` are typed from `spec.matchConstraints.resourceRules` as for a ValidatingAdmissionPolicy: fields down the tree, shared fields first for several kinds, `dyn` when a kind can't be known, and a warning naming the kind for a field it lacks (#323).

## [0.12.1] - 2026-10-10

### Fixed

- `go install` of the engine from the public repository works (#315).

### Changed

- The engine is licensed under Apache-2.0 and its module path is `github.com/JairJosafath/cel-lens/engine`; the README is the engine's page for contributors (#314).

## [0.12.0] - 2026-10-09

### Changed

- `internal/assist` lookup tables are immutable (#123).
- The macro catalog keeps macro receiver kinds (#173).

## [0.11.0] - 2026-10-09

### Fixed

- Quick fixes detect CRLF line endings in UTF-16 files (#284).

### Changed

- Each CEL expression is parsed once for diagnostic spans (#112).
- `internal/assist` shares one string-literal scanner (#246).

### Tests

- The unreadable-subfolder test runs as root too (#94).

## [0.10.0] - 2026-10-09

### Fixed

- UTF-16 bad characters are reported on the right document of a multi-document stream (#283).
- `procwatch` treats access-denied as alive on Windows (#244).
- The e2e binary directory is created with `os.MkdirTemp` (#109).

### Tests

- Marked overloads for namespaced functions are tested beyond `optional` (#280).
- The LSP test helpers fail the running subtest (#279).

## [0.9.2] - 2026-10-09

### Fixed

- UTF-16 YAML streams that hold several documents are checked (#263).

### Tests

- `firstBroken` is tested against every bad-character reason in `pkg/extract` (#262).

## [0.9.1] - 2026-10-09

### Fixed

- Hover marks the form of every optional call whose type can be told, including after `optMap`, `optFlatMap`, `or` and `value`, and for `optional.of` and `optional.none` (#250).

### Tests

- `TestComplete` covers group receivers (#251).
- The language server test asserts the column of bad-character diagnostics (#252).
- The assist test covers bracket matching with stray closers and escaped quotes (#253).

## [0.9.0] - 2026-10-09

### Fixed

- CI and the release workflow keep Go's temporary build files in `.gotmp` in the job's workspace, not in the shared `/tmp` (#270).
- A quote after a verbatim tag with a comma in a flow collection is read as quoted (#229).

### Changed

- The release workflow is updated by a Standard change (#239).
- `.gitea/workflows/check.yaml` and `.gitea/workflows/release.yaml` are updated by a Standard change (#273).

### Tests

- Every yaml.v3 bad-character reason that `badChar` relies on is pinned (#228).
- `TestCheckMalformedYAML` asserts the message and column of every YAML diagnostic (#225).
- A fixture covers an aliased message in an `x-kubernetes-validation` (#235).
- The language server assist test pins the broken-YAML diagnostic (#248).

## [0.8.0] - 2026-10-09

### Fixed

- Hover skips quoted brackets when it finds the receiver to type (#205).
- Completion resolves groups after keyword operators (#218).
- `procwatch` lint findings on Windows are fixed (#97).
- `entry` checks context cancellation while following merge keys (#128).

### Tests

- A file with a broken later document is tested end to end through the language server for assists (#208).
- A bad character is tested end to end through the language server (#209).

### Refactored

- `line.go` helpers share one string-literal scanner (#217).

## [0.7.0] - 2026-10-09

### Tests

- Fixtures cover aliases in `fieldPath`, `message` and flow-style validations (#106).
- Untyped-node costs are tested through `Check` with a whole CRD (#165).

### Docs

- The other optional-value functions link to their cel-go sections (#202).

## [0.6.1] - 2026-10-09

### Fixed

- A broken document's own YAML error is reported when a bad character follows it (#192).
- 'invalid length of a UTF-8 sequence' errors are placed on the offending character (#195).
- A quote after an anchor or tag in a flow collection is read as a quoted scalar (#193).

## [0.6.0] - 2026-10-09

### Fixed

- Completion resolves a parenthesised receiver and a literal-aware receiver (#172).
- The doc comment of `yamlfield.Field.Node` is accurate (#180).

### Tests

- Quick fixes from `codeAction` are tested against a different problem after `didChange` (#124).
- `Serve` is tested to stop on cancellation when a message is also ready (#152).
- The version-level root error texts are pinned (#130).
- `yamlfield.Tree.Remove` is unit-tested for alias values (#180).

### Documentation

- CHANGELOG headings carry release dates (#183).

## [0.5.1] - 2026-10-09

### Fixed

- `cel-lens-core check` prints a bad character's problem once, at its own line and column, instead of also naming the line where its document starts (#204).
- The hover on a function marks the form the call uses when the receiver or an argument is a call such as `quantity(...)`, an optional from `?.`, or a macro's loop variable (#186).
- The hovers on `sum`, `isLessThan`, `orValue`, `size`, `startsWith` and `all` link to their own section of the docs (#187).
- Quick fixes, hovers and completions work in the documents before a document with a bad character, as in a clean file (#203).
- A block scalar is no longer dropped when a comment separates its anchor or tag from the header (#190).
- Schemas that alias or merge a large `properties` map are handled in full (#179).

## [0.5.0] - 2026-10-09

### Fixed

- A block scalar keeps its range when the anchor or tag and the header are split by a tab or a line break (#107).
- Lines are counted the same way as the YAML parser for a lone CR and for Unicode line separators (#114).
- CEL in the documents before a bad character is still checked (#163).
- An apostrophe inside a plain flow scalar keeps the wrong-type range at the closing bracket (#161).
- "Invalid Unicode character" errors are placed on the offending character (#164).

### Tests

- Later documents are checked after a wrong-typed field (#117).

## [0.4.1] - 2026-10-08

### Fixed

- A YAML alias of a list in a text field, such as `description: *a`, no longer crashes the engine; it gets the wrong-type problem on the alias (#176).

## [0.4.0] - 2026-10-08

### Added

- Hovers link to the function's section of the docs (#143).
- Macro completions are offered only on receivers they apply to (#142).
- The hover marks the matching overload from the argument types too (#141).

## [0.3.0] - 2026-10-08

### Fixed

- Control-character and encoding errors are placed on the offending character (#147).
- A wrong-typed flow list or mapping is underlined from its opening to its closing bracket, an alias by its name, and a multi-line plain scalar by its first line, no longer to the end of the line (#148).
- Remembered costs are no longer reused for schema nodes without type information (#153).

## [0.2.0] - 2026-10-08

### Added

- Hovers on CEL functions and macros show more (#136).

### Changed

- Checking a large CRD with rules over their cost budget is about three times faster:
  costs are remembered without converting the schema again, sizing stops at the
  smallest number when it is already too big, and the background cost work only
  starts when a size is unlimited (#129).
- The fix and the advice on every over-budget rule are sized so the CRD total is
  solved too, extending the size fixes of 0.1.3 (#137).

### Fixed

- The parser blaming an empty line no longer sends an empty range (#113).
- The wrong-type diagnostic is placed on the value, not on the whole line (#116).
- A rule on a schema whose `type` is the wrong type is no longer checked against
  the type the engine guessed, so only the wrong type is reported (#115).

## [0.1.3] - 2026-10-08

### Changed

- codeAction answers from the diagnostics of the last check of the document
  version instead of checking it again (#44).
- Validation and cost explanation share one schema compile (#41).
- Expanding YAML merge keys checks for context cancellation (#72).
- Schema errors are reported in CRDs that have no CEL rules (#65).
- `initialize` reports the supported Kubernetes versions and the default (#92).
- The internal-name test covers member and namespaced completion (#49).

### Fixed

- Completion excludes all CEL reserved words (#48).
- The fix and the advice on the diagnostic that reports a CRD's total cost are
  sized so the total is under budget too (#46).
- The diagnostic for a CRD whose total cost is over budget explains why when no
  single rule is (#40).

## [0.1.2] - 2026-10-08

### Changed

- Diagnostic spans are computed from the checked AST (#36).

### Fixed

- Problems are placed on YAML aliases, not on their anchors (#38).
- Block-scalar indicators after an anchor or tag are honoured (#60).
- The end-to-end binary tests no longer time out under full-suite load (#61).
- Schema errors are placed correctly for property names that contain brackets
  (#66).
- Broken YAML on a blank-only line never gets an empty range (#67).
- Decode errors point at the right YAML field, fast and cancellable (#93).

## [0.1.1] - 2026-10-07

### Added

- The release job downloads every published asset and `checksums.txt` over the
  documented URLs, verifies the checksums and runs `--version` (#33).
- The language server caches parsed documents (#14) and reuses the base CEL
  environment across documents (#12).
- Finding CEL in CRD schemas follows YAML merge keys (#6).

### Changed

- `check` has clearer error messages and exits with the interrupt exit code
  when interrupted (#19).
- `check` accepts flags after paths and keeps walking past unreadable
  subfolders (#18).
- `lsp` stops promptly on interrupt (#16), rejects values on `--stdio` and
  reports every bad `--clientProcessId` (#32), and `Serve` stops on its own
  when the client process is gone (#31).
- The release-build test stays out of the fast test loop (#34).
- Standard changes to the check and release workflows and the Makefile (#88,
  #89).
- Fixture checks are tighter (#12).

### Fixed

- The JSON-RPC layer ignores client responses and bounds header size (#15).

## [0.1.0] - 2026-10-07

### Added

- The language server reports broken YAML as a problem at the place where it
  breaks, so a file whose YAML is broken never looks clean (#52).

[Unreleased]: ../../compare/v0.14.0...HEAD
[0.14.0]: ../../compare/v0.13.1...v0.14.0
[0.13.1]: ../../compare/v0.13.0...v0.13.1
[0.13.0]: ../../compare/v0.12.1...v0.13.0
[0.12.1]: ../../compare/v0.12.0...v0.12.1
[0.12.0]: ../../compare/v0.11.0...v0.12.0
[0.11.0]: ../../compare/v0.10.0...v0.11.0
[0.10.0]: ../../compare/v0.9.2...v0.10.0
[0.9.2]: ../../compare/v0.9.1...v0.9.2
[0.9.1]: ../../compare/v0.9.0...v0.9.1
[0.9.0]: ../../compare/v0.8.0...v0.9.0
[0.8.0]: ../../compare/v0.7.0...v0.8.0
[0.7.0]: ../../compare/v0.6.1...v0.7.0
[0.6.1]: ../../compare/v0.6.0...v0.6.1
[0.6.0]: ../../compare/v0.5.1...v0.6.0
[0.5.1]: ../../compare/v0.5.0...v0.5.1
[0.5.0]: ../../compare/v0.4.1...v0.5.0
[0.4.1]: ../../compare/v0.4.0...v0.4.1
[0.4.0]: ../../compare/v0.3.0...v0.4.0
[0.3.0]: ../../compare/v0.2.0...v0.3.0
[0.2.0]: ../../compare/v0.1.3...v0.2.0
[0.1.3]: ../../compare/v0.1.2...v0.1.3
[0.1.2]: ../../compare/v0.1.1...v0.1.2
[0.1.1]: ../../compare/v0.1.0...v0.1.1
[0.1.0]: ../../releases/tag/v0.1.0
