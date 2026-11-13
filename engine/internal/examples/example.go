// Package examples finds the examples of a CustomResourceDefinition among the
// YAML documents of a workspace, says what each expects, and checks each as
// the API server checks a custom resource before its CEL rules.
//
// An example of a CustomResourceDefinition is a document whose apiVersion
// and kind are a version the CustomResourceDefinition serves, in another
// file than the CustomResourceDefinition's own. It expects to be allowed,
// unless its path says it is refused (a folder or a word of its file name is
// invalid, bad, refused, rejected or denied), or a # cel-lens: comment at the
// top of the document says what it expects, which wins over the path (see
// Find). Two documents of one kind, namespace and name in one file are an
// update: the second is an update from the first, as kubectl apply -f of the
// file makes it.
//
// Check runs the API server's own code on each example
// (k8s.io/apiextensions-apiserver): the decoding of a custom resource, which
// prunes the fields the schema does not know and reports them, as kubectl's
// default strict field validation has it, the CustomResourceDefinition's
// defaults, then the validation of the object's metadata, of its schema, of
// its embedded resources and of its list types.
//
// Run then runs each x-kubernetes-validations rule of the CRD (see Rules) on
// each example with the API server's own CEL validator
// (k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel), creates and
// updates, and says of each result whether it is what the example expects.
package examples

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
	"k8s.io/apiextensions-apiserver/pkg/apihelpers"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crdvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	apiservervalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/JairJosafath/cel-lens/engine/internal/yamlfield"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// CRD is a CustomResourceDefinition the API server accepts, ready to check
// its examples. Read makes it; it does not change after, so it is safe for
// concurrent use.
type CRD struct {
	// File is the name of the file it is in, as Read was given it.
	File string
	// Name is its metadata.name; Group and Kind name the kind it defines,
	// and Plural the resource it is served as.
	Name, Group, Kind, Plural string
	namespaced                bool
	// converts reports whether the API server converts an object of one of
	// its versions to another by setting its apiVersion alone (the
	// conversion strategy None).
	converts bool
	// versions are the versions it serves, by name; names are the names of
	// all its versions, by their index in spec.versions.
	versions map[string]*servedVersion
	names    []string
	// data is the text of File, doc the index of its document there, node
	// that document, and kube the Kubernetes version it was read for: Rules
	// finds its rules with them.
	data []byte
	doc  int
	node *yaml.Node
	kube kubeversion.Version
}

// servedVersion is what the API server checks a custom resource of one
// version with.
type servedVersion struct {
	// name is the version's name, such as v1.
	name string
	// structural is the version's schema, its defaults pruned, as the API
	// server prunes, defaults and checks objects with it.
	structural *structuralschema.Structural
	validator  apiservervalidation.SchemaValidator
	// status is true when the version has the status subresource: the API
	// server then ignores the status of a create, and keeps the old one on
	// an update.
	status bool
	// rules is the schema with no rule, and its key, made once when Rules
	// first needs it (see bareSchema).
	rules     sync.Once
	bare      *structuralschema.Structural
	schemaKey string
}

// Read returns the CustomResourceDefinitions in data, the text of file, that
// the API server would create, for the Kubernetes version v, with the
// versions they serve. One the API server refuses, for anything but its
// x-kubernetes-validations rules, which the API server only compiles, is
// left out: its problems are already on it, and its examples get none. A
// document that is no apiextensions.k8s.io/v1 CustomResourceDefinition, or
// whose YAML does not parse, gives none. A cancelled ctx gives none.
func Read(ctx context.Context, file string, data []byte, v kubeversion.Version) []*CRD {
	out, _ := ReadWithProblems(ctx, file, data, v)
	return out
}

// ReadWithProblems is Read, with the problems of each CustomResourceDefinition
// it leaves out: for a field of the wrong type, the problem check gives, at
// the value (such as `n`, which kubectl reads as a boolean where a string is
// wanted); for another reason, one that says what the API server refuses, at
// the first line of the document. A CRD's problems with its rules are not
// among them.
func ReadWithProblems(ctx context.Context, file string, data []byte, v kubeversion.Version) ([]*CRD, []check.Diagnostic) {
	if !bytes.Contains(data, []byte("CustomResourceDefinition")) {
		return nil, nil
	}
	docs, _, err := extract.Documents(ctx, file, data)
	if err != nil {
		return nil, nil
	}
	var (
		out      []*CRD
		problems []check.Diagnostic
	)
	for _, d := range docs {
		if ctx.Err() != nil {
			return nil, nil // validating another would hold the version (see check.UnderVersion)
		}
		if apiVersion, kind, _ := typeMeta(d.Node); apiVersion != "apiextensions.k8s.io/v1" || kind != "CustomResourceDefinition" {
			continue
		}
		c, why := readCRD(ctx, d.Node, v)
		if c == nil {
			if why != "" {
				problems = append(problems, unreadable(ctx, file, data, d.Node, &apiextensionsv1.CustomResourceDefinition{}, why)...)
			}
			continue
		}
		c.File, c.data, c.doc, c.node, c.kube = file, data, d.Index, d.Node, v
		out = append(out, c)
	}
	return out, problems
}

