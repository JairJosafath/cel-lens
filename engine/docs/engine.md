# Engine reference

What the README leaves out: how the engine behaves in detail, for contributors
and editor authors.

## Kubernetes version

By default cel-lens matches **Kubernetes 1.37**: results come from the 1.37
Kubernetes CEL libraries and cost rules. Clusters on another version can pick
theirs, from 1.33 to 1.37 (`pkg/kubeversion` defines the range and the default):

- command line: `cel-lens-core check --kube-version 1.34 deploy/`; an
  unsupported value fails with exit code 2 and lists the supported versions;
- language server: set `kubernetesVersion` (for example `"1.34"`) in
  `initializationOptions` or in the `settings` of `workspace/didChangeConfiguration`.
  A change re-checks every open document; an empty value returns to the
  default, and an unsupported value is refused with a message and ignored.
  The `initialize` result reports the version in use as the top-level
  `kubernetesVersion`. Under `capabilities.experimental.celLens`,
  `supportedKubernetesVersions` lists the versions the engine supports (newest
  first, written like `kubernetesVersion`) and `kubernetesVersion` is the
  version used when none is picked; for example
  `"supportedKubernetesVersions": ["1.37", "1.36", "1.35", "1.34", "1.33"]`
  and `"kubernetesVersion": "1.37"`.

The same object reports the limits of the workspace CRD scan, whether or not
`crd-typed-policies` is on: `workspaceScanMaxFiles` is the greatest number of
files (a count) the scan reads from a workspace, and `workspaceScanMaxFileBytes`
is the greatest size of one file (in bytes) it reads. They come from the
constants the scan uses. Clients such as the VS Code extension read them to
check their docs against the running engine.

## Language server

`cel-lens-core lsp` serves CEL support for Kubernetes YAML over the Language
Server Protocol on stdin and stdout, for any editor:

- **Diagnostics** for every CEL expression in an open CustomResourceDefinition
  or ValidatingAdmissionPolicy, published on open and on every change, with the
  API server's messages. A document whose YAML does not parse gets an error on
  the line where it breaks (`YAML is broken here: ...`); the other documents of
  the file are still checked.
- **Hover** on a variable, field or function inside an expression: its type and
  a short description.
- **Completion** inside an expression: `self`/`oldSelf` or `object`/`request`/...,
  the fields of the type before a dot, and the Kubernetes CEL functions. The
  trigger characters are `.`, `/`, `#`, `(` and `:` for YAML files, and the
  same and the space for `.cellens` files: in a YAML file only `.` gives
  items, and a request for another of them (`context.triggerKind` 2) gets
  `null` at once. A client whose `textDocument.completion` has
  `dynamicRegistration` gets no `completionProvider` from `initialize`, and
  after `initialized` the server registers completion twice, once
  (`client/registerCapability`): for YAML files on disk,
  `{"scheme": "file", "language": "yaml", "pattern": "**/*.yaml"}` and the
  same for `**/*.yml`, with the five, and for
  `{"scheme": "file", "pattern": "**/.cellens"}` with the space too, so a
  space typed in a YAML file, in a comment or a CEL string, asks for
  nothing. No file matches both, not even a `.cellens` the editor opens as
  YAML, as VS Code does: a client asks each provider that matches and shows
  the items of each. A client that refuses the registration gets a line in
  its log (`window/logMessage`) saying the session has no completion, as
  `initialize` declared none. Another
  client gets `completionProvider` with all six for every file, as the
  protocol has one list then: it asks after a space in every file, and gets
  `null` at once but in a `.cellens` after a key's colon.

- **Quick fixes** (the light bulb): "Change to `maxReplicas`" for a misspelled
  field, function or macro when exactly one name valid there is within two edits
  of it, and "Add `maxLength: 1000` to `spec.name`" (or `maxItems`,
  `maxProperties`) for a cost error, with the largest round number that brings
  the rule under its budget (a cost error on the CRD's total is solved when the
number also brings the total under its budget). No fix is offered when there is no clear one.

