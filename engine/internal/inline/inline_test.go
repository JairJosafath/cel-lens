package inline_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/inline"
)

// part is a Part without its arguments, which tests check apart.
type part struct{ title, command string }

func titles(parts []inline.Part) []part {
	out := make([]part, len(parts))
	for i, p := range parts {
		out[i] = part{p.Title, p.Command}
	}
	return out
}

func dynValue(name string) inline.Value {
	return inline.Value{Name: name, Target: inline.Target{Value: name}}
}

func typedValue(name, typ string) inline.Value {
	return inline.Value{Name: name, Type: typ, Entry: true, Target: inline.Target{Value: name, Type: typ}}
}

// show is the command of a line's first part.
const show = inline.ShowRule

// TestLine checks the wording of the line above an expression, the UI of
// inline info (#3): the subject and whether it compiles, then each dyn value
// with the command that types it, within 80 characters.
func TestLine(t *testing.T) {
	t.Parallel()
	const widget = "self: Widget.spec · compiles"
	tests := []struct {
		name string
		rule inline.Rule
		want []part
	}{
		{
			name: "compiles",
			rule: inline.Rule{Subject: "self", SubjectType: "Widget.spec"},
			want: []part{{widget, show}},
		},
		{
			name: "one problem",
			rule: inline.Rule{Subject: "self", SubjectType: "Widget", Problems: 1},
			want: []part{{"self: Widget · 1 problem", show}},
		},
		{
			name: "problems",
			rule: inline.Rule{Subject: "object", SubjectType: "apps/v1 Deployment", Problems: 3},
			want: []part{{"object: apps/v1 Deployment · 3 problems", show}},
		},
		{
			name: "dyn value",
			rule: inline.Rule{Subject: "self", SubjectType: "Widget.spec", Values: []inline.Value{dynValue("self.spec.config")}},
			want: []part{{widget, show}, {"self.spec.config: dyn · Give it a type", inline.GiveType}},
		},
		{
			name: "value typed from .cellens",
			rule: inline.Rule{Subject: "object", SubjectType: "apps/v1 Deployment", Values: []inline.Value{typedValue("params", "{name: string, replicas: int}")}},
			want: []part{{"object: apps/v1 Deployment · compiles", show}, {"params: {name, replicas} · Edit", inline.EditType}, {"Remove", inline.RemoveType}},
		},
		{
			name: "kind from .cellens",
			rule: inline.Rule{Subject: "object", SubjectType: "dyn", Values: []inline.Value{typedValue("params", "v1 ConfigMap")}},
			want: []part{{"object: dyn · compiles", show}, {"params: v1 ConfigMap · Edit", inline.EditType}, {"Remove", inline.RemoveType}},
		},
		{
			name: "list type",
			rule: inline.Rule{Subject: "self", SubjectType: "W", Values: []inline.Value{typedValue("self.ports", "list({port: int, protocol: string})")}},
			want: []part{{"self: W · compiles", show}, {"self.ports: list({port, protocol}) · Edit", inline.EditType}, {"Remove", inline.RemoveType}},
		},
		{
			name: "many fields cut past 40 characters",
			rule: inline.Rule{Subject: "self", SubjectType: "W", Values: []inline.Value{typedValue("self", "{name: string, replicas: int, labels: map(string, string), selector: {app: string}, annotations: map(string, string)}")}},
			want: []part{{"self: W · compiles", show}, {"self: {name, replicas, labels, selector, …} · Edit", inline.EditType}, {"Remove", inline.RemoveType}},
		},
		{
			name: "two values that fit",
			rule: inline.Rule{Subject: "self", SubjectType: "W", Values: []inline.Value{dynValue("self.a"), dynValue("self.b")}},
			want: []part{{"self: W · compiles", show}, {"self.a: dyn · Give it a type", inline.GiveType}, {"self.b: dyn · Give it a type", inline.GiveType}},
		},
		{
			name: "more than two values",
			rule: inline.Rule{Subject: "self", SubjectType: "Widget.spec", Values: []inline.Value{dynValue("self.a"), dynValue("self.b"), dynValue("self.c"), dynValue("self.d")}},
			want: []part{{widget, show}, {"self.a: dyn · Give it a type", inline.GiveType}, {"+3 more", inline.ListValues}},
		},
		{
			name: "a second value that does not fit",
			rule: inline.Rule{Subject: "self", SubjectType: "Widget.spec", Values: []inline.Value{dynValue("self.spec.config"), dynValue("self.spec.port")}},
			want: []part{{widget, show}, {"self.spec.config: dyn · Give it a type", inline.GiveType}, {"+1 more", inline.ListValues}},
		},
		{
			name: "long name cut in the middle",
			rule: inline.Rule{Subject: "object", SubjectType: "apps/v1 Deployment", Values: []inline.Value{dynValue("object.spec.template.spec.containers[].env[].valueFrom")}},
			want: []part{{"object: apps/v1 Deployment · compiles", show}, {"object.…valueFrom: dyn · Give it a type", inline.GiveType}},
		},
		{
			name: "a variable is never cut",
			rule: inline.Rule{Subject: "object", SubjectType: strings.Repeat("K", 40), Problems: 12, Values: []inline.Value{{Name: "variables.max", Target: inline.Target{Variable: "variables.max"}}}},
			want: []part{{"object: " + strings.Repeat("K", 40) + " · 12 problems", show}, {"+1 more", inline.ListValues}},
		},
		{
			name: "an entry that does not apply",
			rule: inline.Rule{Subject: "object", SubjectType: "dyn", Values: []inline.Value{{Name: "params", Entry: true, Target: inline.Target{Variable: "params", Kind: "example.com/v1 Missing"}}}},
			want: []part{{"object: dyn · compiles", show}, {"params: dyn · Edit", inline.EditType}, {"Remove", inline.RemoveType}},
		},
		{
			name: "long type cut to fit",
			rule: inline.Rule{Subject: "self", SubjectType: "Widget.spec.template", Values: []inline.Value{typedValue("self.spec.config", "{name: string, replicas: int, labels: map(string, string), annotations: map(string, string)}")}},
			want: []part{{"self: Widget.spec.template · compiles", show}, {"self.…config: {name, …} · Edit", inline.EditType}, {"Remove", inline.RemoveType}},
		},
		{
			// #15: a schema or Go type is written as a type: entry writes one,
			// with dyn where the schema leaves one, and shown the same way.
			name: "schema with a dyn field",
			rule: inline.Rule{Subject: "object", SubjectType: "dyn", Values: []inline.Value{{Name: "params", Type: "{maxReplicas: int, surge: dyn, team: string}", Entry: true, Target: inline.Target{Variable: "params", Go: "./api/v1#QuotaSpec"}}}},
			want: []part{{"object: dyn · compiles", show}, {"params: {maxReplicas, surge, team} · Edit", inline.EditType}, {"Remove", inline.RemoveType}},
		},
		{
			name: "schema with a dyn field, cut to fit",
			rule: inline.Rule{Subject: "self", SubjectType: "Widget.spec.template", Values: []inline.Value{{Name: "self.spec.config", Type: "{name: dyn, replicas: {a: dyn}, labels: map(string, dyn), annotations: map(string, string)}", Entry: true}}},
			want: []part{{"self: Widget.spec.template · compiles", show}, {"self.…config: {name, …} · Edit", inline.EditType}, {"Remove", inline.RemoveType}},
		},
		{
			name: "scalar schema",
			rule: inline.Rule{Subject: "self", SubjectType: "W", Values: []inline.Value{{Name: "self.port", Type: "int", Entry: true}}},
			want: []part{{"self: W · compiles", show}, {"self.port: int · Edit", inline.EditType}, {"Remove", inline.RemoveType}},
		},
		{
			name: "long subject type",
			rule: inline.Rule{Subject: "object", SubjectType: "apps/v1 Deployment, apps/v1 StatefulSet, apps/v1 DaemonSet", Problems: 2},
			want: []part{{"object: apps/v1 Deployment, apps/v1 StatefulSet… · 2 problems", show}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := inline.Line(tc.rule)
			if !reflect.DeepEqual(titles(got), tc.want) {
				t.Errorf("Line(%+v) = %+v, want %+v", tc.rule, titles(got), tc.want)
			}
			if w := inline.Width(got); w > inline.MaxWidth {
				t.Errorf("Width(Line(%+v)) = %d, want at most %d", tc.rule, w, inline.MaxWidth)
			}
		})
	}
}

