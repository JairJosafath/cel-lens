# Where a YAML document tells a CEL expression its types

Before the engine stops falling back to `dyn`, this is the list of every place
a Kubernetes or Kyverno YAML document says what a CEL variable is. Each table
below is one kind of document that holds CEL; each row is one variable an
expression of that kind can use.

## The rule: one document decides

Only the YAML document an expression sits in decides its types. A file split by
`---` holds several independent documents, and a document never takes a type from
another one. The API server resolves a `paramKind` or a custom resource against
CustomResourceDefinitions in the cluster, so the engine looks them up in the CRDs
of the workspace (`check.CRDs`).

**Gap against the rule, today:** the engine also adds the CRDs of the policy's own
file to that lookup (`internal/scan/scan.go`, `internal/lsp/lsp.go`). So a CRD in
another `---` document of the same file does decide the type of `params` (and of
`object`/`oldObject` of a custom resource). Reproduced: a file with a CRD for
`example.com/v1 Limit`, then `---`, then a ValidatingAdmissionPolicy with that
`paramKind` and `params.nosuch == 1` is warned about; the policy alone in its own
file is not. By the rule this must stop: such a CRD may only come from other files
of the workspace, never from a neighbouring document of the same file.

Typing `params` from a CRD at all is the experimental feature `crd-typed-policies`
(README, "Stable and experimental features"): off, `params` of a custom resource
is `dyn` and hover says the feature is experimental and off. The gap above stays
while the feature is experimental. Only `params` is typed from a CRD: `object`
and `oldObject` of a custom resource are `dyn` with or without one. The
workspace scan still runs inside `initialize` when the feature is off; only the
use of the CRDs is switched off. Hover saying why a value is `dyn` is the
experimental feature `fitting-completion`.

When the document does not say, the variable is `dyn`, and hover says why.

## How to read the tables

- **Variable**: the name an expression uses.
- **Type comes from**: the field of the document that fixes the type, or "the
  kind itself" when the Kubernetes or Kyverno API fixes it for every document
  of that kind.
- **Source**: where Kubernetes or Kyverno says so.
- **Engine today**: what `cel-lens-core` gives the variable now. "Typed" means
  a schema type; `dyn` means the engine does not know the type.
- **Could the document tell?** Only for a `dyn` row: whether the YAML document,
  with the Kubernetes or Kyverno knowledge the engine ships, has enough to type it.

Today the engine reads CEL only in these kinds: `CustomResourceDefinition`,
`ValidatingAdmissionPolicy`, `MutatingAdmissionPolicy` and Kyverno
`ValidatingPolicy` (`pkg/extract`). For every other kind in this list the engine
finds no CEL, so no variable is given at all ("not read today"); the last column
says whether the document could type it once it is read.

Sources, by short name:

