package check

import (
	"context"
	"fmt"
	"strings"

	kyvernov1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/internal/kyverno"
	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// kyvernoPolicy reports the expressions of a Kyverno ValidatingPolicy that
// Kyverno's CEL environment does not accept. When the policy matches built-in
// kinds, the validations that compile are checked again for each kind with
// object and oldObject typed from it, and what fails is a warning naming the
// kind, as for a ValidatingAdmissionPolicy.
func (r *reporter) kyvernoPolicy(ctx context.Context, node *yaml.Node, exprs map[string]extract.Expression) error {
	var p kyvernov1.ValidatingPolicy
	if _, err := r.read(ctx, node, &p); err != nil {
		return fmt.Errorf("read ValidatingPolicy: %w", err)
	}
	problems, err := kyverno.Compile(ctx, &p.Spec, p.Namespace, nil)
	if err != nil {
		return fmt.Errorf("check ValidatingPolicy: %w", err)
	}
	clean := map[string]bool{}
	for field := range exprs {
		clean[field] = strings.HasPrefix(field, "spec.validations[")
	}
	for _, pr := range problems {
		delete(clean, pr.Field)
		if e, ok := exprs[pr.Field]; ok {
			r.report(e, pr.Detail)
		}
	}
	if p.Spec.MatchConstraints == nil || len(clean) == 0 {
		return nil
	}
	rules := make([]matchkind.Rule, len(p.Spec.MatchConstraints.ResourceRules))
	for i, rule := range p.Spec.MatchConstraints.ResourceRules {
		rules[i] = matchkind.Rule{Groups: rule.APIGroups, Versions: rule.APIVersions, Resources: rule.Resources}
	}
	kinds, ok := matchkind.Resolve(rules)
	if !ok {
		return nil
	}
	for _, kind := range kinds {
		typed, err := kyverno.Compile(ctx, &p.Spec, p.Namespace, &kind)
		if err != nil {
			return fmt.Errorf("check ValidatingPolicy for %s: %w", kind, err)
		}
		for _, pr := range typed {
			if e, ok := exprs[pr.Field]; ok && clean[pr.Field] {
				r.reportAs(e, SeverityWarning, kind.String()+": ", pr.Detail)
			}
		}
	}
	return nil
}
