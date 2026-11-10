package assume_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
)

// schemaOf decodes a schema written as JSON.
func schemaOf(t *testing.T, js string) map[string]any {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal([]byte(js), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

const root = `{"type": "object", "properties": {
	"spec": {"type": "object", "properties": {
		"config": {"x-kubernetes-preserve-unknown-fields": true, "description": "free", "x-kubernetes-validations": [{"rule": "true"}]},
		"size": {"x-kubernetes-int-or-string": true},
		"open": {"type": "object", "x-kubernetes-preserve-unknown-fields": true},
		"name": {"type": "string"},
		"ports": {"type": "array", "items": {"type": "object", "properties": {"extra": {"x-kubernetes-preserve-unknown-fields": true}}}},
		"labels": {"type": "object", "additionalProperties": {"x-kubernetes-preserve-unknown-fields": true}},
		"bare": {"type": "array"}
	}}
}}`

func TestAt(t *testing.T) {
	t.Parallel()
	typ := map[string]any{"type": "object", "properties": map[string]any{"replicas": map[string]any{"type": "integer"}}}
	for _, tc := range []struct {
		name string
		mode assume.Mode
		path []string
		// typ is the type to place, as JSON; empty for an object with a field replicas.
		typ string
		// want is the value at path after placing, as JSON; empty when an error is wanted.
		want    string
		reason  assume.Reason
		message string
	}{
		{
			name: "preserve-unknown-fields without a type", path: []string{"spec", "config"},
			want: `{"type": "object", "properties": {"replicas": {"type": "integer"}}, "description": "free", "x-kubernetes-validations": [{"rule": "true"}]}`,
		},
		{name: "int-or-string", path: []string{"spec", "size"}, want: `{"type": "object", "properties": {"replicas": {"type": "integer"}}}`},
		{name: "open object", path: []string{"spec", "open"}, want: `{"type": "object", "properties": {"replicas": {"type": "integer"}}}`},
		{name: "list items", path: []string{"spec", "ports", "[]", "extra"}, want: `{"type": "object", "properties": {"replicas": {"type": "integer"}}}`},
		{name: "map values", path: []string{"spec", "labels", "[*]"}, want: `{"type": "object", "properties": {"replicas": {"type": "integer"}}}`},
		{name: "typed", path: []string{"spec", "name"}, reason: assume.Typed, message: "self.spec.name is already typed as string: remove this entry"},
		{name: "typed root", path: nil, reason: assume.Typed, message: "self is already typed as object: remove this entry"},
		{name: "no field", path: []string{"spec", "nosuch"}, reason: assume.NoField, message: "self.spec has no field nosuch"},
		{name: "field of a map", path: []string{"spec", "labels", "x"}, reason: assume.NoField, message: "self.spec.labels has no field x: it is a map, write [*] for its values"},
		{name: "not a list", path: []string{"spec", "name", "[]"}, reason: assume.NotList, message: "self.spec.name is string, not a list: [] steps into a list's items"},
		{name: "not a map", path: []string{"spec", "ports", "[*]"}, reason: assume.NotMap, message: "self.spec.ports is list(object), not a map: [*] steps into a map's values"},
		{name: "below dyn", path: []string{"spec", "config", "replicas"}, reason: assume.BelowDyn, message: "self.spec.config is dyn, so nothing below it is known: write the entry for self.spec.config"},
		{name: "list without items", path: []string{"spec", "bare", "[]"}, reason: assume.BelowDyn},
		// A CRD's rules see only x-kubernetes-int-or-string as dyn (#2), an
		// int or a string (#4).
		{name: "rules: int-or-string", mode: assume.Rules, path: []string{"spec", "size"}, typ: `{"type": "integer"}`, want: `{"type": "integer"}`},
		{name: "rules: int-or-string as uint", mode: assume.Rules, path: []string{"spec", "size"}, typ: `{"type": "integer", "minimum": 0}`, want: `{"type": "integer", "minimum": 0}`},
		{name: "rules: int-or-string as string", mode: assume.Rules, path: []string{"spec", "size"}, typ: `{"type": "string"}`, want: `{"type": "string"}`},
		{
			name: "rules: int-or-string as an object", mode: assume.Rules, path: []string{"spec", "size"}, reason: assume.NotIntOrString,
			message: "self.spec.size is x-kubernetes-int-or-string, an int or a string to a rule: give it int or string",
		},
		{name: "rules: int-or-string as a timestamp", mode: assume.Rules, path: []string{"spec", "size"}, typ: `{"type": "string", "format": "date-time"}`, reason: assume.NotIntOrString},
		{name: "rules: int-or-string as a list", mode: assume.Rules, path: []string{"spec", "size"}, typ: `{"type": "array", "items": {"type": "string"}}`, reason: assume.NotIntOrString},
		{
			name: "rules: preserve-unknown-fields without a type", mode: assume.Rules, path: []string{"spec", "config"}, reason: assume.Unreadable,
			message: "the API server lets no rule read self.spec.config, which keeps unknown fields: give it a schema in the CustomResourceDefinition instead of this entry",
		},
		{name: "rules: open object", mode: assume.Rules, path: []string{"spec", "open"}, reason: assume.Unreadable},
		{name: "rules: below an open value", mode: assume.Rules, path: []string{"spec", "ports", "[]", "extra", "x"}, reason: assume.Unreadable},
		{name: "rules: typed", mode: assume.Rules, path: []string{"spec", "name"}, reason: assume.Typed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := schemaOf(t, root)
			before := assume.Copy(in)
			placed := typ
			if tc.typ != "" {
				placed = schemaOf(t, tc.typ)
			}
			got, err := assume.At(in, "self", tc.path, placed, tc.mode)
			if !reflect.DeepEqual(in, before) {
				t.Errorf("At(%v) changed its input", tc.path)
			}
			if tc.want == "" {
				var ae *assume.Error
				if !errors.As(err, &ae) || ae.Reason != tc.reason {
					t.Fatalf("At(%v) error = %v, want reason %d", tc.path, err, tc.reason)
				}
				if tc.message != "" && err.Error() != tc.message {
					t.Errorf("At(%v) error = %q, want %q", tc.path, err, tc.message)
				}
				return
			}
			if err != nil {
				t.Fatalf("At(%v) error = %v", tc.path, err)
			}
			node := any(got)
			for _, step := range tc.path {
				m, _ := node.(map[string]any)
				switch step {
				case assume.Items:
					node = m["items"]
				case assume.Values:
					node = m["additionalProperties"]
				default:
					props, _ := m["properties"].(map[string]any)
					node = props[step]
				}
			}
			if want := schemaOf(t, tc.want); !reflect.DeepEqual(node, want) {
				t.Errorf("At(%v) placed %v, want %v", tc.path, node, want)
			}
		})
	}
}

func TestIsDynAndTypeName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		schema string
		dyn    bool
		name   string
	}{
		{`{}`, true, "dyn"},
		{`{"x-kubernetes-preserve-unknown-fields": true}`, true, "dyn"},
		{`{"x-kubernetes-int-or-string": true}`, true, "dyn"},
		{`{"type": "object", "x-kubernetes-preserve-unknown-fields": true}`, true, "dyn"},
		{`{"type": "object", "x-kubernetes-preserve-unknown-fields": true, "properties": {"a": {"type": "string"}}}`, false, "object"},
		{`{"type": "object"}`, false, "object"},
		{`{"type": "string", "format": "date-time"}`, false, "timestamp"},
		{`{"type": "string", "format": "byte"}`, false, "bytes"},
		{`{"type": "string", "format": "duration"}`, false, "duration"},
		{`{"type": "number"}`, false, "double"},
		{`{"type": "boolean"}`, false, "bool"},
		{`{"type": "array", "items": {"type": "integer"}}`, false, "list(int)"},
		{`{"type": "object", "additionalProperties": {"type": "string"}}`, false, "map(string, string)"},
	} {
		s := schemaOf(t, tc.schema)
		if got := assume.IsDyn(s); got != tc.dyn {
			t.Errorf("IsDyn(%s) = %t, want %t", tc.schema, got, tc.dyn)
		}
		if got := assume.TypeName(s); got != tc.name {
			t.Errorf("TypeName(%s) = %q, want %q", tc.schema, got, tc.name)
		}
	}
}

