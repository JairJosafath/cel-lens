package assist

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"k8s.io/kube-openapi/pkg/validation/spec"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// variable is a name an expression can use.
type variable struct {
	name string
	desc string
	// typeName replaces the name of the schema's type when set.
	typeName string
	schema   schema
	// celType is the name of an opaque type that has no schema, such as
	// resource.Context, when only the functions declared on it apply.
	celType string
}

func (v variable) typ() string {
	if v.typeName != "" {
		return v.typeName
	}
	return v.schema.name()
}

// unknownType returns the schema of a value whose fields the API server does not
// know until it runs: any JSON.
func unknownType() map[string]any {
	return map[string]any{"x-kubernetes-preserve-unknown-fields": true}
}

// scope returns the variables the expression e of doc can use, with the
// types the API server gives them. It returns nil for a file it cannot read
// the document from.
func scope(doc *Document, e extract.Expression, v kubeversion.Version) []variable {
	if e.Document >= len(doc.roots) {
		return nil
	}
	root := doc.roots[e.Document]
	switch root["kind"] {
	case "CustomResourceDefinition":
		return crdScope(root, e)
	case "ValidatingAdmissionPolicy", "MutatingAdmissionPolicy":
		return policyScope(root, e, v, doc.crds)
	}
	return nil
}

// crdScope declares self and oldSelf with the schema the rule is attached to.
func crdScope(root map[string]any, e extract.Expression) []variable {
	path, _, ok := cutLast(e.FieldPath, ".x-kubernetes-validations[")
	if !ok {
		return nil
	}
	s, _ := walk(root, path).(map[string]any)
	if s == nil {
		return nil
	}
	return []variable{
		{name: "self", desc: "The value the rule is attached to; its type comes from the schema.", schema: s},
		{name: "oldSelf", desc: "The value before the update, only in transition rules (rules that use oldSelf).", schema: s},
	}
}

// policyScope declares the variables of the admission CEL environment:
// params only when the policy has a paramKind, variables only for the
// expressions that may use them.
func policyScope(root map[string]any, e extract.Expression, v kubeversion.Version, crds *check.CRDs) []variable {
	policySpec, _ := root["spec"].(map[string]any)
	object := variable{name: "object", desc: "The object being admitted; null on DELETE.", schema: unknownType()}
	oldObject := variable{name: "oldObject", desc: "The existing object; null on CREATE.", schema: unknownType()}
	if kinds, ok := matchedKinds(policySpec); ok {
		object.typeName, object.schema = kinds.name, kinds.schema
		oldObject.typeName, oldObject.schema = kinds.name, kinds.schema
	}
	vars := []variable{
		object, oldObject,
		{name: "request", desc: "The attributes of the admission request.", typeName: "kubernetes.AdmissionRequest", schema: requestType(false)},
		{name: "namespaceObject", desc: "The namespace the object belongs to; null for cluster-scoped objects.", typeName: "kubernetes.Namespace", schema: namespaceType()},
		{name: "authorizer", desc: "Checks what the requesting user may do; start with authorizer.group(...), authorizer.path(...) or authorizer.serviceAccount(...). authorizer.requestResource checks the resource of the request.", typeName: authorizerType, celType: authorizerType},
	}
	if policySpec["paramKind"] != nil {
		vars = append(vars, paramsVariable(policySpec["paramKind"], crds))
	}
	if strings.HasPrefix(e.FieldPath, "spec.matchConditions[") {
		return vars
	}
	return append(vars, variable{
		name: "variables", desc: "The policy variables of spec.variables, by name.", typeName: "kubernetes.variables",
		schema: policyVariables(root, e, v, crds),
	})
}

