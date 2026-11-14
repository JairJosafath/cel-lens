package lsp

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/cel-lens/engine/internal/inline"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// RuleList is the answer of cel-lens.ruleList: the documents with CEL rules
// of the workspace folders and of the open files, in the order of the
// folders, then of the files' paths, then of the file, and the generation of
// the list. Full is true when they are all of them; else they are those of
// the files whose entry changed after the generation asked for, each file
// whole, and Removed are the URIs of the files listed then and not now.
type RuleList struct {
	KubernetesVersion string         `json:"kubernetesVersion"`
	Generation        int64          `json:"generation"`
	Full              bool           `json:"full"`
	Documents         []RuleDocument `json:"documents"`
	Removed           []string       `json:"removed,omitempty"`
}

// RuleDocument is a document with CEL rules: the URI of its file (the
// editor's for an open file), the deepest workspace folder holding the file,
// as a file URI, "" and left out for an open file outside every folder, the
// file's slash-separated path below that folder (outside every folder, its
// path, or its URI when it is no file), the document as
// cel-lens.exampleResults names it, its kind and metadata.name, its range in
// the file and its rules, in the order of the file.
type RuleDocument struct {
	URI      string       `json:"uri"`
	Folder   string       `json:"folder,omitempty"`
	File     string       `json:"file"`
	Document string       `json:"document"`
	Kind     string       `json:"kind"`
	Name     string       `json:"name"`
	Range    lspRange     `json:"range"`
	Rules    []ListedRule `json:"rules"`
}

// ListedRule is a rule of a RuleDocument: one entry of a list that holds
// CEL. It is named as cel-lens.exampleResults names it: a
// CustomResourceDefinition's by its version, path and index, a policy's by
// its field (validation, matchCondition, variable, auditAnnotation or
// mutation), name (a match condition's or variable's name, an audit
// annotation's key) and index. ID is the file's URI, as a file URI is
// written from its path whoever opened it, "#", and those parts joined
// with "#", the document's name followed by "#2" for the second document
// of the same kind and name in the file: it does not change with the
// rule's text or its line. Label and LabelFrom are what inline.RuleLabel
// makes of it, Location the place of its expression as cel-lens.showRule
// takes it, Span the range from the line of its list item to the end of the
// item, Compile what the check of its file says of it, Examples what its
// examples say, nil when there is nothing to say (see withExamples), and
// Problem whether either says something is wrong: Compile's state is error
// or warning, or Examples' is wrong or error.
type ListedRule struct {
	ID        string        `json:"id"`
	Document  string        `json:"document"`
	Version   *string       `json:"version,omitempty"`
	Path      *string       `json:"path,omitempty"`
	Field     string        `json:"field,omitempty"`
	Name      string        `json:"name,omitempty"`
	Index     int           `json:"index"`
	Label     string        `json:"label"`
	LabelFrom string        `json:"labelFrom"`
	Location  RuleLocation  `json:"location"`
	Span      lspRange      `json:"span"`
	Compile   CompileState  `json:"compile"`
	Examples  *RuleExamples `json:"examples"`
	Problem   bool          `json:"problem"`
}

// RuleExamples is what a rule's examples say, as the line above the rule
// says it: State is wrong, error, pass or notRun, and Words the line's part
// about the examples, before the line shortens it (see inline.ExamplesState).
type RuleExamples struct {
	State string `json:"state"`
	Words string `json:"words"`
}

// CompileState is what the check of a rule's file says of the rule: State
// is error when a diagnostic of severity error starts in one of its
// expressions, or one kept the check from compiling them (a document that
// cannot be read, a CRD schema the API server refuses before the rules),
// else warning for a warning, else ok; Problems counts those diagnostics,
// and Messages are the first three, those that kept the expressions from
// being compiled first, then in the order of the file.
type CompileState struct {
	State    string           `json:"state"`
	Problems int              `json:"problems"`
	Messages []CompileMessage `json:"messages"`
}

