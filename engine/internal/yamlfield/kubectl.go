package yamlfield

import (
	"go.yaml.in/yaml/v3"
)

// YAML11Bool is the boolean that YAML 1.1 reads the plain string s as, and
// false for ok when s is not one: y, yes and on are true, n, no and off false,
// each in the three cases that sigs.k8s.io/yaml (go-yaml v2) takes.
func YAML11Bool(s string) (value, ok bool) {
	switch s {
	case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON":
		return true, true
	case "n", "N", "no", "No", "NO", "off", "Off", "OFF":
		return false, true
	}
	return false, false
}

// PlainBool reports whether n is a plain scalar, one written without quotes or
// a tag, that kubectl and the API server read as a boolean although go-yaml v3
// reads it as a string: y, yes, on, n, no and off. It gives the boolean.
// Their YAML parser, sigs.k8s.io/yaml, is YAML 1.1 for these words.
func PlainBool(n *yaml.Node) (value, ok bool) {
	if n.Kind != yaml.ScalarNode || n.Style != 0 || n.ShortTag() != "!!str" {
		return false, false
	}
	return YAML11Bool(n.Value)
}

// Decode decodes the YAML node into out as kubectl sends the document to the
// API server, which is the way n.Decode decodes it but for the plain scalars
// PlainBool takes for booleans: a field that wants a string then gets a
// boolean, and fails, as it does for the API server. The keys of a mapping
// are read as written. node is not changed.
func Decode(node *yaml.Node, out any) error {
	if !hasPlainBool(node) {
		return node.Decode(out)
	}
	t := Copy(node)
	retag(t.Root)
	return t.Root.Decode(out)
}

// hasPlainBool reports whether a value below n is a plain scalar PlainBool
// accepts. Aliases are not followed: the anchor they name is in the tree.
func hasPlainBool(n *yaml.Node) bool {
	for i, c := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			if hasPlainBool(c) {
				return true
			}
			continue
		}
		if _, ok := PlainBool(c); ok || hasPlainBool(c) {
			return true
		}
	}
	return false
}

// retag makes the plain scalars of the values below n that PlainBool accepts
// the booleans they are.
func retag(n *yaml.Node) {
	for i, c := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			retag(c)
			continue
		}
		if v, ok := PlainBool(c); ok {
			c.Tag, c.Value = "!!bool", "false"
			if v {
				c.Value = "true"
			}
			continue
		}
		retag(c)
	}
}
