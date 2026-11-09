# The .cellens file

Where Kubernetes gives a CEL value no type, it is `dyn`: a field with
`x-kubernetes-preserve-unknown-fields` or no `type`, `params` of a custom kind
with `crd-typed-policies` off, a Kyverno variable. cel-lens can't check,
complete or explain a `dyn` value. A `.cellens` file says what such a value
looks like, so cel-lens can assume that type there. Commit it, so everyone
working on the repository gets the same types.

The engine has a reader for the format (`internal/cellens`), but nothing reads
`.cellens` files yet: checks, hover and completion that use the types, and
problems shown in the editor, come in a later release.

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
    path: spec.config
    type: "{name: string, replicas: int, labels: map(string, string)}"
  - file: policies/limits.yaml
    document: ValidatingAdmissionPolicy limits
    variable: params
    path: spec
    kind: example.com/v1 Limit
```

| Key | What it says |
| --- | --- |
| `file` | The YAML file the value is in, relative to the `.cellens` file's folder, and not outside it. |
| `document` | The document of that file: its kind and `metadata.name`, separated by a space. The kind is one whose CEL cel-lens reads: CustomResourceDefinition, ValidatingAdmissionPolicy, MutatingAdmissionPolicy or ValidatingPolicy. |
| `version` | For a CustomResourceDefinition: the served version the value is in, such as `v1` or `v1beta1`. |
| `variable` | For a policy: `params`, `object`, `oldObject` or `variables.<name>`. |
| `path` | Optional: the value, from the root of the object (a CRD) or from the variable (a policy). Field names separated by dots, `[]` for a list's items and `[*]` for a map's values, such as `spec.ports[].config`. Left out, it is the root. |
| `type` | The type to assume, written as below. |
| `kind` | Instead of `type`: a kind, by its apiVersion and kind, such as `apps/v1 Deployment` or a CustomResourceDefinition's `example.com/v1 Limit`. |

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