// CompileMessage is a diagnostic of a rule: its message and its severity,
// error or warning.
type CompileMessage struct {
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// The compile states of a rule.
const (
	compileOK      = "ok"
	compileWarning = "warning"
	compileError   = "error"
)

// maxCompileMessages is how many messages a CompileState gives.
const maxCompileMessages = 3

// The words a file with a document cel-lens.ruleList lists has: the kind of
// a CustomResourceDefinition, the end of the kinds of the admission
// policies, and Kyverno's ValidatingPolicy.
const (
	ruleWordCRD     = "CustomResourceDefinition"
	ruleWordPolicy  = "AdmissionPolicy"
	ruleWordKyverno = "ValidatingPolicy"
)

// hasRuleWords reports whether text may hold a document cel-lens.ruleList
// lists: whether it has CustomResourceDefinition, AdmissionPolicy or, with
// kyverno, ValidatingPolicy. A text without them is not parsed.
func hasRuleWords(text []byte, kyverno bool) bool {
	return bytes.Contains(text, []byte(ruleWordCRD)) || bytes.Contains(text, []byte(ruleWordPolicy)) ||
		kyverno && bytes.Contains(text, []byte(ruleWordKyverno))
}

// listedDoc is a document with rules as listRules finds it, with no
// compile states yet: its rules' compile states come from the check of the
// file (see withCompile).
type listedDoc struct {
	doc RuleDocument
	// cel are the places of the expressions of each rule, by its index in
	// doc.Rules: a CRD rule's rule and messageExpression, a validation's
	// expression and messageExpression, and the one expression of each
	// other rule.
	cel [][]celPlace
	// policy is true for an admission policy or a Kyverno policy, whose
	// check may read the CRDs the session knows.
	policy bool
	// ids name each rule's expression as the check of the examples names
	// what it did (see ruleExamples), by its index in doc.Rules.
	ids []ruleID
}

// celPlace is where an expression is written in its file.
type celPlace struct{ start, end extract.Position }

// The policy lists that hold CEL, and the field each one's rules have.
var policyFields = map[string]string{
	"validations":      "validation",
	"matchConditions":  "matchCondition",
	"variables":        "variable",
	"auditAnnotations": "auditAnnotation",
	"mutations":        "mutation",
}

// listRules lists the documents of text, the file at uri, that cel-lens
// checks and that hold CEL, with their rules, in the order of the file: a
// CustomResourceDefinition of apiextensions.k8s.io/v1, a
// ValidatingAdmissionPolicy and a MutatingAdmissionPolicy of
// admissionregistration.k8s.io, and, with kyverno, a Kyverno ValidatingPolicy
// of policies.kyverno.io/v1 in the evaluation mode Kubernetes or JSON, which
// are the ones package check checks. A UTF-16 text is read in UTF-8, as
// check.Check reads it, and a text without the words of those kinds is not
// parsed. A document that does not parse is left out. The error is
// ctx's.
func listRules(ctx context.Context, uri string, text []byte, kyverno bool) ([]listedDoc, error) {
	if text = extract.UTF8(text); !hasRuleWords(text, kyverno) {
		return nil, nil
	}
	docs, _, err := extract.Documents(ctx, uri, text)
	if err != nil {
		return nil, err
	}
	exprs, _ := extract.FindIn(ctx, uri, text, docs, nil)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	byDoc := map[int][]extract.Expression{}
	for _, e := range exprs {
		byDoc[e.Document] = append(byDoc[e.Document], e)
	}
	lines := splitLines(text)
	idURI := uri
	if path, ok := diskPath(uri); ok {
		idURI = pathURI(path)
	}
	seen := map[string]int{}
	var out []listedDoc
	for i, d := range docs {
		root := yamlResolve(d.Node)
		kind, ok := listedKind(root, kyverno)
		if !ok || len(byDoc[d.Index]) == 0 {
			continue
		}
		name := yamlString(yamlChild(yamlChild(root, "metadata"), "name"))
		document := strings.TrimSpace(kind + " " + name)
		next := extract.Position{Line: len(lines) + 1, Column: 1}
		if i+1 < len(docs) {
			next = nodeStart(docs[i+1].Node)
		}
		ld := listedDoc{policy: kind != ruleWordCRD}
		items := map[string]int{}
		var nodes []*yaml.Node
		positions := nodePositions(root)
		for _, e := range byDoc[d.Index] {
			r, item, ok := ruleOf(root, kind, e)
			if !ok {
				continue
			}
			if at, ok := items[item]; ok {
				ld.cel[at] = append(ld.cel[at], celPlace{e.Start, e.End})
				continue
			}
			if !r.main {
				continue // a messageExpression without its rule, which is no rule
			}
			items[item] = len(ld.doc.Rules)
			ld.cel = append(ld.cel, []celPlace{{e.Start, e.End}})
			ld.ids = append(ld.ids, ruleID{e.Document, e.FieldPath, e.Text})
			nodes = append(nodes, r.node)
			r.rule.Document = document
			r.rule.Label, r.rule.LabelFrom = r.label, r.labelFrom
			r.rule.Location = RuleLocation{URI: uri, Range: toRange(lines, e.Start, e.End)}
			from := extract.Position{Line: r.node.Line, Column: 1}
			r.rule.Span = toRange(lines, from, endOf(lines, positions, r.node, next))
			ld.doc.Rules = append(ld.doc.Rules, r.rule)
		}
		if len(ld.doc.Rules) == 0 {
			continue
		}
		seen[document]++
		idDoc := document
		if n := seen[document]; n > 1 {
			idDoc += "#" + strconv.Itoa(n)
		}
		for j := range ld.doc.Rules {
			ld.doc.Rules[j].ID = idURI + "#" + idDoc + "#" + ruleIDParts(ld.doc.Rules[j])
		}
		sortRules(&ld, nodes)
		start := nodeStart(d.Node)
		ld.doc.URI, ld.doc.Document, ld.doc.Kind, ld.doc.Name = uri, document, kind, name
		ld.doc.Range = toRange(lines, extract.Position{Line: start.Line, Column: 1}, endBefore(lines, next, start.Line))
		out = append(out, ld)
	}
	return out, nil
}

// ruleIDParts are the parts of the id of r after its document: a CRD
// rule's version, path and index, a policy rule's field, name and index.
func ruleIDParts(r ListedRule) string {
	if r.Version != nil && r.Path != nil {
		return *r.Version + "#" + *r.Path + "#" + strconv.Itoa(r.Index)
	}
	return r.Field + "#" + r.Name + "#" + strconv.Itoa(r.Index)
}

// sortRules puts the rules of d, with their expressions, in the order of
// the file: that of nodes, their list items.
func sortRules(d *listedDoc, nodes []*yaml.Node) {
	order := make([]int, len(nodes))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Or(nodes[a].Line-nodes[b].Line, nodes[a].Column-nodes[b].Column)
	})
	rules := make([]ListedRule, len(order))
	cel := make([][]celPlace, len(order))
	ids := make([]ruleID, len(order))
	for i, o := range order {
		rules[i], cel[i], ids[i] = d.doc.Rules[o], d.cel[o], d.ids[o]
	}
	d.doc.Rules, d.cel, d.ids = rules, cel, ids
}

