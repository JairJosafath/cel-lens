# The .cellens file

Where Kubernetes gives a CEL value no type, it is `dyn`: an
`x-kubernetes-int-or-string` field to a CustomResourceDefinition's rule, a field
a policy reads that keeps unknown fields with
`x-kubernetes-preserve-unknown-fields`, `params` of a custom kind with
`crd-typed-policies` off, a Kyverno variable. cel-lens can't check,
complete or explain a `dyn` value. A `.cellens` file says what such a value
looks like, so cel-lens can assume that type there. Commit it, so everyone
working on the repository gets the same types.

The language server reads every `.cellens` of the workspace and uses its
types: an expression that reads a value an entry names is checked, completed
and explained with that type (see "How the types are used" below).
`cel-lens-core check` does not read `.cellens` yet.

## The format

A `.cellens` is YAML, in any folder of the workspace. Its one key, `types`,
lists entries. Each entry names a value in a YAML file at or below the
`.cellens` file's folder, and the type to assume there:

```yaml
# .cellens: types cel-lens assumes where Kubernetes gives dyn. Commit it.
types:
  - file: config/crd/bases/widgets.yaml
    document: CustomResourceDefinition widgets.example.com
    version: v1
    path: spec.maxSurge
    type: int
  - file: policies/limits.yaml
    document: ValidatingAdmissionPolicy limits
    variable: params
    type: "{name: string, replicas: int, labels: map(string, string)}"
  - file: policies/limits.yaml
    document: ValidatingAdmissionPolicy limits
    variable: object
    kind: example.com/v1 Widget
```

