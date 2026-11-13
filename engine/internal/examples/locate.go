package examples

import (
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// at returns where the problem of o at the field path field is: the key of
// the nearest field of the path the document has, an item of a list where
// the path names one, or the first key of the document for its root.
//
// The API server writes a path with dots between the names and an index or
// a key in brackets ("spec.ports[0].name", "metadata.labels[app]"); the
// schema validation also writes an index, and a key with dots, between dots
// ("spec.ports.0.name", "spec.labels.example.com/team").
func (o *Object) at(field string) (start, end extract.Position) {
	root := content(o.node)
	found := root
	if root != nil && field != "<nil>" {
		found = walk(root, steps(field))
	}
	if found == nil || found == root {
		found = firstKey(root)
	}
	if found == nil {
		return extract.Position{Line: o.Line, Column: 1}, extract.Position{Line: o.Line, Column: 1}
	}
	return o.span(found)
}

// Head returns where the document of o starts, where a problem of the
// whole example shows: its first key, or the start of the line its content
// starts at when it has none.
func (o *Object) Head() (start, end extract.Position) { return o.at("<nil>") }

// step is one step of a field path: a name, or what is in brackets.
type step struct {
	text     string
	brackets bool
}

// steps splits a field path into its steps.
func steps(field string) []step {
	var out []step
	for field != "" {
		switch field[0] {
		case '.':
			field = field[1:]
		case '[':
			end := strings.IndexByte(field, ']')
			if end < 0 {
				end = len(field)
			}
			out = append(out, step{text: field[1:end], brackets: true})
			field = field[min(end+1, len(field)):]
		default:
			end := strings.IndexAny(field, ".[")
			if end < 0 {
				end = len(field)
			}
			out = append(out, step{text: field[:end]})
			field = field[end:]
		}
	}
	return out
}

// walk follows the steps from n, and returns the node to point at for the
// last one the document has: a mapping's key, or an item of a list; n when
// it has none of them.
func walk(n *yaml.Node, path []step) *yaml.Node {
	at := n
	for len(path) > 0 {
		n = resolve(n)
		switch n.Kind {
		case yaml.MappingNode:
			// A name with dots, as of a label, may be written between dots:
			// the shortest run of names the mapping has a key for is taken.
			key, value, used := (*yaml.Node)(nil), (*yaml.Node)(nil), 0
			for i := 1; i <= len(path) && key == nil; i++ {
				if i > 1 && path[i-1].brackets {
					break
				}
				names := make([]string, i)
				for j := range i {
					names[j] = path[j].text
				}
				key, value = lookup(n, strings.Join(names, "."))
				used = i
			}
			if key == nil {
				return at
			}
			at, n, path = key, value, path[used:]
		case yaml.SequenceNode:
			i, err := strconv.Atoi(path[0].text)
			if err != nil || i < 0 || i >= len(n.Content) {
				return at
			}
			n = n.Content[i]
			at, path = n, path[1:]
		case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
			return at
		}
	}
	return at
}

// lookup returns the key and value of the mapping n for name, through its
// merge keys (<<); nil when it has none.
func lookup(n *yaml.Node, name string) (key, value *yaml.Node) {
	var merged []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Tag == "!!merge" {
			merged = append(merged, v)
			continue
		}
		if k.Value == name {
			return k, v
		}
	}
	for _, m := range merged {
		m = resolve(m)
		from := []*yaml.Node{m}
		if m.Kind == yaml.SequenceNode {
			from = m.Content
		}
		for _, f := range from {
			if f = resolve(f); f.Kind == yaml.MappingNode {
				if k, v := lookup(f, name); k != nil {
					return k, v
				}
			}
		}
	}
	return nil, nil
}

// resolve is the node an alias stands for, or n.
func resolve(n *yaml.Node) *yaml.Node {
	for i := 0; n.Kind == yaml.AliasNode && n.Alias != nil && i < 100; i++ {
		n = n.Alias
	}
	return n
}

// firstKey is the first key of the mapping n, or n when it is no mapping or
// has none.
func firstKey(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.MappingNode && len(n.Content) > 0 {
		return n.Content[0]
	}
	return n
}

// span returns where n is written: a scalar from its first character to
// after its last on its line, with its quotes; a mapping or list, as an item
// of a list, at its first key or item.
func (o *Object) span(n *yaml.Node) (start, end extract.Position) {
	for (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) && len(n.Content) > 0 && n.Style&yaml.FlowStyle == 0 {
		n = n.Content[0]
	}
	start = extract.Position{Line: n.Line, Column: n.Column}
	width := len([]rune(n.Value))
	switch {
	case n.Kind != yaml.ScalarNode, n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0, strings.Contains(n.Value, "\n"):
		width = 1
		if n.Line >= 1 && n.Line <= len(o.lines) {
			line := strings.TrimRight(string(o.lines[n.Line-1]), " \t\r")
			width = max(len([]rune(line))-n.Column+1, 1)
		}
	case n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0:
		width += 2
	}
	return start, extract.Position{Line: n.Line, Column: n.Column + width}
}
