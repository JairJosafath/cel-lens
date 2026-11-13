package check

import (
	"context"
	"strings"

	"go.yaml.in/yaml/v3"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/JairJosafath/cel-lens/engine/internal/yamlfield"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// Root is a value whose type an AssumedType names the root of: a served
// version of a CustomResourceDefinition, or a variable of a
// ValidatingAdmissionPolicy or MutatingAdmissionPolicy.
type Root struct {
	// Kind and Name name its document by its kind and metadata.name; Line is
	// the document's first line, from 1.
	Kind, Name string
	Line       int
	// Version is the version of a CustomResourceDefinition, empty for a
	// policy; Variable is the variable of a policy, params, object,
	// oldObject or variables.<name>, empty for a CustomResourceDefinition.
	Version, Variable string
	// Schemas are the value's schemas, in the form YAML decodes a CRD's
	// schema to: one, or for object and oldObject one per kind the policy
	// matches. None when the type of the value is not known: it is dyn.
	Schemas []map[string]any
	// Type names the value's type: the kind of a CustomResourceDefinition's
	// version, such as Widget, the kind of params, the kinds a policy
	// matches, comma-separated, a variable's CEL type, or dyn.
	Type string
}

// Roots returns the roots of the values the documents of data, a YAML file,
// let an AssumedType name, in the order of the documents: the versions of a
// CustomResourceDefinition of apiextensions.k8s.io/v1 that have a schema,
// and of a ValidatingAdmissionPolicy or MutatingAdmissionPolicy its params,
// when it has a paramKind, object, oldObject and each of its variables. A
// document without metadata.name has none. Options WithVersion, WithFeatures
// and WithCRDs apply, as they decide which values are typed. The error is
// for a cancelled ctx.
func Roots(ctx context.Context, data []byte, opts ...Option) ([]Root, error) {
	var set settings
	for _, o := range opts {
		o(&set)
	}
	docs, _, err := extract.Documents(ctx, "", extract.UTF8(data))
	if err != nil {
		return nil, err
	}
	r := reporter{version: set.version, crds: set.typingCRDs()}
	var out []Root
	for _, d := range docs {
		head, _ := typeOf(d.Node)
		doc := Root{Kind: head.Kind, Name: metadataName(d.Node), Line: d.Node.Line}
		if d.Node.Kind == yaml.DocumentNode && len(d.Node.Content) > 0 {
			doc.Line = d.Node.Content[0].Line
		}
		if doc.Name == "" {
			continue
		}
		switch {
		case head.Kind == kindCRD && head.APIVersion == "apiextensions.k8s.io/v1":
			var crd apiextensionsv1.CustomResourceDefinition
			var scratch reporter // takes the wrong-typed fields read reports, which are Check's to report
			if _, err := scratch.read(ctx, d.Node, &crd); err != nil {
				continue
			}
			for _, v := range crdSchemas(&crd) {
				root := doc
				root.Version, root.Schemas, root.Type = v.name, []map[string]any{v.schema}, crd.Spec.Names.Kind
				out = append(out, root)
			}
		case isAdmissionPolicy(head.APIVersion, head.Kind):
			roots, err := r.policyRoots(ctx, d.Node, doc)
			if err != nil {
				return nil, err
			}
			out = append(out, roots...)
		}
	}
	return out, ctx.Err()
}

// policyRoots returns the roots of the policy in node, whose document doc
// names, as placePolicy types them. The error is for a cancelled ctx.
func (r *reporter) policyRoots(ctx context.Context, node *yaml.Node, doc Root) ([]Root, error) {
	p, ok := readPolicy(node)
	if !ok {
		return nil, nil // a policy whose spec cannot be read has none
	}
	var out []Root
	add := func(variable, typ string, schemas ...map[string]any) {
		v := doc
		v.Variable, v.Type = variable, typ
		for _, s := range schemas {
			if s != nil {
				v.Schemas = append(v.Schemas, s)
			}
		}
		if len(v.Schemas) == 0 {
			v.Type = "dyn"
		}
		out = append(out, v)
	}
	if p.Spec.ParamKind != nil {
		s, name := r.paramsSchema(p.Spec.ParamKind)
		add("params", name, s)
	}
	var kinds []map[string]any
	var names []string
	if matched, ok := r.crds.Resolve(p.rules()); ok && p.Spec.MatchConstraints != nil {
		for _, k := range matched {
			s, ok := toMap(k.Schema)
			if !ok {
				kinds, names = nil, nil
				break
			}
			kinds, names = append(kinds, s), append(names, k.String())
		}
	}
	add("object", strings.Join(names, ", "), kinds...)
	add("oldObject", strings.Join(names, ", "), kinds...)
	vars, err := r.variableTypes(ctx, p)
	if err != nil {
		return nil, err
	}
	for _, v := range vars {
		if v.Type == "dyn" {
			add("variables."+v.Name, "dyn")
			continue
		}
		add("variables."+v.Name, v.Type, v.Schema)
	}
	return out, nil
}

// readPolicy reads the part of the policy in node that decides the types of
// its variables, and reports whether it could.
func readPolicy(node *yaml.Node) (*policyVars, bool) {
	var root map[string]any
	if yamlfield.Decode(node, &root) != nil {
		return nil, false
	}
	p, err := readPolicyVars(root)
	return p, err == nil
}