| Key | What it says |
| --- | --- |
| `file` | The YAML file the value is in, relative to the `.cellens` file's folder, and not outside it. |
| `document` | The document of that file: its kind and `metadata.name`, separated by a space. The kind is one whose CEL cel-lens reads: CustomResourceDefinition, ValidatingAdmissionPolicy, MutatingAdmissionPolicy or ValidatingPolicy. |
| `version` | For a CustomResourceDefinition: the served version the value is in, such as `v1` or `v1beta1`. |
| `variable` | For a policy: `params`, `object`, `oldObject` or `variables.<name>`. |
| `path` | Optional: the value, from the root of the object (a CRD) or from the variable (a policy). Field names separated by dots, `[]` for a list's items and `[*]` for a map's values, such as `spec.ports[].config`. Left out, it is the root. |
| `type` | The type to assume, in CEL's names, written as in [Types](#types). |
| `kind` | Instead of `type`: a kind, by its apiVersion and kind, such as `apps/v1 Deployment` or a CustomResourceDefinition's `example.com/v1 Limit`. A built-in kind always works; a CustomResourceDefinition's kind is looked up among the CRDs of the workspace, which is the experimental feature `crd-typed-policies`. |
| `schema` | Instead of `type`: an OpenAPI v3 schema, written as a CustomResourceDefinition's `openAPIV3Schema` ([Schemas](#schemas)). |
| `go` | Instead of `type`: a Go type, as `<folder of its package, relative to this .cellens>#<TypeName>` ([Go types](#go-types)). |

An entry has exactly one of `type`, `kind`, `schema` and `go`, and one entry
per value. The same entries, with a schema and a Go type:

```yaml
types:
  - file: policy.yaml
    document: ValidatingAdmissionPolicy replica-limit
    variable: params
    schema:                      # as in a CRD's openAPIV3Schema
      type: object
      properties:
        maxReplicas: {type: integer, minimum: 0}
        team: {type: string}
  - file: other.yaml
    document: ValidatingAdmissionPolicy quota
    variable: params
    go: ./api/v1#QuotaSpec       # a Go type: folder of its package, relative to this .cellens, and its name
```

### Which to choose

- `kind` when the value is an object of a kind that exists: a built-in kind,
  or a custom resource whose CustomResourceDefinition is in the workspace. The
  type follows the kind.
- `go` when the value's type is a Go struct of the repository, as in a
  kubebuilder or controller-gen project: the type follows the Go code, with
  its doc comments as descriptions in hover.
- `schema` when there is no such Go type, or the value is shaped by YAML only,
  and a schema like a CRD's says it best: with descriptions, formats and the
  `x-kubernetes-` extensions.
- `type` for a short type that needs nothing more, such as `int` or
  `{name: string, replicas: int}`; it is also what the editor's one-line box
  takes.

## Types

`type` takes CEL's type names and structs:

- `string`, `int`, `uint`, `double`, `bool`, `bytes`, `timestamp`, `duration`
- `list(T)`: a list of `T`, such as `list(int)`
- `map(string, T)`: a map from strings to `T`; Kubernetes objects only have string keys
- `{field: T, ...}`: an object with these fields, to any depth, such as
  `{name: string, ports: list({port: int, protocol: string})}`

A field name starts with a letter, `_` or `$`, and goes on with letters,
digits, `_`, `$` and `-`, as JSON field names in Kubernetes objects do. A type
nests at most 100 levels deep. Kubernetes has no unsigned integer: `uint` is an
integer of at least 0, which CEL in Kubernetes reads as `int`.

Quote every type that has a struct in it, such as `type: "list({port: int})"`:
unquoted, YAML reads `{...}` as a mapping, and a `{` after `(` makes the whole
file unreadable.

## Schemas

`schema` takes a mapping written as the `openAPIV3Schema` of a
CustomResourceDefinition's version: `type`, `properties`, `items`,
`additionalProperties`, `required`, `enum`, `format`, `nullable`,
`description`, `x-kubernetes-int-or-string`,
`x-kubernetes-preserve-unknown-fields`, `x-kubernetes-list-type`,
`x-kubernetes-list-map-keys`, `x-kubernetes-map-type`,
`x-kubernetes-embedded-resource`, the size limits (`maxLength`, `maxItems`,
`maxProperties` and their minimums) and every other keyword such a schema
has. It is the schema of the value, not of a whole resource: the rules the API
server has only for the root of a CRD's schema (an object, not nullable, a
restricted `metadata`) do not apply.

The engine checks it as the API server checks a CRD when it is created
(k8s.io/apiextensions-apiserver's own validation): it must be structural,
every keyword must be one a CRD's schema has, with a value of the right kind,
and what the API server refuses, such as `type: int`, `uniqueItems: true` or
`$ref`, is refused. A value YAML reads as a timestamp, such as
`description: 2020-01-01`, is the string written, as the API server reads it.
A few more things are refused here: `x-kubernetes-validations`, since a schema
here gives a value a type, and its rules belong in the CustomResourceDefinition
or the policy that reads it; and YAML aliases (`*name`) and merge keys (`<<`)
in a schema, which cel-lens never follows, so a schema that contains itself,
or aliases that multiply, cannot hold the editor: write the schema out.

The value then gets the type the API server gives a CRD's schema in CEL, with
the API server's own conversion, so a `schema:` entry and a CRD with that
schema behave the same: an `x-kubernetes-int-or-string` value is `dyn`, a
`date-time` string a `timestamp`, and `x-kubernetes-preserve-unknown-fields` is
ignored: CEL sees only the fields the schema declares. Hover shows the
descriptions.

## Go types

`go` names a Go type as `<folder>#<TypeName>`: the folder of its package,
relative to the `.cellens` file's folder and slash-separated (`./api/v1`,
`../api/v1`, or `.` for the folder itself), then `#` and the type's name. The
folder must be in the workspace folder the `.cellens` is in (or, for a
`.cellens` outside every workspace folder, its own folder).

The engine reads the `.go` files of that folder, but its `_test.go` files, with
Go's parser: nothing is built, no `go` command runs, and no other package is
read, so the code need not compile. Of those files it reads the ones a
`go build` on the machine the editor runs on compiles, as their names
(`_linux.go`, `_arm64.go`) and `//go:build` lines say (`//go:build ignore`
leaves a file out), and of those the ones of the package most of them are of,
so a `package main` tool beside the types is left out. It reads at most 20000
files of at most 1 MiB each, through the workspace folder, so a symbolic link
cannot lead out of it. It turns the type into a schema as controller-gen does
for a CustomResourceDefinition, but for pointers:

- a field's `json` tag gives its name; a field without one is left out, as
  controller-gen refuses it, and so are `json:"-"` and unexported fields;
- `json:",inline"`, or a tag without a name, puts the fields of the field's
  struct in the struct's own, as for an embedded `metav1.TypeMeta`;
- `omitempty` and `omitzero` make a field optional, and so does a pointer,
  where controller-gen needs `omitempty` or `+optional`; every other field is
  required;
- `string`, `bool`, the integers (`int32` and `int64` with their formats), the
  floats, `[]byte` (a `byte` string), slices, arrays, maps with string keys,
  structs and the named types of the package map to their schemas, and a type
  that contains itself stops there. A named type is written out each time a
  field uses it; past 20000 schemas (core/v1 PodTemplateSpec has about
  1400), as when each type uses the next twice, the field where that happens
  and those after it are `dyn`, which CEL does not check, and the entry's
  warning names that field by its Go name and its path, such as
  `go: T25.B, at a.a.b of T0: the schema grows past 20000 schemas here, ...`;
- of other packages, `metav1.ObjectMeta` is an object, `metav1.Time` and
  `metav1.MicroTime` `date-time` strings, `metav1.Duration` a string,
  `intstr.IntOrString` and `resource.Quantity` `x-kubernetes-int-or-string`,
  as controller-gen writes them, and the other types of
  `k8s.io/apimachinery/pkg/apis/meta/v1` and `k8s.io/api/core/v1` the engine
  has schemas for (those its built-in kinds are made of, and
  `metav1.LabelSelector`, `metav1.Condition` and `metav1.ListMeta`) have
  those schemas;
- doc comments are the descriptions, without their marker lines.

Markers such as `+kubebuilder:validation:Maximum` or `+optional` are not read:
the type says what CEL can read, not every rule of the values.

A field the engine cannot turn into a schema, such as one of a type from
another package, a map with non-string keys, an interface or a func, is left
out, and the entry gets a warning that names the field and why, such as
`go: QuotaSpec.Owner: type other.Owner is from package example.com/other,
which cel-lens does not read: the field is left out`; the rest of the struct
is used. A Go type that cannot be read at all, as its folder does not exist or
its package declares no such type, is a warning on the entry, which then types
nothing. The engine reads the files again when one of the folder's `.go` files
changed, was added or went: the documents show it when they are next checked,
such as after an edit.

## Problems

The reader reports every problem in a `.cellens` with its line and column, and
says what to do: an unknown key or one written twice, a missing `file` or
`document`, a file outside the folder, more than one or none of `type`,
`kind`, `schema` and `go`, `version` on a policy or `variable` on a
CustomResourceDefinition, a type that doesn't parse, a `go` that is not
`<folder>#<TypeName>`, two entries for the same value, a text that is not
YAML. A schema the API server
would refuse as a CustomResourceDefinition's schema is a problem at the line
of the offending key, in the API server's words, after the path in the schema:
`schema: properties[max].type: Unsupported value: "int": supported values:
"array", "boolean", "integer", "number", "object", "string"`. A type written over several
lines, or with escapes, gets its problem on the value, with the character
counted in the message. An entry with a problem is left out; the others still
count. A text YAML cannot read whole, as one being typed often is, is read
entry by entry, each from its dash to the next: the entries YAML can read
count, and each one it cannot is a problem at its own line, such as
`not YAML: line 8: could not find expected ':'`; so is a line at the column
of the dashes that starts no entry, and the entries after it still count.

## How the types are used

The language server finds the `.cellens` files in the workspace folders
(skipping hidden folders and `node_modules`, like the CRD scan) in the
background when it starts, whatever the experimental features, and follows
them: opening, editing, saving, creating or deleting a `.cellens`, or a folder
holding one, checks the open documents again, without a restart; an edit in
the editor checks again only those whose types it changes. For changes
on disk it asks the editor to watch `**/.cellens`. An open `.cellens` counts
with the editor's text, as it is typed ([Editing a .cellens](#editing-a-cellens)).

An entry applies only to the file, document and value it names, and only
where the value is `dyn` without it:

- In a CustomResourceDefinition, an `x-kubernetes-int-or-string` value, the
  one value a rule sees as `dyn`. The API server validates such a value as an
  integer or a string, so give it `int` (or `uint`) or `string`, or a schema
  or Go type that is an integer or a string without a format: another type,
  even `timestamp` or `bytes`, is a problem on the entry. A value that keeps
  unknown fields with `x-kubernetes-preserve-unknown-fields` is no `dyn` to a
  rule: the API server lets a rule read only the fields its schema declares,
  and none of one without a `type`, so an entry there is a problem that says
  to give it a schema in the CustomResourceDefinition instead. The path
  follows the version's schema from its root: `properties` for a field,
  `items` for `[]`, `additionalProperties` for `[*]`. The type replaces the
  schema's own; its `description` and `x-kubernetes-validations` stay, so its
  rules still apply.
- In a ValidatingAdmissionPolicy or MutatingAdmissionPolicy, `params` when
  `spec.paramKind` names no built-in kind and no CustomResourceDefinition types
  it, `object` and `oldObject` when the kinds the policy matches are not all
  known, and `variables.<name>` when the variable's type is `dyn` (hover says
  why). With a path, a `dyn` value inside a typed variable, such as a field of
  a custom resource that keeps unknown fields: the API server reads a policy's
  objects whole when it runs it.

