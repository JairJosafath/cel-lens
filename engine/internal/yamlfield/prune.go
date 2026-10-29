package yamlfield

import (
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Tree is a private copy of a YAML document from which fields can be removed,
// so the rest of the document can be decoded again.
type Tree struct {
	// Root is the copied document node. Its nodes keep their lines and columns.
	Root    *yaml.Node
	parents map[*yaml.Node]*yaml.Node
}

// Copy returns a copy of the document below root. Aliases in the copy point at
// the copied anchors. root itself is not changed.
func Copy(root *yaml.Node) *Tree {
	t := &Tree{parents: map[*yaml.Node]*yaml.Node{}}
	t.Root = t.copyNode(root, nil, map[*yaml.Node]*yaml.Node{})
	return t
}

func (t *Tree) copyNode(n, parent *yaml.Node, done map[*yaml.Node]*yaml.Node) *yaml.Node {
	c := *n
	done[n] = &c
	if parent != nil {
		t.parents[&c] = parent
	}
	if n.Alias != nil {
		if a, ok := done[n.Alias]; ok {
			c.Alias = a
		}
	}
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, ch := range n.Content {
		c.Content[i] = t.copyNode(ch, &c, done)
	}
	return &c
}

// Remove drops the field f, found in the tree, from it: the entry of a mapping
// or the item of a list, except that an item of a list of objects is replaced
// by an empty object, so the items after it keep their indices; for an entry whose value is an alias, f.Node may be
// that value. It reports whether f was in the tree. Dropping the
// type of a schema would leave a schema that is not structural, which hides
// the rules of the whole version, so the schema then gets the type its other
// keys imply and keeps unknown fields, and its rules are still checked.
func (t *Tree) Remove(f Field) bool {
	p := t.parents[f.Node]
	if p == nil {
		return false
	}
	if p.Kind == yaml.MappingNode && IsSchemaType(f) {
		return t.retype(p, f.Node)
	}
	for i, c := range p.Content {
		if c != f.Node {
			continue
		}
		if p.Kind == yaml.SequenceNode && f.Expected == "an object" {
			p.Content = slices.Clone(p.Content)
			p.Content[i] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: c.Line, Column: c.Column}
			t.parents[p.Content[i]] = p
			return true
		}
		n := 1
		if p.Kind == yaml.MappingNode {
			// f.Node is the value, not the key, of an entry whose value is
			// an alias, so the entry starts at the even index.
			i, n = i&^1, 2
		}
		p.Content = append(p.Content[:i:i], p.Content[min(i+n, len(p.Content)):]...)
		return true
	}
	return false
}

// IsSchemaType reports whether f is the "type" field of a CRD schema.
func IsSchemaType(f Field) bool {
	return strings.HasSuffix(f.Path, ".type") && strings.Contains(f.Path, "openAPIV3Schema") &&
		f.Node.Kind == yaml.ScalarNode && f.Node.Value == "type"
}

// retype replaces the entry of key in the schema mapping p with a valid type.
func (t *Tree) retype(p, key *yaml.Node) bool {
	typ, unknown := "object", false
	for i := 0; i+1 < len(p.Content); i += 2 {
		switch p.Content[i].Value {
		case "items":
			typ = "array"
		case "x-kubernetes-preserve-unknown-fields":
			unknown = true
		}
	}
	for i := 0; i+1 < len(p.Content); i += 2 {
		if p.Content[i] != key {
			continue
		}
		val := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: typ, Line: key.Line, Column: key.Column}
		p.Content = slices.Clone(p.Content)
		p.Content[i+1] = val
		if !unknown {
			p.Content = append(p.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "x-kubernetes-preserve-unknown-fields"},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
		}
		return true
	}
	return false
}
