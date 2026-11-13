package examples

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// benchPolicy is a policy of Deployments with n validations, half of which
// read its params, bound with Deny to a ConfigMap of params.
func benchPolicy(n int) string {
	var b strings.Builder
	b.WriteString(`apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata: {name: bench}
spec:
  paramKind: {apiVersion: v1, kind: ConfigMap}
  matchConstraints:
    resourceRules:
      - {apiGroups: [apps], apiVersions: [v1], operations: [CREATE, UPDATE], resources: [deployments]}
  variables:
    - {name: max, expression: "int(params.data.max)"}
  validations:
`)
	for i := range n {
		if i%2 == 0 {
			fmt.Fprintf(&b, "    - {expression: \"object.spec.replicas <= variables.max + %d\", messageExpression: \"'at most ' + string(variables.max + %d)\"}\n", i, i)
		} else {
			fmt.Fprintf(&b, "    - {expression: \"object.spec.template.spec.containers.all(c, c.image != 'bad%d')\", message: \"no bad image\"}\n", i)
		}
	}
	b.WriteString("---\napiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicyBinding\nmetadata: {name: bench}\nspec: {policyName: bench, validationActions: [Deny], paramRef: {name: limits, namespace: default, parameterNotFoundAction: Deny}}\n")
	return b.String()
}

// benchDeployments are n Deployments, one in ten over the limit.
func benchDeployments(n int) string {
	var docs []string
	for i := range n {
		replicas := 2
		if i%10 == 0 {
			replicas = 9
		}
		docs = append(docs, deployment(fmt.Sprintf("d%d", i), replicas))
	}
	return strings.Join(docs, "---\n")
}

// BenchmarkPolicyRun measures Run of a policy of 20 validations over 100
// examples (2,000 results): with an empty cache, every example admitted
// again, and with the cache of the run before, nothing changed (#19).
func BenchmarkPolicyRun(b *testing.B) {
	const validations, examples = 20, 100
	params := [2]string{"params.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: limits}\ndata: {max: \"5\"}\n"}
	for _, warm := range []bool{false, true} {
		name := "cold"
		if warm {
			name = "unchanged"
		}
		b.Run(name, func(b *testing.B) {
			ws := benchWorkspace(b, benchPolicy(validations), params, [2]string{"deploy.yaml", benchDeployments(examples)})
			p := ws.policies[0]
			rules, err := p.Rules(b.Context())
			if err != nil {
				b.Fatal(err)
			}
			var c PolicyCache
			run := func() int {
				out, err := c.Run(b.Context(), p, rules, ws.in, PolicyOptions{})
				if err != nil {
					b.Fatal(err)
				}
				return len(out.Results)
			}
			if n := run(); n != validations*examples {
				b.Fatalf("Run() = %d results, want %d", n, validations*examples)
			}
			b.Logf("input: %d validations, %d examples", validations, examples)
			for b.Loop() {
				if !warm {
					c = PolicyCache{}
				}
				run()
			}
		})
	}
}

// benchWorkspace is readWorkspace for a benchmark.
func benchWorkspace(b *testing.B, policy string, files ...[2]string) workspace {
	b.Helper()
	ctx := b.Context()
	kube := kubeversion.Default()
	ws := workspace{policies: ReadPolicies(ctx, policyFile, []byte(policy), kube), in: PolicyInput{Bindings: ReadBindings(ctx, policyFile, []byte(policy))}}
	for _, f := range files {
		objs, err := FindBuiltIn(ctx, "/w/"+f[0], f[0], []byte(f[1]), func(string, string) bool { return true })
		if err != nil {
			b.Fatal(err)
		}
		ws.in.Objects = append(ws.in.Objects, objs...)
	}
	return ws
}