// TestLineArguments checks that the first part shows the rule, at its
// location, that each value's command carries the value's target, and that
// +N more lists the values left out, each with its commands (#3).
func TestLineArguments(t *testing.T) {
	t.Parallel()
	a := dynValue("self.spec.a")
	b := typedValue("self.spec.b", "{x: int}")
	c := dynValue("self.spec.c")
	where := map[string]any{"uri": "file:///crd.yaml"}
	got := inline.Line(inline.Rule{Subject: "self", SubjectType: "Widget", Location: where, Values: []inline.Value{a, b, c}})
	if len(got) != 3 {
		t.Fatalf("Line() = %+v, want the summary, one value and +2 more", titles(got))
	}
	if got[0].Command != inline.ShowRule || !reflect.DeepEqual(got[0].Arguments, []any{where}) {
		t.Errorf("Line()[0] = %+v, want %s with the rule's location", got[0], inline.ShowRule)
	}
	if args := got[1].Arguments; !reflect.DeepEqual(args, []any{a.Target}) {
		t.Errorf("Line()[1].Arguments = %+v, want the target of self.spec.a", args)
	}
	want := []inline.Part{
		{Title: "self.spec.b: {x} · Edit", Command: inline.EditType, Arguments: []any{b.Target}},
		{Title: "self.spec.b: {x} · Remove", Command: inline.RemoveType, Arguments: []any{b.Target}},
		{Title: "self.spec.c: dyn · Give it a type", Command: inline.GiveType, Arguments: []any{c.Target}},
	}
	var items []inline.Part
	for _, arg := range got[2].Arguments {
		p, ok := arg.(inline.Part)
		if !ok {
			t.Fatalf("+2 more argument %#v is no Part", arg)
		}
		items = append(items, p)
	}
	if got[2].Title != "+2 more" || got[2].Command != inline.ListValues || !reflect.DeepEqual(items, want) {
		t.Errorf("Line()[2] = %q %q %+v, want +2 more listing %+v", got[2].Title, got[2].Command, items, want)
	}
}

