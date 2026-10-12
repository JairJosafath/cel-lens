package yamlfield_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/internal/yamlfield"
)

type item struct {
	Name string `json:"name"`
	Size int    `json:"size"`
}

type doc struct {
	Meta  struct{ Title string } `json:"meta"`
	Items []item                 `json:"items"`
	Named map[string]item        `json:"named"`
}

// find decodes src into a doc, as Check does, and locates the failing field.
func find(ctx context.Context, t *testing.T, src string) (yamlfield.Field, bool, error) {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("parse: %v", err)
	}
	var raw any
	if err := node.Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	js, _ := json.Marshal(raw)
	var d doc
	var typeErr *json.UnmarshalTypeError
	if err := json.Unmarshal(js, &d); !errors.As(err, &typeErr) {
		t.Fatalf("Unmarshal() error = %v, want a *json.UnmarshalTypeError", err)
	}
	return yamlfield.Find(ctx, &node, &d, typeErr)
}

func TestFind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, src string
		wantPath  string
		wantLine  int
	}{
		{"struct field", "meta:\n  Title: [a]\n", "meta.Title", 2},
		{"list item", "items:\n  - size: 1\n  - size: x\n", "items[1].size", 3},
		{"map entry in key order", "named:\n  b:\n    size: x\n  a:\n    size: true\n", "named[a].size", 5},
		{"alias", "base: &b\n  size: x\nitems:\n  - *b\n", "items[0].size", 4},
		{"merge", "base: &b\n  size: x\nitems:\n  - <<: *b\n    name: n\n", "items[0].size", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok, err := find(context.Background(), t, tt.src)
			if err != nil || !ok {
				t.Fatalf("Find(%q) = %v, %v, want a field", tt.src, ok, err)
			}
			if got.Path != tt.wantPath || got.Node.Line != tt.wantLine {
				t.Errorf("Find(%q) = %s on line %d, want %s on line %d", tt.src, got.Path, got.Node.Line, tt.wantPath, tt.wantLine)
			}
		})
	}
}

func TestFindCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ok, err := find(ctx, t, "items:\n  - size: x\n")
	if ok || !errors.Is(err, context.Canceled) {
		t.Errorf("Find(cancelled) = %v, %v, want context.Canceled", ok, err)
	}
}

func TestFindManyAliases(t *testing.T) {
	t.Parallel()
	// Every item stands for the same node, so a search that does not
	// remember where it has been would visit it items times.
	var b strings.Builder
	b.WriteString("base: &b\n  name: n\nitems:\n")
	for range 5000 {
		b.WriteString("  - *b\n")
	}
	b.WriteString("  - size: x\n")
	got, ok, err := find(context.Background(), t, b.String())
	if err != nil || !ok || got.Path != "items[5000].size" {
		t.Errorf("Find(aliases) = %v, %v, %v, want items[5000].size", got.Path, ok, err)
	}
}

func TestTreeRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		// pick returns the node to remove from the mapping or list at the
		// document's top level.
		pick func(top *yaml.Node) *yaml.Node
		src  string
		want string
	}{
		{
			name: "key",
			src:  "a: 1\nb: 2\n",
			pick: func(top *yaml.Node) *yaml.Node { return top.Content[0] },
			want: "b: 2\n",
		},
		{
			name: "list item",
			src:  "- 1\n- 2\n",
			pick: func(top *yaml.Node) *yaml.Node { return top.Content[1] },
			want: "- 1\n",
		},
		{
			name: "alias that is an entry's value",
			src:  "x: &v 1\na: *v\nb: 2\n",
			pick: func(top *yaml.Node) *yaml.Node { return top.Content[3] },
			want: "x: &v 1\nb: 2\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(tt.src), &doc); err != nil {
				t.Fatalf("Unmarshal(%q) = %v", tt.src, err)
			}
			tree := yamlfield.Copy(&doc)
			top := tree.Root.Content[0]
			f := yamlfield.Field{Node: tt.pick(top)}
			if !tree.Remove(f) {
				t.Fatalf("Remove(%q) = false, want true", tt.name)
			}
			if top.Kind == yaml.MappingNode && len(top.Content)%2 != 0 {
				t.Errorf("Remove(%q) left %d nodes in the mapping, want an even number", tt.name, len(top.Content))
			}
			got, err := yaml.Marshal(top)
			if err != nil {
				t.Fatalf("Marshal = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("Remove(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
