package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
	"github.com/JairJosafath/cel-lens/engine/internal/cellens"
	"github.com/JairJosafath/cel-lens/engine/internal/infer"
	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// maxExamples is the most examples typeChoices lists for a value.
const maxExamples = 100

// example is a document of the workspace that typeChoices offers as an
// example of a value, for inferType: where it is, its line from 1, and its
// kind.
type example struct {
	URI string `json:"uri"`
	// File is the path of the document's file below the workspace folder it
	// is in, slash-separated.
	File       string `json:"file"`
	Line       int    `json:"line"`
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
}

// exampleSearch is a search of the workspace folders for the documents whose
// kind is one a value has: the kind spec.paramKind names for params, the
// kinds the policy's spec.matchConstraints.resourceRules match for object
// and oldObject. A value of another variable, or of a
// CustomResourceDefinition, has no kind, and no examples. The session makes
// it, and run reads only what it holds, so it runs off the message loop.
type exampleSearch struct {
	// match is nil for a value with no kind.
	match *kindMatch
	// open are the texts of the documents the editor has open, by key.
	open    map[string][]byte
	folders []string
	dirFS   func(dir string) fs.FS
	limit   int
}

// exampleSearch returns the search for the examples of the value of v.
func (ss *session) exampleSearch(ctx context.Context, v valueRef) exampleSearch {
	limit := ss.fileCap
	if limit <= 0 {
		limit = maxWorkspaceFiles
	}
	open := make(map[string][]byte, len(ss.docs))
	for uri, d := range ss.docs {
		open[crdKey(uri)] = d.text // a document's text is replaced, never changed
	}
	return exampleSearch{match: ss.exampleKinds(ctx, v), open: open, folders: outermost(ss.folders), dirFS: ss.dirFS(), limit: limit}
}

// run returns the examples: it walks the YAML files as the workspace scan
// does, with its limits, and reads an open file as the editor has it; it
// lists at most maxExamples, in the order of the walk. The error is ctx's.
func (s exampleSearch) run(ctx context.Context) ([]example, error) {
	out := []example{}
	if s.match == nil {
		return out, ctx.Err()
	}
	walkYAML(ctx, s.dirFS, s.folders, s.limit, yamlWalk{file: func(dir, rel string, _ fs.DirEntry) bool {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		data, open := s.open[crdKey(pathURI(path))]
		if !open {
			// readIn refuses what a symbolic link leads to outside the
			// folder, no regular file, and more than the size limit.
			var err error
			if data, err = readIn(dir, filepath.FromSlash(rel)); err != nil {
				return true
			}
		}
		data = extract.UTF8(data)
		if s.match.words != nil && !slices.ContainsFunc(s.match.words, func(w string) bool { return bytes.Contains(data, []byte(w)) }) {
			return true
		}
		docs, _, err := extract.Documents(ctx, path, data)
		if err != nil {
			return false
		}
		for _, d := range docs {
			apiVersion, kind, line := typeMeta(d.Node)
			if !s.match.match(apiVersion, kind) {
				continue
			}
			out = append(out, example{URI: pathURI(path), File: rel, Line: line, APIVersion: apiVersion, Kind: kind})
			if len(out) == maxExamples {
				return false
			}
		}
		return true
	}})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// typeMeta returns the apiVersion and kind of the document doc, and the line
// its content starts at, from 1; "" for one that is no mapping or lacks them.
func typeMeta(doc *yaml.Node) (apiVersion, kind string, line int) {
	n := doc
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return "", "", 0
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, val := n.Content[i], n.Content[i+1]
		if val.Kind != yaml.ScalarNode || val.ShortTag() != "!!str" {
			continue
		}
		switch k.Value {
		case "apiVersion":
			apiVersion = val.Value
		case "kind":
			kind = val.Value
		}
	}
	return apiVersion, kind, n.Line
}

// policyKinds is the part of a policy that says the kinds of its params,
// object and oldObject.
type policyKinds struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		ParamKind *struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
		} `yaml:"paramKind"`
		MatchConstraints struct {
			ResourceRules []struct {
				APIGroups   []string `yaml:"apiGroups"`
				APIVersions []string `yaml:"apiVersions"`
				Resources   []string `yaml:"resources"`
			} `yaml:"resourceRules"`
		} `yaml:"matchConstraints"`
	} `yaml:"spec"`
}

