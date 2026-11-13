package yamlfield_test

import (
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/internal/yamlfield"
)

func TestYAML11Bool(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in        string
		want, yes bool
	}{
		{"y", true, true},
		{"Y", true, true},
		{"yes", true, true},
		{"Yes", true, true},
		{"YES", true, true},
		{"on", true, true},
		{"On", true, true},
		{"ON", true, true},
		{"n", false, true},
		{"N", false, true},
		{"no", false, true},
		{"No", false, true},
		{"NO", false, true},
		{"off", false, true},
		{"Off", false, true},
		{"OFF", false, true},
		// go-yaml v2 takes no other case, and no other word.
		{"yEs", false, false},
		{"nO", false, false},
		{"oFF", false, false},
		{"yess", false, false},
		{"true", false, false},
		{"", false, false},
		{"t", false, false},
	} {
		got, ok := yamlfield.YAML11Bool(tt.in)
		if got != tt.want || ok != tt.yes {
			t.Errorf("YAML11Bool(%q) = %v, %v, want %v, %v", tt.in, got, ok, tt.want, tt.yes)
		}
	}
}

// TestDecodeAsKubectl is the test of #27 at the lowest tier: Decode reads a
// plain y, n, yes, no, on or off as the boolean kubectl sends, and nothing
// else differently from yaml.Node.Decode.
func TestDecodeAsKubectl(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, src string
		want      any
	}{
		{"plain", "a: n\nb: Yes\nc: [on, OFF]\n", map[string]any{"a": false, "b": true, "c": []any{true, false}}},
		{"quoted", "a: \"n\"\nb: 'yes'\nc: !!str on\n", map[string]any{"a": "n", "b": "yes", "c": "on"}},
		{"block scalar", "a: |\n  n\n", map[string]any{"a": "n\n"}},
		{"other words", "a: nope\nb: yesterday\nc: nO\nd: true\ne: 5\nf: ~\n", map[string]any{"a": "nope", "b": "yesterday", "c": "nO", "d": true, "e": 5, "f": nil}},
		{"keys as written", "n: n\non: y\n", map[string]any{"n": false, "on": true}},
		{"through an alias", "a: &x n\nb: *x\nc: &m {d: no}\ne: *m\n", map[string]any{"a": false, "b": false, "c": map[string]any{"d": false}, "e": map[string]any{"d": false}}},
		{"through a merge key", "base: &m {d: no}\ne:\n  <<: *m\n  f: g\n", map[string]any{"base": map[string]any{"d": false}, "e": map[string]any{"d": false, "f": "g"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var node yaml.Node
			if err := yaml.Unmarshal([]byte(tt.src), &node); err != nil {
				t.Fatal(err)
			}
			var got any
			if err := yamlfield.Decode(&node, &got); err != nil {
				t.Fatalf("Decode(%q) error = %v", tt.src, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Decode(%q) = %#v, want %#v", tt.src, got, tt.want)
			}
			var again yaml.Node
			if err := yaml.Unmarshal([]byte(tt.src), &again); err != nil || !reflect.DeepEqual(node.Content[0].Content[1].Value, again.Content[0].Content[1].Value) {
				t.Errorf("Decode(%q) changed the node", tt.src)
			}
		})
	}
}

// TestFindPlainBool checks that Find points at a plain n where a string is
// wanted, and says why.
func TestFindPlainBool(t *testing.T) {
	t.Parallel()
	got, ok, err := find(t.Context(), t, "items:\n  - name: n\n")
	if err != nil || !ok {
		t.Fatalf("Find() = %v, %v, want the field", ok, err)
	}
	if got.Path != "items[0].name" || got.Value.Line != 2 || got.Expected != "a string" || got.Got != "a boolean: kubectl reads an unquoted n as one (YAML 1.1), so quote it if a string is meant" {
		t.Errorf("Find() = %s on line %d, want a string, got %q", got.Path, got.Value.Line, got.Got)
	}
}
