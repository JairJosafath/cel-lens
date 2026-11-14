package assist

import (
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"

	"github.com/JairJosafath/cel-lens/engine/internal/assume"
	"github.com/JairJosafath/cel-lens/engine/internal/hover"
	"github.com/JairJosafath/cel-lens/engine/internal/meaning"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// Rule is what the hover says of a CEL expression as a whole, wherever the
// cursor is in it, each section in Markdown without its label (see
// hover.Sections).
type Rule struct {
	// Checks says what a check expression checks, in a sentence; "" for an
	// expression that is no check, and when package meaning has no
	// sentence for it.
	Checks string
	// Returns says what an expression that is no check gives, in a fixed
	// sentence from its field; "" for a check.
	Returns string
	// Uses lists the variables the expression reads, in the order it first
	// reads them, each once, with its type and what it is; "" when it reads
	// none, or does not parse.
	Uses string
}

// Rule describes the expression e of doc for its hover. It is false for an
// expression the check does not check: one of a CustomResourceDefinition
// whose apiVersion is not apiextensions.k8s.io/v1, of a Kyverno
// ValidatingPolicy while the experimental feature kyverno is off, or of a
// document that does not decode.
func (a *Assistant) Rule(doc *Document, e extract.Expression) (Rule, bool) {
	root, b, ok := a.checked(doc, e)
	if !ok {
		return Rule{}, false
	}
	kind, _ := root["kind"].(string)
	check, isCheck, returns := role(root, e, b.vars != nil)
	doc.memoMu.Lock()
	defer doc.memoMu.Unlock()
	m := doc.memoOf(e.Start)
	r := Rule{Returns: returns}
	if isCheck {
		// The sentence depends only on the text.
		if !m.said {
			if parsed, ok := m.parsed(a.exprs, e.Text); ok {
				m.sentence, _ = meaning.Sentence(parsed, check)
			}
			m.said = true
		}
		r.Checks = m.sentence
	}
	if kind != "CustomResourceDefinition" {
		r.Uses = b.uses(doc, root, e, m.readNames(a.exprs, e.Text))
		return r, true
	}
	// self and oldSelf depend only on the file, whatever the version,
	// features and CRDs, so the section is kept.
	if !m.used {
		m.uses, m.used = b.uses(doc, root, e, m.readNames(a.exprs, e.Text)), true
	}
	r.Uses = m.uses
	return r, true
}

// role says what the expression e of root is for, by its field: a check of
// kind check, or no check, with returns, the sentence that says what it
// gives. kyverno is true for a Kyverno ValidatingPolicy.
func role(root map[string]any, e extract.Expression, kyverno bool) (check meaning.Kind, isCheck bool, returns string) {
	if root["kind"] == "CustomResourceDefinition" {
		if e.Kind == extract.KindMessageExpression {
			return 0, false, "the message the API server gives when the rule is false."
		}
		return meaning.Check, true, ""
	}
	item, field, _ := cutLast(e.FieldPath, ".")
	entry, _ := walk(root, strings.TrimSuffix(strings.TrimSuffix(item, ".applyConfiguration"), ".jsonPatch")).(map[string]any)
	switch {
	case strings.HasPrefix(e.FieldPath, "spec.matchConditions["):
		return meaning.MatchCondition, true, ""
	case strings.HasPrefix(e.FieldPath, "spec.validations[") && field == "expression":
		return meaning.Check, true, ""
	case field == "messageExpression" && kyverno:
		return 0, false, "the message Kyverno gives when the validation is false."
	case field == "messageExpression":
		return 0, false, "the message the API server gives when the rule is false."
	case strings.HasPrefix(e.FieldPath, "spec.variables["):
		name, _ := entry["name"].(string)
		return 0, false, "the value of " + hover.Code("variables."+name) + "."
	case strings.HasPrefix(e.FieldPath, "spec.auditAnnotations["):
		key, _ := entry["key"].(string)
		return 0, false, "the value of the audit annotation " + hover.Code(key) + "."
	case strings.HasSuffix(item, ".applyConfiguration"):
		return 0, false, "the apply configuration the policy applies."
	case strings.HasSuffix(item, ".jsonPatch"):
		return 0, false, "the JSON patch the policy applies."
	}
	return 0, false, ""
}

// checked returns the document of the expression e of doc and the
// Assistant that answers for it, when the check checks it: one of a
// CustomResourceDefinition of apiextensions.k8s.io/v1, of a
// ValidatingAdmissionPolicy or MutatingAdmissionPolicy, or of a Kyverno
// ValidatingPolicy while kyverno is on.
func (a *Assistant) checked(doc *Document, e extract.Expression) (map[string]any, *Assistant, bool) {
	if e.Document >= len(doc.roots) || doc.roots[e.Document] == nil {
		return nil, nil, false
	}
	root := doc.roots[e.Document]
	b, ok := a.forExpr(doc, e)
	if !ok {
		return nil, nil, false
	}
	kind, _ := root["kind"].(string)
	apiVersion, _ := root["apiVersion"].(string)
	switch {
	case b.vars != nil: // a Kyverno ValidatingPolicy
	case kind == "CustomResourceDefinition" && apiVersion == "apiextensions.k8s.io/v1":
	case (kind == "ValidatingAdmissionPolicy" || kind == "MutatingAdmissionPolicy") && strings.HasPrefix(apiVersion, "admissionregistration.k8s.io/"):
	default:
		return nil, nil, false
	}
	return root, b, true
}

// uses is the Uses section of the expression e of root, a document of doc,
// which reads the names reads (see readNames).
func (a *Assistant) uses(doc *Document, root map[string]any, e extract.Expression, reads []string) string {
	if len(reads) == 0 {
		return ""
	}
	var vars []variable
	subject := ""
	switch root["kind"] {
	case "CustomResourceDefinition":
		vars = crdScope(root, e, false)
		if _, place, ok := crdPlace(root, e.FieldPath); ok {
			subject = crdSubject(root, place)
		}
	case "ValidatingAdmissionPolicy", "MutatingAdmissionPolicy":
		// The types of spec.variables cost most to work out, so only an
		// expression that reads them gets them.
		withVariables := slices.ContainsFunc(reads, func(r string) bool { return r == "variables" || strings.HasPrefix(r, "variables.") })
		assumed := doc.assumedFor(root)
		vars = newPolicyTypes(root, a.version, doc.crds, a.features.Has(feature.CRDTypedPolicies), nil, assumed).scope(e, withVariables)
		assumeInPolicy(vars, assumed)
	default:
		vars = a.scope(doc, e)
	}
	var lines []string
	for _, r := range reads {
		name, field, dotted := strings.Cut(r, ".")
		i := slices.IndexFunc(vars, func(v variable) bool { return v.name == name })
		switch {
		case i < 0:
		case dotted:
			if line, ok := variableUse(vars[i], field); ok {
				lines = append(lines, line)
			}
		default:
			lines = append(lines, use(vars[i], subject))
		}
	}
	switch len(lines) {
	case 0:
		return ""
	case 1:
		return lines[0]
	}
	return "\n\n- " + strings.Join(lines, "\n- ")
}

// use is the line of the Uses section for the variable v: its name, its
// type, what it is, why it is dyn when it is, and where a type .cellens
// assumes for it is written. subject is what self is about in a
// CustomResourceDefinition, as the line above the rule says it.
func use(v variable, subject string) string {
	line := hover.Code(v.name) + ": "
	if (v.name == "self" || v.name == "oldSelf") && subject != "" {
		line += hover.Code(subject) + " (" + hover.Escape(v.typ()) + ")"
	} else {
		line += hover.Code(v.typ())
	}
	line += ", " + useWords(v)
	if v.typeName == "" {
		line += dynBecause(v.schema)
	}
	return withNote(line+".", v.schema)
}

// variableUse is the line of the Uses section for variables.<name>, a policy
// variable of spec.variables that v, the variable variables, has. It is
// false when there is no such variable.
func variableUse(v variable, name string) (string, bool) {
	f := v.schema.sub("properties").sub(name)
	if f == nil {
		return "", false
	}
	line := hover.Code("variables."+name) + ": " + hover.Code(assumedType(f))
	if src, ok := v.sources[name]; ok {
		line += ", the policy variable defined by " + hover.Code(src)
	} else {
		line += ", a policy variable of spec.variables"
	}
	return withNote(line+dynBecause(f)+".", f), true
}

// dynBecause is "; dyn because <why>" for a value of schema s that is dyn,
// and "" for one that is not.
func dynBecause(s schema) string {
	if why := dynReasonOf(s); why != "" {
		return "; dyn because " + why.text()
	}
	return ""
}

// withNote adds to line the sentence that says where the type of s is
// written, when s comes from a type .cellens assumes, as hover says it.
func withNote(line string, s schema) string {
	if note := s.str(assumedKey); note != "" {
		return line + " " + hover.Escape(note)
	}
	return line
}

// useWords says in a few words what the variable v is.
func useWords(v variable) string {
	switch v.name {
	case "self":
		return "the value the rule is on"
	case "oldSelf":
		return "the value before the update, so the rule runs only on update"
	case "object":
		return "the object being admitted, null on DELETE"
	case "oldObject":
		return "the existing object, null on CREATE"
	case "params":
		return "the parameter resource the binding refers to, null when there is none"
	case "request":
		return "the attributes of the admission request"
	case "namespaceObject":
		return "the namespace the object belongs to, null for cluster-scoped objects"
	case "authorizer":
		return "checks what the requesting user may do"
	case "variables":
		return "the policy variables of spec.variables, by name"
	}
	// Kyverno's own variables: their description, as a phrase.
	d := strings.TrimSuffix(v.desc, ".")
	r, n := utf8.DecodeRuneInString(d)
	return string(unicode.ToLower(r)) + d[n:]
}

// crdSubject is what self is about in a rule of root, a
// CustomResourceDefinition, at place below a version's schema: the kind and
// the place, such as Widget.spec or Widget for the root, as the line above
// the rule says it.
func crdSubject(root map[string]any, place []string) string {
	crdSpec, _ := root["spec"].(map[string]any)
	names, _ := crdSpec["names"].(map[string]any)
	kind, _ := names["kind"].(string)
	if kind == "" {
		kind = cmp.Or(documentName(root), "CustomResourceDefinition")
	}
	return assume.Name(kind, place)
}

// parsed returns the expression of text parsed by p, parsed the first time
// and kept: Uses: and Checks: share it. It is false when text does not
// parse.
func (m *exprMemo) parsed(p *meaning.Parser, text string) (meaning.Expression, bool) {
	if !m.done {
		m.expr, m.ok = p.Parse(text)
		m.done = true
	}
	return m.expr, m.ok
}

// readNames returns the names the expression of text reads, as names
// does, from the parse it shares with the sentence, made the first time
// and kept.
func (m *exprMemo) readNames(p *meaning.Parser, text string) []string {
	if !m.read {
		if e, ok := m.parsed(p, text); ok {
			m.reads = names(e.AST.Expr())
		}
		m.read = true
	}
	return m.reads
}

// names returns the names root, an expression parsed without macros (see
// meaning.Parser), reads that a scope could declare, in the order it first
// reads them, each once: every name that is no variable of a macro, and
// variables.<name> for a field of variables.
func names(root ast.Expr) []string {
	var out []string
	add := func(name string) {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	var visit func(e ast.Expr, bound []string)
	visit = func(e ast.Expr, bound []string) {
		if field, ok := variablesField(e, bound); ok {
			add("variables." + field)
			return
		}
		switch e.Kind() {
		case ast.IdentKind:
			if !slices.Contains(bound, e.AsIdent()) {
				add(e.AsIdent())
			}
		case ast.SelectKind:
			visit(e.AsSelect().Operand(), bound)
		case ast.CallKind:
			c := e.AsCall()
			if vars, from, ok := macroVars(c); ok {
				// A macro: its range, and what comes before its
				// variables are bound, with the names around it; the
				// rest with its variables too.
				if c.IsMemberFunction() && c.FunctionName() != "bind" {
					visit(c.Target(), bound)
				}
				inner := append(slices.Clone(bound), vars...)
				for i, arg := range c.Args()[len(vars):] {
					if i+len(vars) < from {
						visit(arg, bound)
					} else {
						visit(arg, inner)
					}
				}
				return
			}
			if c.IsMemberFunction() {
				visit(c.Target(), bound)
			}
			for _, arg := range c.Args() {
				visit(arg, bound)
			}
		case ast.ListKind:
			for _, el := range e.AsList().Elements() {
				visit(el, bound)
			}
		case ast.MapKind:
			for _, en := range e.AsMap().Entries() {
				visit(en.AsMapEntry().Key(), bound)
				visit(en.AsMapEntry().Value(), bound)
			}
		case ast.StructKind:
			for _, f := range e.AsStruct().Fields() {
				visit(f.AsStructField().Value(), bound)
			}
		case ast.ComprehensionKind, ast.UnspecifiedExprKind, ast.LiteralKind:
		}
	}
	visit(root, nil)
	return out
}

// macroVars returns the variables of c when it is a macro of the
// Kubernetes CEL environment that declares some, written as a call: x.all(v,
// P), x.map(v, F, T), x.transformMap(k, v, T), cel.bind(v, init, body) and
// the others; and from, the index of the first argument they are bound in.
// It is false for any other call, and for one whose variables are not
// names.
func macroVars(c ast.CallExpr) (vars []string, from int, ok bool) {
	args := c.Args()
	var n int
	switch name := c.FunctionName(); {
	case !c.IsMemberFunction():
		return nil, 0, false
	case name == "bind" && c.Target().Kind() == ast.IdentKind && c.Target().AsIdent() == "cel" && len(args) == 3:
		// cel.bind(v, init, body): v is bound in body, not in init.
		if args[0].Kind() != ast.IdentKind {
			return nil, 0, false
		}
		return []string{args[0].AsIdent()}, 2, true
	case (name == "all" || name == "exists" || name == "existsOne") && (len(args) == 2 || len(args) == 3):
		n = len(args) - 1
	case (name == "exists_one" || name == "filter" || name == "optMap" || name == "optFlatMap") && len(args) == 2,
		name == "map" && (len(args) == 2 || len(args) == 3):
		n = 1
	case (name == "transformList" || name == "transformMap" || name == "transformMapEntry") && (len(args) == 3 || len(args) == 4):
		n = 2
	default:
		return nil, 0, false
	}
	for _, v := range args[:n] {
		if v.Kind() != ast.IdentKind {
			return nil, 0, false
		}
		vars = append(vars, v.AsIdent())
	}
	return vars, n, true
}

// variablesField returns name when e reads variables.name: as a field,
// variables.name, an optional field, variables.?name, or an index by a
// string, variables['name'], where variables is no variable of a macro.
func variablesField(e ast.Expr, bound []string) (string, bool) {
	var operand ast.Expr
	var field string
	if e.Kind() == ast.SelectKind {
		operand, field = e.AsSelect().Operand(), e.AsSelect().FieldName()
	} else {
		if e.Kind() != ast.CallKind {
			return "", false
		}
		c := e.AsCall()
		name, args := c.FunctionName(), c.Args()
		if len(args) != 2 || name != operators.OptSelect && name != operators.Index && name != operators.OptIndex || args[1].Kind() != ast.LiteralKind {
			return "", false
		}
		s, ok := args[1].AsLiteral().Value().(string)
		if !ok {
			return "", false
		}
		operand, field = args[0], s
	}
	if operand.Kind() != ast.IdentKind || operand.AsIdent() != "variables" || slices.Contains(bound, "variables") {
		return "", false
	}
	return field, true
}
