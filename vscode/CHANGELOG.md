# Changelog

Every user-visible change, newest first. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/): the `version` in `package.json`. Versions before 0.14.0 were released before the public repository existed, so they have no link.

## [Unreleased]

## [0.40.0] - 2026-11-14

### Added

- Giving a value a type of your own, the `.cellens` files and **Give it a type**, is the experimental feature `cellens-types`, which `cel-lens.experimental` lists with the other three. The setting's description and the README's settings table name it, and the README's _Experimental_ table says what it does and why it is experimental: the types are yours, and nothing checks them against a cluster. _Give a value a type_ in the README opens with how to turn it on.
- **cel-lens: Give a Value a Type…** with the feature off says so and offers **Turn on cellens-types**, which adds the name to `cel-lens.experimental` at the scope that already sets the setting (the workspace's, else yours). The command stays in the Command Palette, where people look for it.
- With the feature off, a workspace that holds a `.cellens` gets one notice per session from the engine, with the action **Turn on cellens-types**. The extension answers the action itself: it adds the feature to `cel-lens.experimental` at the scope that sets the setting, as the button of the notice about a misspelt name does.
- The README shows giving a value a type in `.cellens` with a GIF, `images/cellens-types.gif`: `params: dyn · Give it a type` above a rule of a ValidatingAdmissionPolicy, **Write a schema in .cellens…** picked, `.cellens` opening beside the rule, the schema typed with completion, and the line above the rule following to `params: {maxReplicas} · Edit`. The README also says what the engine writes for a new schema: a block with `properties:` and the cursor on the line under it.

### Changed

- **A workspace with a `.cellens` needs `cellens-types` in `cel-lens.experimental` from this version on.** Since v0.36.0 the types of a `.cellens` were on for everyone; now, without the feature, no `.cellens` is searched for or read, the values it types are `dyn` (hover says the feature is off), the line above a rule offers no **Give it a type**, **Edit** or **Remove**, and an open `.cellens` gets no diagnostics, hover or completion. Turning the feature on or off takes effect at once, with no restart.
- The extension ships `cel-lens-core` v0.32.0, which has all of the above, with nothing to set in `cel-lens.serverPath`.

## [0.39.0] - 2026-11-13

### Added

- The engine runs the examples in CI: `cel-lens-core examples [--format text|json] [--kube-version <1.x>] [--fail-on wrong|error|none] [--verbose] [paths...]` runs the rules of the CustomResourceDefinitions and ValidatingAdmissionPolicies of the files and folders given (the current folder with none) on their examples, with the results the editor shows, and exits 1 when a verdict is not what its example expects, so a repository's examples are a test. It runs the check of the examples once over the workspace the paths make up (a folder is walked as the editor's workspace scan walks it; a file given alone is the workspace of its own, but for the folders given). The text lists the problems of the examples and each rule with a wrong verdict or an error, the wrong ones first, with the API server's message and paths relative to the working folder, and ends with `12 rules, 40 examples: 2 wrong, 1 error, 3 not run`; with nothing wrong that line is all it prints, and `--verbose` lists every result. `--format json` prints one object, the answer of `cel-lens.exampleResults` for every file with rules and the `problems` of the examples. `--fail-on` picks what fails the run: `wrong` (the default) a verdict that is not what its example expects, a rule that stops with an error on an example that expects to be allowed, and a problem of an example; `error` also a rule that stopped with an error on an example that expects to be refused; `none` never; a rule that did not run never fails. Exit code 0 means nothing failed, 1 that a verdict did, 2 a usage or read error, 130 an interrupted run. It runs every example it finds, not only the first 100 of a CRD or that a policy matches, which is the editor's cap; files over 1 MiB and past the first 20000 are not read, which stderr warns of and the last line counts, and the exit code ignores.
- `cel-lens-core examples` reports each CustomResourceDefinition, ValidatingAdmissionPolicy and ValidatingAdmissionPolicyBinding of the workspace that the API server cannot read, which the engine used to leave out without a word: a CI check passed on a policy the API server refuses. For a field of the wrong type the problem is the one the editor shows, at the word; for a CRD that the API server refuses for anything but its rules it says what it refuses, at the first line of the document. It counts as wrong, so the default exits 1. The editor says the same on such a file of the workspace that is not open, where its example results said nothing for it; an open document has the problem already.
- The README says how to run the examples in CI: how to install `cel-lens-core` (`go install github.com/JairJosafath/cel-lens/engine@v0.31.0` or the release file), what it runs, and its exit codes.

### Changed

- The extension ships `cel-lens-core` v0.31.0, which has all of the above, with nothing to set in `cel-lens.serverPath`.

### Fixed

- A plain `y`, `n`, `yes`, `no`, `on` or `off` (and `Y`, `Yes`, `YES`, `N`, `No`, `NO`, `On`, `ON`, `Off`, `OFF`) is read as the boolean that kubectl and the API server read, not as a string. The engine read the file as YAML 1.2, where `n` is a string, so a policy whose audit annotation `key` is `n` passed the check, as did a policy label `tier: yes`, a binding's `tier: on` and a string enum `[y, n]` in a CRD, whose entry the API server takes as a boolean, so a value equal to the word is refused. A field that wants a string is now an error at the word (`spec.auditAnnotations[0].key: expected a string, got a boolean: kubectl reads an unquoted n as one (YAML 1.1), so quote it if a string is meant`), the CEL of the document is still checked, and a string enum with such an entry is a warning at the entry. A quoted word, or one tagged `!!str`, is a string, as before. The schema's problems on an example of a CRD that come of such a word name its line, and an example the engine used to drop from the example results for one is no longer dropped. Not covered yet: a mapping key such as `on:` is still read as written, where kubectl sends `true`.
- A CustomResourceDefinition whose version says `served: yes` or `storage: on`, or another plain `y`, `n`, `yes`, `no`, `on` or `off` where the API server wants a boolean, is read as the API server reads it: the check no longer reports `expected a boolean, got a string` for it, and its examples run, which they did not, as the CRD was left out. A CEL expression that is a plain `yes` is one problem, a boolean where a string is wanted, and no longer also an expression that does not compile.

## [0.38.0] - 2026-11-13

### Added