// policyVariables returns the schema of variables for e: a property for each
// variable of spec.variables that e may use, each typed by its expression as
// the API server types it (see check.VariableTypes). When the policy cannot be
// compiled to types, the properties are dyn.
func policyVariables(root map[string]any, e extract.Expression, v kubeversion.Version, crds *check.CRDs) schema {
	policySpec, _ := root["spec"].(map[string]any)
	typed, err := check.VariableTypes(context.Background(), root, check.WithVersion(v), check.WithCRDs(crds))
	if err != nil {
		return variablesType(policySpec, e)
	}
	props := map[string]any{}
	for i, tv := range typed {
		if limit := variableLimit(e); limit >= 0 && i >= limit {
			break
		}
		s := maps.Clone(tv.Schema)
		s["description"] = fmt.Sprintf("Policy variable %s, defined in spec.variables by %#q.", tv.Name, tv.Expression)
		props[tv.Name] = s
	}
	return schema{"type": "object", "properties": props}
}

// paramsVariable declares params for the paramKind of a policy, typed from the
// built-in kind it names or from the CustomResourceDefinition in crds that
// defines it, as the API server's type checker resolves it. When the kind is
// found in neither, params is dyn and the description says why.
func paramsVariable(paramKind any, crds *check.CRDs) variable {
	const desc = "The parameter resource the binding refers to; null when there is none."
	pk, _ := paramKind.(map[string]any)
	apiVersion, _ := pk["apiVersion"].(string)
	kind, _ := pk["kind"].(string)
	if k, ok := matchkind.Lookup(apiVersion, kind); ok {
		if s, ok := toSchema(k.Schema); ok {
			return variable{name: "params", desc: desc, typeName: k.String(), schema: s}
		}
	}
	if s, ok := crds.Schema(apiVersion, kind); ok {
		return variable{name: "params", desc: desc, typeName: apiVersion + " " + kind, schema: s}
	}
	return variable{
		name: "params", schema: unknownType(),
		desc: desc + " It is dyn, not typed, because spec.paramKind " + apiVersion + " " + kind +
			" was not found: it is no built-in kind and no CustomResourceDefinition among the workspace files defines it.",
	}
}

// requestType returns the schema of the admission request, as the API server
// declares it for CEL. Kyverno's request also has the unique ID of the request,
// which withUID adds.
func requestType(withUID bool) map[string]any {
	props := map[string]any{
		"kind":               gvk("The fully-qualified type of the object being admitted."),
		"resource":           gvr("The resource being requested."),
		"subResource":        str("The subresource being requested, if any."),
		"requestKind":        gvk("The type of the request as sent, before any conversion."),
		"requestResource":    gvr("The resource of the request as sent, before any conversion."),
		"requestSubResource": str("The subresource of the request as sent, if any."),
		"name":               str("The name of the object; empty on CREATE with generateName."),
		"namespace":          str("The namespace of the object; empty for cluster-scoped objects."),
		"operation":          str("CREATE, UPDATE, DELETE or CONNECT."),
		"userInfo": obj(map[string]any{
			"username": str("The name of the user."),
			"uid":      str("The unique ID of the user."),
			"groups":   map[string]any{"type": "array", "items": str(""), "description": "The groups the user belongs to."},
			"extra":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "array", "items": str("")}, "description": "Extra information from the authenticator."},
		}, "The user making the request."),
		"dryRun":  map[string]any{"type": "boolean", "description": "True when the request does not persist anything."},
		"options": unknownType(),
	}
	if withUID {
		props["uid"] = str("The unique ID of the request.")
	}
	return obj(props, "The attributes of the admission request.")
}

// namespaceType returns the schema of namespaceObject, as the API server
// declares it for CEL: a Namespace with only some of its metadata, and the
// UID field named as the API server names it.
func namespaceType() map[string]any {
	list := map[string]any{"type": "array", "items": str("")}
	strMap := map[string]any{"type": "object", "additionalProperties": str("")}
	return obj(map[string]any{
		"metadata": obj(map[string]any{
			"name":                       str("The name of the namespace."),
			"generateName":               str(""),
			"namespace":                  str(""),
			"labels":                     strMap,
			"annotations":                strMap,
			"UID":                        str("The unique ID of the namespace."),
			"creationTimestamp":          map[string]any{"type": "string", "format": "date-time"},
			"deletionGracePeriodSeconds": map[string]any{"type": "integer"},
			"deletionTimestamp":          map[string]any{"type": "string", "format": "date-time"},
			"generation":                 map[string]any{"type": "integer"},
			"resourceVersion":            str(""),
			"finalizers":                 list,
		}, "The metadata of the namespace."),
		"spec": obj(map[string]any{"finalizers": list}, "The spec of the namespace."),
		"status": obj(map[string]any{
			"conditions": map[string]any{"type": "array", "items": obj(map[string]any{
				"status":             str(""),
				"type":               str(""),
				"lastTransitionTime": map[string]any{"type": "string", "format": "date-time"},
				"message":            str(""),
				"reason":             str(""),
			}, "")},
			"phase": str("The phase of the namespace: Active or Terminating."),
		}, "The status of the namespace."),
	}, "The namespace the object belongs to.")
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func obj(props map[string]any, desc string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "description": desc}
}