// listedKind returns the kind of the document root when cel-lens.ruleList
// lists its kind: a CustomResourceDefinition of apiextensions.k8s.io/v1, a
// ValidatingAdmissionPolicy or MutatingAdmissionPolicy of
// admissionregistration.k8s.io, or, with kyverno, a ValidatingPolicy of
// policies.kyverno.io/v1 whose evaluation mode Kyverno's compiler checks.
func listedKind(root *yaml.Node, kyverno bool) (string, bool) {
	apiVersion := yamlString(yamlChild(root, "apiVersion"))
	kind := yamlString(yamlChild(root, "kind"))
	group, _, _ := strings.Cut(apiVersion, "/")
	switch {
	case apiVersion == "apiextensions.k8s.io/v1" && kind == ruleWordCRD:
	case group == "admissionregistration.k8s.io" && (kind == "ValidatingAdmissionPolicy" || kind == "MutatingAdmissionPolicy"):
	case kyverno && apiVersion == "policies.kyverno.io/v1" && kind == ruleWordKyverno:
		switch yamlString(yamlChild(yamlChild(yamlChild(root, "spec"), "evaluation"), "mode")) {
		case "", "Kubernetes", "JSON":
		default:
			return "", false // evaluated by another engine, which the check does not check
		}
	default:
		return "", false
	}
	return kind, true
}

// foundRule is a rule of a document as ruleOf finds it from one of its
// expressions: the rule with its name, the words of its label, its list
// item, and whether the expression is the rule's own, its rule or
// expression, not a messageExpression.
type foundRule struct {
	rule             ListedRule
	label, labelFrom string
	node             *yaml.Node
	main             bool
}

