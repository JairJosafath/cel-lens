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
  server's `triggerCharacters` are `.`, `/`, `#`, `(`, `:` and the space: in a
  YAML file only `.` gives items, and a request for another of them
  (`context.triggerKind` 2) gets `null` at once. The protocol has one list for
  every document, so a client asks after a space in every file; only in a
  `.cellens`, after a key's colon, does the space give items.

- **Quick fixes** (the light bulb): "Change to `maxReplicas`" for a misspelled
  field, function or macro when exactly one name valid there is within two edits
  of it, and "Add `maxLength: 1000` to `spec.name`" (or `maxItems`,
  `maxProperties`) for a cost error, with the largest round number that brings
  the rule under its budget (a cost error on the CRD's total is solved when the
number also brings the total under its budget). No fix is offered when there is no clear one.

- **Types from `.cellens`**: the server finds the `.cellens` files of the
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
- **Commands** (`workspace/executeCommand`): the type choices for a `dyn`
  value, with the workspace's examples of its kind, the check of a type as it
  is typed, a schema worked out from examples, and setting or removing a
  `.cellens` entry through `workspace/applyEdit`, or adding one to fill in.
  See [Commands](#commands).

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
  - for each value the expression reads that Kubernetes leaves `dyn` and a
    `.cellens` entry can type, `self.spec.port: dyn · Give it a type`, which
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
  shows new lines when it next asks, such as after an edit. The lenses of a
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

`initialize` declares five commands under `executeCommandProvider`, which a
client runs with `workspace/executeCommand`. The VS Code extension runs them
from the lines above a rule: their parts run its own commands
`cel-lens.giveType`, `cel-lens.editType` and `cel-lens.removeType`, which ask
the person for a type and pass these commands the same argument, with the
type or kind chosen. Each but `cel-lens.inferType` takes one argument, an
object with these keys:

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
  built in or of a CustomResourceDefinition it has read, by its kind (a
  built-in one in any version too, for `apiVersions: ["*"]`), and another by
  the plural Kubernetes guesses from a kind (`widgets` for `Widget`); a
  subresource matches nothing. Each example gives its file's
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
  `path`), and `required: true` to list the fields every example has. It
  returns the schema, its keywords in the order the API server writes them
  and its properties in the order the examples first have them, and the
  notes on what the examples do not settle, `[]` when none:

  ```json
  {"schema": {"type": "object", "properties": {"items": {"type": "array",
              "items": {"x-kubernetes-preserve-unknown-fields": true}}}},
   "notes": ["items: an empty list says nothing about its items"]}
  ```

  for `{"text": "items: []"}`. It runs in the background, as `typeChoices`
  does (see below). A text it cannot read, too big, nested deeper
  than 100 objects and lists, or with no example (at the `line` or `path`
  given), is refused with `InvalidParams` (-32602) and what is wrong, with the
  line for YAML that does not parse.

Commands in the background: `typeChoices` and `inferType` do their work off
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
