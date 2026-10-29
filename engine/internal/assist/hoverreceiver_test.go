package assist_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// TestHoverAuthorizerStepsMarkOneOverload checks that hover on each step of an
// authorizer chain lists only the overloads of its receiver's type and marks
// the one the chain selects.
func TestHoverAuthorizerStepsMarkOneOverload(t *testing.T) {
	t.Parallel()
	const (
		auth = "kubernetes.authorization."
		full = "authorizer.group('apps').resource('deployments')"
	)
	tests := []struct {
		name, expr, needle string
		marked             string
		absent             []string
	}{
		{"path", "authorizer.path('/healthz').check('get').allowed()", "path(", auth + "Authorizer.path(string) -> " + auth + "PathCheck", nil},
		{"group", full + ".check('get').allowed()", "group(", auth + "Authorizer.group(string) -> " + auth + "GroupCheck", []string{"PathCheck.group"}},
		{"resource", full + ".check('get').allowed()", "resource(", auth + "GroupCheck.resource(string) -> " + auth + "ResourceCheck", nil},
		{"subresource", full + ".subresource('scale').check('get').allowed()", "subresource(", auth + "ResourceCheck.subresource(string) -> " + auth + "ResourceCheck", nil},
		{"namespace", full + ".namespace('ns').check('get').allowed()", "namespace(", auth + "ResourceCheck.namespace(string) -> " + auth + "ResourceCheck", nil},
		{"name", full + ".name('d').check('get').allowed()", "name(", auth + "ResourceCheck.name(string) -> " + auth + "ResourceCheck", nil},
		{"check on resource", full + ".check('update').allowed()", "check(", auth + "ResourceCheck.check(string) -> " + auth + "Decision", []string{"PathCheck.check"}},
		{"check on path", "authorizer.path('/healthz').check('get').allowed()", "check(", auth + "PathCheck.check(string) -> " + auth + "Decision", []string{"ResourceCheck.check"}},
		{"allowed", full + ".check('get').allowed()", "allowed(", auth + "Decision.allowed() -> bool", nil},
		{"reason", full + ".check('get').reason() == ''", "reason(", auth + "Decision.reason() -> string", nil},
		{"errored", full + ".check('get').errored()", "errored(", auth + "Decision.errored() -> bool", nil},
		{"error", full + ".check('get').error() == ''", "error(", auth + "Decision.error() -> string", nil},
		{"serviceAccount", "authorizer.serviceAccount('ci', 'build').group('').resource('pods').check('get').allowed()", "serviceAccount(", auth + "Authorizer.serviceAccount(string, string) -> " + auth + "Authorizer", nil},
	}
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc := matching(deploymentRule, tt.expr)
			got, ok := a.Hover(assist.Parse(context.Background(), "policy.yaml", []byte(doc)), posIn(t, doc, tt.needle, 2))
			if !ok {
				t.Fatalf("Hover(%s) = none", tt.name)
			}
			if want := tt.marked + " // this call"; !strings.Contains(got.Markdown, want) {
				t.Errorf("Hover(%s) = %q, want it to contain %q", tt.name, got.Markdown, want)
			}
			if n := strings.Count(got.Markdown, "// this call"); n != 1 {
				t.Errorf("Hover(%s) marks %d overloads, want 1: %q", tt.name, n, got.Markdown)
			}
			for _, bad := range tt.absent {
				if strings.Contains(got.Markdown, bad) {
					t.Errorf("Hover(%s) = %q, want no %q", tt.name, got.Markdown, bad)
				}
			}
		})
	}
}

// TestHoverMarksOverloadOnTypedReceivers checks the marked overload on a
// field of object, on a variable and on a CRD rule's self, and that a dyn
// receiver keeps every overload listed.
func TestHoverMarksOverloadOnTypedReceivers(t *testing.T) {
	t.Parallel()
	const startsWith = "string.startsWith(string) -> bool // this call"
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	hover := func(t *testing.T, name, doc, needle string, offset int) string {
		t.Helper()
		got, ok := a.Hover(assist.Parse(context.Background(), name, []byte(doc)), posIn(t, doc, needle, offset))
		if !ok {
			t.Fatalf("Hover(%q) = none", needle)
		}
		return got.Markdown
	}
	check := func(t *testing.T, md string) {
		t.Helper()
		if !strings.Contains(md, startsWith) || strings.Count(md, "// this call") != 1 {
			t.Errorf("Hover = %q, want only %q marked", md, startsWith)
		}
		if strings.Contains(md, "bytes") {
			t.Errorf("Hover = %q, want only overloads that fit a string", md)
		}
	}
	t.Run("object field", func(t *testing.T) {
		t.Parallel()
		doc := matching(deploymentRule, "object.metadata.name.startsWith('a')")
		check(t, hover(t, "policy.yaml", doc, "startsWith", 2))
	})
	t.Run("variables", func(t *testing.T) {
		t.Parallel()
		doc := strings.Replace(varsPolicy("variables.name.startsWith('a')"), "  variables:\n", "  variables:\n    - name: name\n      expression: object.metadata.name\n", 1)
		check(t, hover(t, "policy.yaml", doc, "startsWith", 2))
	})
	t.Run("self", func(t *testing.T) {
		t.Parallel()
		doc := load(t, "crd.yaml")
		check(t, hover(t, "crd.yaml", doc, "startsWith('w')", 2))
	})
	t.Run("dyn receiver", func(t *testing.T) {
		t.Parallel()
		md := hover(t, "policy.yaml", matching("{apiGroups: [apps], apiVersions: [v1], resources: [nothings]}", "object.name.startsWith('a')"), "startsWith", 2)
		if strings.Contains(md, "this call") || !strings.Contains(md, "string.startsWith(string) -> bool") {
			t.Errorf("Hover on a dyn receiver = %q, want every overload and none marked", md)
		}
	})
}