func TestName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		root string
		path []string
		want string
	}{
		{"", nil, "the root"},
		{"params", nil, "params"},
		{"", []string{"spec", "ports", "[]", "config"}, "spec.ports[].config"},
		{"variables.cfg", []string{"[*]"}, "variables.cfg[*]"},
	} {
		if got := assume.Name(tc.root, tc.path); got != tc.want {
			t.Errorf("Name(%q, %v) = %q, want %q", tc.root, tc.path, got, tc.want)
		}
	}
}

// TestTypeText checks that a schema's type is written as a .cellens type
// writes it, for the inline line and hover of a schema: or go: entry (#15).
func TestTypeText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, schema, want string }{
		{"a struct in name order", `{"type": "object", "properties": {"team": {"type": "string"}, "max": {"type": "integer"}}}`, "{max: int, team: string}"},
		{"lists and maps", `{"type": "object", "properties": {"ports": {"type": "array", "items": {"type": "integer"}}, "labels": {"type": "object", "additionalProperties": {"type": "string"}}}}`, "{labels: map(string, string), ports: list(int)}"},
		{"formats", `{"type": "string", "format": "date-time"}`, "timestamp"},
		{"int-or-string is dyn", `{"type": "object", "properties": {"surge": {"x-kubernetes-int-or-string": true, "anyOf": [{"type": "integer"}, {"type": "string"}]}}}`, "{surge: dyn}"},
		{"an empty object", `{"type": "object"}`, "{}"},
		{"deep objects", `{"type": "object", "properties": {"a": {"type": "object", "properties": {"b": {"type": "object", "properties": {"c": {"type": "object", "properties": {"d": {"type": "string"}}}}}}}}}`, "{a: {b: {c: object}}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := assume.TypeText(schemaOf(t, tc.schema)); got != tc.want {
				t.Errorf("TypeText(%s) = %q, want %q", tc.schema, got, tc.want)
			}
		})
	}
}