// ruleOf returns the rule that the expression e of the document root, of
// kind, is part of, and the field path of its list item, which names it in
// the document. It is false for an expression of no rule cel-lens.ruleList
// lists, such as one of a v1beta1 CRD's spec.validation.
func ruleOf(root *yaml.Node, kind string, e extract.Expression) (foundRule, string, bool) {
	if kind == ruleWordCRD {
		return crdRuleOf(root, e)
	}
	rest, ok := strings.CutPrefix(e.FieldPath, "spec.")
	if !ok {
		return foundRule{}, "", false
	}
	list, rest, ok := strings.Cut(rest, "[")
	field, known := policyFields[list]
	if !ok || !known {
		return foundRule{}, "", false
	}
	index, rest, ok := strings.Cut(rest, "].")
	i, err := strconv.Atoi(index)
	if !ok || err != nil {
		return foundRule{}, "", false
	}
	item := "spec." + list + "[" + index + "]"
	node := yamlIndex(yamlChild(yamlChild(root, "spec"), list), i)
	if node == nil {
		return foundRule{}, "", false
	}
	var words inline.RuleWords
	name := ""
	switch field {
	case "validation":
		words.Message = yamlString(yamlChild(node, "message"))
	case "matchCondition", "variable":
		name = yamlString(yamlChild(node, "name"))
		words.Name = name
	case "auditAnnotation":
		name = yamlString(yamlChild(node, "key"))
		words.Key = name
	}
	words.Expression = e.Text
	label, from := inline.RuleLabel(words)
	return foundRule{
		rule:  ListedRule{Field: field, Name: name, Index: i},
		label: label, labelFrom: from, node: node,
		main: rest != "messageExpression",
	}, item, true
}

// crdRuleOf is ruleOf for a CustomResourceDefinition: e is the rule or the
// messageExpression of an x-kubernetes-validations entry of the schema of a
// version.
func crdRuleOf(root *yaml.Node, e extract.Expression) (foundRule, string, bool) {
	item, ok := strings.CutSuffix(e.FieldPath, "."+string(e.Kind))
	if !ok || e.Kind != extract.KindRule && e.Kind != extract.KindMessageExpression {
		return foundRule{}, "", false
	}
	steps, ok := fieldSteps(item)
	// spec, versions, [n], schema, openAPIV3Schema, ..., x-kubernetes-validations, [i]
	if !ok || len(steps) < 7 || steps[0] != "spec" || steps[1] != "versions" || steps[3] != "schema" || steps[4] != "openAPIV3Schema" || steps[len(steps)-2] != "x-kubernetes-validations" {
		return foundRule{}, "", false
	}
	v, err := strconv.Atoi(steps[2])
	i, ierr := strconv.Atoi(steps[len(steps)-1])
	path, pok := schemaPath(steps[5 : len(steps)-2])
	if err != nil || ierr != nil || !pok {
		return foundRule{}, "", false
	}
	node := yamlAt(root, steps)
	if node == nil {
		return foundRule{}, "", false
	}
	version := yamlString(yamlChild(yamlIndex(yamlChild(yamlChild(root, "spec"), "versions"), v), "name"))
	label, from := inline.RuleLabel(inline.RuleWords{Message: yamlString(yamlChild(node, "message")), Expression: e.Text})
	return foundRule{
		rule:  ListedRule{Version: &version, Path: &path, Index: i},
		label: label, labelFrom: from, node: node,
		main: e.Kind == extract.KindRule,
	}, item, true
}

// fieldSteps splits a field path as package extract writes it, such as
// spec.versions[0].schema.openAPIV3Schema.properties[spec].x-kubernetes-validations[1],
// into its steps: the names, and what is between brackets, an index or a
// property's name, which may hold dots and brackets itself and ends at the
// first "]" that a "." or the end of the path follows. It is false for a
// path it cannot split.
func fieldSteps(path string) ([]string, bool) {
	var out []string
	for path != "" {
		if rest, ok := strings.CutPrefix(path, "["); ok {
			end := -1
			for i := range len(rest) {
				if rest[i] == ']' && (i+1 == len(rest) || rest[i+1] == '.' || rest[i+1] == '[') {
					end = i
					break
				}
			}
			if end < 0 {
				return nil, false
			}
			out = append(out, rest[:end])
			path = rest[end+1:]
		} else {
			path = strings.TrimPrefix(path, ".")
			end := strings.IndexAny(path, ".[")
			if end < 0 {
				end = len(path)
			}
			if end == 0 {
				return nil, false
			}
			out = append(out, path[:end])
			path = path[end:]
		}
	}
	return out, true
}

