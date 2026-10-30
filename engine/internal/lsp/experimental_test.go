package lsp_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const kyvernoPolicy = "apiVersion: policies.kyverno.io/v1\nkind: ValidatingPolicy\nspec:\n  validations:\n    - expression: nope\n"

func TestExperimentalListedInInitializeResult(t *testing.T) {
	t.Parallel()
	c := start(t)
	res, rerr := c.call("initialize", map[string]any{"processId": nil, "capabilities": map[string]any{}, "rootUri": nil})
	if rerr != nil {
		t.Fatalf("initialize error = %+v", rerr)
	}
	var r struct {
		Capabilities struct {
			Experimental struct {
				CelLens struct{ ExperimentalFeatures []string }
			}
		}
	}
	if err := json.Unmarshal(res, &r); err != nil {
		t.Fatal(err)
	}
	got := r.Capabilities.Experimental.CelLens.ExperimentalFeatures
	for _, want := range []string{"kyverno", "crd-typed-policies", "fitting-completion"} {
		if !slices.Contains(got, want) {
			t.Errorf("experimentalFeatures = %v, want it to list %s", got, want)
		}
	}
	if len(got) != 3 {
		t.Errorf("experimentalFeatures = %v, want three names", got)
	}
}

func TestExperimentalInitializationOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		options any
		want    int
	}{
		{"absent", nil, 0},
		{"empty list", map[string]any{"experimental": []string{}}, 0},
		{"other feature", map[string]any{"experimental": []string{"fitting-completion"}}, 0},
		{"kyverno", map[string]any{"experimental": []string{"kyverno"}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := start(t)
			initializeWith(t, c, tt.options)
			c.open(kyvernoPolicy)
			if _, diags := c.diagnostics(); len(diags) != tt.want {
				t.Errorf("diagnostics = %+v, want %d", diags, tt.want)
			}
		})
	}
}

func TestExperimentalDidChangeConfiguration(t *testing.T) {
	t.Parallel()
	c := start(t)
	initializeWith(t, c, nil)
	c.open(kyvernoPolicy)
	if _, diags := c.diagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics with kyverno off = %+v, want none", diags)
	}
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": []string{"kyverno"}}})
	if _, diags := c.diagnostics(); len(diags) != 1 || !strings.Contains(diags[0].Message, "undeclared reference to 'nope'") {
		t.Fatalf("diagnostics after turning kyverno on = %+v, want one about nope", diags)
	}
	c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": []string{}}})
	if _, diags := c.diagnostics(); len(diags) != 0 {
		t.Errorf("diagnostics after turning kyverno off = %+v, want none", diags)
	}
}

func TestExperimentalKyvernoOffHasNoAssist(t *testing.T) {
	t.Parallel()
	c := start(t)
	initializeWith(t, c, nil)
	policy := "apiVersion: policies.kyverno.io/v1\nkind: ValidatingPolicy\nspec:\n  validations:\n    - expression: request.object\n"
	c.open(policy)
	c.diagnostics()
	if labels := c.completeLabels(policy, "request.object", len("req")); len(labels) != 0 {
		t.Errorf("completion = %v, want none with kyverno off", labels)
	}
	if got := c.hoverText(policy, "request.object", 2); got != "" {
		t.Errorf("hover = %q, want none with kyverno off", got)
	}
}

func TestExperimentalUnknownName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		send func(c *client)
	}{
		{"initializationOptions", func(c *client) { initializeWith(t, c, map[string]any{"experimental": []string{"nope", "nope"}}) }},
		{"didChangeConfiguration", func(c *client) {
			initializeWith(t, c, nil)
			c.notify("workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{"experimental": []string{"nope"}}})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := start(t)
			tt.send(c)
			for {
				m := c.read()
				if m.Method == "window/showMessage" {
					t.Fatalf("window/showMessage %s, want the warning only in window/logMessage", m.Params)
				}
				if m.Method != "window/logMessage" {
					continue
				}
				var p struct {
					Type    int
					Message string
				}
				if err := json.Unmarshal(m.Params, &p); err != nil {
					t.Fatal(err)
				}
				if p.Type != 2 || !strings.Contains(p.Message, `"nope"`) || !strings.Contains(p.Message, "crd-typed-policies, fitting-completion, kyverno") {
					t.Errorf("logMessage = %+v, want a warning naming nope and the known names", p)
				}
				break
			}
			// Nothing else was switched on, and the server still answers.
			c.open(kyvernoPolicy)
			if _, diags := c.diagnostics(); len(diags) != 0 {
				t.Errorf("diagnostics = %+v, want none: the unknown name is ignored", diags)
			}
		})
	}
}

func TestExperimentalWarningCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		list []string
		want int
	}{
		{"two unknown names", []string{"a-one", "b-two", "kyverno"}, 1},
		{"only known names", []string{"kyverno", "fitting-completion"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := start(t)
			initializeWith(t, c, map[string]any{"experimental": tt.list})
			got := 0
			if tt.want > 0 {
				// The pipe has no buffer: read the warning before sending more.
				for m := c.read(); ; m = c.read() {
					if m.Method == "window/showMessage" {
						t.Fatalf("window/showMessage %s, want none", m.Params)
					}
					if m.Method == "window/logMessage" {
						got++
						if !strings.Contains(string(m.Params), "a-one") || !strings.Contains(string(m.Params), "b-two") {
							t.Errorf("logMessage = %s, want both names", m.Params)
						}
						break
					}
				}
			}
			c.open(kyvernoPolicy)
			for {
				m := c.read()
				if m.Method == "window/showMessage" {
					t.Fatalf("window/showMessage %s, want none", m.Params)
				}
				if m.Method == "window/logMessage" {
					got++
				}
				if m.Method == "textDocument/publishDiagnostics" {
					break
				}
			}
			if got != tt.want {
				t.Errorf("logMessage count = %d, want %d", got, tt.want)
			}
		})
	}
}