- Example results for a ValidatingAdmissionPolicy, as for a CRD's rules: what each validation, match condition and audit annotation of a policy does to the example objects of the workspace. The verdict is the API server's own: the policy is compiled as its ValidatingAdmissionPolicy plugin compiles it, for the Kubernetes version picked, and the plugin's own dispatcher and matcher admit each example as a request by no user in particular, so `matchConditions`, `variables`, `validations` with `message`, `messageExpression` and `reason`, `failurePolicy`, `auditAnnotations` and the binding's `validationActions` are its. The examples are the documents the policy's `matchConstraints` match as the API server matches them (resource rules, `excludeResourceRules`, `matchPolicy`, `namespaceSelector` against the workspace's Namespace documents, `objectSelector`), of built-in kinds and of the workspace's CRDs, outside the policy's own file, at most 100; a namespace without a Namespace document has the one label the API server gives every namespace, `kubernetes.io/metadata.name`. Each runs as a create, or as an update from the last example of the same object that the API server would have stored.
- Each example runs under each ValidatingAdmissionPolicyBinding of the workspace that binds the policy and matches it, with the params its `paramRef` names or selects: the documents of the policy's `paramKind`, each param a run of its own, and `parameterNotFoundAction` followed (`Deny` refuses the example, `Allow` runs nothing). A failed validation refuses the example under `Deny`, allows it with the API server's warning under `Warn` and records the audit annotation under `Audit`; an audit annotation's value never refuses, but one that fails under `failurePolicy: Fail` refuses the example under any binding. With a `selector` that matches several params they are taken in the order of their namespaces and names, where the API server has no fixed order, so when several fail under `Deny` the message it refuses with may be another param's. With no binding the policy runs as if bound with `Deny` and no params.
- The line above each validation, match condition and audit annotation, its hover and the warnings show it, under the setting `cel-lens.exampleResults`. The line gains `warns 1 example`, `audits 1 example`, `excludes 1 example` (a match condition is false), `annotates 1 example` and `under binding <name>` (`under 2 bindings`, `under no binding`), with the words of a CRD's rules; an example run under several bindings or params counts once, as the first of what they did. The hover and the warnings name the binding and the param. An example that expects to be refused is right when the schema, a rule of its CRD or any policy under any binding refuses it; a warning or an audit annotation refuses nothing. An expression that runs on no example says why: `not run: excluded by matchConditions`, `not run: no params`, `not run: no binding gives params`, `not run: binding refuses the request`, `not run: examples refused before the rules`, `not run: needs a conversion` (a built-in kind matched in another version, or a custom resource whose CRD converts with a `Webhook`), `not run: reads authorizer` (cel-lens runs no authorizer check) and `not run: policy refused`. With a policy of 20 validations over 100 examples on 4 shared CPUs a run takes about 40 ms from an empty cache and 6 ms with nothing changed, and hover and the lines of the open policy take as long while it runs as while nothing runs.
- The picker the part above a policy's expression opens lists each example under each binding and param, the wrong verdicts first. A line has its own icon and the verdict in words that fit it (`refused`, `warned · allowed, with a warning`, `audited · allowed, audited`, `annotated · an annotation has a value`, `excluded · a match condition is false`), the binding it ran under after the file (`big.yaml:1 · deny-limits`), and, in its detail, the param it ran with and the API server's answer: what it denied with, its warnings and its audit annotations. Before, a result with one of these verdicts was left out of the picker.
- The README describes example results for admission policies (examples, bindings and params, what `Deny`, `Warn` and `Audit` do, the words of the line), with a picture of the picker of a policy, and _Not supported yet_ lists only what is still not covered: example results for a MutatingAdmissionPolicy and for Kyverno's policies. A document of a built-in kind is decoded strictly, as the API server decodes a request, but gets no defaulting or validation of its own, so a policy that reads a field the API server defaults, such as a Deployment's `spec.strategy`, sees it absent when the document does not set it.

### Changed

- The extension ships `cel-lens-core` v0.30.0, which has all of the above, with nothing to set in `cel-lens.serverPath`. The engine is about 7 MB bigger (52.6 MB to 59.7 MB), because it carries the API server's admission code that runs a policy, so the extension takes about 7 MB more on disk for each platform, and its `.vsix`, which is compressed, is 1.5 to 1.7 MB bigger to download; no informer is created or run.

### Fixed

- A release made by the `release.yml` workflow of JairJosafath/cel-lens no longer shows a failed run when it is complete and signed. Its `sign` job started `sign-release.yml` and watched the run at once, and GitHub answered HTTP 404 for a run it had not yet made readable (run 43465064181 of core v0.30.0). The job now reads the run until GitHub can answer, 24 reads 5 seconds apart (about 2 minutes), then watches it; a run that stays unreadable fails the job, saying to sign by hand, and a run that fails still fails it.

## [0.37.0] - 2026-11-13

### Added

- Example results: what each rule of a CRD's `x-kubernetes-validations` does to the example objects of the workspace, with the engine running the API server's own validator. There is no cluster and nothing to register. The examples of a CRD are the YAML documents of the workspace of a version it serves, found as the examples under **Examples in the workspace** of **Give it a type** are, at most 100 a CRD and never in the CRD's own file. Each is expected to be allowed, unless a folder or a word of its file name is `invalid`, `bad`, `refused`, `rejected` or `denied`, or a `# cel-lens: refused`, `# cel-lens: refused "part of the message"` or `# cel-lens: allowed` comment at the top of the document says otherwise; another `# cel-lens:` comment there is an error naming the three forms. The same kind, namespace and name twice in a file is an update from the first, as `kubectl apply -f` does, so a rule that reads `oldSelf` runs on it with the API server's ratcheting. Each example is checked with the API server's own decoding, defaulting and validation of a custom resource, for the Kubernetes version picked, before its CEL rules; what it refuses is an error on the example at the field, in the API server's words, unless the example expects to be refused. The CRDs are those of the open documents and of the workspace, whatever the experimental features. The check runs in the background once typing pauses, so hover, completion and the lines above the rules take as long with 100 examples as with none, except that a request that arrives just as a check starts, in the first 25 ms or so after typing pauses, takes 1.5 to 4 ms longer than its usual 4.5 ms or so. Only the rules of CRDs run on examples: no admission policy is covered yet.
- The line above each rule of a CRD gains a last part: `3 examples pass`, or the wrong verdicts first, `refuses 1 example it should allow · 2 pass`, `allows 1 example it should refuse`, `1 example errors`, or, for a rule that runs on no example, why: `not run: needs an update example`, `not run: CRD refused`. A refused example that expects to be refused passes, and a rule without examples gets no part. The part shortens first (`2 wrong · 3 pass`, then `2 wrong`) to keep the line within 80 characters. Clicking it opens a picker titled after the rule's line, such as "Example results of the rule on line 22", that lists the examples that are not as expected first and then the others, each with its verdict, its file and line, and the API server's message. The picker opens at once and says the results are being computed until the engine answers; picking an example opens it at its line beside the rule (in the editor group that already shows it, if one does). A part whose rule changed since the line was drawn says so and asks for a click on its part again, and never shows another rule's results.
- The rule's hover gains a section listing each example, linked to its file and line, with its verdict and the API server's message, the wrong verdicts first, at most 10 and then `and N more`. A wrong verdict is a warning, source `cel-lens`, on the rule (`refuses config/samples/big.yaml:3, which expects to be allowed: <message>`) and at the example's first key (`expected to be allowed, but the rule "self.size <= 10" refuses it: <message>`, or `expected to be refused, but nothing refuses it`), so it shows in the Problems view; the schema's own problems on an example are errors on it. The warnings and the lines follow the examples, the CRDs and the Kubernetes version as they change.
- The setting `cel-lens.exampleResults` (on by default, resource scope) and the command **cel-lens: Toggle Example Results** turn example results off and on, as for the inline info: the part, the hover's section, the warnings and the engine's check of the examples, so nothing about the examples is read or run while it is off; open editors follow at once, without a reload. The status bar menu shows **Example results: on** or **off** and flips it. The toggle writes the scope that sets the value in force for the open file, or the user settings. With the setting off, the part says so and offers a button that turns it on. A workspace folder that turns the results off loses the part of the line and gets the off message from the picker; the engine has one switch for the window, so the hover and the warnings stay while another folder has them on.
- The README describes example results, with a picture of the line above a rule with its part and one of the picker it opens: how the examples are found, how one says it expects to be refused, updates, the line, the hover, the warnings and the toggle. Dry runs stay under _Not supported yet_ for admission policies only.

### Changed

- The extension ships `cel-lens-core` v0.29.0, which has all of the above, with nothing to set in `cel-lens.serverPath`. The engine checks CRDs for the same Kubernetes version at once, where it checked one at a time: the check of an edited open CRD no longer waits for the background check of a CRD that is not open, which the examples make, and with 20 such CRDs of 50 rules and 5 examples each the worst wait for a change and a hover in a session is 12 to 23 ms, as without examples, where it was 21 to 48 ms. Changing `cel-lens.kubernetesVersion` has the engine work the results out again for the new version.

## [0.36.0] - 2026-11-13

### Added