// schemaPath writes the steps below openAPIV3Schema to a schema node, such
// as properties, spec, items, as a .cellens entry and
// cel-lens.exampleResults write a path: spec.ports[] ([] for the items of a
// list, [*] for the values of a map, "" for the root). It is false for a
// step that is none of those.
func schemaPath(steps []string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(steps); i++ {
		switch steps[i] {
		case "properties":
			if i+1 >= len(steps) {
				return "", false
			}
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(steps[i+1])
			i++
		case "items":
			b.WriteString("[]")
		case "additionalProperties":
			b.WriteString("[*]")
		default:
			return "", false
		}
	}
	return b.String(), true
}

// withCompile returns the documents of docs with the compile state of each
// rule, from diags, the check of their file, which sorts them by position.
// It changes nothing of docs.
func withCompile(docs []listedDoc, diags []check.Diagnostic) []RuleDocument {
	out := make([]RuleDocument, len(docs))
	for i, d := range docs {
		out[i] = d.doc
		out[i].Rules = slices.Clone(d.doc.Rules)
		for j := range out[i].Rules {
			c := compileState(d.cel[j], diags)
			out[i].Rules[j].Compile, out[i].Rules[j].Problem = c, c.State != compileOK
		}
	}
	return out
}

// compileState is the compile state of a rule whose expressions are at cel,
// from the diagnostics of its file that start in one of them and those that
// kept one of them from being compiled (check.Diagnostic.Uncompiled): such a
// rule is an error, whatever its CEL, and those diagnostics come first.
func compileState(cel []celPlace, diags []check.Diagnostic) CompileState {
	s := CompileState{State: compileOK, Messages: []CompileMessage{}}
	var stopped, found []check.Diagnostic
	for _, d := range diags {
		switch {
		case slices.ContainsFunc(cel, func(c celPlace) bool { return slices.Contains(d.Uncompiled, c.start) }):
			stopped = append(stopped, d)
		case slices.ContainsFunc(cel, func(c celPlace) bool { return !before(d.Start, c.start) && !before(c.end, d.Start) }):
			found = append(found, d)
		}
	}
	for _, d := range append(stopped, found...) {
		s.Problems++
		switch {
		case d.Severity == check.SeverityError || len(d.Uncompiled) > 0:
			s.State = compileError
		case s.State == compileOK:
			s.State = compileWarning
		}
		if len(s.Messages) < maxCompileMessages {
			s.Messages = append(s.Messages, CompileMessage{Message: d.Message, Severity: string(d.Severity)})
		}
	}
	return s
}

// nodeStart is where the content of the document node doc starts.
func nodeStart(doc *yaml.Node) extract.Position {
	if n := yamlResolve(doc); n != nil {
		return extract.Position{Line: n.Line, Column: n.Column}
	}
	return extract.Position{Line: doc.Line, Column: doc.Column}
}

// nodePositions lists where each node of the tree below n is written,
// sorted, aliases not followed.
func nodePositions(n *yaml.Node) []extract.Position {
	var out []extract.Position
	var walk func(*yaml.Node)
	walk = func(m *yaml.Node) {
		out = append(out, extract.Position{Line: m.Line, Column: m.Column})
		for _, c := range m.Content {
			walk(c)
		}
	}
	if n != nil {
		walk(n)
	}
	slices.SortFunc(out, func(a, b extract.Position) int { return cmp.Or(a.Line-b.Line, a.Column-b.Column) })
	return out
}

// endOf is where the list item item ends: before the first node of its
// document, whose nodes are written at positions, after the last of its
// own, or before next, where the next document starts, when none follows.
func endOf(lines [][]rune, positions []extract.Position, item *yaml.Node, next extract.Position) extract.Position {
	last := extract.Position{Line: item.Line, Column: item.Column}
	for _, p := range nodePositions(item) {
		if before(last, p) {
			last = p
		}
	}
	i, _ := slices.BinarySearchFunc(positions, last, func(a, b extract.Position) int { return cmp.Or(a.Line-b.Line, a.Column-b.Column) })
	for i < len(positions) && !before(last, positions[i]) {
		i++
	}
	if i < len(positions) && before(positions[i], next) {
		next = positions[i]
	}
	return endBefore(lines, next, item.Line)
}