// TestHoverListsOnlyOverloadsOfReceiver checks that hover on size lists the
// overloads for the receiver's type only, whatever the receiver is.
func TestHoverListsOnlyOverloadsOfReceiver(t *testing.T) {
	t.Parallel()
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	crd := strings.Replace(load(t, "crd.yaml"), "self.name.startsWith('w')", "self.name.size() > 0 && self.ports.size() > 0", 1)
	named := strings.Replace(varsPolicy("variables.name.size() > 0 && variables.groups.size() > 0"), "  variables:\n", "  variables:\n    - name: name\n      expression: object.metadata.name\n", 1)
	tests := []struct {
		name, file, doc, needle string
		marked                  string
		absent                  []string
	}{
		{"object field", "policy.yaml", matching(deploymentRule, "object.metadata.name.size() > 0"), "size", "string.size() -> int", []string{"bytes", "list", "map"}},
		{"object list", "policy.yaml", matching(deploymentRule, "object.spec.template.spec.containers.size() > 0"), "size", "list(<A>).size() -> int", []string{"string", "bytes", "map"}},
		{"variables string", "policy.yaml", named, "variables.name.size", "string.size() -> int", []string{"bytes", "list", "map"}},
		{"variables list", "policy.yaml", named, "variables.groups.size", "list(<A>).size() -> int", []string{"string", "bytes", "map"}},
		{"self string", "crd.yaml", crd, "self.name.size", "string.size() -> int", []string{"bytes", "list", "map"}},
		{"self list", "crd.yaml", crd, "self.ports.size", "list(<A>).size() -> int", []string{"string", "bytes", "map"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			off := strings.LastIndex(tt.needle, "size")
			if off < 0 {
				off = 0
			}
			got, ok := a.Hover(assist.Parse(context.Background(), tt.file, []byte(tt.doc)), posIn(t, tt.doc, tt.needle, off+2))
			if !ok {
				t.Fatalf("Hover(%s) = none", tt.name)
			}
			if want := tt.marked + " // this call"; !strings.Contains(got.Markdown, want) || strings.Count(got.Markdown, "// this call") != 1 {
				t.Errorf("Hover(%s) = %q, want only %q marked", tt.name, got.Markdown, want)
			}
			for _, bad := range tt.absent {
				if strings.Contains(got.Markdown, bad+".size()") || strings.Contains(got.Markdown, bad+"(<") {
					t.Errorf("Hover(%s) = %q, want no %s overload", tt.name, got.Markdown, bad)
				}
			}
		})
	}
}

// TestHoverResolvesOpaqueReceivers checks that hover marks the overload the
// receiver's type selects when the receiver spans lines, goes through a policy
// variable or sits in parentheses.
func TestHoverResolvesOpaqueReceivers(t *testing.T) {
	t.Parallel()
	const (
		resource = "kubernetes.authorization.ResourceCheck.check(string) -> kubernetes.authorization.Decision // this call"
		path     = "kubernetes.authorization.PathCheck.check(string) -> kubernetes.authorization.Decision // this call"
	)
	a, err := assist.New(kubeversion.Default(), allOn())
	if err != nil {
		t.Fatal(err)
	}
	withVar := func(name, expr, rule string) string {
		return strings.Replace(matching(deploymentRule, rule), "  validations:\n", "  variables:\n    - name: "+name+"\n      expression: "+expr+"\n  validations:\n", 1)
	}
	tests := []struct {
		name, doc, needle, marked, absent string
	}{
		{"block scalar", matching(deploymentRule, "|-\n        authorizer.group('apps')\n          .resource('deployments')\n          .check('update').allowed()"), "check(", resource, "PathCheck"},
		{"variable", withVar("rc", "authorizer.group('apps').resource('deployments')", "variables.rc.check('get').allowed()"), "check(", resource, "PathCheck"},
		{"variable of variable", strings.Replace(withVar("rc", "authorizer.group('apps').resource('deployments')", "variables.rd.check('get').allowed()"), "  validations:", "    - name: rd\n      expression: variables.rc.name('x')\n  validations:", 1), "check(", resource, "PathCheck"},
		{"path variable", withVar("pc", "authorizer.path('/x')", "variables.pc.check('get').allowed()"), "check(", path, "ResourceCheck"},
		{"parentheses", matching(deploymentRule, "(authorizer.path('/x')).check('get').allowed()"), "check(", path, "ResourceCheck"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := a.Hover(assist.Parse(context.Background(), "policy.yaml", []byte(tt.doc)), posIn(t, tt.doc, tt.needle, 2))
			if !ok {
				t.Fatalf("Hover(%s) = none", tt.name)
			}
			if !strings.Contains(got.Markdown, tt.marked) || strings.Count(got.Markdown, "// this call") != 1 || strings.Contains(got.Markdown, tt.absent) {
				t.Errorf("Hover(%s) = %q, want %q marked and no %s", tt.name, got.Markdown, tt.marked, tt.absent)
			}
		})
	}
}