// unreadable gives the problems of the document doc of file, whose text is
// data, which cannot be read as the Kubernetes type of out: those that check
// reports where it reads a document of the type, and, when it reports none,
// one at the first line of the document that says why, as a message.
func unreadable(ctx context.Context, file string, data []byte, doc *yaml.Node, out any, why string) []check.Diagnostic {
	if ctx.Err() != nil {
		return nil
	}
	diags, err := check.ReadProblems(ctx, file, data, doc, out)
	if len(diags) > 0 {
		return diags
	}
	if err != nil {
		why = err.Error()
	}
	_, _, line := typeMeta(doc)
	at := extract.Position{Line: line, Column: 1}
	return []check.Diagnostic{{File: file, Message: why, Severity: check.SeverityError, Start: at, End: at}}
}

// readCRD reads the CustomResourceDefinition doc, false when the API server
// would refuse it, and then, in why, what it refuses; why is "" for a CRD that
// is left out without a word: one that serves no version, or when ctx is
// cancelled.
func readCRD(ctx context.Context, doc *yaml.Node, v kubeversion.Version) (c *CRD, why string) {
	// Read as the engine reads a CustomResourceDefinition to check it (see
	// package check), so a CRD it checks is the one its examples have.
	var raw any
	if err := yamlfield.Decode(doc, &raw); err != nil {
		return nil, "the CustomResourceDefinition cannot be read: " + err.Error()
	}
	js, err := json.Marshal(raw)
	if err != nil {
		return nil, "the CustomResourceDefinition cannot be read: " + err.Error()
	}
	var versioned apiextensionsv1.CustomResourceDefinition
	if json.Unmarshal(js, &versioned) != nil {
		return nil, "the CustomResourceDefinition cannot be read"
	}
	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(&versioned)
	var internal apiextensions.CustomResourceDefinition
	if apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&versioned, &internal, nil) != nil {
		return nil, "the API server refuses the CustomResourceDefinition: it cannot convert it"
	}
	// The rules are left out: compiling them is the costly part of the
	// check, and a rule that does not compile leaves the schema as it is.
	withoutRules := internal.DeepCopy()
	dropRules(withoutRules.Spec.Validation)
	for i := range withoutRules.Spec.Versions {
		dropRules(withoutRules.Spec.Versions[i].Schema)
	}
	var errs field.ErrorList
	err = check.UnderVersion(v, func() { errs = crdvalidation.ValidateCustomResourceDefinition(ctx, withoutRules) })
	switch {
	case ctx.Err() != nil:
		return nil, ""
	case err != nil:
		return nil, "the API server refuses the CustomResourceDefinition: " + err.Error()
	case len(errs) > 0:
		return nil, "the API server refuses the CustomResourceDefinition: " + errs.ToAggregate().Error()
	}
	c = &CRD{
		Name: versioned.Name, Group: versioned.Spec.Group, Kind: versioned.Spec.Names.Kind, Plural: versioned.Spec.Names.Plural,
		namespaced: versioned.Spec.Scope == apiextensionsv1.NamespaceScoped,
		converts:   versioned.Spec.Conversion == nil || versioned.Spec.Conversion.Strategy == apiextensionsv1.NoneConverter,
		versions:   map[string]*servedVersion{},
	}
	for _, ver := range versioned.Spec.Versions {
		c.names = append(c.names, ver.Name)
		if !ver.Served {
			continue
		}
		sv, ok := readVersion(&versioned, ver.Name, v)
		if !ok {
			return nil, "the API server refuses the CustomResourceDefinition: its version " + ver.Name + " cannot be read"
		}
		c.versions[ver.Name] = sv
	}
	if len(c.versions) == 0 {
		return nil, "" // no example can be of a kind it does not serve
	}
	return c, ""
}

// readVersion makes what the API server checks a custom resource of the
// version name of crd with, as its handler for the resource makes it.
func readVersion(crd *apiextensionsv1.CustomResourceDefinition, name string, v kubeversion.Version) (*servedVersion, bool) {
	val, err := apihelpers.GetSchemaForVersion(crd, name)
	if err != nil || val == nil {
		return nil, false
	}
	internal := &apiextensions.CustomResourceValidation{}
	if apiextensionsv1.Convert_v1_CustomResourceValidation_To_apiextensions_CustomResourceValidation(val, internal, nil) != nil {
		return nil, false
	}
	s, err := structuralschema.NewStructural(internal.OpenAPIV3Schema)
	if err != nil {
		return nil, false
	}
	s = s.DeepCopy()
	if defaulting.PruneDefaults(s) != nil {
		return nil, false
	}
	validator, _, err := apiservervalidation.NewSchemaValidatorForVersion(internal.OpenAPIV3Schema, v.Compat())
	if err != nil {
		return nil, false
	}
	sub, err := apihelpers.GetSubresourcesForVersion(crd, name)
	if err != nil {
		return nil, false
	}
	return &servedVersion{name: name, structural: s, validator: validator, status: sub != nil && sub.Status != nil}, true
}

