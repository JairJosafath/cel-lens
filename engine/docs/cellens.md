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
| `type` | The type to assume, written as below. |
| `kind` | Instead of `type`: a kind, by its apiVersion and kind, such as `apps/v1 Deployment` or a CustomResourceDefinition's `example.com/v1 Limit`. A built-in kind always works; a CustomResourceDefinition's kind is looked up among the CRDs of the workspace, which is the experimental feature `crd-typed-policies`. |

An entry has exactly one of `type` and `kind`, and one entry per value.

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

## Problems

The reader reports every problem in a `.cellens` with its line and column, and
says what to do: an unknown key or one written twice, a missing `file` or
`document`, a file outside the folder, both or neither of `type` and `kind`,
`version` on a policy or `variable` on a CustomResourceDefinition, a type that
doesn't parse, two entries for the same value. A type written over several
lines, or with escapes, gets its problem on the value, with the character
counted in the message. An entry with a problem is left out; the others still
count.

## How the types are used

The language server finds the `.cellens` files in the workspace folders
(skipping hidden folders and `node_modules`, like the CRD scan) in the
background when it starts, whatever the experimental features, and follows
them: opening, editing, saving, creating or deleting a `.cellens`, or a folder
holding one, checks the open documents again, without a restart. For changes
on disk it asks the editor to watch `**/.cellens`. An open `.cellens` counts
with the editor's text.

An entry applies only to the file, document and value it names, and only
where the value is `dyn` without it:

- In a CustomResourceDefinition, an `x-kubernetes-int-or-string` value, the
  one value a rule sees as `dyn`. The API server validates such a value as an
  integer or a string, so give it `int` (or `uint`) or `string`: another type,
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
the entry writes it, with `Typed from .cellens, line 5`. The line above each
rule (inline info, see [engine.md](engine.md)) shows each value the rule reads
that an entry types, `self.spec.port: int · Edit | Remove`, each `dyn` value an
entry could type, `self.spec.port: dyn · Give it a type`, and a value whose
entry does not apply, `params: dyn · Edit | Remove`; their commands carry the
entry's `file`, `document`, `version` or `variable` and `path`, and the
`.cellens` the entry is in or would go in. Hover on a policy
variable whose expression reads an assumed value still shows the type it has
without the `.cellens`.

The problems of a `.cellens` are diagnostics on that file, at the entry's
line: those of the format above as errors, and as warnings an entry whose
file does not exist or is no regular file, whose file has no such document,
version, variable or value, whose type an `x-kubernetes-int-or-string` value
cannot have, whose value Kubernetes already types or no rule
may read, whose kind is not found, or whose value another `.cellens` already
types: the one first by its path gives the type. A symbolic link to a
`.cellens` is not followed, and a file over 1 MiB is not read.

## Writing entries from the editor

The lines above a rule offer `Give it a type`, `Edit` and `Remove` for a `dyn`
value; the editor asks what type is meant and the language server writes the
entry, with the commands of [engine.md](engine.md#commands). It never writes a
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
that the value cannot have, a value Kubernetes already types, a file or
document that does not exist, a kind that is not found.

The edit changes only the entry's own lines, so every other line, comments
included, stays as a person wrote it:

- A changed type replaces the `type:` or `kind:` of the entry, keeping a
  comment after it on its line, and the comments around and inside the entry.
- A new entry goes before the first entry that sorts after it, by `file`, then
  `document`, then `version` or `variable`, then `path`, and after the last one
  otherwise, above the comment lines directly above the entry it goes before,
  and before a comment after a blank line at the end of the list.
  Its keys come in the order of the table above, indented like the other
  entries.
- Types are written in the form above (`{name: string, replicas: int}`), quoted
  when they have a struct or a comma in them, or anything YAML would read
  otherwise, such as `true`.
- Removing a type removes the entry with its comments: the comment lines
  directly above its `-`, those inside it, and those right after it, indented
  more than its `-`. A comment after a blank line is not the entry's above
  it, and stays. When nothing would be left but
  `types:` and the comment cel-lens starts a `.cellens` with, the editor
  deletes the file when it can, and else keeps those two lines.

A `.cellens` that is not YAML, or whose `types` is not a list of entries on
their own lines (`- file: ...`, with the other keys below), is not edited: the
command says what to fix.