- **Types from `.cellens`**, the experimental feature `cellens-types` (see
  [Types from `.cellens`](#types-from-cellens) for what off means): the
  server finds the `.cellens` files of the
  workspace folders, and follows them as they are opened, edited, saved,
  created or deleted (it registers a watcher for `**/.cellens`, and one for
  `**/*.go`, for the Go types a `go:` can name). A `dyn` value
  an entry names gets the entry's type, a CEL type, a kind, an OpenAPI schema
  or a Go type of the workspace, in diagnostics, hover and completion;
  a problem only that type shows is a warning that names the `.cellens` file and
  line, and the problems of a `.cellens` are published on it. See
  [cellens.md](cellens.md).
- **Editing a `.cellens`**: in an open `.cellens`, completion of an entry's
  keys and of what each can name, hover on what an entry names and the type
  it gives, and its problems as it is typed. The client sends the `.cellens`
  files (`didOpen`, `didChange`, `didClose`) as it sends YAML files; the server
  knows them by their name. In a `.cellens` the trigger characters start a
  path (`.`), a file or folder (`/`), a Go type (`#`), a type's argument
  (`(`) and a value (`:`), and the space typed after a key's colon, as in
  `go: `, gives the items asking for completion there gives, so the list the
  colon opened comes back after the space; a space anywhere else, as between
  the words of a value, gets `null`. A completion item replaces the word at the cursor
  (`textEdit`); `list($1)`, `map(string, $1)` and `{$1}` are snippets
  (`insertTextFormat` 2) for a client with
  `textDocument.completion.completionItem.snippetSupport`, and plain text
  otherwise; a key that starts a block, such as `schema:`, comes with the line
  below it, indented, in the line ending of the text, as is
  (`insertTextMode` 1), for a client whose
  `textDocument.completion.completionItem.insertTextModeSupport` has it, and
  alone otherwise. The folders after `go:` come from the last listing of the
  workspace's Go types, which runs in the background, never on the message
  loop: while there is none, or a `.go` file or a folder changed since (the
  server watches `**/*.go`), the server answers with what it has,
  `isIncomplete: true`, and lists them again, so the client asks again as the
  person types. A folder's item ends with `#` and runs the client command
  `editor.action.triggerSuggest`, for its struct types to show at once.
  `go test -bench GoCompletion ./internal/lsp/` measures the completion and
  the listing on 2,000 Go files. See [cellens.md](cellens.md#editing-a-cellens).
- **Examples of a CRD**: the documents of the workspace of a version a
  CustomResourceDefinition the server knows serves, what each expects, and
  the problems its schema finds in each, as errors on the example; and what
  each of its rules does to each: a part of the line above the rule, a
  section of its hover, a warning on the rule and on the example for each
  wrong verdict, and, on request, every result. The setting
  `exampleResults` turns it all off. See
  [Examples of a CustomResourceDefinition](#examples-of-a-customresourcedefinition).
- **Commands** (`workspace/executeCommand`): the type choices for a `dyn`
  value, with the workspace's examples of its kind, the check of a type as it
  is typed, a schema worked out from examples, and setting or removing a
  `.cellens` entry through `workspace/applyEdit`, or adding one to fill in,
  and what a CRD's rules do to its examples. See [Commands](#commands).

- **Inline info**: `textDocument/codeLens` (the server declares
  `codeLensProvider`, without `resolveProvider`: each lens comes with its
  command and arguments). Above the line of the key of each CEL expression the
  server checks (a rule, a `messageExpression`, a policy's validation, match
  condition, variable, audit annotation or mutation; a Kyverno policy only
  while `kyverno` is on) is one quiet line, one code lens per part, and every
  part runs a command when clicked:
  - the subject and its type, then `compiles` or the number of problems the
    expression has: `self: Widget.spec · compiles` for a rule on a CRD's
    `spec` (`self: Widget` on the root, `self: Widget.spec.ports[]` on a list's
    items), `object: apps/v1 Deployment · 2 problems` for a policy (`object:
    dyn` when its kinds are not known). It runs `cel-lens.showRule`, for the
    client to show the expression's problems or its hover.
  - only while `cellens-types` is on, for each value the expression reads
    that Kubernetes leaves `dyn` and a `.cellens` entry can type,
    `self.spec.port: dyn · Give it a type`, which
    runs `cel-lens.giveType`; for one an entry types, `self.spec.port: int ·
    Edit` and `Remove`, or `params: {max, x} · Edit` and `Remove`, which run
    `cel-lens.editType` and `cel-lens.removeType`. A value an entry names that
    is `dyn` all the same, as the entry does not apply (its kind is not found,
    say), reads `params: dyn · Edit` and `Remove` for that entry, never `Give
    it a type`, which would write a second one. A policy variable Kubernetes
    leaves `dyn` whose expression returns a type with the types `.cellens`
    entries give the values it reads, such as `limit: params.maxReplicas`
    with `params` typed, reads `variables.limit: int`, a part that runs
    `cel-lens.showRule` with the place of the entry its type follows from
    (`{"uri": "<the .cellens>", "range": <empty, where its type starts>}`),
    as no entry names the value itself. A
    variable whose expression returns `dyn` all the same, such as an object,
    still reads `Give it a type`. A struct shows its field
    names, cut with `…` past 40 characters; a kind shows as written
    (`params: v1 ConfigMap · Edit`). The values are named as the expression
    reads them, written as a `.cellens` path writes them
    (`self.spec.ports[].target`) with field names unescaped (`self.spec.my-port`
    for `self.spec.my__dash__port`), each once, in the order read. In a
    CustomResourceDefinition they are the values with
    `x-kubernetes-int-or-string`, the only `dyn` values the API server lets a
    rule read; in a ValidatingAdmissionPolicy or MutatingAdmissionPolicy
    `params`, `object`, `oldObject` and `variables.<name>` when they are `dyn`,
    and the `dyn` values inside them. `request.options`, Kyverno's variables, a
    field whose name has a `.`, and a document without `metadata.name` get
    none, as no entry can name them.
  - a line is at most 80 characters as VS Code shows it, with ` | ` between
    its lenses. It shows at most two values, as many as fit, cutting a long
    name in the middle after its variable (`object.…valueFrom`; `self`,
    `params` and `variables.max` are never cut) and then a long type to fewer
    fields; the others are `+N more`, which runs `cel-lens.listValues` with, as
    its arguments, each one's parts as protocol Commands with whole names, for
    the client to list. Two expressions whose keys share a line give the line
    to the first.
  - last, above a CustomResourceDefinition's rule with examples, what the
    rule does to them: `3 examples pass`, or the wrong verdicts first,
    `refuses 1 example it should allow · 2 pass`, which runs
    `cel-lens.showExampleResults` with the rule's location (see [What the
    editor shows of the results](#what-the-editor-shows-of-the-results)).
    When the line is too long, this part is shortened first (`1 wrong · 2
    pass`, then `1 wrong`), before the values are.

  The commands run in the client (the server executes none).
  `cel-lens.showRule` takes `{"uri": "<the document>", "range": <the
  expression's range>}`. `cel-lens.giveType`, `cel-lens.editType` and
  `cel-lens.removeType` take one argument that names the value as a `.cellens`
  entry does: `{"cellens": "file:///ws/.cellens", "file": "deploy/crd.yaml",
  "document": "CustomResourceDefinition widgets.example.com", "version": "v1",
  "path": "spec.port", "value": "self.spec.port"}`, with `variable` (`params`,
  `object`, `oldObject` or `variables.<name>`) in place of `version` for a
  policy, no `path` for the root, and the entry's `type`, `kind`, `schema` (a
  JSON object) or `go` when an entry names the value. A schema or a Go type
  shows on the line as the type a `type:` entry would write, `params:
  {maxReplicas, team} · Edit`. `cellens` is the URI of the `.cellens` the entry is in
  or would go in: the nearest one the server knows in a folder at or above the
  file, else the one at the root of the file's workspace folder, else the one
  beside the file; `file` is relative to its folder. A document that is no
  `file:` URI gets no values. The setting `inlineInfo` (`initializationOptions`
  or the `settings` of `workspace/didChangeConfiguration`; default `true`)
  turns every line off and on, without a restart. When it changes, and whenever
  the open documents are checked again (a `.cellens`, a CRD or a setting
  changed), the server sends `workspace/codeLens/refresh` to a client whose
  capabilities have `workspace.codeLens.refreshSupport`; a client without it
  shows new lines when it next asks, such as after an edit. Until the client
  has asked again for the lines of each open document it asked lines for,
  which changed since, the editor opening, closing or saving a `.cellens`
  (`didOpen`, `didClose`, a watched change) sends the refresh again, even
  with the text the server has, and so does, once, a second after the last
  refresh, the server itself: VS Code drops a refresh when the editor that
  would ask loses the focus first, as when the extension shows a `.cellens`
  it has just written beside the rule, and the save, the reopening and the
  watched change that follow come after that. A keystroke in a `.cellens`
  that changes no entry sends none, as a document in a hidden tab stays owed
  its lines; the refresh sent a second later sets no other. The lenses of a
  version of a document are kept until it is checked again or a `.cellens`
  changes, so asking again only sends them.

In a YAML file, positions outside CEL expressions get no hover and no
completion. Documents are
synchronised in full; the server exits 0 after `shutdown` and `exit`, and 1 when
the client leaves without `shutdown`. `go test -bench Diagnostics ./internal/lsp/`
measures the 300 ms diagnostics budget on a 1,000-line CRD, and
`go test -bench CodeLens ./internal/lsp/` a code lens request on a CRD of 500
rules and on policies, the first one after a change and one asked again, and
`go test -bench CellensKeystroke ./internal/lsp/` a change of an open
`.cellens` of 200 entries, and one that types an open policy's variables
again, with that policy's code lenses.

The command accepts the flags language clients pass: `--stdio` (the default and
only transport) and `--clientProcessId=<pid>`, which makes the server exit when
that process is gone. `--socket`, `--pipe` and `--node-ipc` are refused with
exit code 2. `lsp --help` lists them.

## Commands

`initialize` declares six commands under `executeCommandProvider`, which a
client runs with `workspace/executeCommand`. While `cellens-types` is off,
`cel-lens.typeChoices`, `cel-lens.checkType`, `cel-lens.setType`,
`cel-lens.removeType` and `cel-lens.inferType`, the commands behind
**Give it a type**, fail (`-32803`) with a message that says so:
`cel-lens.setType: giving a value a type of your own in .cellens is the
experimental feature cellens-types, which is off: turn it on to give a value
a type`; `cel-lens.exampleResults` runs whatever the features. The VS Code extension runs them
from the lines above a rule: their parts run its own commands
`cel-lens.giveType`, `cel-lens.editType` and `cel-lens.removeType`, which ask
the person for a type and pass these commands the same argument, with the
type or kind chosen. `cel-lens.exampleResults` is described with the
examples it is about, in [What the rules do to the
examples](#what-the-rules-do-to-the-examples). Each of the others but
`cel-lens.inferType` takes one argument, an object with these keys:

| Key | What it is |
| --- | --- |
| `cellens` | The URI of the `.cellens` file that `file` is relative to |
| `file`, `document`, `version` or `variable`, `path` | The value, as an entry of that `.cellens` names it ([cellens.md](cellens.md)); a `path` left out, or `""`, is the root |
| `value` | The value's CEL, such as `self.spec.config`, for messages and the edit's label |
| `type`, `kind`, `schema` or `go` | The type to check or set: a type as `type:` writes it, a kind, an OpenAPI schema as a JSON object, or a Go type as `go:` writes it, relative to the folder of `cellens`; `null` counts as left out |
| `snippet` | For `setType`, in place of a type: `type` or `schema`, the form of a new entry to fill in |

- `cel-lens.typeChoices` takes no argument, or a line's (below), and returns
  the kinds a `kind:` entry can name, each with a one-line description: the
  first sentence of its schema's description, or where it comes from when it
  has none; and the Go types a `go:` entry can name.

  ```json
  {
    "kubernetesVersion": "1.37",
    "builtIn": [{"apiVersion": "apps/v1", "group": "apps", "version": "v1", "kind": "Deployment",
                 "description": "Deployment enables declarative updates for Pods and ReplicaSets.",
                 "stability": "ga"}],
    "crds": [{"apiVersion": "example.com/v1", "group": "example.com", "version": "v1", "kind": "Widget",
              "description": "A widget."}],
    "goTypes": [{"folder": "file:///repo/api/v1", "name": "QuotaSpec", "doc": "QuotaSpec is the quota of a team."}]
  }
  ```

  `builtIn` lists the built-in kinds the Kubernetes version in use has,
  sorted by group, version and kind: the kinds of the bundled `k8s.io/api`
  whose API version its prerelease lifecycle says was introduced by that
  version and not yet removed, so
  `admissionregistration.k8s.io/v1beta1 MutatingAdmissionPolicy` is listed
  from 1.34 on. That includes alpha and beta API versions, which an API
  server serves only when they are turned on: `stability` says which a kind
  is, `ga`, `beta` or `alpha`, from its version. A kind without lifecycle
  data in `k8s.io/api` is listed for every version. `crds` lists each version of each kind the
  CustomResourceDefinitions the server knows define, from the workspace scan
  and the open documents, sorted the same way. The server reads the workspace's
  CRDs, and a `kind:` entry can name one, only while `crd-typed-policies` is
  on: off, `crds` is `[]` and `crdsNote` says why. `goTypes` lists the
  exported struct types with a field with a `json` tag, and no type
  parameters, that the `.go` files of the workspace folders declare, but
  `_test.go` files, sorted by folder and name: `folder` is the file URI of the
  package's folder, which a `go:` entry names relative to its `.cellens`, and
  `doc` the first sentence of the type's doc comment, `""` without one. The
  walk runs in the background, so the server answers other requests
  meanwhile, and `typeChoices` answers when it ends (see "Commands in the
  background" below).
  It skips hidden folders, `node_modules`, `vendor` and `testdata`, and the
  files a `go build` on this machine leaves out, looks at most at 20000 `.go`
  files of at most 1 MiB each, read through the workspace folder, parses only
  those with a `json` tag in them, and reads again only those whose size or
  time changed since the last walk; it runs whatever the experimental
  features. It lists at most 1000 types, the first by folder and name, and
  then `goTypesNote` says so.

  Given the argument of a line, which names a value, it also lists
  `examples`: the documents of the workspace of the value's kind, which
  `cel-lens.inferType` can make a schema from. For a policy's `params` that is
  the kind `spec.paramKind` names; for `object` and `oldObject` the kinds
  `spec.matchConstraints.resourceRules` match: a resource the server knows,
  built in or of a CustomResourceDefinition it has read, by its kind, and
  another by the plural Kubernetes guesses from a kind (`widgets` for
  `Widget`); a subresource matches nothing. A kind is one in any version of
  its API group (`example.com/v2 Widget` for `example.com/v1`), as the API
  server converts an object to the version the rule or `paramKind` names. Each example gives its file's
  `uri`, its `file` below the workspace folder, the `line` its document
  starts at, from 1, and its `apiVersion` and `kind`, in the order of the
  files' paths:

  ```json
  "examples": [{"uri": "file:///repo/config/samples/limit.yaml", "file": "config/samples/limit.yaml",
                "line": 1, "apiVersion": "rules.example.com/v1", "kind": "ReplicaLimit"}]
  ```

  The search runs in the background with the walk of the Go types, and
  `typeChoices` answers when both end. It walks the YAML files of the
  workspace folders as the CRD scan does, whatever the experimental features:
  it skips hidden folders and
  `node_modules`, files over 1 MiB, what a symbolic link leads to outside the
  folder, and stops after 20,000 files; an open file counts with the editor's
  text. It lists at most 100 examples, and parses only a file whose text has
  the kind's name, or the group's for a kind it guesses (any file for the core
  group or `*`). Another value, such
  as `variables.<name>` or a CustomResourceDefinition's, has no kind, and gets
  `"examples": []`; without an argument there is no `examples`. The argument
  is checked as for `checkType`.

  With a value, it also answers `valueKinds`: the kinds the root of the
  value can be, the value itself or the object it is in, whatever its
  `path`, each an `apiVersion` and a `kind`. For `params` that is the kind
  `spec.paramKind` names; for `object` and `oldObject` the kinds the policy's
  resource rules match, when the server knows them all, as its type checker
  resolves them (a built-in kind, or one of a CustomResourceDefinition it has
  read; a wildcard leaves them not known). It is `[]` when they are not
  known, and for a value with no kind; without a value there is no
  `valueKinds`. A client can tell from it whether a pasted example is a whole
  resource the value is in or the value itself, without parsing it: see
  `roots` of `cel-lens.inferType`.

  ```json
  "valueKinds": [{"apiVersion": "example.com/v1", "kind": "Widget"},
                 {"apiVersion": "apps/v1", "kind": "Deployment"}]
  ```
- `cel-lens.checkType` with `type`, `schema` or `go`, one of them, returns
  `null` when the type is fine. A type that does not parse gets the message
  and the character it is at, from 1, in Unicode code points:
  `{"type": "{name: strin}"}` returns
  `{"message": "unknown type \"strin\": use string, int, ...", "column": 8}`.
  A schema the API server would refuse gets what it refuses, in its words,
  after the place in the schema:
  `{"message": "schema: properties[max].type: Unsupported value: \"int\": ..."}`.
  A Go type is read relative to the folder of `cellens`, which is all the
  argument needs to name besides it; one that cannot be read gets why, and one
  that leaves fields out gets them with `"warning": true`, as the entry would
  still apply: `{"message": "go: QuotaSpec.Owner: type other.Owner is from
  package example.com/other, which cel-lens does not read: the field is left
  out", "warning": true}`.
  When the argument also names a value, the type is checked for it as an
  entry would be, and what the entry would get on its line of the `.cellens`
  comes back without `column`, such as
  `{"message": "spec.config is x-kubernetes-int-or-string, an int or a string to a rule: give it int or string"}`.
  A `kind` in the argument is not checked.
- `cel-lens.setType` with a value and one of `type`, `kind`, `schema` and
  `go` writes its entry, a schema as a block of YAML below `schema:`, the
  keys of each schema in it in the order the API server writes them (see
  [cellens.md](cellens.md#writing-entries-from-the-editor)), a Go
  type's folder relative to the `.cellens` the entry goes in, and returns
  `{"cellens": "file:///repo/.cellens", "change": "added"}`:
  the `.cellens` it changed and how, `added`, `replaced` or `created`. Which
  `.cellens` the entry goes in, and how its text changes, is in
  [cellens.md](cellens.md#writing-entries-from-the-editor).
- `cel-lens.setType` with a value and `snippet`, `"type"` or `"schema"`, in
  place of a type, writes a new entry for the value with a type to fill in,
  where `setType` would write one, and answers where the editor puts the
  cursor in the `.cellens` once the edit is applied:
  `{"cellens": "file:///repo/.cellens", "change": "added", "selection":
  {"start": {"line": 9, "character": 11}, "end": {"line": 9, "character": 11}}}`.
  For a value of a policy, `type` writes `type: "{}"`, the cursor between the
  braces, and `schema` writes a block, `schema:`, `type: object` and
  `properties:` without a value, which is no properties, as in a CRD, then an
  empty line indented two more than `properties:`, the cursor at its end,
  where the first property goes; the entry has no problem while it is so.
  For a CustomResourceDefinition's value, which takes an int or a
  string, `type: int` and `schema:` with `type: integer`, with `int` or
  `integer` selected, so typing replaces it. The entry is checked as any
  `setType` checks one, with that type. A value an entry already types keeps
  it: nothing is written, and the answer, with `"change": "unchanged"` and no
  `workspace/applyEdit`, puts the cursor where that entry's type starts, also
  for a client that cannot apply an edit. The
  VS Code extension opens `cellens` with `selection` selected
  (`window.showTextDocument`), so the person types the type there, with
  completion. A `snippet` beside a type, or other than `type` or `schema`, is
  `InvalidParams`.
- `cel-lens.removeType` with a value removes its entry, and returns
  `{"cellens": "file:///repo/.cellens", "change": "removed"}`, or `deleted`
  when the `.cellens` went with it. A value no `.cellens` types is an error.
- `cel-lens.inferType` works out an OpenAPI v3 schema from examples, in the
  form of a CustomResourceDefinition's `openAPIV3Schema`, for an entry's
  `schema:`; [cellens.md](cellens.md#a-schema-from-examples) says how. Its
  argument is `{"text": "..."}`, YAML or JSON with one or more documents, or
  a list of objects, at most 1 MiB, and optionally `line`, from 1, to take
  only the document at that line (an example's `line`), `path`, written as a
  `.cellens` path, to take the value there in each example (the line's
  `path`), `required: true` to list the fields every example has, and
  `value`, the value the schema is for, as the argument of a line names it
  (what `typeChoices` takes). It returns the schema, its keywords in the order
  the API server writes them and its properties in the order the examples
  first have them, the notes on what the examples do not settle, `[]` when
  none, and `roots`: for each example, in order, the `apiVersion` and `kind`
  at its root, `null` for one without both, before `path` takes the value
  in it:

  ```json
  {"schema": {"type": "object", "properties": {"items": {"type": "array",
              "items": {"x-kubernetes-preserve-unknown-fields": true}}}},
   "notes": ["items: an empty list says nothing about its items"],
   "roots": [null]}
  ```

  With `value` and `path`, when the value's kinds are known (`valueKinds` of
  `typeChoices`), an example whose root is of none of them, by API group and
  kind in any version, as for `examples`, such as a pasted
  value without `apiVersion` and `kind`, or a document of another kind, is
  taken as the value itself, not as an object the value is in, and a note,
  before the others, says so: `the value: the example at line 6 has no
  apiVersion and kind, so it is not of rules.example.com/v1 ReplicaLimit: it
  is taken as the value at spec itself`, with the line the example is
  written at (an alias's own). An example of one of them gives
  the value at `path`, as every example does when the kinds are not known or
  there is no `value`. A `value` that cannot be read is `InvalidParams`, as
  for `checkType`.

  for `{"text": "items: []"}`. It runs in the background, as `typeChoices`
  does (see below). A text it cannot read, too big, nested deeper
  than 100 objects and lists, or with no example (at the `line` or `path`
  given), is refused with `InvalidParams` (-32602) and what is wrong, with the
  line for YAML that does not parse.

Commands in the background: `typeChoices`, `inferType` and
`exampleResults` do their work off
the server's message loop, so the server answers other messages meanwhile,
such as an edit and its diagnostics, and the command answers when its work
ends. A request with the same arguments as one still running waits for the
same work and gets the same answer; one with other arguments stops it, and
the requests that waited for it are answered with `RequestCancelled`
(-32800), as the editor asks again when what it wants changed. A
`typeChoices` without a value and one with a value are answered both: one
never stops the other. `typeChoices` lists the Go types and searches for
the examples at once. `$/cancelRequest` answers its request with
`RequestCancelled`; the work goes on while another request waits for it.
At most four commands' work runs at once, work stopped but still ending
included; another request is refused with `RequestFailed` (-32803) until
one has ended, and `shutdown` answers those still running with
`RequestFailed`. Work that is stopped ends soon: the decoding of
`inferType` looks every 4,096 values whether it was.

`setType` and `removeType` never write a file. They send the client a
`workspace/applyEdit`: a text edit on the `.cellens` as it is then (the
editor's text, at its version, when it has the file open, else the file on
disk), in `documentChanges` when the client supports them, and otherwise in
`changes`, which carry no version, so the client cannot tell that the file
changed since; a new `.cellens` is a `create` followed by its text, and an empty
one may be a `delete`, which the client must list in
`workspace.workspaceEdit.resourceOperations`. Without `create` the command
fails and asks for the file to be created; without `delete` the file keeps
its first comment. The command answers when the client does: with the result
above when it applied the edit, and else with the error `RequestFailed`
(-32803) saying that the editor refused the edit and nothing was written. A
client without `workspace.applyEdit` gets that error at once, and so does one
that shuts the server down first. While an edit of a `.cellens` waits for the
client's answer, a command that would read or edit that file is refused with
`RequestFailed`, as its text would not have that edit yet: run it again once
the edit is applied. A client that cancels a waiting command
(`$/cancelRequest`) gets `RequestCancelled` (-32800), and its answer to the
edit is then ignored. The `cellens` of the argument must be in a workspace
folder, or in the folder of a file the editor has open; another is refused. A wrong argument, or an entry that would get
a problem on the `.cellens`, such as a type that does not parse, a schema the
API server would refuse, a Go type that cannot be read or a struct for an
`x-kubernetes-int-or-string` value, is refused before any edit with
`InvalidParams` (-32602) and the reason. A Go type that leaves fields out is
written: its entry gets the warning.

## Examples of a CustomResourceDefinition

For each CustomResourceDefinition it knows, the language server knows its
examples, what each expects, and whether the CRD's schema accepts it, and
shows the schema's problems on the example. The CRDs it knows here are
those of the open documents and of the YAML files of the workspace, found
by the walk that finds the examples, whatever the experimental features:
`crd-typed-policies` only decides whether a policy's `params` and the like
are typed from them. The check also runs the CRD's CEL rules on its
examples, and the editor shows what each does to each above the rule, in
its hover and as warnings (see [What the editor shows of the
results](#what-the-editor-shows-of-the-results)); the command
`cel-lens.exampleResults` answers every result (see [What the rules do to
the examples](#what-the-rules-do-to-the-examples)). The setting
`exampleResults` turns all of it off, the schema's problems on the
examples included.

**An example** is a YAML document of the workspace whose `apiVersion` and
`kind` are a version the CRD serves: its group, a version with `served:
true`, and its kind. They are found as `cel-lens.typeChoices` finds its
`examples`: the walk of the workspace folders skips hidden folders and
`node_modules`, files over 1 MiB and what a symbolic link leads to outside
the folder, and stops after 20,000 YAML files; an open file counts with the
editor's text; a file whose text does not have the kind's name is not
parsed. A CRD has at most 100 examples, the first in the order of the files'
paths. A document in the CRD's own file is none of its examples, and nor is
a `.cellens`, which is no YAML file. An open file outside every workspace
folder is not walked, and so is no example.

**What it expects.** An example expects the API server to allow it, unless:

- a folder of its path below its workspace folder, or a word of its file
  name split at `-`, `_` and `.`, is `invalid`, `bad`, `refused`, `rejected`
  or `denied`, in any case: then it expects to be refused
  (`testdata/invalid/big.yaml`, `config/samples/widget-bad.yaml`, but not
  `badge.yaml`);
- a comment at the top of the document says what it expects, which wins over
  the path: `# cel-lens: refused`, `# cel-lens: refused "part of the
  message"`, a double-quoted string, or `# cel-lens: allowed`. The comments
  read are the comment lines directly above the document's first key, with
  no blank line between, and, after a `---`, the comment on that line and the
  comment lines directly after it. The first that reads is taken. A
  `# cel-lens:` comment there that is none of the three forms is an error on
  that comment: `this # cel-lens: comment is none of # cel-lens: allowed, #
  cel-lens: refused and # cel-lens: refused "part of the message"`.

**Updates.** Two examples of one kind (its group and kind, in any version),
namespace and `metadata.name` in one file are an update: the first is
created, each later one is an update from the one before, as `kubectl apply
-f` of the file does. A namespaced example without a namespace is in
`default`; the namespace of a cluster-scoped one does not count; an example
without a name is always a create. The schema check below pairs an example
with the one before it when the schema accepts that one; the rules pair it
by what the API server would have stored (see [What the rules do to the
examples](#what-the-rules-do-to-the-examples)).

**The schema check.** Each example is checked as the API server checks a
custom resource before its CEL rules, with its own code
(`k8s.io/apiextensions-apiserver`), for the version the example names:

1. decoding, with the strict field validation `kubectl` asks for by default:
   a field the schema does not have is pruned and is a problem, `unknown
   field "spec.extra"`; `metadata` the API server cannot read is the only
   problem;
2. the CRD's defaults;
3. what the API server does before validating: the namespace `default` for a
   namespaced example that names none, and none for a cluster-scoped one, a
   name for a `generateName` (it ends with `xxxxx`), and, with the status
   subresource, no status on a create and the old one on an update;
4. the validation of `metadata`, of the schema (types, `required`, `enum`,
   `maximum`, `pattern`, `format` as the Kubernetes version picked validates
   formats, and the rest), of embedded resources and of
   `x-kubernetes-list-type` lists. An update is checked as an update from
   the example before it, with the API server's ratcheting: a field left as
   it was is not refused again. When the schema refuses the example before
   it, there is nothing to update, and it is checked as a create, as
   `kubectl apply -f` would create it.

The scale subresource's check of the replicas is not run. Each problem is a
diagnostic on the example, severity error (1), source `cel-lens`, with the
API server's message, such as `spec.size: Invalid value: 20: spec.size in
body should be less than or equal to 10`, at the key of the field it names,
or of the nearest field above it the document has (`spec` for `spec.size:
Required value`), or at an item of a list (`spec.ports[1]: Duplicate
value`), and at the document's first key for its root. An example that
expects to be refused gets no diagnostic for the schema's problems: it is
right to be refused. One the schema accepts gets none either: what its rules
do to it is a warning when it is wrong (see [What the editor shows of the
results](#what-the-editor-shows-of-the-results)). A CRD the API
server would refuse, for anything but its rules, has no example diagnostics:
its problems are already on it. A rule that does not compile leaves the
schema as it is, so its examples are still checked.

**When.** The examples are found and checked in the background, never on the
message loop, so hover, completion and the lines above the rules wait for
nothing. Opening, editing or closing an example or a CRD, a change on disk
the client reports (`workspace/didChangeWatchedFiles`: a YAML file or a
folder created, changed or deleted), the end of a workspace scan that
changed the CRDs, and a change of `kubernetesVersion` stop the check running;
the next starts once nothing changed for 250 ms, so checks run when typing
pauses. A `.cellens` starts none: it gives types to the values CEL reads to
check expressions, and the schema check, like the API server's validation
of an object, never reads them. While the server handles a message, the
check waits between two steps of its work (an example, a file), so a
request that comes during a check takes no time from it. When a check ends and nothing changed meanwhile, the diagnostics of
each file whose example problems changed are sent. An open example's
diagnostics are its document's own and its example problems: after an edit
its own are sent at once, and its example problems join them when the check
of the new text ends. A file that is not open gets its example problems at
its file URI, and an empty list once it has none; closing an example sends
the problems of the file on disk. A file on disk is read again when its size
or time changed, or the client reported a change, and a CRD when its text or
the Kubernetes version did; the examples of a file are found and checked
again only when its text or the CRDs read changed. `go test -bench Examples ./internal/lsp/`
measures hover, completion and the code lenses on a 1,000-line CRD as it is
typed, 260 ms after each change, with no example and with 100, and
`go test -bench ExampleCheckRunning ./internal/lsp/` the same requests while
a check of 100 examples is in progress and while none is.

### What the rules do to the examples

`cel-lens.exampleResults` runs each `x-kubernetes-validations` rule of the
CustomResourceDefinitions of a file on each of their examples, with the API
server's own CEL validator, and answers what each rule does to each
example; for a file with ValidatingAdmissionPolicies, what each of their
expressions does too (see [What an admission policy does to the
examples](#what-an-admission-policy-does-to-the-examples)). Its one argument is `{"uri": ...}`, the file, for every rule of
it, or `{"uri": ..., "range": ...}`, a rule's location as the line above
the rule gives it to `cel-lens.showRule`, for that rule alone: the rule
whose text, or whose `messageExpression`, has the range's start. The file
is read as the editor has it, else from disk, and its CRDs count whatever
the experimental features; the examples are found and checked as above. A
CRD the API server would refuse for anything but its rules has none, and a
file without a CRD or a policy answers `"rules": []`. Another argument is refused with
`InvalidParams` (-32602), a file that cannot be read with `RequestFailed`
(-32803).

**How a rule runs**, all with the API server's code
(`k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel`); nothing about
what a rule means is the engine's own:

1. A rule for which the API server refuses the whole CustomResourceDefinition
   does not run: no object is ever validated with it. That is what the
   engine's own check of the CRD file (the diagnostics of the file, for the
   Kubernetes version picked) finds on the rule's `x-kubernetes-validations`
   entry: a rule or `messageExpression` that does not compile, a cost
   estimated over the limit of one rule, a `fieldPath` that is not a path of
   the schema, `oldSelf` where the schema cannot correlate an old value,
   `optionalOldSelf` on a rule that does not read `oldSelf`, and the rest
   the API server says of a rule. The reason gives that check's message, the
   one the line above the rule shows. When the estimated cost of all the
   rules of a schema together is over the limit of one schema, the API
   server refuses the whole CRD, naming its four most expensive rules
   (fewer when it has fewer) as having contributed to the total, so no rule
   of the CRD runs, with that message. For a CRD the editor has open, the
   check is the one the server made of its text for its diagnostics; for
   another, the command checks it, off the message loop.
2. It runs on the object the schema check above made (decoded, its defaults
   applied and prepared as for the API server), with the validator the
   API server builds for the version's structural schema (`NewValidator`,
   the limit of one call `PerCallLimit`, 1,000,000), holding this rule
   alone, and its `Validate` with the budget of one object
   (`RuntimeCELCostBudget`, 10,000,000). The validator walks the object: a
   rule under a list or a map runs once for each item or value, and each
   refusal has its own path. `message`, `messageExpression`, `reason` and
   `fieldPath` give the message as the API server gives it.
3. An example is an update exactly when an example before it in its file,
   of the same object (see **Updates** above), would have been stored by
   the API server, its schema and each of its rules accepting it, as a
   create or an update itself: it is then an update from the last such
   example, so `oldSelf` is that example's value. Otherwise it is a create.
   That is what `kubectl apply -f` of the file does: an example the API
   server refuses changes nothing, so the next one is created when none
   was stored, and else updates what was. An example the schema check above
   paired otherwise is checked against the schema again, as the create or
   update it is, for its results; its diagnostics stay those of the schema
   check. A rule that reads `oldSelf` runs only where the update has an old
   value, unless it sets `optionalOldSelf`: then it runs on creates and
   updates. An update has the API server's validation ratcheting, on by
   default since 1.30 and always on since 1.33, so in every supported
   version: a refusal of a value the update left as it was is ignored, and
   the result says so. As an update is only ever from an example the same
   rules allowed, that does not happen with these examples.
4. As the API server, no rule runs on an example it cannot decode, one with
   an unknown field (`kubectl` asks for strict field validation), or one
   whose schema problems include one the API server stops at (a missing
   required field, a wrong type, an unsupported value, a string or list
   too long).
5. Where a rule runs is found with a probe: a rule at the same place that is
   `false` wherever it runs (`[oldSelf].size() == 0` for a rule that reads
   `oldSelf`, with its `optionalOldSelf`), which the same validator refuses
   once for each value it runs on. The probe has none of the rule's
   meaning.
6. The rules of one version share the API server's budget of one object:
   when, on an example, they cost more together, or one hits a limit,
   after which the API server runs no further rule, each that ran is an
   `error`, as which of them the API server runs before it stops depends on
   the order it visits the object's fields in.

The answer has the Kubernetes version and, for each rule, in the order of
the file, its name as the line above it and a `.cellens` entry name it
(`document`, `version`, `path`: field names separated by dots, `[]` for the
items of a list, `[*]` for the values of a map, `""` for the root), its
`index` in that node's `x-kubernetes-validations`, its text, its
`location` and its `results`, one for each example of its version, in the
order of the files' paths:

```json
{
  "kubernetesVersion": "1.37",
  "rules": [{
    "document": "CustomResourceDefinition widgets.example.com", "version": "v1",
    "path": "spec.ports[]", "index": 0, "rule": "self.name.size() <= 3",
    "location": {"uri": "file:///repo/crd.yaml", "range": {"start": {"line": 38, "character": 30}, "end": {"line": 38, "character": 51}}},
    "results": [{
      "example": {"uri": "file:///repo/config/samples/widget.yaml", "file": "config/samples/widget.yaml",
                  "line": 1, "apiVersion": "example.com/v1", "kind": "Widget", "name": "w",
                  "operation": "create", "expect": "allowed"},
      "result": "refused",
      "refusals": [{"path": "spec.ports[2]", "message": "spec.ports[2]: Invalid value: port name https is too long"}],
      "ran": ["spec.ports[0]", "spec.ports[1]", "spec.ports[2]"],
      "expected": false, "wrong": "rule"
    }]
  }]
}
```

| Key of a result | What it is |
| --- | --- |
| `example` | The example: its file's `uri` and its `file` below the workspace folder, the `line` its content starts at, from 1, its `apiVersion`, `kind`, `namespace` (left out when it has none) and `name`, the `operation` the rule ran as, `create` or `update`, what it `expect`s, `allowed` or `refused`, and the part of the `message` it expects, when it gives one |
| `result` | `allowed`, `refused`, `not run` or `error` |
| `refusals` | For `refused`, and for an `error` that refused values before it stopped: each refusal, its `path` as the API server writes it (`""` for the root) and its `message`, the API server's, the path first (`<nil>` for the root, as the API server writes it) |
| `ignored` | The messages of refusals ratcheting ignored, on an update |
| `ran` | The paths of the values the rule ran on |
| `reason` | Why a rule did `not run`: `the API server refuses the CustomResourceDefinition for this rule, so it validates no object with it: ...` (also for a rule that does not compile for the Kubernetes version picked), `the API server refuses the CustomResourceDefinition for the estimated cost of its rules together, so it validates no object with any of them: ...`, `the rule's field is absent from the example`, `the example is not an update and the rule needs oldSelf` (followed, for a create after a refused example, by `: the API server refuses the example before it, so kubectl apply -f creates this one`), `the rule needs oldSelf, and the update has no old value where the rule is`, or what keeps the API server from running any rule on it (above) |
| `error` | What stopped an `error`: the API server's runtime error (`division by zero evaluating rule: ...`), a cost limit, or the budget the rules share |
| `expected`, `wrong` | Whether the result is what the example expects, and, when not, where that shows: `rule` or `example` |

**What the example expects.** An example that expects to be allowed
expects every rule to allow it or not to run: a rule that refuses it, or
fails on it, is `wrong` at the `rule`. An example that expects to be
refused is right when the schema or at least one rule refuses it (an
`error` counts, as the API server refuses the object with it), with the
message part it gives in that refusal's message when it gives one; then
each of its results is `expected`, and otherwise none is, and each is
`wrong` on the `example`.

**When.** The rules run in the background, off the message loop: the
request waits, while hover, completion and the lines above the rules are
answered meanwhile. While the setting `exampleResults` is `false` the
command runs nothing and is refused with `RequestFailed` (-32803), saying
so. A request for the same file and location waits for the
same work; one for the same file and another location stops it, and so
does `$/cancelRequest` when no other request waits for it (see "Commands in
the background" above). A change of `kubernetesVersion` stops it too, as
its answer would be for the old version, and answers its requests with
`RequestCancelled`. What each rule did to each example is kept, for
each file asked about, until the rule, the version's schema, the example
(its object after the schema check, or the object it updates) or the
Kubernetes version changes: asking again runs only what changed, and what
the file's rules and examples no longer have is dropped. The command
finds the examples with the walk of the check above, and takes what the
last check kept when no check runs: the examples of a file whose text and
CRDs did not change are not checked again. While the server handles a
message, the command waits between two steps of its work, as the check
does. `go test
-bench ExampleResults ./internal/lsp/` measures a CRD of 50 rules with 100
examples: with its schema changed, so every rule runs again, with one rule
changed, and unchanged.

### What the editor shows of the results

The check of the examples above also runs every rule of each CRD on its
examples, as `cel-lens.exampleResults` does, sharing what it keeps, and
shows what it found where a person looks. The words are in one place,
package `inline`, whose tests hold each of them.

**The line above the rule** gains a last part, which runs
`cel-lens.showExampleResults` with the rule's location, the argument
`cel-lens.showRule` takes: a client command, which the server does not
run and `initialize` does not declare. It counts the examples of the rule's
version, a refused example that expects to be refused counting as
passing, the wrong verdicts first:

| What the rule does | The part |
| --- | --- |
| every example as it expects | `3 examples pass`, `1 example passes` |
| refuses examples that expect to be allowed | `refuses 1 example it should allow · 2 pass` |
| refuses an example that expects another message (`# cel-lens: refused "..."`) and that nothing refuses with it | `refuses 1 example with another message · 2 pass` |
| allows an example that expects to be refused and that nothing refuses | `allows 1 example it should refuse` |
| fails on examples (a runtime error or a cost limit), whatever they expect | `1 example errors`, `2 examples error`; after a wrong verdict `1 error` |
| runs on none of them | `not run: CRD refused` (the API server refuses the CRD for the rule), `not run: needs an update example` (the rule reads `oldSelf` and every example is a create), `not run: no old value in the updates`, `not run: field absent from the examples`, `not run: examples refused before the rules` (decoding, or a schema problem the API server stops at); `not run on 3 examples` for several of these |
| has no example | no part |

The first count names the examples and those after it do not
(`refuses 2 examples it should allow · allows 1 it should refuse · 1
error · 3 pass`). An example the rule did not run on is left out, but
when it ran on none. The line keeps its 80 characters: the part is
shortened first, to the wrong verdicts as one count (`2 wrong · 3 pass`),
then to its first count (`2 wrong`), and only then are the values before
it cut or left to `+N more`, and last the subject's type.

**The rule's hover** gains a section below what the place under the cursor
is, or alone where that is nothing: the part in words, then each example,
linked to its file and line (`file:///...#L3`), with its verdict, `as an
update` for an update, what it expected when the verdict is wrong, and the
API server's message (its refusals, its error, or why the rule did not
run). The `#L<line>` at the end of a link is VS Code's convention, which
opens the file at that line; the protocol has none, and another client
may open the file at its top. The wrong verdicts come first, then the
errors, then the others in the order of the files' paths; at most 10,
then `and N more`.

**A wrong verdict is a warning**, severity 2, source `cel-lens`: the API
server would accept the CRD, and it is the example that disagrees.

- A rule that refuses, or fails on, an example that expects to be allowed:
  on the rule, `refuses config/samples/big.yaml:3, which expects to be
  allowed: <the API server's message>`, and on the example, at its first
  key, `expected to be allowed, but the rule "self.size <= 10" refuses it:
  <message>` (the rule on one line, cut past 60 characters).
- An example that expects to be refused and that neither the schema nor a
  rule refuses: on the example, `expected to be refused, but nothing
  refuses it`, or, with a message part, `expected to be refused with "at
  most 10", but nothing refuses it with that message`.

They are sent as the schema's problems on the examples are (see **When**
above): with an open file's own diagnostics, for the text they were found
in, and at the URI of a file that is not open, the file of the CRD
included; they change, and clear, when the check of the new text ends.

**When.** The rules run in the check of the examples, once typing pauses,
off the message loop, waiting while the loop handles a message before each
run of a rule; a rule, its schema, an example and the Kubernetes version
that did not change keep their results. For an open CRD the check takes
the server's own check of its text, as the command does; for a CRD that is
not open it checks the file itself, once per text and version. That
check, the command's and the server's own check of an open document all
validate a CRD with the API server's code, which reads the Kubernetes
version from a setting of the whole process; checks for the same version
share it and run at once, so the server's checks never wait for the
others. The one exception is the moment after a change of
`kubernetesVersion`, which stops the check of the examples and every
`cel-lens.exampleResults` running: the CRD each of them is validating for
the old version as the change comes is validated to its end, and the
server's checks for the new version wait for it (about 45 ms for one of 50
rules); neither validates another. `go test -bench
ClosedCRDs ./internal/lsp/` measures a person typing in a small open CRD
while 20 CRDs that are not open, of 50 rules with 5 examples each, are
checked: the longest wait is the same as without the examples. Hover,
completion and the lines never wait for the results: they show the last
results a check found for the Kubernetes version in use, and the server
asks the client to refresh the lines (`workspace/codeLens/refresh`) when a
check finds others. The line and the hover find a rule's results by its
document, field path and text, so an edit elsewhere in the CRD keeps them
above the rule until the check of the new text ends, and a rule whose text
changed has none until then. After a change of `kubernetesVersion` the
lines show none until the check for the new version ends.

**The setting** `exampleResults` (`initializationOptions` or the
`settings` of `workspace/didChangeConfiguration`; default `true`) turns
off the part, the hover's section, the warnings and the schema's problems
on the examples, and the check of the examples itself: off, nothing about
the examples is read, checked or run, and `cel-lens.exampleResults` is
refused. Turning it off sends empty diagnostics where any were shown and
refreshes the lines; turning it on starts a check, without a restart.

`go test -bench ExampleLine ./internal/lsp/` measures hover and the code
lenses of an open CRD of 50 rules with 100 examples, 255 ms after a change,
as the check of the examples starts; it runs on a server without any of
this too. `go test -bench ExampleRulesRunning ./internal/lsp/` measures the
same requests while the check runs every rule on every example again, from
an empty cache, and while no check runs.

### What an admission policy does to the examples

The check of the examples, and `cel-lens.exampleResults` given a file with a
ValidatingAdmissionPolicy (`admissionregistration.k8s.io/v1`), run each
policy of the workspace on its examples, and the editor shows what each of
its validations, match conditions and audit annotations does to them, above
the expression, in its hover and as warnings, with the words above and those
below. A variable has no verdict of its own: the API server evaluates it
when an expression reads it, and its error is that expression's.

**Its examples** are the documents of the workspace, outside the policy's own
file, that its `matchConstraints` match, as the API server's
ValidatingAdmissionPolicy plugin matches a request, with its own matcher
(`k8s.io/apiserver/pkg/admission/plugin/policy/matching`): the `resourceRules`
(groups, versions, resources, operations, `scope`, `resourceNames`),
`excludeResourceRules`, `matchPolicy` (with `Equivalent`, the API server's
default, another version the API server serves of the same resource matches
too), the `namespaceSelector` against the labels of the Namespace document
of the workspace that names the example's namespace, and the
`objectSelector` against the example's own labels. A namespace the
workspace has no Namespace document for is taken as the API server has it
with no label of its own: with the label `kubernetes.io/metadata.name`
alone, which it gives every namespace. A document is one of a built-in kind
the Kubernetes version picked serves, or an example of a
CustomResourceDefinition of the workspace (see above); a policy or a
binding is none, as the API server exempts them; another kind is none, as
its resource and scope are not known. The walk is the check's, with its
limits, and reads the files with the name of a kind a policy may match, its
params' kind or `Namespace` (every file, for a rule with `*` in its groups
or resources). At most 100 examples of a policy run, the first in the order
of the files' paths. What an example expects, and when it is an update, are
as for a CRD's examples, by path and `# cel-lens:` comments; an update is
from the last example before it of the same object that the API server
would have stored: one nothing refused before the policies and that no
binding of the policy refuses.

A document of a built-in kind is read as the API server reads it, into its
Go type with the strict field validation kubectl asks for: a field the
type does not have refuses it before any policy runs. Its defaults and its
validation are the API server's own registry's (`k8s.io/kubernetes`), which
the engine does not have: the policy sees the document as its type reads
it, in its namespace (`default` when a namespaced one names none), named
for its `generateName`. A custom resource is the object the schema check
made, defaulted; one the schema, or a rule of its CRD, refuses is refused
before the policies, as the API server validates a custom resource before
its validating admission, and no expression runs on it (`not run: examples
refused before the rules`).

**The bindings.** The policy runs once under each
ValidatingAdmissionPolicyBinding of the workspace that names it in
`policyName`, on the examples the binding's `matchResources` also match,
with its params: the documents of the policy's `paramKind` that its
`paramRef` names (`name`, or `selector`, which may match several, in
`namespace`, or the example's own for a namespaced kind), each a run of its
own, in the order of their namespaces and names. The API server lists them
in no fixed order, so when several params of a `selector` make a
validation fail under `Deny`, the message the API server refuses the
example with may be another param's than the first cel-lens gives; with none found, its
`parameterNotFoundAction` (`Deny` refuses the example, `Allow` runs
nothing). A param of another version of a custom resource whose CRD has
the conversion strategy `None` counts, in the version `paramKind` names. With
no binding of the workspace for the policy, it runs as if bound with
`validationActions: [Deny]` and no params, and an expression that reads
`params`, directly or through a variable, is `not run` (`no binding gives
params`). When a match condition reads `params`, whether the policy runs
at all is not known without them, and nothing of it runs, each expression
`not run: no binding gives params`. Each result says which binding, and which param, it is from.

**How it runs**, all with the API server's code for the Kubernetes version
picked; nothing about what an expression means is the engine's own:

1. A policy for which the engine's check of its file finds an error on a
   variable, a validation, a match condition or an audit annotation (it
   does not compile in the API server's environment for new expressions)
   is refused by the API server, which then admits nothing with it: every
   expression is `not run` (`policy refused`), with the check's messages.
2. The policy is compiled as the ValidatingAdmissionPolicy plugin compiles
   it (`compilePolicy` of
   `k8s.io/apiserver/pkg/admission/plugin/policy/validating`, with its
   CEL compiler, `k8s.io/apiserver/pkg/admission/plugin/cel`), in the
   environment of the version picked, into the plugin's own evaluator
   (`validating.NewValidator`); each request is admitted by the plugin's
   own dispatcher (`validating.NewDispatcher`), which matches the binding,
   collects the params (`generic.CollectParams`), and acts on what the
   evaluator decides, with the binding's `validationActions`.
3. The request is a `CREATE`, or an `UPDATE` with `oldObject`, by no user
   in particular (`request.userInfo` is empty), with `namespaceObject` the
   example's Namespace document, or the one the API server has.
4. `matchConditions` first: a match condition that is false excludes the
   example (`excluded`), and nothing else of the policy runs on it (`not
   run: excluded by matchConditions`); an error is the policy's
   `failurePolicy`'s: under `Fail` it is an `error` of every validation,
   which refuses the example under `Deny`, unless another condition is
   false; under `Ignore` the condition is an `error` that says the API
   server ignores it, and the policy does not run on the example. Each match condition's own verdict comes from
   evaluating it alone with the API server's matcher, as the evaluator says
   only whether all of them match.
5. `validations`, with `variables`, under the API server's cost budget: a
   validation that is false fails with its `messageExpression`, else its
   `message`, else `failed expression: ...`, and its `reason`; an error
   follows `failurePolicy` (`Fail` fails the validation, `Ignore` admits).
   A failure is `refused` under a binding with `Deny`, `warned` under
   `Warn` without `Deny` (the example is allowed, with the API server's
   warning), and `audited` under `Audit` alone (allowed, with the
   `validation.policy.admission.k8s.io/validation_failure` annotation).
6. `auditAnnotations`: a value is `annotated`, never a refusal; null or
   empty adds none (`allowed`); an error under `Fail` refuses the example,
   as the API server does.
7. `authorizer` checks are not run: an expression that reads `authorizer`,
   directly or through a variable, is `not run` (`reads authorizer`) and
   left out of the policy as it runs; with a match condition that reads it,
   nothing of the policy runs.
8. An example the policy matches in another version than its own is
   converted by the API server first: for a custom resource whose CRD has
   the conversion strategy `None`, as it does (its `apiVersion` is set);
   another is `not run` (`needs a conversion`).

The answer of `cel-lens.exampleResults` for a file with policies lists their
expressions after its CRDs' rules, each with `document`
(`ValidatingAdmissionPolicy <name>`), `field` (`validation`,
`matchCondition` or `auditAnnotation`), `name` (a match condition's name or
an annotation's key), `index`, `rule` (its text) and `location`, and its
results, one for each example under each binding and param:

```json
{
  "document": "ValidatingAdmissionPolicy replicas", "field": "validation", "index": 0,
  "rule": "object.spec.replicas <= int(params.data.max)",
  "location": {"uri": "file:///repo/policy.yaml", "range": {"start": {"line": 14, "character": 18}, "end": {"line": 14, "character": 62}}},
  "results": [{
    "example": {"uri": "file:///repo/deploy.yaml", "file": "deploy.yaml", "line": 1, "apiVersion": "apps/v1",
                "kind": "Deployment", "name": "web", "operation": "create", "expect": "allowed"},
    "binding": "limits",
    "param": {"uri": "file:///repo/params.yaml", "file": "params.yaml", "line": 1, "apiVersion": "v1", "kind": "ConfigMap", "name": "small"},
    "result": "warned", "message": "at most 3 replicas",
    "admission": {"warnings": ["Validation failed for ValidatingAdmissionPolicy 'replicas' with binding 'limits': at most 3 replicas"]},
    "expected": true
  }]
}
```

| Key of a policy's result | What it is |
| --- | --- |
| `binding` | The binding, left out when no binding of the workspace binds the policy |
| `param` | The param it ran with, left out for none |
| `result` | `allowed`, `refused`, `warned`, `audited`, `annotated`, `excluded`, `not run` or `error` |
| `message` | A failed validation's message, an annotation's value |
| `reason`, `error` | Why it did `not run`; what failed, for `error` |
| `admission` | The API server's answer to the example under the binding: `denied`, the error it refuses it with (`deployments.apps "web" is forbidden: ValidatingAdmissionPolicy 'replicas' with binding 'limits' denied request: ...`), its `warnings` and its audit `annotations` |
| `expected`, `wrong` | As for a CRD's rule |

**What the example expects.** An example that expects to be allowed is
wrong at an expression whose result refuses it: a failed validation under
`Deny`, an error that refuses, a failed audit annotation. One that expects
to be refused is right when something refuses it as it expects: the schema,
a rule of its CRD, or any policy under any binding, the message part, when
it gives one, in that refusal's message; a warning or an audit annotation
refuses nothing. That holds across the CRDs and the policies: a CRD's rule
that allows an example a policy refuses as it expects passes too.

**The line and the hover** use the words above, and these:

| What the expression does | The part |
| --- | --- |
| a validation fails under `Warn` | `warns 1 example` |
| a validation fails under `Audit` alone | `audits 1 example` |
| a match condition is false | `excludes 1 example` |
| an audit annotation has a value | `annotates 1 example` |
| runs on none of them | `not run: policy refused`, `not run: excluded by matchConditions`, `not run: no binding gives params`, `not run: no params` (`parameterNotFoundAction: Allow`), `not run: binding refuses the request` (`Deny`), `not run: reads authorizer`, `not run: needs a conversion` |

An example a policy ran on under several bindings or params counts once, as
the first of what they did in the order wrong, error, warns, audits,
excludes, annotates, passes. The words end with the binding when all the
results are under one, `under binding limits`, or `under 2 bindings`, or
`under no binding`, which the part drops first when the line is long. The
hover lists each result with its binding and param (`**warned** under
binding limits with param params.yaml:1: at most 3 replicas`), and so do the
warnings (`refuses deploy.yaml:1 under binding limits with param
params.yaml:1, which expects to be allowed: at most 3 replicas`).

**When.** As for the rules of a CRD: in the check of the examples, off the
message loop, waiting while the loop handles a message before each
example is admitted; for an open policy, the check takes the server's own
check of its text. What each policy did to each example under each binding
is kept until the policy, the binding, its params, the example's namespace,
the example (its object, the object it updates, what refused it before) or
the Kubernetes version changes. Nothing of this reads the process-wide
compatibility version that `check.UnderVersion` guards: the policy is
compiled in the environment of the version picked. `go test -bench
PolicyRun ./internal/examples/` measures a policy of 20 validations over
100 examples, from an empty cache and unchanged; `go test -bench
PolicyLineRunning ./internal/lsp/` hover and the code lenses of the open
policy while the check runs it on 100 examples again and again, and while
no check runs.

## Types from `.cellens`

Giving a `dyn` value a type of your own, the `.cellens` files and **Give it
a type**, is the experimental feature `cellens-types` (see the README): it
is off unless `experimental` lists it, in `initializationOptions` or in the
`settings` of `workspace/didChangeConfiguration`. Off, the server acts as if
no `.cellens` existed:

- no `.cellens` is read, and the search for them does not run; a `.cellens`
  the editor opens is kept as the editor's text, unread, for when the
  feature turns on;
- the line above a rule shows no `Give it a type`, `Edit` or `Remove`, and
  no value a `.cellens` types: only the subject and whether it compiles;
- a value a `.cellens` would type is `dyn`, in diagnostics, hover and
  completion, and hover on a `dyn` value a `.cellens` entry could type
  (`params`, `object`, `oldObject` or `variables.<name>` of an admission
  policy, a `dyn` value inside them, or an `x-kubernetes-int-or-string`
  value of a CRD) adds `Giving it a type of your own in .cellens is the
  experimental feature cellens-types, which is off.`, as hover on `params`
  says `crd-typed-policies` is off;
- an open `.cellens` gets no diagnostics, hover or completion: they answer
  `null`, and the diagnostics a `.cellens` had are taken back;
- the example results are those of values left `dyn`, as they always are:
  the examples run with the API server's own types, which no `.cellens`
  changes;
- the commands behind **Give it a type** fail with a message that says the
  feature is off (see [Commands](#commands)).

Off, the server tells the user once per session that the workspace holds a
`.cellens` it does not read: at `initialize` it walks the workspace folders,
as the search would but opening no file, until it finds one, and it also
learns of one the editor opens, one `workspace/didChangeWatchedFiles`
reports created or changed, and one in a folder reported created. It sends
`window/showMessageRequest`, `type` 2, `cel-lens does not read
deploy/.cellens: giving a value a type of your own in .cellens is the
experimental feature cellens-types, which is off: add cellens-types to the
experimental features (cel-lens.experimental in VS Code) to read it.`, with
one action,
`{"title": "Turn on cellens-types", "experimental": "cellens-types"}`.
The client turns the feature on when that action is picked, by adding the
name its `experimental` names to its setting of the experimental features,
at the scope that sets it (the VS Code extension: `cel-lens.experimental`);
the server changes no setting, and ignores the answer. Turning the feature
off in a session sends no notice, then or later: the person just chose it.

Turning `cellens-types` on or off with `workspace/didChangeConfiguration`
needs no restart. Off, the server stops the search in progress, forgets
every `.cellens` and checks the open documents again. On, it reads the open
`.cellens` files with the editor's text, checks the open documents again and
searches the workspace folders for the others in the background, after
which the open documents are checked again with what it found, as at
`initialize`. One method of the server, `readsLenses`, answers whether
`.cellens` is read, and every path above asks it once
(`TestReadsLensesPaths` lists them). On, everything behaves as in v0.31.0.

## Typed `object` and `oldObject`

In a ValidatingAdmissionPolicy whose `matchConstraints.resourceRules` match
built-in kinds, `object` and `oldObject` are typed from that kind's schema, as
the API server does when it type-checks a policy: completion offers the kind's
fields down the tree, hover names the kind (`object: apps/v1 Deployment`) and
gives a field's type and description, and a field the kind lacks gets a
**warning** (not an error) such as `apps/v1 Deployment: undefined field
'additionalContainers'`, the API server's `status.typeChecking` wording.

The kind comes from the `apiGroups`, `apiVersions` and `resources` of the rule,
including the core group `""`; subresources such as `deployments/scale` are
ignored as the API server ignores them. Typed kinds are the built-in resources
of `k8s.io/api`. A `*` in a rule, a custom resource or any resource the engine
has no schema for, leave `object` as `dyn` with no field warning, even when
the other matched kinds are known. The schemas are those of the bundled `k8s.io/api`, not
yet of the `--kube-version` chosen.

When a policy matches several kinds, the API server type-checks it against
each. Completion after `object.` lists the fields every kind has first, then
the others, and each item names the kinds that have the field (`int ·
Deployment, StatefulSet`); hover on `object` lists the kinds and hover on a
field says which have it; a field only some kinds have gets one warning for each
kind that lacks it, such as `apps/v1 Deployment: undefined field 'serviceName'`.

## Typed policy variables

Every CEL field of a ValidatingAdmissionPolicy and of a MutatingAdmissionPolicy
(match conditions, variables, and the expression of each mutation) knows the
variables the API server's admission compiler declares, with completion, hover
and checking:

| Variable | Type | Notes |
| --- | --- | --- |
| `object` | the matched kind, as above | null on DELETE |
| `oldObject` | the same type as `object` | null on CREATE; `oldObject == null` checks clean |
| `request` | `kubernetes.AdmissionRequest`: `kind`, `resource`, `subResource`, `requestKind`, `requestResource`, `requestSubResource`, `name`, `namespace`, `operation`, `userInfo`, `dryRun`, `options` | |
| `namespaceObject` | `kubernetes.Namespace`: `metadata`, `spec`, `status` | null for cluster-scoped resources |
| `authorizer` | `kubernetes.authorization.Authorizer` | `group`, `path`, `serviceAccount`, then `resource`, `subresource`, `namespace`, `name`, `fieldSelector`, `labelSelector`, `check` and the decision's `allowed`, `reason`, `errored`, `error`; `authorizer.requestResource` (a `kubernetes.authorization.ResourceCheck` field, with hover and completion) is a ready-made resource check |

Completion follows an `authorizer` chain such as
`authorizer.group('apps').resource('deployments').namespace('ns').check('update').allowed()`
and offers the next method at each step. A wrong field such as `request.usrInfo`
gets the API server's `undefined field 'usrInfo'`, and `self`, which only
`x-kubernetes-validations` rules declare, gets `undeclared reference to 'self'`.
`params` is typed from `spec.paramKind` (next section). `variables.<name>` has
the type of the expression of `spec.variables[]` that defines it, as the API
server types it: a scalar, list or map keeps its type (`object.spec.replicas`
is `int`, `request.userInfo.groups` is `list(string)`) and anything else,
such as an object, or an expression that does not compile, is `dyn`. With
`params`, `object` or `oldObject` typed by a `.cellens` entry, each variable is
typed in order with the type its expression returns with that type, in
checks, completion, hover and inline info ([cellens.md](cellens.md#how-the-types-are-used)). Completion
after `variables.` offers the variables defined before that point, each with its
type, and hover gives the type and the defining expression. A name the policy
has no variable for, or defines later, gets `undefined field '<name>'`. A type-check warning that depends on no
variable, such as the `authorizer.requestResource` one, is still one line per
matched kind naming that kind, as `status.typeChecking` writes it. For a
MutatingAdmissionPolicy, a
mutation must return `Object` (`applyConfiguration`) or a list of `JSONPatch`
(`jsonPatch`), and `object` and `oldObject` are typed for completion and hover
but not checked against the matched kinds, as the API server does not.

A variable whose expression compiles, but not with the type of a kind the
policy matches, is `dyn`, and hover says it does not compile. The API
server's type checker skips variables, so it reports nothing; the engine
checks each variable with each matched kind's type, in a
ValidatingAdmissionPolicy and in a MutatingAdmissionPolicy, whose variables
it types with the kinds too, and the problem is a warning at the expression
that names the kind and says it is the engine's own check:
`apps/v1 Deployment: undefined field 'replicaz' (cel-lens checks a variable
with the type of the kind matched, which the API server's type check skips:
the API server would not report this)`. A matched kind whose schema gives no
CEL type types nothing, and the variable is typed without it.

## Typed `params`

Typing `params` from a CustomResourceDefinition is the experimental feature
`crd-typed-policies` (see the README): off, `params` of a custom resource is `dyn`, and so are `object` and `oldObject` of a policy that matches one.
A built-in kind needs no feature.

`params` gets its type from `spec.paramKind` of the policy, as the API server's
type checker does:

- A built-in kind (`{apiVersion: v1, kind: ConfigMap}`): the schema of that kind
  at the Kubernetes version the results match.
- A custom resource (`{apiVersion: example.com/v1, kind: Widget}`): the schema
  of the named version in a CustomResourceDefinition. The language server reads
  the CRDs in the workspace folders (YAML files, skipping hidden folders and
  `node_modules`, up to 20,000 files of up to 1 MiB; when the scan stops at the file cap the
  client gets one warning, and one file skipped for its size is named in its own log
  message, many are reported in one summary line with their count, the limit and the first three names) and in the open documents; a rescan forgets a CRD file the scan no
  longer lists, but keeps one under a skipped folder (hidden or `node_modules`,
  learned from an open document) for as long as the file is on disk, and reads it again, so a CRD edited on disk there
  while closed gets its new schema at the next rescan. A
  workspace folder that is missing or cannot be read is logged as incomplete,
  by name: the rescan keeps the CRDs it knows below that folder, since nothing is known
  about them, and still forgets the CRD files deleted below the folders it did
  read. A scan that stops at the file cap keeps the CRDs of every folder;
  `cel-lens-core check` reads the CRDs among the files and folders it checks, so
  name the CRD's file or folder too. When a CRD is added, edited, closed or
  deleted (a deleted or renamed folder too: every CRD file below it is
  forgotten, and a folder created or renamed in is read: the YAML files below it,
  with the limits of the workspace scan), the open policies are checked again without restarting the server;
  for files that change on disk, the editor must send
  `workspace/didChangeWatchedFiles`, which the server asks for when the client
  allows it. These events are followed only while `crd-typed-policies` is on,
  except deletions; turning the feature on scans the workspace again.
- Anything else stays `dyn`, as before, with no field warnings: a kind that is
  neither built in nor defined by a CRD found as above, an unknown version, or
  a `paramKind` that is not a mapping with `apiVersion` and `kind`. Hover on
  `params` then says it is `dyn` because the kind was not found.

Hover on `params` is where its kind is named (`params: example.com/v1 Widget`).
A wrong `params` field, like a wrong `object` or `oldObject` field, gets one
warning per matched kind naming that kind, as the API server's
`status.typeChecking` does: `apps/v1 Deployment: undefined field 'maxReplica'`,
and a second line for `apps/v1 StatefulSet` when the policy matches both. Like `object`, `params` is checked against its
type only for a policy that matches built-in kinds. A policy without
`spec.paramKind` has no `params`: using it is the error `undeclared reference to
'params' (in container '')`, as the API server's admission compiler declares
`params` only when the policy has a `paramKind`.

Like the API server, the engine reports no cost problem on a policy expression
(`expression`, `messageExpression`, match conditions, variables and audit
annotations): the API server only compiles them when it validates a policy, and
its cost budgets apply when a request is evaluated. CRD rules
(`x-kubernetes-validations`) keep their cost checks.

## Kyverno policies

Experimental: nothing below happens unless the feature `kyverno` is on (see
the README). Off, a Kyverno policy gets no diagnostic, hover or completion.

The engine checks the CEL of a Kyverno `ValidatingPolicy`
(`policies.kyverno.io/v1`): `spec.matchConditions[].expression`,
`spec.variables[].expression`, `spec.validations[].expression` and
`messageExpression`, and `spec.auditAnnotations[].valueExpression`, in plain,
quoted and block scalars. Expressions are compiled in Kyverno's environment for
ValidatingPolicy, taken from **Kyverno v1.19.1**: `object`, `oldObject`,
`request`, `namespaceObject`, `variables`, `exceptions` and the libraries
`resource`, `http`, `globalContext`, `user`, `hash`, `math`, `json`, `yaml`,
`random`, `x509`, `time`, `transform`, `gzip` and the rest of that release's
libraries, with
Kyverno's messages and rules (a `messageExpression` must be a string, for
example). A function is only checked, never called, so nothing needs a cluster
or the network. The Kubernetes `--kube-version` does not apply to a Kyverno policy.

`object` and `oldObject` are typed from `spec.matchConstraints.resourceRules` as
for a ValidatingAdmissionPolicy (see
"Typed `object` and `oldObject`" above): completion and hover offer the
fields of the matched kinds, a field a kind lacks gets a warning that names the
kind, and `object` stays `dyn` when a matched kind can't be known. Completion and
hover in a ValidatingPolicy offer Kyverno's own variables and functions
(`resource.`, `http.` and the rest, but not `authorizer` or `params`),
and `variables.<name>` completes the policy's own variables.

Not yet supported: Kyverno's other policy kinds (the kind that verifies signatures and attestations,
`MutatingPolicy`, `GeneratingPolicy`, `DeletingPolicy`, the namespaced
variants, `ClusterPolicy` and `Policy`), other apiVersions of `ValidatingPolicy`,
and a ValidatingPolicy with another `evaluation.mode` than `Kubernetes` or
`JSON` (such as `Envoy`), which is not checked.

## Truth set

The answers the API server recorded in `status.typeChecking` live in
`pkg/check/testdata/truth/`, one YAML file per case: its `name`, the Kubernetes
`version` the answer came from (`v1.37.0`), the `policy` document as it was sent
to the API server, the `typeChecking` lines as the API server wrote them, and, only for a case
the engine does not yet answer the same way, `divergence` and `engine`.
`TestTruthSet` runs `check.Check` on each policy at that version and compares the
warnings with the lines: each line must match exactly one warning, whose message
is `<group/version> <Kind>: <message>`, and there must be as many warnings as
lines. The position after `<input>:` is kept for the record and not compared.
An answer with no line is `typeChecking: []`. When the API server returns several
`ERROR` entries for one kind, they stay in that kind's one answer, in the order
returned, each after the first on a new line of the same string (`\n` in a quoted
scalar): `"apps/v1, Kind=Deployment: ERROR: <input>:1:12: ...\nERROR: <input>:1:36: ..."`.
The test counts each entry as one line.

A case the engine does not yet answer as the API server does carries two more
fields: `divergence`, a note saying how the engine differs, and `engine`, the
warnings (full messages) the engine gives today, in order. The test then passes
while the engine's warnings equal `engine`, and fails when they agree with the
API server (remove both fields) or change in any other way (a new prefix or
wording: review it, then record the new answer). When the API server reports an error
the engine misses, the engine gives no warnings and the case records `engine: []`. A
`divergence` without `engine` (absent, not empty), or an `engine` without `divergence`, fails.

To add a case, record the policy and the `status.typeChecking` lines from a real
cluster, name the Kubernetes version it ran, and drop the file in the folder.
A change to the wording or the prefix of a type-check warning is checked against
these cases first and never made from a guess about the API server.

## How the YAML is read

The engine reads a document into its Kubernetes type as `kubectl apply` sends it
to the API server, whose YAML (`sigs.k8s.io/yaml`) is YAML 1.1 for the plain
scalars `y`, `n`, `yes`, `no`, `on` and `off`, in the cases `y`, `Y`, `yes`,
`Yes`, `YES` and the like (`yEs` is a string): they are the booleans `true` and
`false`. A field that wants a string then gets a boolean, which the API server
refuses with `cannot unmarshal bool into Go struct field`, and so the engine
reports it as an error at the word: `spec.auditAnnotations[0].key: expected a
string, got a boolean: kubectl reads an unquoted n as one (YAML 1.1), so quote it
if a string is meant`, with the rest of the document, its CEL too, still
checked. A word in quotes, or tagged `!!str`, is a string, as is any other
word. The enum of a string schema of a CRD with such a word is a warning at the
entry (the API server takes the CRD with the boolean in the enum, so a value of
the field equal to the word is refused), and
a schema problem on an example that comes of one names the line of the word.
`internal/yamlfield.Decode` is the one place that reads a document so, for every
path. Mapping keys are read as written, though kubectl sends the key `on` as
`true`.

## Command-line check

`cel-lens-core check [--format text|json] [--experimental name,name] <paths...>` runs the editor's checks
on files and folders (folders are walked for `.yaml` and `.yml` files):

```sh
cel-lens-core check deploy/ crd.yaml
```

Each problem is one line, `file:line:col: error: message` (`warning` for a field warning, which does not change the exit code); `--format json`
prints an array of `{file, start, end, severity, message}` instead (with `fixes`, a list of `{fieldPath, limit, value}`, when a limit such as `maxLength` would bring a rule under its cost budget; the language server sends the same as the diagnostic's `data`). Exit code 0
means no errors, 1 that errors were found, 2 a usage or read error (a missing
path, malformed YAML, which is also printed as a problem at the line where it
breaks, as the language server shows it), 130 that the run was interrupted (Ctrl-C). Unreadable paths do not stop the others being checked.


### Command-line examples

`cel-lens-core examples [--format text|json] [--kube-version <1.x>] [--fail-on wrong|error|none] [--verbose] [paths...]`
runs the rules of the CustomResourceDefinitions and ValidatingAdmissionPolicies
of the files and folders on their examples and exits 1 when a verdict is not
what its example expects, so a repository can run its examples in CI:

```sh
cel-lens-core examples ./config
```

It is not a second implementation of the editor's results: `lsp.RunExamples`
runs the check of the examples that the server's background check and
`cel-lens.exampleResults` run (finding the CRDs and the examples, pairing
updates, the bindings and their params, the API server's validation and
admission, `not run` and its reasons, the examples' own problems) once over
the workspace the paths make up, and answers the answer of
`cel-lens.exampleResults` for each file with rules, in the order of the
files' paths. `TestRunExamplesIsExampleResults` holds the two to each other for
a workspace with a CRD, a policy with its binding and their examples. With no
paths it runs the current folder.

**The workspace.** A folder is walked as the language server's workspace scan
walks its workspace, with its limits: `.yaml` and `.yml` files only, hidden
folders and `node_modules` left out, files over 1 MiB not read, and the files
after the first 20000 not read. The editor's cap of 100 examples of a CRD, and
of 100 examples a policy matches, is for its latency and does not apply: the
command runs every example it finds. A file given alone, which must end in
`.yaml` or `.yml`, is the whole workspace of its folder: the CRDs, policies,
bindings, params and examples are those of the files given, and of the folders
given, not of the other files beside it. A file below a folder given is read
with the folder, so a hidden folder is read only when a file in it is named
while no folder above it is. What the limits leave out is a warning on stderr
(`cel-lens-core examples: warning: ...`), counted in `filesLeftOut` of the
JSON and said by the summary line (`... 0 wrong · 1 file left out at the
limits`); it does not change the exit status. A folder or file that cannot be
read is named on stderr and exits 2, and the rest is run.

**Text.** Quiet: it lists the problems of the examples, then each rule with a
wrong verdict or an error, wrong ones first, with the examples it is wrong for,
and ends with a summary:

```
config/samples/invalid/small.yaml:2:1: error: expected to be refused, but nothing refuses it
config/crd/widget.yaml:22: self.size <= 10  (CustomResourceDefinition widgets.example.com v1 spec)
  wrong config/samples/huge.yaml:1: refused, expected to be allowed: spec: Invalid value: at most 10
3 rules, 5 examples: 2 wrong, 3 not run
```

A rule is `file:line: text` (shortened to 60 characters) with its document and
where in it; a result is its mark (`wrong`, `error`, `skip` for a rule that did
not run, `ok`), its example's file and line, what the rule did (`refused`,
`allowed`, `error`, `warned`, ... `as an update`, `under binding "name"`), what
the example expected when that is not it, and the API server's message on one
line. Paths are relative to the working folder when they are below it.
`--verbose` lists every result, `skip`s and `ok`s included, and the rules with no
example. The summary counts the rules, the distinct examples any of them has a
result for, and each kind of result of a rule on an example once:

- `wrong`: a verdict that is not what the example expects, the rule being to
  blame (it refuses an example that expects to be allowed, or stops with an
  error on it, which the API server answers as a refusal), plus each problem of
  an example. An example that expects to be refused, with a part of the message
  or not, and that nothing refuses so, is one problem of the example however
  many rules allow it, as the editor warns on it once; the rules that allow it
  are listed with `--verbose`.
- `error`: a rule that stopped with an error, a runtime error or a cost limit,
  on an example that expects to be refused: the example is as expected, so it
  is not wrong, but it is not a verdict either.
- `not run`: a rule the API server does not run on the example, with the reason.

Zero errors and zero rules that did not run are left out of the line.

**Problems.** What the editor shows on the examples themselves is reported in
the problems, one line each, `file:line:col: error: message`, before the rules,
and counts as wrong: the schema's problems on an example that expects to be
allowed (the API server's words, at the field), a `# cel-lens:` comment that is
none of the three forms, an example that expects to be refused that nothing
refuses, and a CustomResourceDefinition, ValidatingAdmissionPolicy or binding of
the workspace that the API server cannot read, which the command would
otherwise leave out without a word: a field of the wrong type, such as a plain
`n` where a string is wanted (kubectl reads it as a boolean), is reported at the
word as `check` reports it, and a CRD the API server refuses for anything but its
rules says what it refuses, at the first line of the document. A document with
no name, a binding that names no policy, a CRD that serves no version and a file
whose YAML does not parse are left out without a problem. The language server
says the same on such a file of the workspace that is not open.

**JSON.** `--format json` prints one object: `kubernetesVersion` and `rules`,
each as `cel-lens.exampleResults` gives it (an example's `file` is its path below
the workspace folder it is in, `uri` its file URI), the rules of each file after
those of the file before it, and `problems`, a list of `{uri, file, line,
column, severity, message}`, empty when there are none. `--verbose` does not
change it.

**What fails.** `--fail-on wrong` (the default) fails the run, with exit 1, on a
result that is wrong, an error that refuses an example expected to be allowed
included, or a problem of an example, a CRD, policy or binding the API server cannot read among them; `error` also on a rule that ended in an
error on an example that expects to be refused; `none` never. A rule that did not run never fails
a run. `--kube-version` (also spelled `--kubernetes-version`) is what `check`
takes, with the same default and range. Exit code 0 means nothing failed, 1 that
a verdict did, 2 a usage or read error (a missing path, a file that is not YAML,
an unknown flag or value, a folder that cannot be read), 130 that the run was
interrupted. Flags may come before, between or after the paths.
