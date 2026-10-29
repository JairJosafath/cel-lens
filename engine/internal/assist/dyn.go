package assist

import "github.com/JairJosafath/cel-lens/engine/internal/matchkind"

// dynReason is why a value is dyn: the type of the value cannot be known from
// its document. Every fallback to dyn records one of these, and hover says it.
type dynReason string

// The reasons a value is dyn. The list is fixed: a new fallback to dyn picks
// one of them, or adds one here with its sentence in text.
const (
	// reasonWildcard: a match rule has a wildcard, so no one kind is matched.
	reasonWildcard dynReason = "wildcard-resource-rule"
	// reasonNoRule: the policy has no match rule that names a resource.
	reasonNoRule dynReason = "no-resource-rule"
	// reasonUnknownKind: a matched resource is a kind the engine has no schema for.
	reasonUnknownKind dynReason = "unknown-kind"
	// reasonParamKind: the CRD of paramKind is neither built in nor in the workspace.
	reasonParamKind dynReason = "paramkind-not-found"
	// reasonOpenSchema: the schema leaves the field open with x-kubernetes-preserve-unknown-fields.
	reasonOpenSchema dynReason = "preserve-unknown-fields"
	// reasonCRDsOff: the experimental feature crd-typed-policies is off.
	reasonCRDsOff dynReason = "crd-typed-policies-off"
	// reasonNoType: the schema of the field gives it no type.
	reasonNoType dynReason = "schema-without-type"
	// reasonVariable: the policy variable's type is not worked out for this policy.
	reasonVariable dynReason = "policy-variable-untyped"
	// reasonVarResult: the API server makes the type of the variable's result dyn, as for an object.
	reasonVarResult dynReason = "variable-result-dyn"
	// reasonVarInput: the variable's expression reads a value whose type is not known.
	reasonVarInput dynReason = "variable-input-dyn"
	// reasonVarUncompiled: the variable's expression does not compile.
	reasonVarUncompiled dynReason = "variable-uncompiled"
	// reasonVarKinds: the matched kinds give the variable different types.
	reasonVarKinds dynReason = "variable-kinds-differ"
)

// dynKey is the extension of a schema that records why the value is dyn.
const dynKey = "x-cel-lens-dyn"

// text is the reason as a plain sentence, without a final full stop.
func (r dynReason) text() string {
	switch r {
	case reasonWildcard:
		return "this policy matches resources with a wildcard (for example `resources: [\"*\"]`), so no one kind is known"
	case reasonNoRule:
		return "this policy has no match rule that names a resource, so no kind is known"
	case reasonUnknownKind:
		return "this policy matches a resource whose fields the engine does not know, such as a custom resource"
	case reasonParamKind:
		return "spec.paramKind is no built-in kind and no CustomResourceDefinition among the workspace files defines it"
	case reasonCRDsOff:
		return "typing params from a CustomResourceDefinition is the experimental feature `crd-typed-policies`, which is off"
	case reasonOpenSchema:
		return "the schema leaves this value open with `x-kubernetes-preserve-unknown-fields`"
	case reasonNoType:
		return "the schema gives this value no type"
	case reasonVariable:
		return "the type of this policy variable is not worked out for this policy"
	case reasonVarResult:
		return "the API server makes the type of this variable dyn, because its expression returns an object"
	case reasonVarInput:
		return "the expression of this policy variable reads a value whose type is not known (dyn)"
	case reasonVarUncompiled:
		return "the expression of this policy variable does not compile, so it has no type"
	case reasonVarKinds:
		return "the kinds this policy matches give this policy variable different types"
	}
	return ""
}

// unknownType returns the schema of a value whose fields the API server does
// not know until it runs: any JSON, dyn for the reason why.
func unknownType(why dynReason) map[string]any {
	return map[string]any{"x-kubernetes-preserve-unknown-fields": true, dynKey: string(why)}
}

// dynReasonOf returns why a value with schema s is dyn, or "" when its type is
// known.
func dynReasonOf(s schema) dynReason {
	if s.name() != "dyn" {
		return ""
	}
	if why := dynReason(s.str(dynKey)); why != "" {
		return why
	}
	if s.flag("x-kubernetes-preserve-unknown-fields") {
		return reasonOpenSchema
	}
	return reasonNoType
}

// withDynReason adds to the hover text md the reason the value with schema s is
// dyn, when it is and the experimental feature fitting-completion is on.
func (a *Assistant) withDynReason(md string, s schema) string {
	if !a.fitting() {
		return md
	}
	if why := dynReasonOf(s); why != "" {
		return md + "\n\ndyn: " + why.text()
	}
	return md
}

// whyNotMatched says why the match rules of rules do not name one known set of
// kinds, in the order matchkind.Resolve checks them.
func whyNotMatched(rules []matchkind.Rule) dynReason {
	named := false
	for _, r := range rules {
		if len(r.Groups) == 0 || len(r.Versions) == 0 || len(r.Resources) == 0 {
			continue
		}
		named = true
		if hasWildcard(r.Groups) || hasWildcard(r.Versions) || hasWildcard(r.Resources) {
			return reasonWildcard
		}
		if _, ok := matchkind.Resolve([]matchkind.Rule{r}); !ok {
			return reasonUnknownKind
		}
	}
	if !named {
		return reasonNoRule
	}
	return reasonUnknownKind
}

func hasWildcard(list []string) bool {
	for _, s := range list {
		for _, c := range s {
			if c == '*' {
				return true
			}
		}
	}
	return false
}