Where Kubernetes gives the value a type, that type wins, and the entry is a
problem: `spec.name is already typed as string: remove this entry`. A path
cannot go below a `dyn` value, since nothing below it is known: give the `dyn`
value itself the type.

With the type, cel-lens checks the rules of a CustomResourceDefinition and
the validations of a ValidatingAdmissionPolicy as the API server would if
Kubernetes had the type. A problem that only the type shows is a warning,
never an error, and says where the type comes from, by the `.cellens` file's
path in its workspace folder and the entry's line:

```
undefined field 'nosuch' (the type comes from .cellens:5: the API server would not report this)
```

The API server's own problems stay as they are, with their severity. The API
server does not type check a MutatingAdmissionPolicy, so there its entries
give completion and hover only.

Completion after a typed value offers its fields, and hover shows the type as
the entry writes it, a schema's or a Go type's as a `type:` entry would write
it (`{maxReplicas: int, team: string}`), with where it comes from: `Typed from
.cellens, line 5`, `Typed from the schema at .cellens, line 8`, or `Typed from
the Go type QuotaSpec in api/v1/quota_types.go:12, named in .cellens, line 5`.
The line above each rule (inline info, see [engine.md](engine.md)) shows each
value the rule reads that an entry types, `self.spec.port: int · Edit |
Remove` (a schema or a Go type in the same short form as a `type:`, such as
`params: {maxReplicas, team} · Edit`), each `dyn` value an entry could type,
`self.spec.port: dyn · Give it a type`, and a value whose entry does not
apply, `params: dyn · Edit | Remove`; their commands carry the entry's `file`,
`document`, `version` or `variable` and `path`, its `type`, `kind`, `schema`
or `go`, and the `.cellens` the entry is in or would go in. Hover on a policy
variable whose expression reads an assumed value still shows the type it has
without the `.cellens`.