- A quiet line above each CEL rule, from the engine: what `self` (or a policy's `object`) is, whether the rule compiles or how many problems it has, and the values it reads as `dyn`, such as `self: Widget.spec · compiles` or `params: dyn · Give it a type`. A line shows at most two values; **+N more** lists the others, titled after the rule's line (such as "More of the rule on line 16"), each with its action, and picking one runs it. Its first part opens the problems peek at the rule's first problem, or the hover there when VS Code's problem order would open another problem first, or the rule's hover when it has no problem. A part whose type comes from `.cellens`, such as `variables.limit: int`, opens that entry beside the rule, selected. The setting `cel-lens.inlineInfo` (on by default) and the command **cel-lens: Toggle Inline Info** turn the lines off and on; open editors follow at once, without a reload. The status bar menu shows **Inline info: on** or **off** and flips it. The toggle writes the scope that sets the value in force for the open file (its workspace folder, the workspace or the user settings), or the user settings when none does.
- `.cellens`, a file that says what a value is where Kubernetes gives it no type: commit it, so everyone gets the same types. The engine reads every `.cellens` of the workspace and, for the value an entry names (an `x-kubernetes-int-or-string` value of a CRD, or `params`, `object`, `oldObject` or `variables.<name>` of a ValidatingAdmissionPolicy), checks, completes and explains with the type the entry gives it: a CEL type such as `{name: string, replicas: int}`, a built-in kind or a kind of a workspace CRD (`kind: apps/v1 Deployment`), an OpenAPI schema written as a CRD's, or a Go struct of the workspace (`go: ./api/v1#QuotaSpec`). A policy variable that reads a typed value has the type its expression then returns. A problem that only the type shows is a warning naming the `.cellens` file and line. The problems of a `.cellens` itself show on it, with their line and column, and the other entries keep their types while one is being typed. A schema is checked as the API server checks a CRD's, and what it refuses is an error at the offending key, in its words. A Go type is read from the files of its package without building or running anything, and a field it cannot read is a warning naming it, the rest being used. Hover says where a type comes from, and on a policy variable whose expression fails only with the types of `.cellens` it says so and names the entries. Editing a `.cellens` has the engine's completion and hover for its keys, files, documents, variables, kinds, Go types and schema keywords.
- **Give it a type** on the line above a CEL rule gives a `dyn` value a type in `.cellens`. It opens a picker titled after the value, such as "Give params a type": **Write a schema in .cellens…** (OpenAPI), **Use a Go type…** and **Write a short type in .cellens…** first, then the built-in kinds and the kinds of the workspace's CRDs, each with its icon, its group and version (and `alpha` or `beta` when it is not GA), and its description; typing `deploy` finds Deployment. The picker opens at once and lists the Go types and kinds as the engine gives them. **Write a schema in .cellens…** and **Write a short type in .cellens…** have the engine add the entry with a type to fill in, such as `type: "{}"`, and open `.cellens` beside the rule (in the editor group that already shows it, if one does) with the cursor where the type goes, so the type is written in a real editor and the line above the rule follows. **Use a Go type…** opens a picker of the Go struct types of the workspace folder of the `.cellens`, each with its folder and doc comment, which starts with what was typed in the first picker and has a **Back** button to it. A Go type or a kind is written at once. A value of a CRD is offered `int` and `string` only. **Edit** opens the same picker on what the value has, where the two lines that write in `.cellens` open it at the entry; **Remove** removes the entry at once. The engine writes `.cellens` in the editor, so **Undo** there takes a change back. A change the engine refuses, such as one made while another edit of `.cellens` waits, is shown once with the reason. The README shows how to give a type in each form.
- **Give it a type** can take an example. Under **Examples in the workspace**, the picker lists the documents of the workspace of the value's kind, such as `config/samples/limit.yaml` with `rules.example.com/v1 ReplicaLimit` and the line it starts at; **Paste an example…** opens an editor beside the rule, named after the value (such as **Example of params.yaml**) and kept in memory, so closing it never asks to save, with a comment saying what to paste and **Use this example as the type** above it and in its editor title (and **cel-lens: Use This Example as the Type** in the Command Palette, on that editor only). The engine works out an OpenAPI schema from the example, writes it as the value's `schema:`, and `.cellens` opens beside the rule at the entry, to refine; values that disagree are merged as Kubernetes accepts both, or left without a type with a note naming the field and the kinds; what the example leaves open, such as the items of an empty list, shows once as an information problem at its key in `.cellens`, until it is edited there. An example over 1 MiB, the most the engine reads, is refused before anything reads it. Using a pasted example closes its editor; closing it writes nothing, even while the engine works on it, and a window reload closes the examples of the window before. A whole resource of the value's kind pasted for a value inside it, such as a ReplicaLimit for `params.spec.config`, gives the schema of that value, also from a YAML list of resources; a resource of another kind, such as an object reference, is the value itself, which the engine tells from the value's kinds and says in a note; when it does not know them, a paste with `apiVersion` and `kind` at its root is read as a resource, and as the value itself when it has no value at the value's path. A type written at once (a Go type, a kind, `int` or `string`, or one from an example), or removed, is saved when the `.cellens` the line names had no unsaved edits and nothing but that write changed it; nothing is saved when the engine writes another `.cellens`.
- The command **cel-lens: Give a Value a Type…** lists the values the rules of the open YAML file read, from the line above each rule, and gives the one picked a type, or edits it; with none, it says so.

### Changed

- The extension ships `cel-lens-core` v0.28.0, which has all of the above, with nothing to set in `cel-lens.serverPath`. Since v0.25.1 it also types `object` and `oldObject` of a Kyverno ValidatingPolicy from a workspace CRD, with `kyverno` and `crd-typed-policies` on, and its hover says `crd-typed-policies` is off when a workspace CRD would type `object`.
- The README shows the line above the rules of a ValidatingAdmissionPolicy and giving a value a type from an example of the workspace, in two new pictures, and every other picture is retaken with the v0.28.0 engine, so each shows the line above the rules. The pictures start from a clean editor, and their backgrounds are flat.
- The tools the extension is built, tested and packaged with take the minor and patch updates Dependabot proposed: `@vscode/vsce` 4.1.0, eslint 10.14.0, playwright 1.65.0, prettier 3.9.10, tinybench 6.2.3, typescript-eslint 8.72.1, vitest 5.1.0 and `vscode-languageserver-protocol` 3.18.5. `@types/vscode` stays 1.100.0, the VS Code that `engines.vscode` asks for. None of them is part of the extension: the client it carries keeps its own `vscode-languageserver-protocol` 3.18.4.

### Fixed

- The workspace scan for CRDs, and a CRD file read again after a change on disk, read only regular files of at most 1 MiB inside the workspace folder: a symbolic link to `/dev/zero`, to a pipe, out of the folder or to a bigger file is skipped, where before it could hold the engine or fill its memory.
- A batch of file changes that changes CRDs and creates a folder while a workspace scan runs is published once, when the scan of that folder ends, with that folder's CRDs: the end of the scan in progress no longer publishes the state without them. Repeated notifications of created folders keep the progress of the scan they start.
- A policy variable whose expression compiles, but not with the type of a kind the policy matches, such as `object.spec.replicaz` for a Deployment, gets a warning at its expression that names the kind and says it is cel-lens's own check. Hover said such a variable does not compile and nothing said why.

### Security

- The bundled engine is built with Go 1.26.11 and newer modules, past the advisories the OSV database lists for the versions v0.25.1 was built with, which is what a vulnerability scanner of the `.vsix` sees: GO-2026-6599, GO-2026-6600, GO-2026-6603, GO-2026-6604, GO-2026-6605, GO-2026-6607, GO-2026-6608, GO-2026-6609, GO-2026-6610, GO-2026-6611, GO-2026-6612, GO-2026-6613, GO-2026-6617, GO-2026-6809 and GO-2026-6859 in the Go standard library (`net/http` and its HTTP/2, `net/textproto`, `mime/multipart`, `html/template`, `crypto/tls`, `crypto/x509` and `os`), GO-2026-6603, GO-2026-6610, GO-2026-6611, GO-2026-6612, GO-2026-6617, GO-2026-6860 and GO-2026-6866 in `golang.org/x/net`, GO-2026-5932 in `golang.org/x/crypto`, GO-2026-6505 in OpenTelemetry's SDK and trace exporters, and GO-2026-6865 in `github.com/klauspost/compress`. Three advisories for Kyverno that the Go vulnerability database gives no fixed version, GO-2025-3615, GO-2026-5337 and GO-2026-5594, do not apply: they are about requests and reads of Kyverno's admission controller, and the engine builds only `pkg/cel/compiler` and `pkg/toggle` of it; the engine's `osv-scanner.toml` ignores each with its reason. The Kubernetes libraries, `go-containerregistry`, `go.yaml.in/yaml/v3`, `golang.org/x/sys` and `docker/cli` are newer minor and patch releases too, and what the engine reports does not change: the built-in kinds of Kubernetes 1.33 to 1.37, their schemas and descriptions, and the CEL functions of each version are the same.
- The packages the extension is built, tested and packaged with are updated past their known vulnerabilities. Vite 8.3.7 replaces 8.3.2, qs 6.17.1 replaces 6.16.0, and `@azure/msal-node` 7.2.1 (with `@azure/identity` 4.14.1) replaces 6.0.1; `overrides` in `package.json` give `eslint`, `@eslint/config-array`, `@typescript-eslint/typescript-estree` and `@vscode/vsce` minimatch 11.0.5 in place of 10.2.6. They fix GHSA-587h-7wmj-h5pm, GHSA-698v-4r3x-3vcv, GHSA-9jrq-w75r-8gcw, GHSA-r825-w8mm-5pxw, GHSA-rq7h-c2jc-7f22 and GHSA-vfpm-58rq-9qcg (Vite and the launch-editor inside it), GHSA-6pq8-jhm2-2vcw (qs), GHSA-xcx6-jx8p-x3h5 (`@azure/msal-node`) and GHSA-g3hv-75wg-m249 (minimatch). All of these are development tools.
- One advisory stays, and does not apply: `vscode-languageclient`, which is part of the extension, keeps minimatch 10.2.6, as it calls `Minimatch.makeRe()`, which minimatch 11 removed, and has no release without it. GHSA-g3hv-75wg-m249 is a denial of service through a glob pattern an attacker chooses, and the client matches only the extension's own patterns and the ones the engine registers. `osv-scanner.toml` ignores it for that copy until 2027-03-01, with this reason; `npm audit` still reports it, as 2 moderate vulnerabilities (minimatch, and `vscode-languageclient` for needing it).
- The files of a release can be checked. Each is signed with Sigstore by the repository's own `sign-release.yml` workflow, under the workflow's own identity, so there is no key to keep, and a signature made from a branch other than `main` is refused by the command; from this release on, a release is built and published by the repository's own `release.yml` workflow, and its files carry build provenance in `provenance.intoto.jsonl`. `SECURITY.md` of JairJosafath/cel-lens, under "Verifying a release", has the `cosign verify-blob` command for a signature, which says that the file is the one the repository's workflow saw when it signed it and not how it was built, and the `gh attestation verify` command for the provenance, which says that a run of `release.yml` built the file, and which run.
- The repository is watched: CodeQL analyses the engine's Go, the extension's JavaScript and TypeScript and the workflows on every push to `main`, every pull request and every week; Dependabot proposes updates every week for the engine's Go modules, the extension's npm packages and the GitHub Actions, and its pull request is not merged on GitHub, as the bump is made in the maintainers' source and the next published release carries it; the workflows pin `github/codeql-action` v4.39.3, `actions/checkout` v7.0.1, `actions/setup-go` v7.0.0 and `actions/setup-node` v7.1.0, each at the commit of its release; and a `govulncheck` job in CI fails on a call into code with a known vulnerability that `engine/osv-scanner.toml` does not leave aside.

## [0.35.0] - 2026-11-04

### Changed

- The README says the engine logs many files skipped for their size in one summary line, and a single file on its own line.
- The extension ships `cel-lens-core` v0.25.1: a created folder below a hidden folder or `node_modules` is skipped by the workspace CRD scan, and a CRD file there edited on disk while closed gives its new schema after a workspace rescan.
- The `server_controller.test.ts` tests use `settle()` or fake timers instead of `vi.waitFor`; no behaviour changes.

## [0.34.1] - 2026-11-03

### Changed

- The engine error handler tests use a deterministic `settle()` helper instead of `vi.waitFor`; no behaviour changes.
- The `test/vscode.ts` stand-in can queue the choices a notification returns; no behaviour changes.

## [0.34.0] - 2026-11-03

### Added

- The notification about unknown names in `cel-lens.experimental` has a Replace button when every unknown name has a suggestion; it rewrites those names in the setting at the scope (user or workspace) that set them.

### Changed

- The README's `crd-typed-policies` row names the background scan, the 20000-file cap and the 1 MiB per-file limit, and says open documents are checked again when the scan ends.
- The engine error tests cover the log line written when the message button cannot be followed; no behaviour changes.
- The workflow tests use the shared `isRecord` helper; no behaviour changes.

## [0.33.0] - 2026-11-03

### Added

- The notification about an unknown name in `cel-lens.experimental` suggests the closest known name when there is one, such as `kyverno` for `kyvernoo`.

### Changed

- The engine tests use the `test/vscode.ts` `LogChannel` stand-in instead of cast log mocks; no behaviour changes.
- One shared `isRecord` narrowing helper serves all of `src`; no behaviour changes.

## [0.32.0] - 2026-11-03

### Changed

- The extension ships `cel-lens-core` v0.25.0: with `crd-typed-policies` on, `object` and `oldObject` of a ValidatingAdmissionPolicy that matches a custom resource are typed from its CustomResourceDefinition in the workspace, so a wrong field gets the API server's warning; a created folder is scanned for CRDs in the background.

## [0.31.0] - 2026-11-03

### Added

- A name in `cel-lens.experimental` that the engine does not know is reported once in a notification naming it, with a button that opens the setting.

### Changed

- The end-to-end test reads the contributions in `package.json` through a pure parser; no behaviour changes.

## [0.30.0] - 2026-11-03

### Changed

- The engine's workspace scan-cap warning is also written to the cel-lens log, so Show Logs lists it with the skipped files even after the notification is dismissed.
- An end-to-end test checks that the engine reads the CRDs below a folder created in the workspace; no behaviour changes.

## [0.29.0] - 2026-11-03

### Changed

- The extension ships `cel-lens-core` v0.24.2: a created folder that is hidden or named `node_modules` is skipped, and a workspace scan that skips many large files logs one summary line instead of one per file.
- The `experimentalFeatures` setting is read through a pure parser in `src/kubernetes_version.ts`; behaviour is unchanged.

## [0.28.1] - 2026-11-03

### Changed

- The extension ships `cel-lens-core` v0.24.1: the engine reads the CRDs below a folder created or renamed in the workspace, and a workspace rescan keeps the CRDs of a folder it could not read instead of forgetting the others' deleted CRDs.

## [0.28.0] - 2026-11-03

### Changed

- The engine's warning that it stopped scanning the workspace at its file cap now has a Show Logs button, and appears once per engine start.

### Fixed

- A test pins the experimental feature names against the engine's `initialize` result.
- The README Experimental table check reports a missing or blank Why cell by name.

## [0.27.3] - 2026-11-02

### Changed

- The picture script guards its waits against a zero timeout at the source.

### Fixed

- A test checks that every hold name in `take.ts` resolves to an import from `src/gif_plans.ts`.
- `experimentalReasons` and `descriptionReason` are unit-tested in `experimental_table.test.ts`.

## [0.27.2] - 2026-11-02

### Changed

- The extension ships `cel-lens-core` v0.24.0: the engine warns once when the workspace scan for CRDs stops at its file cap, and logs a message naming a file it skips for size, so a params type that stays `dyn` has a visible reason.
- The picture script builds the `partHidden` page expression in one helper, and `withTimeout`'s doc comment is back above it.
- The picture script's retry loops treat a zero remaining time as the deadline.

### Fixed

- The `crd-typed-policies` description no longer says the workspace scan blocks the engine's start; it gives the README's reason, as do the `kyverno` and `fitting-completion` descriptions.
- The picture script gives up at once when the empty answer's wait has no time left, and says the message stayed.
- The numeric-hold check in `picture_script.test.ts` no longer trips on comma-bearing text or arithmetic.

## [0.27.1] - 2026-11-02

### Added

- An end-to-end test checks that the engine forgets the CRDs under a deleted folder.

### Changed

- The picture script's `hideSideBar` also uses `partHidden` for its early return.
- The picture script has one helper for the first line of an error's message.
- A lint rule forbids numeric hold literals in `test/pictures/take.ts`, so every hold goes through `src/gif_plans.ts`.
- `compareExperimentalRows` accepts a color note that names only `cel-lens.colorStyle`.

## [0.27.0] - 2026-11-02

### Changed

- The picture script's `hideSideBar` opens the palette again with `F1` and retypes the command when a busy workbench closed the palette while the side bar still showed, all within the step's one deadline; when that runs out, the error says the palette kept closing before it listed the command.
- The `fitting-completion` setting description now says "completion", as the README row does. The test comparing the two counts "diagnostics" as checks and "typed" as typing, and no longer takes "offers" alone as completion.
- Each quick-fix retry in the picture script waits for the empty answer to go away before asking again, and fails with the no-fix message when it stays. Nothing changes in what the editor shows.
- `compareExperimentalRows` accepts a color-only note only when it has no sentence break and names no engine capability such as hover, completion, diagnostics, checks or fixes. Nothing changes in what the editor shows.

## [0.26.0] - 2026-11-02

### Changed

- The extension ships `cel-lens-core` v0.23.0: the engine scans the workspace for CRDs in the background, and forgets the CRDs of a deleted or renamed folder, so a policy no longer keeps a warning from a CRD that is gone.
- `npm run links` shows the network reason (such as `getaddrinfo ENOTFOUND`) after `fetch failed` on the FAIL line of an unreachable host.
- The picture script treats an element hidden by an ancestor as not rendered, shares one page function for "part is off screen", names the reason when its give-up screenshot fails, and names each screenshot by its picture file. A unit test pins the planned lengths of `colors.gif` and `completion.gif`.
- An end-to-end test checks that `object` is typed from each matched kind.

## [0.25.0] - 2026-10-31

### Added

- `npm run links` fetches every link in `CHANGELOG.md` and `README.md` and fails on a dead one; the release steps run it before tagging.

### Changed

- The picture script waits for the light bulb and asks again when the quick fix list comes back empty.
- The `kyverno` row of the README says the extension colors Kyverno documents with the feature on and not with it off.
- `CONTRIBUTING.md` gives a sparse clone of the public monorepo at `core-v<version>` for `CEL_LENS_ENGINE_DIR`, pointing the variable at its `engine/` folder, and documents the variable in one section.

## [0.24.3] - 2026-10-31

### Changed

- The extension ships `cel-lens-core` v0.22.0, which changes only the engine's own tests, checks and changelog; nothing changes in what the editor shows.
- Every wait in the picture script goes through one named-timeout helper; nothing changes in what the editor shows.
- `docs/project-notes.md` mentions `clickWhenReady`, and a unit test covers the GIF planned-length guard through the pure timeline module; nothing changes in what the editor shows.

### Fixed

- The picture script no longer gives up when a side bar hides by itself before the palette lists its close command, and it still stops with an error naming the bar when one stays on screen.
- When the picture script gives up it saves a screenshot of the page under `out/pictures/` and prints its path.

## [0.24.2] - 2026-10-31

### Changed

- The extension ships `cel-lens-core` v0.21.1, which fixes the engine's own tests and changelog links; nothing changes in what the editor shows.
- The end-to-end test covers a per-kind type-check warning, and a test checks that `cursorLineText` serialises standalone for the page; nothing changes in what the editor shows.
- The `crd-typed-policies` description in Settings now mentions hover, as the README does. A test compares the checks, hover, completion and typing each `cel-lens.experimental` description names with the README's "What it does" cell, and the color check accepts colored and coloring.

### Fixed

- The picture script screenshots a hover or a list (quick fixes, completion) only once it is fully shown: rendered, opaque and with no animation still running, so a slow machine no longer catches it half faded in. A widget that never settles fails the run with a message that names it.
- The picture script waits after its undos for the cursor line to be the fixture's line before the last, taken from the fixture file, not read from the editor at a moment the cursor may not have moved yet.
- The store page says again that a Kyverno policy gets no colors while the `kyverno` feature is off: the experimental table's kyverno row names colors, and the stable table's Colors row says a Kyverno policy is colored only with `kyverno` on. The test that compares the table with the engine README allows a note about colors on top of the engine's text.

## [0.24.1] - 2026-10-30

### Changed

- The README says that Kyverno `ValidatingPolicy` documents are colored only with `kyverno` in `cel-lens.experimental`, and the `kyverno` description in Settings says it colors them. A test keeps the setting's feature names and its wording about colors in step with the README's experimental table.
- `contributing.test.ts` reads each section of `CONTRIBUTING.md` through one shared helper.

### Fixed

- The picture script's wait for a line count no longer depends on scrolling, and its remaining Playwright action timeouts name the state they wait for.
- A README GIF plays the same length on a slow machine as on a fast one: its clock is the pacing of the picture script, not the wall clock, and the script stops when a GIF is planned to play 8 seconds or more.

## [0.24.0] - 2026-10-30

### Changed

- The extension ships `cel-lens-core` v0.21.0: a type-check warning is one line per matched kind naming that kind, as the API server writes it.
- The README's `kyverno` and `fitting-completion` rows say what the engine README says. A test compares the experimental features table with the engine's when `CEL_LENS_ENGINE_DIR` is set, so the next drift fails at the version bump.
- The unused `@types/markdown-it` dev dependency is gone.
- A test pins the CHANGELOG rules of `CONTRIBUTING.md`.
- `CONTRIBUTING.md` tells contributors to export `CEL_LENS_ENGINE_DIR` to a `cel-lens-core` checkout at the pinned tag before `make check`, so the public-repo layout test catches a broken link before the push.

### Fixed

- The picture run waits on editor state instead of fixed pauses.
- The Changelog's version links open only for versions the public repository has tags for: versions before 0.14.0 have no link, and 0.14.0 links to its release page.

## [0.23.0] - 2026-10-30

### Changed

- The version links of this file point at the public repository's `vscode-v` tags, and its lines no longer carry issue references.

### Fixed

- The picture run closes the side bars with their close commands and fails when the secondary side bar stays open, so no picture shows an empty CHAT panel.

## [0.22.1] - 2026-10-30

### Changed

- The extension ships `cel-lens-core` v0.20.1: a wrong `params` field is warned about once per matched kind, as the API server does.
- The public repository's CI runs once per publish, not again for each tag, and Scorecard runs weekly and on demand.

## [0.22.0] - 2026-10-30

### Changed

- The extension ships `cel-lens-core` v0.20.0.
- CI fetches the pinned engine source and sets `CEL_LENS_ENGINE_DIR`, so a pull request that links to a file the engine lacks fails the layout test.
- The `releaseDownloadUrl` comment names no engine version, and the README says the `crd-typed-policies` scan runs only while the feature is on.
- `CONTRIBUTING.md` documents `CEL_LENS_ENGINE_DIR`.
- Tests: one GIF frame walker serves `gifFrameCount`, `gifDelaysMs`, `gifDisposals` and `gifColorTables`.
- Tests: the public-repo Markdown link test parses Markdown with a CommonMark library instead of regexes.
- The CI workflow change is merged as a Standard change.

## [0.21.0] - 2026-10-30

### Changed

- `make release-version` fails when `CHANGELOG.md` has no heading for the tagged version.
- Tests: `describeFailure` is tested directly, so the dual-stack download test no longer depends on the runner's network.
- The public-repo Markdown link test resolves the remaining CommonMark link forms.
- The Makefile change is merged as a Standard change.

### Fixed

- Near-black pixels no longer vanish in later frames of the GIFs the picture run makes; the README GIFs are retaken with the next picture run.

## [0.20.0] - 2026-10-30

### Changed

- The extension ships `cel-lens-core` v0.19.0.
- The public-repo layout test builds `engine/` from the checkout named by `CEL_LENS_ENGINE_DIR` when it is set, so a link to a file the engine lacks fails.
- A test checks that each README GIF has one global colour table and no local tables.

## [0.19.0] - 2026-10-30

### Changed

- CONTRIBUTING.md says that Kyverno coloring needs the `kyverno` experimental feature.
- The public-repo Markdown link test also covers reference-style and titled links.

### Fixed

- A failed engine download with no error message names the error code, such as ECONNREFUSED.

## [0.18.0] - 2026-10-30

### Changed

- The public-repo Markdown link test also checks the targets of badge links.
- The public layout test's copy fallback works on unusual layouts.
- CONTRIBUTING.md's Releases section is a numbered checklist: version bump, changelog, pictures, `make check`, tag.

### Fixed

- `npm run pictures` quantizes all frames of a GIF with one palette and compares palette colours, not raw JPEG pixels, so a kept background pixel cannot differ from a rewritten one by a few levels.

## [0.17.0] - 2026-10-29

### Added

- The `cel-lens.experimental` setting: a list of the experimental features to turn on, from `kyverno`, `crd-typed-policies` and `fitting-completion`; empty by default. The extension passes it to the engine at start and when it changes, and the README's "Experimental features" section tells which feature is stable or experimental.

### Changed

- `npm run engine` (the end-to-end step of `make check`) says where it fetches the engine from, and a download that fails, even with no connection, names the URL of the missing file.
- The extension carries the `cel-lens-core` v0.17.2 engine, which reads the experimental list.
- Kyverno `ValidatingPolicy` documents are colored only when `kyverno` is in `cel-lens.experimental`.

## [0.16.0] - 2026-10-29

### Changed

- The extension carries the `cel-lens-core` v0.17.2 engine.
- The release steps in `CONTRIBUTING.md` now say to run `npm run pictures` and commit `images/`, so the store page matches each release.
- The public layout test keeps tooling's temp folders out of the tree it copies, prints the nested run's failures, and links `node_modules` when `TMPDIR` is on another device.
- `colors.gif` and `completion.gif` in the README are retaken: 5.7 s and 4.3 s long, at 10 frames per second, and no completion list shows in `colors.gif`. `npm run pictures` now records with the browser's screencast, encodes after the action with frames that stay on screen (no disposal), and stops if a completion list shows while `colors.gif` records.

## [0.15.2] - 2026-10-29

### Changed

- The README names the status bar menu and the Pick Kubernetes Version and Choose Color Style commands instead of linking to CONTRIBUTING.md.
- The public layout test is lighter on the runner.

## [0.15.1] - 2026-10-29

### Added

- `public-repo/`: the front page of JairJosafath/cel-lens (README, CONTRIBUTING, SECURITY, Code of Conduct, LICENSE, issue forms, and the CI and Scorecard workflows), kept out of the published source by `.gitattributes` and tested by `src/public_repo.test.ts`.
- The pictures of the README, taken from the extension running in a browser-served VS Code: `problems.png`, `hover.png`, `completion.png`, `quick-fix.png` and `colors.png`, and two GIFs, `colors.gif` and `completion.gif`.
- `npm run pictures` retakes them with openvscode-server and Playwright's Chromium, on the fixtures in `test/pictures/`; `CONTRIBUTING.md` says how.
- `SECURITY.md`: report a vulnerability privately through GitHub's private vulnerability reporting; we acknowledge within 7 days and answer within 14 days.

### Changed

- The README is a short store page that leads with what a user sees; the only install route it names is the `.vsix` from the GitHub releases, and it has an Experimental features section.
- How it works, Develop, Releases and Layout moved from the README to `CONTRIBUTING.md`, which says `make check` fails on any warning.

### Fixed

- `npm test` passes in a clone of the published source: the tests that read the maintainers' CI workflow files skip when those files are absent, and a test runs the suite in a copy without them.

## [0.15.0] - 2026-10-12

### Changed

- The extension carries the `cel-lens-core` v0.15.0 engine.
- `package.json` states the Node version contributors need (`engines.node`), and a test keeps it equal to the one in CONTRIBUTING.md.

## [0.14.0] - 2026-10-12

### Changed

- The release workflow test fails with a clear message when its step names change.

## [0.13.1] - 2026-10-12

### Changed

- The extension carries the `cel-lens-core` v0.14.0 engine.
- A check fails when the lockfile's root package does not match `package.json`.
- The store listing test decodes icon pixels with PNG filters.
- The CI temporary-folder cleanup refuses to run against `/tmp`.

### Fixed

- A multi-line plain or quoted rule that starts far above the visible lines is colored on the lines you see.

## [0.13.0] - 2026-10-12

### Changed

- `npm run profile` prints the median time per run, the warm-up and profiled run counts and the `out/profile/` directory.

## [0.12.1] - 2026-10-10

### Changed

- The extension carries the `cel-lens-core` v0.13.1 engine.
- The lockfile licence says Apache-2.0, like `package.json`.
- The "Show Logs" button label lives in `button_labels.ts` with the other labels.

## [0.12.0] - 2026-10-10

### Added

- Every CEL field of ValidatingAdmissionPolicy and MutatingAdmissionPolicy is colored like an `x-kubernetes-validations` rule, in the color style you picked.
- Every CEL field of a Kyverno `ValidatingPolicy` (`policies.kyverno.io/v1`) is colored too, and its errors, hover and completion come from the engine.

### Changed

- The extension carries the `cel-lens-core` v0.13.0 engine. Its typed `object` and `oldObject` in admission policies and its Kyverno `ValidatingPolicy` checks reach the editor through the language server.

## [0.11.1] - 2026-10-10

### Added

- The store page has an icon and keywords (`cel`, `kubernetes`, `crd`, `x-kubernetes-validations`, `validatingadmissionpolicy`), and a test fails if `vsce package` prints a warning.

### Changed

- The extension carries the `cel-lens-core` v0.12.1 engine. Its changes (`go install` of the engine, licence and module path) need nothing from the extension.
- The "engine too old" message names github.com/JairJosafath/cel-lens/releases, and `package.json` points `repository`, `bugs` and `homepage` at the public repository.
- Building the extension downloads the pinned engine from the public GitHub releases by default; `CEL_LENS_CORE_RELEASES_URL` picks another source.
- The extension is licensed under Apache-2.0, and the `.vsix` carries the `LICENSE` file.
- `CONTRIBUTING.md` explains how to build, test, package and run the extension from source, and the README ends with a Contributing section.
- Tooling only: the check and release workflows use the public repository.
- The README is now a store page: pictures, install steps, settings, the supported Kubernetes versions and what is not supported yet.

## [0.11.0] - 2026-10-10

### Changed

- Tooling only, nothing changes for users: the Makefile compile-cache test no longer depends on the caller's environment.

## [0.10.0] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.12.0 engine. Its changes are internal (macro receiver kinds, immutable lookup tables) and need nothing from the extension.
- Tooling only, nothing else changes for users: the CHANGELOG test catches any wording of an engine bump.

### Fixed

- A long block-scalar rule that starts far above the visible lines but runs into them is now colored on its visible lines.

## [0.9.1] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.11.0 engine: bad characters in a UTF-16 YAML stream are reported on the right document, and quick fixes detect CRLF line endings in UTF-16 files. Its other changes need nothing from the extension.
- Tests and tooling only, nothing else changes for users: a test checks that the release workflow keeps `TMPDIR` out of `/tmp` and the checkout, and the check and release workflows clean up the CI temporary folder after each job.

## [0.9.0] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.9.2 engine: hover marks the form of every optional call whose type can be told, including after `optMap`, `optFlatMap`, `or` and `value`, and YAML streams in UTF-16 that hold several documents are checked. Its other changes need nothing from the extension.
- Large CRDs repaint faster: the CEL colors are computed only for the visible lines and a margin around them, and again after a scroll past it.
- Tooling only, nothing changes for users: `npm run profile -- <rules>` CPU-profiles `tokenizeCel` at one rule count in its own process.

## [0.8.1] - 2026-10-09

### Changed

- Tests and tooling only, nothing else changes for users: the CHANGELOG test allows every Keep a Changelog kind and collapses double engine bumps in released sections, the Restart Engine and Open Engine Setting labels share constants, and the check and release workflows were updated.
- The extension carries the `cel-lens-core` v0.9.0 engine: a quote after a verbatim tag with a comma in a flow collection is read as quoted. Its other changes need nothing from the extension.

### Fixed

- Release runs no longer fill the runner's `/tmp`: the workflows stage engine binaries in a temporary folder of the job's own.

## [0.8.0] - 2026-10-09

### Changed

- The README now says which settings files a color style pick changes: your user settings, and the workspace and the open file's folder when they set the key.
- The log line about an engine that reports no Kubernetes versions now says where to download a newer one: the releases page and the `core-vX.Y.Z` tags.

### Fixed

- The start-failure message separates the engine's last lines with `/` and escapes double quotes, so the quoted part is easy to read.

## [0.7.2] - 2026-10-09

### Changed

- Tests and tooling only, nothing changes for users: the check turns Node's compile cache off (`NODE_DISABLE_COMPILE_CACHE=1`), so it no longer fills the CI runner's `/tmp`.

### Fixed

- Picking a color style writes the folder-level `cel-lens.colorStyle` of the open file's workspace folder only, no longer rewriting the other folders' `.vscode/settings.json`.

## [0.7.1] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.8.0 engine: hover skips quoted brackets, completion resolves groups after keyword operators, and merge-key cancellation is checked. Its changes need nothing from the extension.
- Tests and tooling only, nothing changes for users: the test stand-in's document events are checked against the real `vscode` Event types, and its quick pick against `vscode.QuickPick`.
- Tests and tooling only, nothing changes for users: CHANGELOG.md has one Keep a Changelog section per kind.
- Tests and tooling only, nothing changes for users: the tokenizing benchmark's growth line times the 5000 and 10000 rule sizes in alternation, so machine load no longer gives false OVER results.

### Fixed

- The engine start-failure message reads as sentences: it starts with "The cel-lens engine failed to start", puts what the engine printed in double quotes, and gives the advice as its own sentence.

## [0.6.1] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.7.0 engine.
- Tests and tooling only, nothing changes for users: the Show Logs label is one shared constant, a test pins that `ServerController` starts the engine through `spawnEngine`, and the end-to-end docs-link pattern is anchored at a path boundary.

### Fixed

- Open Releases Page opens https://github.com/JairJosafath/cel-lens/releases, where engine releases are tagged `core-vX.Y.Z`, instead of a cluster-internal host.
- The status bar shows the engine as stopped when the connection keeps erroring and the engine is shut down.

## [0.6.0] - 2026-10-09

### Changed

- The extension carries the `cel-lens-core` v0.6.1 engine: a broken document's own YAML error is reported when a bad character follows it, UTF-8 errors sit on the offending character, and a quote after an anchor or tag in a flow collection is read as a quoted scalar. Its changes need nothing from the extension.
- Tests and tooling only, nothing changes for users: a profile explains why `tokenizeCel` grows faster than linearly past 5000 rules, with a benchmark budget for it, and the Makefile declares `e2e` phony and lists it in `make help`.

### Fixed

- The status bar actions menu shows the color style of the active editor's folder, as the Color style menu does.

## [0.5.0] - 2026-10-09

### Changed

- Tests only, nothing changes for users: the manifest test checks that command titles start with a verb, the extension and the end-to-end test share their spawn options, an end-to-end test checks that hover links point at known docs hosts, and the `onDidChangeTextDocument` test stand-in is type-checked.

### Fixed

- The Color style menu opens with the current style highlighted, so pressing Enter at once changes nothing.

## [0.4.1] - 2026-10-09

### Changed

- The cel-lens-core releases URL behind Open Releases Page and the engine download is defined once, so it can move to a public host in one place.
- Tests only, nothing changes for users: `verifyDist` is tested on an empty or missing `dist` folder.
- The extension carries the `cel-lens-core` v0.5.1 engine: the hover marks the right form when the receiver or an argument is a call, an optional or a macro's loop variable, hovers on `sum`, `isLessThan`, `orValue`, `size`, `startsWith` and `all` link to their own docs section, quick fixes, hovers and completions work in the documents before a bad character, a block scalar is kept when a comment separates its anchor or tag from the header, and schemas that alias or merge a large `properties` map are handled in full. Its changes need nothing from the extension.

### Fixed

- The notification for an engine that fails to start leads with the real cause: the engine's last few lines, or "the engine file can't be run (permission denied)" with the path. Stream errors such as `write EPIPE` no longer appear in it.
- The actions menu entry reads `Show Logs`, like the error notification buttons.
- When the connection to the engine keeps erroring, the message says "engine" instead of "server" and offers Restart Engine and Show Logs, once.

## [0.4.0] - 2026-10-09

### Changed

- Tests and benchmarks only, nothing changes for users: `verifyDist` is tested on a real folder of `.vsix` files, the 5000-rule `tokenizeCel` unit test is back, an end-to-end test checks that the real engine's hover reaches the client with its doc link, and the cel tokens benchmark is steadied and checks its 100 ms budget.
- The extension carries the `cel-lens-core` v0.5.0 engine: a block scalar keeps its range when its header is split by a tab or line break, line counts match the YAML parser for a lone CR and Unicode line separators, CEL in the documents before a bad character is still checked, and "Invalid Unicode character" errors sit on the offending character. Its changes need nothing from the extension.

### Fixed

- Coloring the CEL of a large CRD takes less than half the time and allocates less, so a CRD with 5000 rules no longer stalls the editor while it repaints.
- In a multi-root workspace, Choose Color Style pre-selects the style of the active editor's folder, the one painted in that editor.

## [0.3.1] - 2026-10-09

### Fixed

- In a remote window (WSL, SSH, a dev container, VS Code in a browser), the Open Engine Setting button opens the Remote settings filtered to `cel-lens.serverPath`, instead of the User tab that says "No Settings Found".
- When the engine keeps crashing, the message now says "engine" instead of "server" and offers Restart Engine and Show Logs buttons. It still restarts the engine after a crash and gives up after 5 crashes in 3 minutes.
- The `CEL · K8s` status bar item stays on YAML files when the engine failed to start or stopped after crashing. It reads `CEL · Engine down` with an error icon and a tooltip giving the reason, and clicking it opens the actions menu with Restart Engine and Show logs at the top. After a restart it shows the Kubernetes version again.

## [0.3.0] - 2026-10-08

### Changed

- The extension carries the `cel-lens-core` v0.4.1 engine: hovers link to the function's section of the docs, macro completions are offered only on receivers they apply to, the hover marks the matching overload from the argument types too, and a YAML alias of a list in a text field, such as `description: *a`, no longer crashes the engine and gets the wrong-type problem on the alias. Its changes need nothing from the extension.
- The one-time hint to move `celLens.colorStyle` to `cel-lens.colorStyle`, and the Move Setting button, now also cover a value set at workspace-folder scope, read through each folder of a multi-root workspace. `cel-lens.colorStyle` now has resource scope, so VS Code accepts it in workspace and folder settings.
- A benchmark now times the color repaint on a 5000-rule CRD, and the tokenizing benchmark on such a CRD was reworked after it took over 5 s in CI. Nothing changes for users.
- `make dist` now opens every platform `.vsix` it wrote and fails unless each holds exactly one engine binary for its own target and a manifest TargetPlatform that matches its file name. `node src/dist.ts verify` runs the same check on an existing `dist/`.

### Fixed

- Choose Color Style now also writes the pick where the workspace or a folder sets `cel-lens.colorStyle`, and each editor is painted in the style of its own workspace folder.
- The end-to-end engine test reads the engine from `bin/` and makes no network request; `npm run engine` puts it there, and `npm run e2e` runs that first.
- A failed engine start no longer logs the engine's stderr a second time in the summary; the notification still quotes it.
- An engine the system refuses to launch (for example a file that is not executable) now has its cause in the log.

## [0.2.4] - 2026-10-08

### Added

- Whoever still sets `celLens.colorStyle` is told once how to move it to `cel-lens.colorStyle`, with a Move Setting button that writes the new key and removes the old one at the same scope.

### Changed

- Messages call it the engine, not the language server, and every button that opens `cel-lens.serverPath` is labelled Open Engine Setting.
- The extension carries the `cel-lens-core` v0.3.0 engine: diagnostics for control-character, encoding and wrong-typed flow, alias and multi-line scalar errors sit on the exact characters, and costs are no longer reused for untyped schema nodes. Its changes need nothing from the extension.
- `make dist` first compares all six pinned checksums in `core-version.json` with the engine release's `checksums.txt` and fails, naming each asset that differs.
- A test now shows that opening an unrelated document does not repaint other editors. Nothing changes for users.

### Fixed

- Each line the engine writes to stderr now appears once in the cel-lens log, not twice.

## [0.2.3] - 2026-10-08

### Changed

- The extension carries the `cel-lens-core` v0.2.0 engine: richer hovers on CEL functions and macros, faster checks of large CRDs, more accurate diagnostic ranges, and a list of its supported Kubernetes versions in its `initialize` reply, which the version picker already reads, so Pick Kubernetes Version offers them. Its changes need nothing from the extension.

## [0.2.2] - 2026-10-08

### Changed

- The color style setting is now `cel-lens.colorStyle`, like every other cel-lens setting, and the README test expects that name. A value set under the old `celLens.colorStyle` key is still used until `cel-lens.colorStyle` is set.

### Fixed

- The README's Choose Color Style entry names the styles as the quick pick shows them: CEL, Classic and Off.
- An editor whose language mode changes loses or gains its cel-lens colors at once, not at the next repaint.

## [0.2.1] - 2026-10-08

### Changed

- The actions menu names the engine's state (starting, stopped, failed) on the Kubernetes version entry instead of leaving it blank.
- Coloring repaints only the YAML document that changed, not every visible YAML editor, and ignores other documents such as the cel-lens output channel.
- The error button and messages now say **Restart Engine** and "restart the engine", matching the command.
- The actions menu and the Choose Color Style quick pick name the styles the same way: CEL, Classic and Off.

### Fixed

- The README describes the start-failure buttons by source: Open Setting for a `cel-lens.serverPath` binary, Show Logs and a reinstall hint for the bundled engine.
- An editor switched from YAML to another language loses its cel-lens colors at the next repaint.

## [0.2.0] - 2026-10-08

### Added

- A menu of the main actions from the status bar: clicking the item lists Color style, Kubernetes version, Restart Engine and Show logs, the first two showing the current style and version. The tooltip now reads "cel-lens: Kubernetes <version> · click for actions". New commands **cel-lens: Show Actions** and **cel-lens: Show Logs**.
- Color styles for CEL: the parts of each CEL expression in Kubernetes YAML are colored in the `cel` (default), `classic` or `off` style, chosen by theme kind. Set `celLens.colorStyle` or run **cel-lens: Choose Color Style**; open editors recolor at once.

### Changed

- Command and setting names are clearer: **cel-lens: Restart Server** is now **cel-lens: Restart Engine** (same command id); commands that need a YAML file are hidden from the palette without one; settings are ordered with color style first, then Kubernetes version, and every setting has a description and enum values are described.
- Groundwork for coloring the parts of a CEL expression: finding the CEL in a CRD's `x-kubernetes-validations`, a CEL lexer and the colors of the `cel`, `classic` and `off` styles. Nothing is colored yet.
- A `v`-prefixed `cel-lens.kubernetesVersion` such as `v1.36` is matched against the engine's supported versions like `1.36`: no false "cannot match" warning, and the version picker marks the right entry.
- When the engine fails to start, the one message now says to reinstall the extension or clear
  `cel-lens.serverPath`.
- When an engine from `cel-lens.serverPath` fails to start, the message says to fix or clear the setting and its button, Open Setting, goes straight to it; a bundled engine's message says to reinstall and offers Show Logs.

## [0.1.1] - 2026-10-08

### Changed

- The extension carries the `cel-lens-core` v0.1.2 engine: diagnostic spans come from the checked AST, problems sit on YAML aliases, and broken YAML on a blank line never gets an empty range. These need nothing from the extension.

## [0.1.0] - 2026-10-07

The first release: the bundled `cel-lens-core` v0.1.1 engine behind a language client for Kubernetes YAML. The Standard changes to the Makefile change nothing users see.

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

[Unreleased]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.40.0...HEAD
[0.40.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.39.0...vscode-v0.40.0
[0.39.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.38.0...vscode-v0.39.0
[0.38.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.37.0...vscode-v0.38.0
[0.37.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.36.0...vscode-v0.37.0
[0.36.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.35.0...vscode-v0.36.0
[0.35.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.34.1...vscode-v0.35.0
[0.34.1]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.34.0...vscode-v0.34.1
[0.34.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.33.0...vscode-v0.34.0
[0.33.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.32.0...vscode-v0.33.0
[0.32.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.31.0...vscode-v0.32.0
[0.31.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.30.0...vscode-v0.31.0
[0.30.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.29.0...vscode-v0.30.0
[0.29.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.28.1...vscode-v0.29.0
[0.28.1]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.28.0...vscode-v0.28.1
[0.28.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.27.3...vscode-v0.28.0
[0.27.3]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.27.2...vscode-v0.27.3
[0.27.2]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.27.1...vscode-v0.27.2
[0.27.1]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.27.0...vscode-v0.27.1
[0.27.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.26.0...vscode-v0.27.0
[0.26.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.25.0...vscode-v0.26.0
[0.25.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.24.3...vscode-v0.25.0
[0.24.3]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.24.2...vscode-v0.24.3
[0.24.2]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.24.1...vscode-v0.24.2
[0.24.1]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.24.0...vscode-v0.24.1
[0.24.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.23.0...vscode-v0.24.0
[0.23.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.22.1...vscode-v0.23.0
[0.22.1]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.22.0...vscode-v0.22.1
[0.22.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.21.0...vscode-v0.22.0
[0.21.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.20.0...vscode-v0.21.0
[0.20.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.19.0...vscode-v0.20.0
[0.19.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.18.0...vscode-v0.19.0
[0.18.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.17.0...vscode-v0.18.0
[0.17.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.16.0...vscode-v0.17.0
[0.16.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.15.2...vscode-v0.16.0
[0.15.2]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.15.1...vscode-v0.15.2
[0.15.1]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.15.0...vscode-v0.15.1
[0.15.0]: https://github.com/JairJosafath/cel-lens/compare/vscode-v0.14.0...vscode-v0.15.0
[0.14.0]: https://github.com/JairJosafath/cel-lens/releases/tag/vscode-v0.14.0
