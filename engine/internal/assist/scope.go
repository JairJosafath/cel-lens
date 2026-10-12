package assist

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
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
func scope(doc *Document, e extract.Expression) []variable {
	if e.Document >= len(doc.roots) {
		return nil
	}
	root := doc.roots[e.Document]
	switch root["kind"] {
	case "CustomResourceDefinition":
		return crdScope(root, e)
	case "ValidatingAdmissionPolicy":
		return policyScope(root, e)
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
func policyScope(root map[string]any, e extract.Expression) []variable {
	spec, _ := root["spec"].(map[string]any)
	object := variable{name: "object", desc: "The object being admitted; null on DELETE.", schema: unknownType()}
	oldObject := variable{name: "oldObject", desc: "The existing object; null on CREATE.", schema: unknownType()}
	if kinds, ok := matchedKinds(spec); ok {
		object.typeName, object.schema = kinds.name, kinds.schema
		oldObject.typeName, oldObject.schema = kinds.name, kinds.schema
	}
	vars := []variable{
		object, oldObject,
		{name: "request", desc: "The attributes of the admission request.", schema: requestType()},
		{name: "namespaceObject", desc: "The namespace the object belongs to; null for cluster-scoped objects.", schema: unknownType()},
		{name: "authorizer", desc: "Checks what the requesting user may do; start with authorizer.group(...) or authorizer.serviceAccount(...).", typeName: "authorizer"},
	}
	if spec["paramKind"] != nil {
		vars = append(vars, variable{name: "params", desc: "The parameter resource the binding refers to; null when there is none.", schema: unknownType()})
	}
	if strings.HasPrefix(e.FieldPath, "spec.matchConditions[") {
		return vars
	}
	return append(vars, variable{
		name: "variables", desc: "The policy variables of spec.variables, by name.",
		schema: variablesType(spec, e),
	})
}

// requestType returns the schema of the admission request, as the API server
// declares it for CEL.
func requestType() map[string]any {
	return obj(map[string]any{
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
		"uid":     str("The unique ID of the request."),
	}, "The attributes of the admission request.")
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
func matchedKinds(spec map[string]any) (kindsSchema, bool) {
	constraints, _ := spec["matchConstraints"].(map[string]any)
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
		raw, err := json.Marshal(k.Schema)
		if err != nil {
			return kindsSchema{}, false
		}
		var s schema
		if err := json.Unmarshal(raw, &s); err != nil {
			return kindsSchema{}, false
		}
		parts[i] = part{name: shortName(kinds, i), schema: s}
	}
	if len(parts) == 1 {
		return kindsSchema{name: names[0], schema: parts[0].schema}, true
	}
	return kindsSchema{name: strings.Join(names, ", "), schema: merge(parts)}, true
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