// dropRules removes the x-kubernetes-validations of the schema of v, and of
// every schema below it.
func dropRules(v *apiextensions.CustomResourceValidation) {
	if v != nil {
		dropSchemaRules(v.OpenAPIV3Schema)
	}
}

func dropSchemaRules(s *apiextensions.JSONSchemaProps) {
	if s == nil {
		return
	}
	s.XValidations = nil
	for _, m := range []map[string]apiextensions.JSONSchemaProps{s.Properties, s.PatternProperties, s.Definitions} {
		for k, p := range m {
			dropSchemaRules(&p)
			m[k] = p
		}
	}
	for _, l := range [][]apiextensions.JSONSchemaProps{s.AllOf, s.AnyOf, s.OneOf} {
		for i := range l {
			dropSchemaRules(&l[i])
		}
	}
	dropSchemaRules(s.Not)
	if s.Items != nil {
		dropSchemaRules(s.Items.Schema)
		for i := range s.Items.JSONSchemas {
			dropSchemaRules(&s.Items.JSONSchemas[i])
		}
	}
	if s.AdditionalProperties != nil {
		dropSchemaRules(s.AdditionalProperties.Schema)
	}
	if s.AdditionalItems != nil {
		dropSchemaRules(s.AdditionalItems.Schema)
	}
	for k, d := range s.Dependencies {
		dropSchemaRules(d.Schema)
		s.Dependencies[k] = d
	}
}

// serves reports whether c serves the version of apiVersion, for kind.
func (c *CRD) serves(apiVersion, kind string) (*servedVersion, bool) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil || kind != c.Kind || gv.Group != c.Group {
		return nil, false
	}
	sv, ok := c.versions[gv.Version]
	return sv, ok
}

// Expectation is what an example expects of the API server.
type Expectation int

// The expectations.
const (
	// Allowed is an example the API server should accept.
	Allowed Expectation = iota
	// Refused is an example the API server should refuse.
	Refused
)

// String is "allowed" or "refused".
func (e Expectation) String() string {
	if e == Refused {
		return "refused"
	}
	return "allowed"
}

// Object is an example of a CustomResourceDefinition: a document of a file
// whose apiVersion and kind are a version the CustomResourceDefinition
// serves; or a document of a built-in kind an admission policy may run on
// (see FindBuiltIn).
type Object struct {
	// CRD is the CustomResourceDefinition it is an example of; nil for a
	// built-in kind.
	CRD *CRD
	// File is the name of its file, as Find was given it; Line is the line
	// its content starts at, from 1.
	File string
	Line int
	// APIVersion, Kind, Namespace and Name are as the document writes them,
	// "" where it has none.
	APIVersion, Kind, Namespace, Name string
	// Expect is what it expects; Message, for one that expects to be
	// refused, is a part of the message it expects, "" for any.
	Expect  Expectation
	Message string
	// Old is the example it is an update from, the document of the same
	// kind, namespace and name before it in the file; nil for a create.
	Old *Object
	// Problems are what the API server refuses in it before its CEL rules,
	// as Check found them, with the API server's messages, each at the
	// field it names; none before Check.
	Problems []check.Diagnostic
	// Object is the object as the API server would hand it to the CEL
	// rules, its defaults applied, as Check made it; nil before Check, and
	// when the API server cannot decode it.
	Object map[string]any
	// Errors are what the API server's validation refuses in Object before
	// the CEL rules, as it returns them, in its order; Problems has each.
	// None before Check, and when the API server cannot decode the object.
	Errors field.ErrorList
	// DecodeFailed is true when the API server refuses the example while it
	// decodes it, before any validation: for a field the schema does not
	// know, with the strict field validation kubectl asks for ("strict
	// decoding error: unknown field ..."), or for metadata it cannot read.
	// It then validates nothing and runs no CEL rule; Errors are what it
	// would refuse once the decoding passes.
	DecodeFailed bool
	// RulesSkipped is true when Errors has an error of a type for which the
	// API server runs none of the CEL rules (hasBlockingErr of its
	// customResourceStrategy: NotSupported, Required, TooLong, TooMany or
	// TypeInvalid), and says instead that "some validation rules were not
	// checked because the object was invalid".
	RulesSkipped bool

	// paired is true for a copy Run checked again as an update from prev,
	// what the API server would have stored of the example before it, or as
	// a create when prev is nil (see Object.old).
	paired bool
	prev   *Object
	// clusterScoped is true for a document of a built-in kind that has no
	// namespace.
	clusterScoped bool
	// comments are the problems of its # cel-lens: comments; said is true
	// once one of them said what it expects.
	comments []check.Diagnostic
	said     bool
	node     *yaml.Node
	lines    [][]rune
	version  *servedVersion
}

