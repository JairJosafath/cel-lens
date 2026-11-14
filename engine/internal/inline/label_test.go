package inline_test

import (
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/inline"
)

// TestRuleLabel checks the label of a rule in the list of the workspace's
// rules (cel-lens-core#35): its static message, else a match condition's or
// variable's name, else an audit annotation's key, else its expression on
// one line, cut past 60 characters; labelFrom says which.
func TestRuleLabel(t *testing.T) {
	t.Parallel()
	long := "self.spec.replicas <= 10 && self.spec.template.metadata.labels.all(k, k.size() < 63)"
	tests := []struct {
		name      string
		words     inline.RuleWords
		label     string
		labelFrom string
	}{
		{"message", inline.RuleWords{Message: "at most 10 replicas", Expression: "self.replicas <= 10"}, "at most 10 replicas", inline.LabelMessage},
		{"message over a name", inline.RuleWords{Message: "too many", Name: "limit", Expression: "x"}, "too many", inline.LabelMessage},
		{"long message, never cut", inline.RuleWords{Message: long, Expression: "x"}, long, inline.LabelMessage},
		{"multi-line message on one line", inline.RuleWords{Message: "the size\nis at most\t10\n", Expression: "x"}, "the size is at most 10", inline.LabelMessage},
		{"empty message", inline.RuleWords{Message: "", Expression: "self.size <= 10"}, "self.size <= 10", inline.LabelExpression},
		{"blank message", inline.RuleWords{Message: "  \n ", Expression: "self.size <= 10"}, "self.size <= 10", inline.LabelExpression},
		{"match condition's name", inline.RuleWords{Name: "exclude-leases", Expression: "request.resource.group != 'coordination.k8s.io'"}, "exclude-leases", inline.LabelName},
		{"variable's name", inline.RuleWords{Name: "replicas", Expression: "object.spec.replicas"}, "replicas", inline.LabelName},
		{"audit annotation's key", inline.RuleWords{Key: "high-replica-count", Expression: "'Deployment spec.replicas set to ' + string(object.spec.replicas)"}, "high-replica-count", inline.LabelKey},
		{"expression", inline.RuleWords{Expression: "self.minReplicas <= self.replicas"}, "self.minReplicas <= self.replicas", inline.LabelExpression},
		{"expression cut past 60", inline.RuleWords{Expression: long}, "self.spec.replicas <= 10 && self.spec.template.metadata.lab…", inline.LabelExpression},
		{"expression of exactly 60", inline.RuleWords{Expression: long[:60]}, long[:60], inline.LabelExpression},
		{"multi-line block scalar", inline.RuleWords{Expression: "self.minReplicas <= self.replicas &&\n  self.replicas <= self.maxReplicas\n"}, "self.minReplicas <= self.replicas && self.replicas <= self.…", inline.LabelExpression},
		{"short block scalar", inline.RuleWords{Expression: "has(self.a) ||\n  has(self.b)\n"}, "has(self.a) || has(self.b)", inline.LabelExpression},
		{"no words at all", inline.RuleWords{}, "", inline.LabelExpression},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			label, from := inline.RuleLabel(tt.words)
			if label != tt.label || from != tt.labelFrom {
				t.Errorf("RuleLabel(%+v) = %q, %q, want %q, %q", tt.words, label, from, tt.label, tt.labelFrom)
			}
		})
	}
}
