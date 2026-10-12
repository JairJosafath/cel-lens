package check

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	celschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// validationField names the field of an x-kubernetes-validations entry that
// the API server error at field is about, such as "fieldPath", and the field
// path of the schema the entry belongs to. It reports false for errors about
// other fields, such as rule and messageExpression.
func validationField(field string) (schema, name string, ok bool) {
	for _, name := range []string{"fieldPath", "reason", "message"} {
		rest, found := strings.CutSuffix(field, "]."+name)
		if !found {
			continue
		}
		if i := strings.LastIndex(rest, ".x-kubernetes-validations["); i >= 0 {
			return rest[:i], name, true
		}
	}
	return "", "", false
}

// valueAt finds the YAML mapping value at the Kubernetes field path field of
// the document node, with the node of its key. In a CRD whose versions share a
// schema the API server reports spec.validation, so there it looks in the
// schema of every version. It returns nothing when the document has no value
// at the path.
func valueAt(doc *yaml.Node, field string) (found []located) {
	if rest, ok := strings.CutPrefix(field, "spec.validation."); ok {
		versions := walk(doc, "spec.versions")
		if versions.value == nil {
			return nil
		}
		for i := range versions.value.Content {
			if l := walk(doc, fmt.Sprintf("spec.versions[%d].schema.%s", i, rest)); l.value != nil {
				found = append(found, l)
			}
		}
		return found
	}
	if l := walk(doc, field); l.value != nil {
		found = append(found, l)
	}
	return found
}

// located is a mapping value with its key. The value is resolved through
// aliases; use is the node written in the file, which differs from value when
// the file writes an alias.
type located struct{ key, value, use *yaml.Node }

// walk follows the field path, as the API server writes it ("spec.versions[0]",
// "properties[name]"), from node, through aliases. The result is empty when the
// path leads nowhere.
func walk(node *yaml.Node, field string) located {
	cur := located{value: node, use: node}
	for field != "" && cur.value != nil {
		if field[0] == '.' {
			field = field[1:]
			continue
		}
		n := stepLen(field)
		step := field[:n]
		if field[0] == '[' {
			step = strings.TrimSuffix(field[1:n], "]")
		}
		field = field[n:]
		cur = child(cur.value, step)
	}
	return cur
}

// stepLen returns the length of the first step of field, a name up to the next
// '.' or '[' or a bracketed key with its brackets. Brackets in a key are
// matched by depth; the API server does not escape them, so when they do not
// balance the key ends at the first ']'. field is not empty and does not start
// with '.'.
func stepLen(field string) int {
	if field[0] != '[' {
		if end := strings.IndexAny(field, ".["); end >= 0 {
			return end
		}
		return len(field)
	}
	if end := closingBracket(field); end >= 0 {
		return end + 1
	}
	if end := strings.IndexByte(field, ']'); end >= 0 {
		return end + 1
	}
	return len(field)
}

// closingBracket returns the offset of the ']' that closes the '[' at the
// start of s, counting nested brackets, or -1 when s does not close it.
func closingBracket(s string) int {
	depth := 0
	for i := range len(s) {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// child returns the entry named key if n is a mapping, or the item at index key
// if n is a sequence.
func child(n *yaml.Node, key string) located {
	n = unalias(n)
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return located{n.Content[i], unalias(n.Content[i+1]), n.Content[i+1]}
			}
		}
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
	case yaml.SequenceNode:
		if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(n.Content) {
			return located{nil, unalias(n.Content[i]), n.Content[i]}
		}
	}
	return located{}
}

func unalias(n *yaml.Node) *yaml.Node {
	for n != nil {
		switch {
		case n.Kind == yaml.AliasNode && n.Alias != nil:
			n = n.Alias
		case n.Kind == yaml.DocumentNode && len(n.Content) > 0:
			n = n.Content[0]
		default:
			return n
		}
	}
	return &yaml.Node{}
}

// reportValue reports detail on the YAML value l, a scalar, as an error of the
// API server about it. It reports false when the value cannot be placed in the
// file, so that the caller can report the problem elsewhere.
func (r *reporter) reportValue(l located, detail string) bool {
	if l.key == nil || l.value.Kind != yaml.ScalarNode {
		return false
	}
	var start, end extract.Position
	var ok bool
	if l.use.Kind == yaml.AliasNode {
		start, end, ok = aliasSpan(r.lines, l.use)
	} else {
		start, end, ok = extract.Span(r.lines, l.value, l.key.Column-1)
	}
	if !ok {
		return false
	}
	r.diags = append(r.diags, Diagnostic{File: r.file, Message: detail, Severity: SeverityError, Start: start, End: end})
	return true
}

// aliasSpan finds the source range of the alias n, such as "*r", in lines.
func aliasSpan(lines [][]rune, n *yaml.Node) (start, end extract.Position, ok bool) {
	if n.Line < 1 || n.Line > len(lines) || n.Column < 1 {
		return start, end, false
	}
	last := n.Column + utf8.RuneCountInString(n.Value)
	if last > len(lines[n.Line-1]) {
		return start, end, false
	}
	return extract.Position{Line: n.Line, Column: n.Column}, extract.Position{Line: n.Line, Column: last + 1}, true
}

// missingPart explains why fieldPath does not resolve in schema, the schema
// node the x-kubernetes-validations entry belongs to: it names the first step
// of the path that does not exist. It returns detail, the API server's own
// message, when no single step can be named.
func missingPart(schema *yaml.Node, fieldPath, detail string) string {
	var versioned apiextensionsv1.JSONSchemaProps
	if err := decode(schema, &versioned); err != nil {
		return detail
	}
	var internal apiextensions.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&versioned, &internal, nil); err != nil {
		return detail
	}
	structural, err := structuralschema.NewStructural(&internal)
	if err != nil {
		return detail
	}
	prev := 0
	for _, end := range pathSteps(fieldPath) {
		if _, _, err := celschema.ValidFieldPath(fieldPath[:end], structural); err != nil {
			return fmt.Sprintf("%s: %q: %v", detail, strings.TrimPrefix(fieldPath[prev:end], "."), err)
		}
		prev = end
	}
	return detail
}

// pathSteps returns the offsets in path where a field name or a bracketed
// key ends.
func pathSteps(path string) []int {
	var ends []int
	for i := 0; i < len(path); {
		switch path[i] {
		case '.':
			i++
			for i < len(path) && !strings.ContainsRune(".[", rune(path[i])) {
				i++
			}
		case '[':
			end := strings.IndexByte(path[i:], ']')
			if end < 0 {
				return ends
			}
			i += end + 1
		default:
			return ends
		}
		ends = append(ends, i)
	}
	return ends
}
