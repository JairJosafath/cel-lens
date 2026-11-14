package check

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// Limits sizes the limits that would bring the expression of c, an entry of
// Costs for the same file, text and options, to cost at most budget, as the
// cost problems of Check size theirs to bring an expression under its
// budget: one limit for each field the expression depends on that has none
// (a list's maxItems, a map's maxProperties, a string's maxLength), each the
// largest of 1, 2, 5, 10 … 10M that does it alone, preferring a limit of 100
// or more, else one number for all of them together, which together then
// says. Each Fix has its Edits, which write it into data. A limit no number
// of the ladder makes enough for has Value 0.
//
// Limits answers no fix for an expression that is not CostEstimated, that
// is not in data, or whose fields all have their limits: lowering a limit is
// the author's call. Only the expression's own schema node is estimated again,
// not its schema's total. When ctx is cancelled it returns ctx's error.
func Limits(ctx context.Context, file string, data []byte, c Cost, budget uint64, opts ...Option) (fixes []Fix, together bool, err error) {
	if c.Kind != CostEstimated {
		return nil, false, nil
	}
	var set settings
	for _, o := range opts {
		o(&set)
	}
	text := extract.UTF8(data)
	docs, broken, err := extract.Documents(ctx, file, text)
	if err != nil {
		return nil, false, err
	}
	exprs, findErr := extract.FindIn(ctx, file, text, docs, broken)
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	_ = findErr // a document that does not parse has no expression to size
	var node *yaml.Node
	for _, e := range exprs {
		if e.Start == c.Start && e.End == c.End {
			for _, d := range docs {
				if d.Index == e.Document {
					node = d.Node
				}
			}
		}
	}
	if node == nil {
		return nil, false, nil
	}
	r := reporter{version: set.version, file: file, lines: extract.SplitLines(text), data: data, eol: "\n"}
	if bytes.Contains(extract.Text(data), []byte("\r\n")) {
		r.eol = "\r\n"
	}
	var versioned apiextensionsv1.CustomResourceDefinition
	if _, err := r.read(ctx, node, &versioned); err != nil {
		return nil, false, fmt.Errorf("read CustomResourceDefinition: %w", err)
	}
	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(&versioned)
	var crd apiextensions.CustomResourceDefinition
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&versioned, &crd, nil); err != nil {
		return nil, false, fmt.Errorf("convert CustomResourceDefinition: %w", err)
	}
	var sizeErr error
	if err := UnderVersion(r.version, func() {
		fixes, together, sizeErr = r.limits(ctx, node, &crd, c, budget)
	}); err != nil {
		return nil, false, err
	}
	return fixes, together, sizeErr
}

// limits is Limits once the CustomResourceDefinition of the expression of c
// is read from doc.
func (r *reporter) limits(ctx context.Context, doc *yaml.Node, crd *apiextensions.CustomResourceDefinition, c Cost, budget uint64) ([]Fix, bool, error) {
	nodePath, _, ok := strings.Cut(c.FieldPath, ".x-kubernetes-validations[")
	if !ok {
		return nil, false, nil
	}
	for _, root := range crdRoots(crd) {
		prefix := root.path.String()
		if nodePath != prefix && !strings.HasPrefix(nodePath, prefix+".") {
			continue
		}
		keys := sharedKeys(doc)
		if root.version != "" {
			for i, v := range crd.Spec.Versions {
				if v.Schema != nil && v.Schema.OpenAPIV3Schema == root.schema {
					keys = [][]string{versionKeys(i)}
				}
			}
		}
		rootNode := &schemaNode{schema: root.schema, cel: validation.RootCELContext(root.schema), path: root.path}
		buildNodes(rootNode, nil)
		n := findNode(rootNode, nodePath)
		if n == nil {
			return nil, false, nil
		}
		comp := &compiler{envSet: r.version.EnvSet()}
		var rules []ruleCost
		if err := nodeRules(n, comp, &rules); err != nil {
			return nil, false, err
		}
		for _, rc := range rules {
			if rc.path.String() != c.FieldPath {
				continue
			}
			var limitable []Fix
			for _, d := range drivers(rc) {
				if d.fix.Limit != "" {
					limitable = append(limitable, d.fix)
				}
			}
			if len(limitable) == 0 {
				return nil, false, nil
			}
			sz := &sizer{envSet: comp, root: root.schema, path: root.path, nodes: map[string]*schemaNode{}, doc: doc, roots: keys, lines: r.lines, eol: r.eol, budget: budget}
			nodesByName(rootNode, sz.nodes)
			return sz.size(ctx, rc, limitable)
		}
	}
	return nil, false, nil
}