// Diagnostics returns what to show on the example: the problems of its
// # cel-lens: comments, and its Problems unless it expects to be refused,
// as it then is right to be refused.
func (o *Object) Diagnostics() []check.Diagnostic {
	out := append([]check.Diagnostic(nil), o.comments...)
	if o.Expect != Refused {
		out = append(out, o.Problems...)
	}
	return out
}

// Find returns the examples of crds among the documents of data, the text of
// file, whose path below its workspace folder, slash-separated, is rel, in
// the order of the file. A CustomResourceDefinition in file has no example
// there. A document whose YAML does not parse is skipped. The error is ctx's.
func Find(ctx context.Context, crds []*CRD, file, rel string, data []byte) ([]*Object, error) {
	data = extract.UTF8(data)
	var mine []*CRD
	for _, c := range crds {
		if c.File != file && bytes.Contains(data, []byte(c.Kind)) {
			mine = append(mine, c)
		}
	}
	if len(mine) == 0 {
		return nil, ctx.Err()
	}
	docs, _, err := extract.Documents(ctx, file, data)
	if err != nil {
		return nil, err
	}
	lines := splitLines(data)
	byPath := pathExpectation(rel)
	last := map[string]*Object{}
	var out []*Object
	for _, d := range docs {
		apiVersion, kind, line := typeMeta(d.Node)
		for _, c := range mine {
			sv, ok := c.serves(apiVersion, kind)
			if !ok {
				continue
			}
			o := &Object{CRD: c, File: file, Line: line, APIVersion: apiVersion, Kind: kind, Expect: byPath, node: d.Node, lines: lines, version: sv}
			o.Namespace, o.Name = nameOf(d.Node)
			o.readComments(lines)
			if o.Name != "" {
				key := c.Group + "\x00" + c.Kind + "\x00" + o.namespace() + "\x00" + o.Name
				o.Old = last[key]
				last[key] = o
			}
			out = append(out, o)
			break
		}
	}
	return out, nil
}

// namespace is the namespace the API server puts the example in: none for a
// cluster-scoped kind, and default, as kubectl takes it, for a namespaced
// one that names none.
func (o *Object) namespace() string {
	switch {
	case !o.CRD.namespaced:
		return ""
	case o.Namespace == "":
		return "default"
	}
	return o.Namespace
}

// typeMeta returns the apiVersion and kind of doc, strings both, and the line
// its content starts at, from 1; "" for one that is no mapping or lacks them.
func typeMeta(doc *yaml.Node) (apiVersion, kind string, line int) {
	n := content(doc)
	if n == nil || n.Kind != yaml.MappingNode {
		return "", "", 0
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if v.Kind != yaml.ScalarNode || v.ShortTag() != "!!str" {
			continue
		}
		switch k.Value {
		case "apiVersion":
			apiVersion = v.Value
		case "kind":
			kind = v.Value
		}
	}
	return apiVersion, kind, n.Line
}

// nameOf returns metadata.namespace and metadata.name of doc, "" where it
// has none.
func nameOf(doc *yaml.Node) (namespace, name string) {
	var m struct {
		Metadata struct {
			Namespace any `yaml:"namespace"`
			Name      any `yaml:"name"`
		} `yaml:"metadata"`
	}
	if yamlfield.Decode(doc, &m) != nil {
		return "", ""
	}
	namespace, _ = m.Metadata.Namespace.(string)
	name, _ = m.Metadata.Name.(string)
	return namespace, name
}

// content is the root node of doc, nil for an empty document.
func content(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode {
		return doc
	}
	if len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}

// toJSON is doc as kubectl sends it to the API server: its YAML made JSON by
// sigs.k8s.io/yaml, which kubectl reads files with.
func toJSON(doc *yaml.Node) ([]byte, error) {
	text, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("write the document as YAML: %w", err)
	}
	js, err := sigsyaml.YAMLToJSON(text)
	if err != nil {
		return nil, fmt.Errorf("read the document as JSON: %w", err)
	}
	return js, nil
}

// splitLines splits text at line feeds, a carriage return staying at the end
// of its line.
func splitLines(text []byte) [][]rune {
	parts := strings.Split(string(text), "\n")
	out := make([][]rune, len(parts))
	for i, p := range parts {
		out[i] = []rune(p)
	}
	return out
}
