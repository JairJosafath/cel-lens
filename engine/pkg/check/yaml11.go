package check

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/internal/yamlfield"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// yaml11Enums warns of every entry of an enum of a string schema in the
// CustomResourceDefinition node that is a plain y, n, yes, no, on or off (see
// yamlfield.PlainBool). The API server takes the CRD with the boolean kubectl
// reads there in the enum, so a value equal to the word, which is a string, is
// refused, and the entry itself is never a value of the field.
func (r *reporter) yaml11Enums(node *yaml.Node) {
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		node = node.Content[0]
	}
	versions := valueOf(valueOf(node, "spec"), "versions")
	if versions == nil || versions.Kind != yaml.SequenceNode {
		return
	}
	for i, v := range versions.Content {
		r.yaml11Schema(valueOf(valueOf(v, "schema"), "openAPIV3Schema"), fmt.Sprintf("spec.versions[%d].schema.openAPIV3Schema", i), 0)
	}
}

// maxSchemaDepth bounds how deep yaml11Schema follows a schema, so a
// document of deeply nested schemas cannot exhaust the stack.
const maxSchemaDepth = 100

// yaml11Schema looks for the enums of the schema s, at path, and below it.
func (r *reporter) yaml11Schema(s *yaml.Node, path string, depth int) {
	if s == nil || s.Kind != yaml.MappingNode || depth > maxSchemaDepth {
		return
	}
	if typ := valueOf(s, "type"); typ != nil && typ.Kind == yaml.ScalarNode && typ.Value == "string" {
		if enum := valueOf(s, "enum"); enum != nil && enum.Kind == yaml.SequenceNode {
			for i, item := range enum.Content {
				if v, ok := yamlfield.PlainBool(item); ok {
					r.yaml11Enum(item, fmt.Sprintf("%s.enum[%d]", path, i), v)
				}
			}
		}
	}
	for _, name := range []string{"items", "additionalProperties", "not"} {
		r.yaml11Schema(valueOf(s, name), path+"."+name, depth+1)
	}
	for _, name := range []string{"allOf", "anyOf", "oneOf"} {
		if list := valueOf(s, name); list != nil && list.Kind == yaml.SequenceNode {
			for i, item := range list.Content {
				r.yaml11Schema(item, fmt.Sprintf("%s.%s[%d]", path, name, i), depth+1)
			}
		}
	}
	if props := valueOf(s, "properties"); props != nil && props.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(props.Content); i += 2 {
			r.yaml11Schema(props.Content[i+1], fmt.Sprintf("%s.properties[%s]", path, props.Content[i].Value), depth+1)
		}
	}
}

// yaml11Enum warns of the enum entry item, at path, which kubectl reads as the
// boolean value.
func (r *reporter) yaml11Enum(item *yaml.Node, path string, value bool) {
	start := extract.Position{Line: item.Line, Column: item.Column}
	end := extract.Position{Line: item.Line, Column: item.Column + len([]rune(item.Value))}
	msg := fmt.Sprintf("%s: kubectl reads the unquoted %s as the boolean %t, not the string %q (YAML 1.1): quote it", path, item.Value, value, item.Value)
	r.diags = append(r.diags, Diagnostic{File: r.file, Message: msg, Severity: SeverityWarning, Start: start, End: end})
}

// valueOf is the value of the key name in the mapping n, nil when n is not a
// mapping or has none. An alias is not followed: the anchor is in the tree
// where it is written, and is looked at there, once.
func valueOf(n *yaml.Node, name string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == name {
			return n.Content[i+1]
		}
	}
	return nil
}