// kindMatch says which documents are examples of a value.
type kindMatch struct {
	// match reports whether a document of an apiVersion and kind is one.
	match func(apiVersion, kind string) bool
	// words are what the text of a file with one has, one of them at least:
	// the kinds, or the groups of the kinds not known. A file without is not
	// parsed, which most files of a workspace are not. nil when any text
	// may have one.
	words []string
}

// policyOf returns the part of the policy the value of v is in that says
// the kinds of its params, object and oldObject; false for a value of no
// policy variable with a kind, params, object or oldObject, or a policy that
// cannot be read.
func (ss *session) policyOf(ctx context.Context, v valueRef) (policyKinds, bool) {
	e := v.entry
	if e.Document.Kind == cellens.CRDKind || e.Variable != "params" && e.Variable != "object" && e.Variable != "oldObject" {
		return policyKinds{}, false
	}
	data, err := ss.targetText(v.file)
	if err != nil {
		return policyKinds{}, false
	}
	docs, _, err := extract.Documents(ctx, v.file, data)
	if err != nil {
		return policyKinds{}, false
	}
	for _, d := range docs {
		var p policyKinds
		if d.Node.Decode(&p) == nil && p.Kind == e.Document.Kind && p.Metadata.Name == e.Document.Name {
			return p, true
		}
	}
	return policyKinds{}, false
}

// exampleKinds returns which documents are examples of the value of v (see
// examples), or nil when the value has no kind, or its policy cannot be
// read.
func (ss *session) exampleKinds(ctx context.Context, v valueRef) *kindMatch {
	p, ok := ss.policyOf(ctx, v)
	if !ok {
		return nil
	}
	if v.entry.Variable != "params" {
		return ss.ruleKinds(p)
	}
	pk := p.Spec.ParamKind
	if pk == nil || pk.APIVersion == "" || pk.Kind == "" {
		return nil
	}
	want := infer.Kind{APIVersion: pk.APIVersion, Kind: pk.Kind}
	return &kindMatch{
		match: func(apiVersion, kind string) bool {
			return infer.Kind{APIVersion: apiVersion, Kind: kind}.Group() == want.Group() && kind == want.Kind
		},
		words: []string{pk.Kind},
	}
}

// valueKinds returns the kinds the root of the value of v can be, the value
// or the object it is in: for params the kind spec.paramKind names, for
// object and oldObject the kinds the policy's resource rules match, when the
// session knows them all, as the type checker resolves them; none, never
// nil, for another value, a policy whose kinds are not all known, or one
// that cannot be read.
func (ss *session) valueKinds(ctx context.Context, v valueRef) []infer.Kind {
	out := []infer.Kind{}
	p, ok := ss.policyOf(ctx, v)
	switch {
	case !ok:
	case v.entry.Variable == "params":
		if pk := p.Spec.ParamKind; pk != nil && pk.APIVersion != "" && pk.Kind != "" {
			out = append(out, infer.Kind{APIVersion: pk.APIVersion, Kind: pk.Kind})
		}
	default:
		var rules []matchkind.Rule
		for _, r := range p.Spec.MatchConstraints.ResourceRules {
			rules = append(rules, matchkind.Rule{Groups: r.APIGroups, Versions: r.APIVersions, Resources: r.Resources})
		}
		if kinds, ok := ss.crds.Resolve(rules); ok {
			for _, k := range kinds {
				out = append(out, infer.Kind{APIVersion: k.GVK.GroupVersion().String(), Kind: k.GVK.Kind})
			}
		}
	}
	return out
}

// kindRule is one group, version and resource a resource rule matches, "*"
// for any, and the kind served as that resource when the session knows it.
type kindRule struct {
	group, version, resource, kind string
}