// endBefore is where the text before next ends, no higher than the line
// floor: on next's line, after what is before next there but blanks, commas
// and the dashes of list items, when that is something; else at the end of
// the last line above with something on it but a comment or a document
// marker; else at the end of floor.
func endBefore(lines [][]rune, next extract.Position, floor int) extract.Position {
	if next.Line >= 1 && next.Line <= len(lines) {
		line := lines[next.Line-1]
		prefix := strings.TrimRightFunc(string(line[:min(max(next.Column-1, 0), len(line))]), func(r rune) bool { return unicode.IsSpace(r) || r == ',' })
		if strings.TrimFunc(prefix, func(r rune) bool { return unicode.IsSpace(r) || r == '-' }) != "" {
			return extract.Position{Line: next.Line, Column: len([]rune(prefix)) + 1}
		}
	}
	end := func(l int) extract.Position {
		return extract.Position{Line: l, Column: len([]rune(strings.TrimRightFunc(string(lines[l-1]), unicode.IsSpace))) + 1}
	}
	for l := min(next.Line-1, len(lines)); l > floor; l-- {
		t := strings.TrimSpace(string(lines[l-1]))
		if t == "" || strings.HasPrefix(t, "#") || t == "---" || strings.HasPrefix(t, "--- ") || t == "..." {
			continue
		}
		return end(l)
	}
	if floor >= 1 && floor <= len(lines) {
		return end(floor)
	}
	return next
}

// yamlResolve follows an alias to the node it stands for and unwraps a
// document.
func yamlResolve(n *yaml.Node) *yaml.Node {
	for range 100 { // an alias cannot lead to itself, but a bound costs nothing
		switch {
		case n == nil:
			return nil
		case n.Kind == yaml.AliasNode:
			n = n.Alias
		case n.Kind == yaml.DocumentNode && len(n.Content) > 0:
			n = n.Content[0]
		case n.Kind == yaml.DocumentNode:
			return nil
		default:
			return n
		}
	}
	return nil
}

// yamlChild returns the value of key in the mapping n, nil when it has none:
// its own key, the first when it is repeated, else that of the first
// mapping it merges in with "<<" that has it.
func yamlChild(n *yaml.Node, key string) *yaml.Node {
	return yamlChildSeen(n, key, map[*yaml.Node]bool{})
}

func yamlChildSeen(n *yaml.Node, key string, seen map[*yaml.Node]bool) *yaml.Node {
	n = yamlResolve(n)
	if n == nil || n.Kind != yaml.MappingNode || seen[n] {
		return nil
	}
	seen[n] = true
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i]; k.ShortTag() != "!!merge" && k.Value == key {
			return n.Content[i+1]
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].ShortTag() != "!!merge" {
			continue
		}
		sources := []*yaml.Node{n.Content[i+1]}
		if v := yamlResolve(n.Content[i+1]); v != nil && v.Kind == yaml.SequenceNode {
			sources = v.Content
		}
		for _, src := range sources {
			if v := yamlChildSeen(src, key, seen); v != nil {
				return v
			}
		}
	}
	return nil
}

// yamlIndex returns the item i of the list n, nil when there is none.
func yamlIndex(n *yaml.Node, i int) *yaml.Node {
	n = yamlResolve(n)
	if n == nil || n.Kind != yaml.SequenceNode || i < 0 || i >= len(n.Content) {
		return nil
	}
	return n.Content[i]
}

// yamlAt follows steps (see fieldSteps) from n: a name, or what is between
// brackets, is a key of a mapping or the index of a list. It returns the
// node the last step leads to, as written, nil when there is none.
func yamlAt(n *yaml.Node, steps []string) *yaml.Node {
	for _, s := range steps {
		r := yamlResolve(n)
		if r == nil {
			return nil
		}
		if r.Kind == yaml.SequenceNode {
			i, err := strconv.Atoi(s)
			if err != nil {
				return nil
			}
			n = yamlIndex(r, i)
		} else {
			n = yamlChild(r, s)
		}
	}
	return n
}

// yamlString is the value of the scalar n, "" when n is none.
func yamlString(n *yaml.Node) string {
	if n = yamlResolve(n); n != nil && n.Kind == yaml.ScalarNode {
		return n.Value
	}
	return ""
}