// TestDynPaths checks the dyn values a .cellens entry can type below a
// schema: in a policy every value without a type, in a CRD's rules only the
// x-kubernetes-int-or-string ones, never below a dyn value (#16).
func TestDynPaths(t *testing.T) {
	t.Parallel()
	s := schemaOf(t, root)
	for _, tc := range []struct {
		mode  assume.Mode
		limit int
		want  [][]string
	}{
		{assume.Policies, 10, [][]string{
			{"spec", "config"}, {"spec", "labels", assume.Values}, {"spec", "open"}, {"spec", "ports", assume.Items, "extra"}, {"spec", "size"},
		}},
		{assume.Rules, 10, [][]string{{"spec", "size"}}},
		{assume.Policies, 2, [][]string{{"spec", "config"}, {"spec", "labels", assume.Values}}},
	} {
		if got := assume.DynPaths(s, tc.mode, tc.limit); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("DynPaths(root, %v, %d) = %q, want %q", tc.mode, tc.limit, got, tc.want)
		}
	}
	if got := assume.DynPaths(map[string]any{}, assume.Policies, 10); len(got) != 1 || len(got[0]) != 0 {
		t.Errorf("DynPaths of a dyn root = %q, want the root", got)
	}
}

func TestChildrenAndBelow(t *testing.T) {
	t.Parallel()
	s := schemaOf(t, root)
	spec, ok := assume.Below(s, []string{"spec"})
	if !ok {
		t.Fatal("Below(root, spec) = false")
	}
	var steps []string
	for _, c := range assume.Children(spec) {
		steps = append(steps, c.Step)
	}
	if want := []string{"bare", "config", "labels", "name", "open", "ports", "size"}; !reflect.DeepEqual(steps, want) {
		t.Errorf("Children(spec) = %q, want %q", steps, want)
	}
	if items, ok := assume.Below(s, []string{"spec", "ports", assume.Items, "extra"}); !ok || !assume.IsDyn(items) {
		t.Errorf("Below(root, spec.ports[].extra) = %v, %t, want the dyn value", items, ok)
	}
	for _, path := range [][]string{{"spec", "nosuch"}, {"spec", "config", "x"}, {"spec", "name", assume.Items}} {
		if _, ok := assume.Below(s, path); ok {
			t.Errorf("Below(root, %q) = true, want false", path)
		}
	}
}