// ruleKinds returns which documents are of a kind the resource rules of p
// match, or nil when they match none. A resource the session knows, built in
// or of a CustomResourceDefinition it has read, matches its kind, in a rule
// with a wildcard group or version too for a built-in one; another the
// kinds whose plural Kubernetes would guess to be its name, as a
// CustomResourceDefinition usually names it. A subresource has a kind of its
// own, and matches nothing.
func (ss *session) ruleKinds(p policyKinds) *kindMatch {
	var rules []kindRule
	for _, r := range p.Spec.MatchConstraints.ResourceRules {
		for _, g := range r.APIGroups {
			for _, ver := range r.APIVersions {
				for _, res := range r.Resources {
					if strings.Contains(res, "/") {
						continue
					}
					kr := kindRule{group: g, version: ver, resource: res}
					if g != "*" && ver != "*" && res != "*" {
						if kinds, ok := ss.crds.Resolve([]matchkind.Rule{{Groups: []string{g}, Versions: []string{ver}, Resources: []string{res}}}); ok {
							kr.kind = kinds[0].GVK.Kind
						}
					}
					rules = append(rules, kr)
				}
			}
		}
	}
	if len(rules) == 0 {
		return nil
	}
	return &kindMatch{words: ruleWords(rules), match: func(apiVersion, kind string) bool {
		gv, err := schema.ParseGroupVersion(apiVersion)
		if err != nil || apiVersion == "" || kind == "" {
			return false
		}
		for _, r := range rules {
			// The version is not compared: the API server converts an
			// object to the version of the rule, as for inferType.
			if r.group != "*" && r.group != gv.Group {
				continue
			}
			switch {
			case r.kind != "":
				if kind == r.kind {
					return true
				}
			case r.resource == "*":
				return true
			default:
				if builtIn, ok := matchkind.ResourceKind(gv.WithResource(r.resource)); ok {
					if kind == builtIn {
						return true
					}
				} else if plural, _ := meta.UnsafeGuessKindToResource(gv.WithKind(kind)); plural.Resource == r.resource {
					return true
				}
			}
		}
		return false
	}}
}

// ruleWords are the words the text of an example of rules has (see
// kindMatch): the kind of each rule, or its group when the kind is not
// known; nil when a rule matches any group, or the core group, whose name is
// in no text.
func ruleWords(rules []kindRule) []string {
	words := make([]string, 0, len(rules))
	for _, r := range rules {
		switch {
		case r.kind != "":
			words = append(words, r.kind)
		case r.group != "" && r.group != "*":
			words = append(words, r.group)
		default:
			return nil
		}
	}
	return words
}

// inferArgs are the arguments of inferType: the examples' text, the line of
// the one document to take, from 1, the path of the value in each example,
// as a .cellens path writes it, whether to list required fields, and the
// value the schema is for, as typeChoices takes it (see valueKinds).
type inferArgs struct {
	Text     *string         `json:"text"`
	Line     int             `json:"line"`
	Path     string          `json:"path"`
	Required bool            `json:"required"`
	Value    json.RawMessage `json:"value"`
}

// inferKinds returns the kinds of the value the arguments of inferType name
// with value, as valueKinds gives them; none without one. It reads the
// session, so it runs on the message loop, before the job. Arguments that
// are no object, or a value that cannot be read, are InvalidParams.
func (ss *session) inferKinds(ctx context.Context, args []json.RawMessage) ([]infer.Kind, error) {
	if len(args) == 0 {
		return nil, nil // inferType says what is missing
	}
	var a inferArgs
	if err := json.Unmarshal(args[0], &a); err != nil {
		return nil, badParams(commandInferType, err)
	}
	if len(a.Value) == 0 || string(a.Value) == "null" {
		return nil, nil
	}
	var va valueArgs
	if err := json.Unmarshal(a.Value, &va); err != nil {
		return nil, badParams(commandInferType+": value", err)
	}
	v, err := ss.readValue(commandInferType, va)
	if err != nil {
		return nil, err
	}
	return ss.valueKinds(ctx, v), nil
}

// inferType works out the OpenAPI schema of the examples args give, for a
// schema: entry (see package infer), for a value whose root can be one of
// kinds (inferKinds). A wrong argument, or a text it cannot read, is
// InvalidParams with the reason.
func inferType(ctx context.Context, args []json.RawMessage, kinds []infer.Kind) (*infer.Result, error) {
	var a inferArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args[0], &a); err != nil {
			return nil, badParams(commandInferType, err)
		}
	}
	if a.Text == nil {
		return nil, invalid(commandInferType + ` takes {text: "..."}: one or more examples, YAML or JSON`)
	}
	p, err := cellens.ParsePath(a.Path)
	if err != nil {
		return nil, invalid(commandInferType + ": path: " + err.Error())
	}
	var steps []string
	for _, s := range p {
		switch s.Kind {
		case cellens.FieldStep:
			steps = append(steps, s.Name)
		case cellens.ItemsStep:
			steps = append(steps, assume.Items)
		case cellens.ValuesStep:
			steps = append(steps, assume.Values)
		}
	}
	res, err := infer.Schema(ctx, []byte(*a.Text), infer.Options{Required: a.Required, Line: a.Line, Path: steps, Kinds: kinds})
	if cerr := ctx.Err(); cerr != nil {
		return nil, cerr
	}
	if err != nil {
		return nil, invalid(commandInferType + ": " + err.Error())
	}
	return &res, nil
}
