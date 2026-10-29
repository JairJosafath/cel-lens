package assist

import (
	"slices"
	"testing"

	celgo "github.com/google/cel-go/cel"
	plugincel "k8s.io/apiserver/pkg/admission/plugin/cel"
	"k8s.io/apiserver/pkg/cel/environment"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

type accessor struct{ expression string }

func (a accessor) GetExpression() string      { return a.expression }
func (a accessor) ReturnTypes() []*celgo.Type { return []*celgo.Type{celgo.AnyType, celgo.DynType} }

func TestExamplesCompile(t *testing.T) {
	t.Parallel()
	for _, v := range kubeversion.Supported() {
		compiler := plugincel.NewCompiler(v.EnvSet())
		fs, ms, err := buildCatalog(v)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, f := range fs {
			names = append(names, f.name)
		}
		for _, m := range ms {
			names = append(names, m.name)
		}
		for _, name := range slices.Compact(slices.Sorted(slices.Values(names))) {
			ex, ok := example(name)
			if !ok {
				continue
			}
			t.Run(v.String()+"/"+name, func(t *testing.T) {
				t.Parallel()
				res := compiler.CompileCELExpression(accessor{ex.expr}, plugincel.OptionalVariableDeclarations{HasAuthorizer: true}, environment.NewExpressions)
				if res.Error != nil {
					t.Errorf("compile %q in Kubernetes %s: %v", ex.expr, v, res.Error.Detail)
				}
			})
		}
	}
}

// TestExampleDoesNotAllocate fails if example goes back to building a table on
// every hover. It must not run in parallel: AllocsPerRun counts every
// allocation in the process.
func TestExampleDoesNotAllocate(t *testing.T) {
	var ok bool
	if got := testing.AllocsPerRun(100, func() { _, ok = example("has") }); got != 0 || !ok {
		t.Errorf("example(%q) allocates %v times (found %v), want 0 and found", "has", got, ok)
	}
}

func TestEveryFunctionHasExampleAndDocs(t *testing.T) {
	t.Parallel()
	for _, v := range kubeversion.Supported() {
		fs, _, err := buildCatalog(v)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, f := range fs {
			if !internal(f.name) {
				names = append(names, f.name)
			}
		}
		// Go through the environment's own macros, not buildCatalog's, which
		// leaves out a macro that has no form.
		env, err := v.EnvSet().Env(environment.NewExpressions)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range env.Macros() {
			if _, ok := macroForms()[m.MacroKey()]; !ok {
				t.Errorf("Kubernetes %s declares macro %s, which has no form in macroForms", v, m.MacroKey())
			}
			names = append(names, m.Function())
		}
		for _, name := range names {
			ex, ok := example(name)
			if !ok || ex.expr == "" || ex.result == "" {
				t.Errorf("Kubernetes %s declares %q, which has no example", v, name)
			}
			wantTitle, wantURL := k8sCELTitle, k8sCELURL
			if standard(name) {
				wantTitle, wantURL = celSpecTitle, celSpecURL
			}
			if sec, ok := sections(name); ok {
				wantTitle, wantURL = sec.title, sec.page+"#"+sec.anchor
			}
			if title, url := docsURL(name); title != wantTitle || url != wantURL {
				t.Errorf("docsURL(%q) = %q, %q, want %q, %q", name, title, url, wantTitle, wantURL)
			}
		}
	}
}