The problems of a `.cellens` are diagnostics on that file, at the entry's
line: those of the format above as errors, a schema the API server would
refuse included, and as warnings an entry whose
file does not exist or is no regular file, whose file has no such document,
version, variable or value, whose type an `x-kubernetes-int-or-string` value
cannot have, whose value Kubernetes already types or no rule
may read, whose kind or Go type is not found, whose Go type leaves a field
out, or whose value another `.cellens` already types: the one first by its
path gives the type. A symbolic link to a
`.cellens` is not followed, and a file over 1 MiB is not read.

## Editing a .cellens

An open `.cellens` is edited like code: the language server reads it as it
is typed, the editor's text at each change, and reads the line at the cursor
by its indentation and brackets, so a `.cellens` YAML cannot read yet still
gets completion and hover where its line can be read.

Completion offers what can be written where the cursor is, each item with a
short detail:

- in an entry, the keys it does not have yet: `file`, `document`, then
  `version` for a CustomResourceDefinition or `variable` for a policy,
  `path`, and while it has no type `type`, `kind`, `schema` and `go`, or for
  a CustomResourceDefinition `type` and `schema`: its `dyn` values are
  `x-kubernetes-int-or-string`, which take an int or a string, as no kind or
  Go type is. In such an entry `type:` offers `int` and `string`, and a
  `schema:` `integer` and `string`, without what makes them something else,
  such as a `format` of a string;
