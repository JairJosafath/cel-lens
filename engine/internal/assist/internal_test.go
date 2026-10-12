package assist

import (
	"strings"
	"testing"

	"github.com/google/cel-go/common/types"
)

func fakeAssistant() *Assistant {
	return &Assistant{funcs: []function{
		{name: "sets.ok", global: []overload{{sig: "sets.ok(list) -> bool"}}},
		{name: "sets._x_", global: []overload{{sig: "sets._x_(list) -> bool"}}},
		{name: "sets.in", global: []overload{{sig: "sets.in(list) -> bool"}}},
		{name: "fine", members: []member{{recv: types.StringKind, overload: overload{sig: "string.fine() -> bool"}}}},
		{name: "_y_", members: []member{{recv: types.StringKind, overload: overload{sig: "string._y_() -> bool"}}}},
		{name: "@z", members: []member{{recv: types.StringKind, overload: overload{sig: "string.@z() -> bool"}}}},
	}}
}

func labels(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}

func TestNamespacedHidesInternalNames(t *testing.T) {
	t.Parallel()
	got := labels(fakeAssistant().namespaced("sets"))
	if len(got) != 1 || got[0] != "ok" {
		t.Errorf("namespaced(sets) = %v, want [ok]", got)
	}
}

func TestMembersHidesInternalNames(t *testing.T) {
	t.Parallel()
	got := labels(fakeAssistant().members(nil, false, ""))
	for _, l := range got {
		if internal(l) {
			t.Errorf("members(nil) offers internal name %q", l)
		}
	}
	found := false
	for _, l := range got {
		found = found || l == "fine"
	}
	if !found {
		t.Errorf("members(nil) = %v, want it to offer fine", got)
	}
}

func TestChainBeforeGroups(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		line string
		want string
		ok   bool
	}{
		{name: "optional field in group", line: "(self.?name).", want: "self.?name", ok: true},
		{name: "group with spaces", line: "( self.spec ).", want: "self.spec", ok: true},
		{name: "group then field", line: "(self.spec).name.", want: "self.spec.name", ok: true},
		{name: "membership test", line: "('a' in self.ports).", ok: false},
		{name: "comparison", line: "(self.a == self.spec).", ok: false},
		{name: "paren in string", line: "(')' + self.name).", ok: false},
		{name: "call", line: "f(self.name).", ok: false},
		{name: "group after in", line: "'a' in (self.spec).", want: "self.spec", ok: true},
		{name: "group after in without space", line: "'a' in(self.spec).", want: "self.spec", ok: true},
		{name: "call named like in", line: "xin(self.spec).", ok: false},
		{name: "too deep", line: strings.Repeat("(", 10000) + "self.name" + strings.Repeat(")", 10000) + ".", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			line := []rune(tt.line)
			chain, ok := chainBefore(line, len(line)-1)
			var parts []string
			for _, st := range chain {
				n := st.name
				if st.optional {
					n = "?" + n
				}
				parts = append(parts, n)
			}
			got := strings.Join(parts, ".")
			if ok != tt.ok || (ok && got != tt.want) {
				t.Errorf("chainBefore(%.30q) = %q, %v, want %q, %v", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestMacroAccepts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		recv     receivers
		typ      string
		optional bool
		want     bool
	}{
		{"list macro on list", onLists, "list(int)", false, true},
		{"list macro on map", onLists, "map(string, int)", false, false},
		{"list macro on dyn", onLists, "dyn", false, true},
		{"list macro on optional", onLists, "optional_type(int)", true, false},
		{"list and map macro on map", onLists | onMaps, "map(string, int)", false, true},
		{"optional macro on optional", onOptionals, "optional_type(int)", true, true},
		{"optional macro on dyn", onOptionals, "dyn", false, true},
		{"optional macro on list", onOptionals, "list(int)", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (macro{recv: tt.recv}).accepts(tt.typ, tt.optional); got != tt.want {
				t.Errorf("accepts(%q, %v) = %v, want %v", tt.typ, tt.optional, got, tt.want)
			}
		})
	}
}