// TestLineWidth checks that no line passes 80 characters and that every value
// is either shown or counted in +N more, whatever the lengths (#3).
func TestLineWidth(t *testing.T) {
	t.Parallel()
	for n := range 5 {
		for length := 1; length <= 120; length += 7 {
			name := "self." + strings.Repeat("a", length)
			var values []inline.Value
			for i := range n {
				if i%2 == 0 {
					values = append(values, dynValue(name+strconv.Itoa(i)))
				} else {
					values = append(values, typedValue(name+".b.c"+strconv.Itoa(i), "{"+strings.Repeat("f", length)+": int, g: string}"))
				}
			}
			rule := inline.Rule{Subject: "object", SubjectType: strings.Repeat("K", length), Problems: length, Values: values}
			parts := inline.Line(rule)
			if w := inline.Width(parts); w > inline.MaxWidth {
				t.Errorf("Width(Line(%d values, length %d)) = %d, want at most %d: %+v", n, length, w, inline.MaxWidth, titles(parts))
			}
			shown := 0
			for _, p := range parts {
				switch p.Command {
				case inline.GiveType, inline.EditType:
					shown++
				case inline.ListValues:
					rest, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(p.Title, "+"), " more"))
					if err != nil {
						t.Fatalf("+N more title = %q", p.Title)
					}
					shown += rest
				}
			}
			if shown != n {
				t.Errorf("Line(%d values, length %d) shows or counts %d values, want %d: %+v", n, length, shown, n, titles(parts))
			}
		}
	}
}