- after `file:`, the YAML files at or below the `.cellens` file's folder,
  skipping hidden folders and `node_modules`;
- after `document:`, the documents of that file an entry can name, those
  with `dyn` values first, each with how many it has (`1 dyn value`);
- after `version:` or `variable:`, those of that document, `dyn` ones and
  those that hold `dyn` values first, with their type (`params`: `dyn`,
  `object`: `apps/v1 Deployment`);
- after `path:`, the paths of the `dyn` values of that version or variable
  that no other entry types, with the detail `dyn`, then the values one step
  below the path written so far that have `dyn` values below them, the way
  to those (`spec`: `object · 1 dyn value`); never a typed value, which no
  entry can type;
- in a `type:`, where the name of a type goes, the CEL type names,
  `list(T)` and `map(string, T)`, and in quotes `{field: T}`; nothing where
  the name of a struct's field goes, and only `string` for a map's key;
- after `kind:`, the kinds of the workspace's CustomResourceDefinitions (with
  `crd-typed-policies` on), then the built-in kinds, alpha and beta ones last,
  with the first sentence of their description;
- after `go:`, the folders of the workspace's Go packages that have struct
  types, relative to the `.cellens`, with their types' names, from a listing
  that runs in the background and follows the `.go` files as they change;
  after the `#`, the struct types of that package, with the first sentence of
  their doc comment;
- in a `schema:`, the keywords that fit where the cursor is, with what each
  does: `type` first while there is none, then those of its type, such as
  `properties` and `required` for an `object`, `items` for an `array` or
  `format` and `maxLength` for a `string`, without those written already;
  after `type:`, `format:`, `x-kubernetes-list-type:`, `x-kubernetes-map-type:`
  or a boolean keyword, its values. Nothing where the name of a property goes.

An item replaces the word at the cursor, the whole value or the name of a
type in it, in block and flow mappings and inside quotes, quoted or escaped
as YAML needs.

Hover on the `file`, `document`, `version`, `variable` or `path` of an entry
names the value the entry points at as the line above a rule does, with the
type Kubernetes gives it there and why: `self.spec.config: dyn`, as it is
`x-kubernetes-int-or-string`, with the type the entry gives it, or
`self.spec.name: string`, so the entry does not apply. Hover on `kind`, `go`
or `schema` shows the type it gives in the short form of that line,
`params: {maxReplicas, team}`, with the kind's description, the Go type's
file and line, or the whole type; or, for an entry that does not apply,
such as a kind for an `x-kubernetes-int-or-string` value, why.