- **K8s-CEL**: [Common Expression Language in Kubernetes](https://kubernetes.io/docs/reference/using-api/cel/)
- **VAP**: [Validating Admission Policy](https://kubernetes.io/docs/reference/access-authn-authz/validating-admission-policy/)
- **MAP**: [Mutating Admission Policy](https://kubernetes.io/docs/reference/access-authn-authz/mutating-admission-policy/)
- **Webhooks**: [Dynamic Admission Control, matchConditions](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#matching-requests-matchconditions)
- **admissionregistration/v1 types**: [`k8s.io/api/admissionregistration/v1/types.go`](https://github.com/kubernetes/api/blob/master/admissionregistration/v1/types.go)
- **CRD validation rules**: [Validation rules](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules)
- **Authn**: [Structured authentication configuration](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#using-authentication-configuration)
- **Authz**: [Structured authorization configuration, matchConditions](https://kubernetes.io/docs/reference/access-authn-authz/authorization/#using-configuration-file-for-authorization)
- **DRA**: [`k8s.io/api/resource/v1/types.go`](https://github.com/kubernetes/api/blob/master/resource/v1/types.go) (`CELDeviceSelector`) and [Dynamic Resource Allocation](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
- **Kyverno policies**: [Kyverno policy types](https://kyverno.io/docs/policy-types/) and [`kyverno/api` policies.kyverno.io/v1](https://github.com/kyverno/api/tree/main/api/policies.kyverno.io/v1)
- **Kyverno CEL libraries**: [Kyverno CEL libraries](https://kyverno.io/docs/policy-types/cel-libraries/)

## Kubernetes

### CustomResourceDefinition

CEL in `x-kubernetes-validations[].rule` and `.messageExpression`, in any
schema of `spec.versions[].schema.openAPIV3Schema` (or the v1beta1
`spec.validation.openAPIV3Schema`), at any depth through `properties`, `items`
and `additionalProperties`.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `self` | The schema node the rule is attached to: its `type`, `properties`, `items`, `additionalProperties`, `format`, `x-kubernetes-int-or-string`, `x-kubernetes-preserve-unknown-fields` | [CRD validation rules](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules), K8s-CEL | Typed from the node (`crdScope`); a node with no `type` or with preserve-unknown-fields is `dyn`, as in the API server | n/a: the API server also gives `dyn` there |
| `oldSelf` | The same schema node; present only in transition rules (rules that use `oldSelf`) | CRD validation rules ("Transition rules") | Typed like `self` | n/a |

`spec.versions[]` each carry their own schema; a rule sees the schema of its
own version only. `$ref` is not allowed in a CRD schema, so the node is always
inside the document.

### ValidatingAdmissionPolicy

CEL in `spec.validations[].expression` and `.messageExpression`,
`spec.matchConditions[].expression`, `spec.variables[].expression` and
`spec.auditAnnotations[].valueExpression`. `variables` is not in scope in
`matchConditions`; a variable sees only the variables listed before it.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | `spec.matchConstraints.resourceRules[]` (`apiGroups`, `apiVersions`, `resources`), resolved to the built-in kinds | VAP ("Type checking"), admissionregistration/v1 types | Typed when every matched kind is a built-in kind the engine knows (`matchkind`); else `dyn` (wildcards, unknown groups, custom resources, subresources) | For a custom resource, only through a CRD in the workspace; the policy's document does not hold it. Today a CRD in another `---` document of the same file also types it (a gap against the rule) |
| `params` | `spec.paramKind` (`apiVersion`, `kind`), only when the field is set | VAP ("Parameter resources"), admissionregistration/v1 types | Typed from the built-in kind or from a CRD; else `dyn`, and hover says the kind was not found. Not declared when there is no `paramKind`. Today a CRD in another `---` document of the same file types it (a gap against the rule) | Yes for a built-in kind; for a custom kind only through a CRD of another file of the workspace |
| `variables` | `spec.variables[]`: each `name` is a property, typed by the result type of its `expression` | VAP ("Variable composition") | Typed per variable from the compiled expression (`check.VariableTypes`); falls back to `dyn` properties when the policy does not compile | Yes, from the same document |
| `request` | The kind itself: `AdmissionRequest` | admissionregistration/v1 types, VAP | Typed `kubernetes.AdmissionRequest` | n/a |
| `namespaceObject` | The kind itself: a `Namespace` with part of its metadata | admissionregistration/v1 types, VAP | Typed `kubernetes.Namespace` | n/a |
| `authorizer` | The kind itself: the CEL authorizer library, with `authorizer.requestResource` | admissionregistration/v1 types, [Authz library](https://pkg.go.dev/k8s.io/apiserver/pkg/cel/library#Authz) | Typed as the authorizer library type | n/a |

The `ValidatingAdmissionPolicyBinding` holds no CEL. Its `paramRef` and
`policyName` point at objects that may sit in other documents; they never decide a
type of the policy, whose `paramKind` does.

### MutatingAdmissionPolicy

CEL in `spec.matchConditions[].expression`, `spec.variables[].expression`,
`spec.mutations[].applyConfiguration.expression` and
`spec.mutations[].jsonPatch.expression`. The apply configuration and JSON patch
expressions also have `Object`, `Type` and `JSONPatch` helpers
(`Object{...}`, `JSONPatch{...}`) from the mutation environment.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | `spec.matchConstraints.resourceRules[]` | MAP, admissionregistration/v1 types | Same as the ValidatingAdmissionPolicy | Same as the ValidatingAdmissionPolicy |
| `params` | `spec.paramKind` | MAP | Same as the ValidatingAdmissionPolicy | Same |
| `variables` | `spec.variables[]` | MAP | Same | Same |
| `request`, `namespaceObject`, `authorizer` | The kind itself | MAP | Same | n/a |

### ValidatingWebhookConfiguration and MutatingWebhookConfiguration

CEL in `webhooks[].matchConditions[].expression`.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | The webhook's `rules[]` (`apiGroups`, `apiVersions`, `resources`) | Webhooks, admissionregistration/v1 types | Not read today | Yes: `rules[]` has the same shape as `matchConstraints.resourceRules`, so `matchkind` could type it, for the one webhook the condition sits in |
| `request` | The kind itself: `AdmissionRequest` | Webhooks | Not read today | n/a |
| `authorizer` | The kind itself | Webhooks | Not read today | n/a |

There is no `params`, `variables` or `namespaceObject` in a webhook's match condition.

### AuthenticationConfiguration (apiserver.config.k8s.io)

CEL in `jwt[].claimValidationRules[].expression`, `jwt[].claimMappings.*.expression`
(`username`, `groups`, `uid`, `extra[]`) and `jwt[].userValidationRules[].expression`.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `claims` | The kind itself: a map of the JWT claims (`map(string, dyn)`) in claim rules and mappings | Authn | Not read today | No: the claims of a token are not in the document, so `dyn` values are correct |
| `user` | The kind itself: the `UserInfo` (`username`, `uid`, `groups`, `extra`) in `userValidationRules` and in `extra`/`uid` mappings that use it | Authn | Not read today | n/a |

### AuthorizationConfiguration (apiserver.config.k8s.io)

CEL in `authorizers[].webhook.matchConditions[].expression`.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `request` | The kind itself: `SubjectAccessReviewSpec` (`resourceAttributes`, `nonResourceAttributes`, `user`, `groups`, `extra`, `uid`) | Authz | Not read today | n/a |

### DeviceClass, ResourceClaim and ResourceClaimTemplate (resource.k8s.io)

CEL in `spec.selectors[].cel.expression` of a DeviceClass; in
`spec.devices.requests[].exactly.selectors[].cel.expression` and
`...firstAvailable[].selectors[].cel.expression` of a ResourceClaim, and in the
same fields under `spec.spec` of a ResourceClaimTemplate. Newer versions also
have `deviceAttributes`/derived-attribute expressions with the same `device`
variable.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `device` | The kind itself: `driver` (string), `attributes` (map of maps by domain), `capacity` (map of maps), `allowMultipleAllocations` (bool) | DRA, `CELDeviceSelector` | Not read today | The shape yes (fixed by the kind). The attribute values are `dyn` by design: they are set by drivers in ResourceSlices, not in this document |

## Kyverno

All Kyverno CEL policies are `policies.kyverno.io/v1`. The Kyverno CEL libraries
(`resource`, `http`, `image`, `globalContext`, `json`, `yaml`, `user`, and the
rest) are functions and variables fixed by the engine, not by the document.

### ValidatingPolicy

CEL in `spec.validations[].expression` and `.messageExpression`,
`spec.matchConditions[].expression`, `spec.variables[].expression`,
`spec.auditAnnotations[].valueExpression`, and
`spec.webhookConfiguration.matchConditions[].expression`. The engine does not
read the last one today (see the second table).

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | `spec.matchConstraints.resourceRules[]` (and `spec.matchConstraints.namespaceSelector`/`objectSelector`, which narrow but do not change the type) | Kyverno policies | Typed when every matched kind is a known built-in kind; else `dyn`. Policies with `matchConstraints` absent are `dyn` | Same as the ValidatingAdmissionPolicy: yes for built-in kinds; for custom resources only through a workspace CRD |
| `variables` | `spec.variables[]`: each `name` is a property, typed by the result type of its `expression` | Kyverno policies | The checker types each variable by its expression. Hover and completion give every variable `dyn` (`variablesType` in `internal/assist/kyverno.go` marks it preserve-unknown-fields and does not use `check.VariableTypes` as the ValidatingAdmissionPolicy does) | Yes: the same document holds each expression, so hover could show the checked type |
| `request` | The kind itself: the admission request with the request `uid` | Kyverno policies | Typed | n/a |
| `namespaceObject` | The kind itself: a `Namespace` | Kyverno policies | Checker types it `kubernetes.Namespace`; hover and completion give `dyn` | Yes: the kind fixes it, so hover could use the same type as the checker |
| `exceptions` | The kind itself: `allowedImages` and `allowedValues` of the matching PolicyExceptions | Kyverno policies | Typed in hover; the checker declares `libs.Exception` | n/a |
| `resource`, `http`, `image`, `globalContext`, `imagedata`, `user` | The Kyverno CEL libraries that bring a variable or a typed value | Kyverno CEL libraries | Provided by the checker's environment | n/a |
| `json`, `yaml`, `hash`, `math`, `random`, `x509`, `time`, `transform`, `gzip` | The Kyverno CEL libraries that only add functions; no variable | Kyverno CEL libraries | Functions of the checker's environment | n/a |

### NamespacedValidatingPolicy

Same fields as ValidatingPolicy, in `policies.kyverno.io/v1`, but namespaced.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | `spec.matchConstraints.resourceRules[]` | Kyverno policies | Not read today: `pkg/extract/extract.go` and `internal/assist/kyverno.go` match only `ValidatingPolicy` | Yes, like ValidatingPolicy |
| `variables` | `spec.variables[]` | Kyverno policies | Not read today | Yes |
| `request`, `namespaceObject`, `exceptions` and the libraries | The kind itself | Kyverno policies | Not read today | n/a |

### ValidatingPolicy: spec.webhookConfiguration.matchConditions

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject`, `request` | Like the Webhooks' match conditions: `spec.matchConstraints.resourceRules[]` and the kind | Kyverno policies, Webhooks | Not read today: the expression is not found, so no variable is given | Yes |

A `spec.evaluation` or `spec.autogen` field does not change a type.

### MutatingPolicy (and NamespacedMutatingPolicy)

CEL in `spec.matchConditions[].expression`, `spec.variables[].expression`,
`spec.mutations[].applyConfiguration.expression`,
`spec.mutations[].jsonPatch.expression` and
`spec.webhookConfiguration.matchConditions[].expression`;
`spec.targetMatchConstraints` and `spec.matchConstraints` select the resources.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | `spec.matchConstraints.resourceRules[]` | Kyverno policies | Not read today | Yes, like ValidatingPolicy |
| `variables` | `spec.variables[]` | Kyverno policies | Not read today | Yes |
| `request`, `namespaceObject`, `authorizer` | The kind itself | Kyverno policies | Not read today | n/a |
| `target` (target mutation) | `spec.targetMatchConstraints.resourceRules[]` | Kyverno policies | Not read today | Yes, like `object` |
| Kyverno CEL libraries | Fixed by Kyverno | Kyverno CEL libraries | Not read today | n/a |

### GeneratingPolicy (and NamespacedGeneratingPolicy)

CEL in `spec.matchConditions[].expression`, `spec.variables[].expression` and
`spec.generate[].expression`.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | `spec.matchConstraints.resourceRules[]` | Kyverno policies | Not read today | Yes |
| `variables` | `spec.variables[]` | Kyverno policies | Not read today | Yes |
| `request`, `namespaceObject` | The kind itself | Kyverno policies | Not read today | n/a |
| `generator` (the `generator.Apply` helper) | The Kyverno `generator` library | Kyverno CEL libraries | Not read today | n/a |

### ImageValidatingPolicy (and NamespacedImageValidatingPolicy)

CEL in `spec.matchConditions[].expression`, `spec.variables[].expression`,
`spec.matchImageReferences[].expression`, `spec.images[].expression`,
`spec.attestors[]`/`spec.attestations[]` expressions and
`spec.validations[].expression`.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | `spec.matchConstraints.resourceRules[]` | Kyverno policies | Not read today | Yes |
| `variables` | `spec.variables[]` | Kyverno policies | Not read today | Yes |
| `images` | `spec.images[]`: each `name` is a key, its `expression` yields the image list | Kyverno policies | Not read today | Yes, the names; the values are strings found in the resource |
| `attestors`, `attestations` | `spec.attestors[]` and `spec.attestations[]` by `name` | Kyverno policies | Not read today | Yes for the names; the attestation payload is `dyn`, as it is signed data outside the document |
| `request`, `namespaceObject` | The kind itself | Kyverno policies | Not read today | n/a |

### DeletingPolicy (and NamespacedDeletingPolicy)

CEL in `spec.conditions[].expression` and `spec.variables[].expression`.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object` | `spec.matchConstraints.resourceRules[]`: the resource considered for deletion | Kyverno policies | Not read today | Yes |
| `variables` | `spec.variables[]` | Kyverno policies | Not read today | Yes |
| `namespaceObject` | The kind itself | Kyverno policies | Not read today | n/a |

### PolicyException

CEL in `spec.matchConditions[].expression`.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | The `policyRefs` name the policies, which usually sit in other documents; the exception's own document does not say the kind | Kyverno policies | Not read today | No: the type would come from another document, which the rule above forbids, so it stays `dyn` and hover says so |
| `request`, `namespaceObject` | The kind itself | Kyverno policies | Not read today | n/a |

### ClusterPolicy and Policy (kyverno.io/v1)

The older Kyverno kinds hold CEL in `spec.rules[].validate.cel.expressions[]`
(`expression`, `messageExpression`), `spec.rules[].validate.cel.variables[]`,
`spec.rules[].validate.cel.auditAnnotations[]` and `spec.rules[].celPreconditions[]`.

| Variable | Type comes from | Source | Engine today | Could the document tell? |
|---|---|---|---|---|
| `object`, `oldObject` | `spec.rules[].match`/`exclude` (`resources.kinds`, `any`, `all`), per rule | [Kyverno validate rules, CEL](https://kyverno.io/docs/policy-types/cluster-policy/validate/#common-expression-language-cel) | Not read today | Yes for a rule whose `match` lists plain kinds (`Pod`, `apps/v1/Deployment`); `dyn` for wildcards |
| `params` | `spec.rules[].validate.cel.paramKind` | Same | Not read today | Same as the ValidatingAdmissionPolicy |
| `variables` | `spec.rules[].validate.cel.variables[]` | Same | Not read today | Yes |
| `request`, `namespaceObject`, `authorizer` | The kind itself | Same | Not read today | n/a |

The rule's own `match` is the one that counts: another rule of the same policy,
or another document, never decides the type.

## Summary of what the document can add

The engine can already type, from the document alone: CRD `self`/`oldSelf`,
`object`/`oldObject` from `matchConstraints`, `params` from `paramKind` and
`variables` from `variables`. The next steps the table shows are: read the other
kinds, type `namespaceObject` in Kyverno hover from its kind, and type webhook and
Kyverno `object` from each rule or webhook's own match. `dyn` that stays `dyn`:
JWT claims, DRA attribute values, attestation payloads, a custom resource without
a CRD in the workspace, and a PolicyException's `object`.

## How many expressions still get dyn

`make check` (and `make dyn-report` alone) checks every expression of real
policies and prints how many meet a `dyn` value, and why. The policies are in
`testdata/dyn/`: Kyverno's `ValidatingPolicy` library
([kyverno/policies](https://github.com/kyverno/policies), Apache-2.0) and the
ValidatingAdmissionPolicy and MutatingAdmissionPolicy examples of the
[Kubernetes docs](https://github.com/kubernetes/website) (CC-BY-4.0). Each
`<name>.yaml` has a `<name>.yaml.source` note with its URL, commit and licence.
An expression meets `dyn` when hover over any variable or field in it says
`dyn: <reason>`; an expression with several reasons counts once under each.
The reasons are the ones hover gives (`internal/assist/dyn.go`). The test is
`TestDynReport` in `internal/assist/dynreport_test.go`. Current output:

```
dyn report: expressions of real policies that meet dyn
Kyverno policy library  37 policies   121 expressions    26 meet dyn
Kubernetes docs         11 policies    23 expressions     6 meet dyn
total                   48 policies   144 expressions    32 meet dyn
expressions per dyn reason (one expression can have several):
  wildcard-resource-rule        1  this policy matches resources with a wildcard (for example `resources: ["*"]`), so no one kind is known
  unknown-kind                  3  this policy matches a resource whose fields the engine does not know, such as a custom resource
  paramkind-not-found           3  spec.paramKind is no built-in kind and no CustomResourceDefinition among the workspace files defines it
  preserve-unknown-fields       2  the schema leaves this value open with `x-kubernetes-preserve-unknown-fields`
  policy-variable-untyped      23  the type of this policy variable is not worked out for this policy
```