func gvk(desc string) map[string]any {
	return obj(map[string]any{"group": str(""), "version": str(""), "kind": str("")}, desc)
}

func gvr(desc string) map[string]any {
	return obj(map[string]any{"group": str(""), "version": str(""), "resource": str("")}, desc)
}

// cutLast cuts s around the last occurrence of sep.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// walk follows a Kubernetes field path such as "spec.versions[0].schema" or
// "a.properties[spec]" through decoded YAML, and returns what is there, or
// nil.
func walk(node any, path string) any {
	for path != "" {
		var key string
		switch path[0] {
		case '.':
			path = path[1:]
			continue
		case '[':
			end := strings.IndexByte(path, ']')
			if end < 0 {
				return nil
			}
			key, path = path[1:end], path[end+1:]
		default:
			end := strings.IndexAny(path, ".[")
			if end < 0 {
				end = len(path)
			}
			key, path = path[:end], path[end:]
		}
		switch n := node.(type) {
		case map[string]any:
			node = n[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(n) {
				return nil
			}
			node = n[i]
		default:
			return nil
		}
	}
	return node
}

// kindsSchema is the kinds a policy matches as one type: name is their names
// with the API version, joined, and schema has the fields of any of them.
type kindsSchema struct {
	name   string
	schema schema
}

// matchedKinds returns the built-in kinds the policy spec matches, as the API
// server's type checker finds them. It reports false unless every matched kind
// is known. With several kinds, schema is their merge (see merge).
func matchedKinds(policySpec map[string]any) (kindsSchema, bool) {
	constraints, _ := policySpec["matchConstraints"].(map[string]any)
	list, _ := constraints["resourceRules"].([]any)
	var rules []matchkind.Rule
	for _, item := range list {
		m, _ := item.(map[string]any)
		rules = append(rules, matchkind.Rule{Groups: stringList(m["apiGroups"]), Versions: stringList(m["apiVersions"]), Resources: stringList(m["resources"])})
	}
	kinds, ok := matchkind.Resolve(rules)
	if !ok {
		return kindsSchema{}, false
	}
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = k.String()
	}
	parts := make([]part, len(kinds))
	for i, k := range kinds {
		s, ok := toSchema(k.Schema)
		if !ok {
			return kindsSchema{}, false
		}
		parts[i] = part{name: shortName(kinds, i), schema: s}
	}
	if len(parts) == 1 {
		return kindsSchema{name: names[0], schema: parts[0].schema}, true
	}
	return kindsSchema{name: strings.Join(names, ", "), schema: merge(parts)}, true
}

// toSchema converts the OpenAPI schema of a built-in kind to the form a CRD's
// schema has when YAML decodes it.
func toSchema(s *spec.Schema) (schema, bool) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, false
	}
	var out schema
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

// shortName is the name of kinds[i] a reader tells the kinds apart by: its
// Kind, or with its API version when another matched kind has the same Kind.
func shortName(kinds []matchkind.Kind, i int) string {
	for j, k := range kinds {
		if j != i && k.GVK.Kind == kinds[i].GVK.Kind {
			return kinds[i].String()
		}
	}
	return kinds[i].GVK.Kind
}

// stringList returns the strings of v, a decoded YAML list. An item that is no
// string, such as a number, becomes "*": the kind is then not known.
func stringList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, len(list))
	for i, item := range list {
		if str, ok := item.(string); ok {
			out[i] = str
		} else {
			out[i] = "*"
		}
	}
	return out
}