The problems of an entry show as it is typed, and so do the lines above the
rules of the file it names: each change checks again the open documents whose
types it changes, and asks the editor for their lines again; a new line
above an entry moves it, and so changes the line its warnings name. A change
that leaves every entry as it was, on its line, such as in a comment or in
an entry YAML cannot read yet, leaves the documents and their lines as they
are. `go test -bench
CellensKeystroke ./internal/lsp/` measures a keystroke in a `.cellens` of 200
entries.

## Writing entries from the editor

The lines above a rule offer `Give it a type`, `Edit` and `Remove` for a `dyn`
value; the editor asks what type is meant and the language server writes the
entry, with the commands of [engine.md](engine.md#commands). The editor can
also open the `.cellens` at a new entry for the person to write the type in,
with completion: the server writes the entry with a type to fill in and says
where the cursor goes. It never writes a
file itself: it sends the editor an edit (`workspace/applyEdit`), which lands
in the editor's buffer and can be undone, on the `.cellens` as it is then, the
editor's text when it is open. An editor that refuses the edit gets an error
saying so.

Setting a type for a value changes the entry that gives the value its type,
in whichever `.cellens` it is (the first by path, when several are). With no
entry, the new one goes in the nearest `.cellens` at or above the YAML file:
in the file's folder, else the nearest folder above it, up to the workspace
folder. With no `.cellens` there either, the editor creates `.cellens` at the
root of the workspace folder (the file's own folder, outside every workspace
folder), starting with the comment
`# .cellens: types cel-lens assumes where Kubernetes gives dyn. Commit it.`.
A `.cellens` below a hidden folder or `node_modules` counts only while it is
open, as the search for them skips those folders. The entry is refused,
before any edit, when it would get a problem: a type that does not parse, or
that the value cannot have, a schema the API server would refuse, a value
Kubernetes already types, a file or document that does not exist, a kind or a
Go type that is not found. A Go type that leaves a field out is not refused:
its entry gets the warning. A Go type's folder is written relative to the
`.cellens` the entry goes in, which may not be the one the command named.

The edit changes only the entry's own lines, so every other line, comments
included, stays as a person wrote it:

- A changed type replaces the `type:`, `kind:`, `schema:` or `go:` of the
  entry, keeping a comment after it on its line, and the comments around and
  inside the entry but those inside a schema it replaces.
- A new entry goes before the first entry that sorts after it, by `file`, then
  `document`, then `version` or `variable`, then `path`, and after the last one
  otherwise, above the comment lines directly above the entry it goes before,
  and before a comment after a blank line at the end of the list.
  Its keys come in the order of the table above, indented like the other
  entries.
- Types are written in the form above (`{name: string, replicas: int}`), quoted
  when they have a struct or a comma in them, or anything YAML would read
  otherwise, such as `true`. A Go type is written plain, as
  `go: ./api/v1#QuotaSpec`. A schema is written as a block of YAML below
  `schema:`, indented two more than the key, with its keys in the order the
  command gives them, each value plain unless YAML would read it otherwise
  (`"true"`), and a value of several lines as a `|-` block.
- Removing a type removes the entry with its comments: the comment lines
  directly above its `-`, those inside it, and those right after it, indented
  more than its `-`. A comment after a blank line is not the entry's above
  it, and stays. When nothing would be left but
  `types:` and the comment cel-lens starts a `.cellens` with, the editor
  deletes the file when it can, and else keeps those two lines.

A `.cellens` that is not YAML, or whose `types` is not a list of entries on
their own lines (`- file: ...`, with the other keys below), is not edited: the
command says what to fix.

## A schema from examples

Writing a type by hand for something that already exists as an example is
slow: a kubebuilder project keeps samples in `config/samples/`, and the
params of a policy are a resource someone writes as YAML. The language server
works out an OpenAPI v3 schema from one or more examples
(`cel-lens.inferType`, see [engine.md](engine.md#commands)), in the form of a
CustomResourceDefinition's `openAPIV3Schema`, for an entry's `schema:`, where
the person refines it. For a policy's `params`, `object` or `oldObject`, the
editor can offer the documents of the workspace of the value's kind as
examples (`examples` of `cel-lens.typeChoices`).

The schema is conservative: Kubernetes would accept every example given,
and the API server would accept the schema in a CustomResourceDefinition.

- An object gets `properties`, a list `items` merged over all its elements,
  `true` and `false` `boolean`, a whole number `integer`, any other number
  `number`, and a string `string` (a date too, which Kubernetes reads as a
  string). A whole number past what a JSON number holds exactly, such as
  `18446744073709551615`, is a `number`, as Kubernetes decodes it. With
  `required: true` in the arguments, an object lists in `required` the
  fields every example of it has; by default it lists none.
- An unquoted `yes`, `no`, `on`, `off`, `y` or `n` is a string to cel-lens,
  as YAML 1.2 reads it, but kubectl reads YAML 1.1, where it is a boolean: a
  note names the field, so the person quotes it, or types it `boolean`. Such
  a key, such as `on:`, gets a note too: kubectl sends it as the key `true`
  or `false`.
- Where the examples disagree, the schema takes what Kubernetes accepts for
  both: `integer` and `number` make `number`, `integer` and `string` make
  `x-kubernetes-int-or-string`, and `null` makes the value `nullable`. Any
  other mix, such as `boolean` and `string`, leaves the value without a type
  (`x-kubernetes-preserve-unknown-fields`), with a note naming the value and
  the kinds seen; the rest is still worked out.
- An empty list or object, and a value only ever `null`, say nothing about
  what they hold: the list's `items`, the object and the value keep unknown
  fields, and a note says so, such as `spec.items: an empty list says nothing
  about its items`, so the person knows what to fill in.
- At the root of an example, `apiVersion` and `kind` are typed `string` and
  `metadata` `{type: object}`, whatever the example holds there, as a
  CustomResourceDefinition writes them: Kubernetes types `metadata` as
  ObjectMeta, and the API server refuses a schema that says more of it than
  `name` and `generateName`. The rest of the object is worked out from the
  example, `status` too, as the value is the whole object. For a value inside
  the object, `path` takes only the examples' values there, so `status`, like
  everything else outside the path, is left out.
- A text of several YAML documents, a list of objects, or a `v1 List` gives
  each document or item as an example; `line` takes only the document at that
  line, as `typeChoices` gives it: from the `---` at or above it to the next,
  so the other documents need not parse.
- The text is at most 1 MiB, an example nests at most 100 objects and lists
  deep, and the examples hold, once their aliases are expanded, at most ten
  times the values the text writes (10,000 in any case, 1,000,000 at most); a
  text that is bigger, deeper, not YAML or JSON, or with a key written twice
  in one object, is refused with what is wrong and its line.
  The schema's keys come sorted, the same on every run.

Two samples of `config/samples/limits.yaml`:

```yaml
apiVersion: rules.example.com/v1
kind: ReplicaLimit
metadata:
  name: small
  labels: {team: a}
spec:
  maxReplicas: 3
  ratio: 0.5
  port: 8080
  owner: null
  teams: []
---
apiVersion: rules.example.com/v1
kind: ReplicaLimit
metadata:
  name: large
spec:
  maxReplicas: 10
  ratio: 1
  port: http
  owner: platform
  teams: []
```

give this schema, with the note `spec.teams: an empty list says nothing
about its items`:

```yaml
type: object
properties:
  apiVersion: {type: string}
  kind: {type: string}
  metadata: {type: object}   # ObjectMeta to Kubernetes, not the samples' fields
  spec:
    type: object
    properties:
      maxReplicas: {type: integer}
      owner: {type: string, nullable: true}
      port: {x-kubernetes-int-or-string: true}
      ratio: {type: number}
      teams:
        type: array
        items: {x-kubernetes-preserve-unknown-fields: true}
```

With `path: spec`, the schema is that of `spec` alone, and with `required:
true` it lists `maxReplicas`, `owner`, `port`, `ratio` and `teams`, which both
samples have.
