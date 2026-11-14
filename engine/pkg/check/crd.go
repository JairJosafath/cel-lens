package check

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	structuraldefaulting "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	kfield "k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// crd checks the x-kubernetes-validations of a CustomResourceDefinition by
// running the API server's own validation of it. That declares self and
// oldSelf with the type of the schema node, loads the Kubernetes libraries and
// applies the per-rule and per-schema cost limits. Only the errors that belong
// to an x-kubernetes-validations entry found in the file are kept: those about
// its fieldPath, reason or message are placed on that value, the others on the
// rule or messageExpression. The cost errors are replaced by one explained
// problem per expression and one for the total; see costs.
//
// With assumed types for the CustomResourceDefinition (WithAssumedTypes), the
// validation runs a second time on a copy with the types placed, and what only
// that run finds in an expression is added as a warning (see addAssumed).
func (r *reporter) crd(ctx context.Context, node *yaml.Node, exprs map[string]extract.Expression) error {
	var versioned apiextensionsv1.CustomResourceDefinition
	partial, err := r.read(ctx, node, &versioned)
	if err != nil {
		return fmt.Errorf("read CustomResourceDefinition: %w", err)
	}
	apiextensionsv1.SetObjectDefaults_CustomResourceDefinition(&versioned)
	r.yaml11Enums(node)
	base := len(r.diags)
	if err := r.validateCRD(ctx, node, exprs, &versioned, partial); err != nil {
		return err
	}
	types := r.assumedIn(node)
	if len(types) == 0 {
		return nil
	}
	placed, _ := placeCRD(crdSchemas(&versioned), types)
	typed, ok := withAssumed(&versioned, placed)
	if len(placed) == 0 || !ok {
		return nil
	}
	return r.addAssumed(ctx, base, exprs, typesOfVersion(&versioned, types), func() error {
		return r.validateCRD(ctx, node, exprs, typed, partial)
	})
}

// validateCRD runs the API server's validation of versioned, read from node,
// partial when read left wrong-typed fields out, and reports its errors.
func (r *reporter) validateCRD(ctx context.Context, node *yaml.Node, exprs map[string]extract.Expression, versioned *apiextensionsv1.CustomResourceDefinition, partial bool) error {
	var internal apiextensions.CustomResourceDefinition
	if err := apiextensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(versioned, &internal, nil); err != nil {
		return fmt.Errorf("convert CustomResourceDefinition: %w", err)
	}
	var costErrs []*kfield.Error
	var schemaErrs []string
	var problems kfield.ErrorList
	comp := &compiler{envSet: r.version.EnvSet()}
	wait, cancel := func() {}, func() {}
	if mayCostMuch(&internal) {
		wait, cancel = r.warm(ctx, node, exprs, internal.DeepCopy(), comp)
	}
	defer func() {
		cancel()
		wait()
	}()
	if err := UnderVersion(r.version, func() { problems = validation.ValidateCustomResourceDefinition(ctx, &internal) }); err != nil {
		return err
	}
	problems = slices.DeleteFunc(problems, r.ruleOfGuessedSchema)
	whole, err := r.wholeSchemaErrors(ctx, &internal, problems)
	if err != nil {
		return err
	}
	for _, fe := range problems {
		if fe.Detail == "" {
			// Errors such as Required carry no detail; say what and where.
			fe.Detail = fe.Type.String() + ": " + fe.Field
		}
		if isCostError(fe) {
			costErrs = append(costErrs, fe)
			continue
		}
		reported := len(r.diags)
		schemaErr := isSchemaError(fe)
		if schemaErr {
			schemaErrs = append(schemaErrs, fe.Field)
		}
		if !r.validationValue(node, fe.Field, fe.Detail) {
			at := expressionsAt(exprs, fe.Field)
			for _, e := range at {
				r.report(e, fe.Detail)
			}
			if len(at) == 0 && !partial {
				r.schemaNode(node, fe.Field, fe.Detail)
			}
		}
		// Every problem of a schema the API server compiles no rule of
		// stands for all its rules, whatever its kind, so one does.
		var skipped []extract.Position
		if w := slices.IndexFunc(whole, func(w string) bool { return underError(w, []string{fe.Field}) }); w >= 0 {
			skipped = starts(rulesOf(exprs, whole[w]))
		} else if schemaErr {
			skipped = starts(uncompiledBy(exprs, errorNode(fe)))
		}
		for i := reported; i < len(r.diags); i++ {
			r.diags[i].Uncompiled = skipped
			if len(skipped) > 0 {
				r.diags[i].Stop = StopSchema
			}
		}
	}
	if len(costErrs) == 0 {
		return nil
	}
	wait()
	placed, err := r.costs(ctx, node, &internal, exprs, costErrs, schemaErrs, comp)
	if err != nil {
		return err
	}
	for _, fe := range costErrs {
		node, _, isRule := strings.Cut(fe.Field, ".x-kubernetes-validations[")
		// The total's diagnostic stands for the rules that contributed to it.
		covered := len(placed) > 0 && (!isRule || strings.HasPrefix(fe.Detail, "contributed to"))
		if placed[fe.Field] || covered || (isRule && underError(node, schemaErrs)) {
			continue
		}
		for _, e := range expressionsAt(exprs, fe.Field) {
			r.report(e, fe.Detail)
		}
	}
	return nil
}

// warm starts to work out the costs of crd in the background, while the API
// server's validation of it runs, so that comp already knows what costs needs
// when the validation says it does. What it finds is only kept in comp, which
// depends on the schema alone, so the result of costs is the same without it.
// The work ends by itself; wait waits for it and cancel stops it early. Both
// can be called more than once.
func (r *reporter) warm(ctx context.Context, doc *yaml.Node, exprs map[string]extract.Expression, crd *apiextensions.CustomResourceDefinition, comp *compiler) (wait, cancel func()) {
	ctx, stop := context.WithCancel(ctx)
	scratch := &reporter{version: r.version, file: r.file, lines: r.lines, eol: r.eol}
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A cost error that names no rule asks for all of them. A failure here
		// is found again by costs, which reports it.
		_, _ = scratch.costs(ctx, doc, crd, exprs, []*kfield.Error{{}}, nil, comp)
	}()
	return func() { <-done }, stop
}

// mayCostMuch reports whether crd has a schema in which a rule can be over its
// cost budget for want of a limit: a rule on a node, or above or below one,
// that has a string, list or map with no limit on its size. Only then does warm
// pay off; with every size limited a cost error is rare and the work would be
// wasted, so costs does it when the validation finds one.
func mayCostMuch(crd *apiextensions.CustomResourceDefinition) bool {
	costly := func(s *apiextensions.JSONSchemaProps) bool {
		found, _ := costlyRule(s, false)
		return found
	}
	if v := crd.Spec.Validation; v != nil && v.OpenAPIV3Schema != nil && costly(v.OpenAPIV3Schema) {
		return true
	}
	return slices.ContainsFunc(crd.Spec.Versions, func(v apiextensions.CustomResourceDefinitionVersion) bool {
		return v.Schema != nil && v.Schema.OpenAPIV3Schema != nil && costly(v.Schema.OpenAPIV3Schema)
	})
}

// costlyRule reports whether s or the schemas below it have a rule with an
// unlimited string, list or map at it, above it (the parent's flag) or below
// it, and whether s or the schemas below it have such a thing at all.
func costlyRule(s *apiextensions.JSONSchemaProps, above bool) (found, unlimited bool) {
	_, own := unbounded(s)
	above = above || own
	unlimited = own
	var below []*apiextensions.JSONSchemaProps
	for k := range s.Properties {
		p := s.Properties[k]
		below = append(below, &p)
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
		below = append(below, s.AdditionalProperties.Schema)
	}
	if s.Items != nil {
		if s.Items.Schema != nil {
			below = append(below, s.Items.Schema)
		}
		for i := range s.Items.JSONSchemas {
			below = append(below, &s.Items.JSONSchemas[i])
		}
	}
	for _, b := range below {
		f, u := costlyRule(b, above)
		found, unlimited = found || f, unlimited || u
	}
	return found || len(s.XValidations) > 0 && (above || unlimited), unlimited
}

// isSchemaError reports whether fe is an error of the schema itself, as opposed
// to one found by compiling its CEL: the API server skips the CEL of a schema
// node that has one at or below it. The API server does not say which kind an
// error is, so this tells from the place and the text. The root errors the API server
// adds outside that walk are not schema errors.
func isSchemaError(fe *kfield.Error) bool {
	_, after, ok := strings.Cut(fe.Field, "openAPIV3Schema")
	if !ok {
		return false
	}
	switch after {
	case "", ".nullable", ".type":
		// The API server adds these root errors outside the schema walk
		// (nullable at the root, the status subresource), so it still compiles
		// the rules of the root. The walk puts errors at the same places
		// (a list type on a root that is not an array), so the text decides.
		if isVersionRootMessage(fe.Detail) {
			return false
		}
	}
	switch {
	case strings.HasSuffix(fe.Field, ".rule"):
		return fe.Detail == "rule is not specified"
	case strings.HasSuffix(fe.Field, ".messageExpression"):
		return fe.Detail == "messageExpression must be non-empty if specified"
	case strings.HasSuffix(fe.Field, ".optionalOldSelf"), strings.HasSuffix(fe.Field, ".x-kubernetes-validations"):
		return false
	}
	return true
}

// wholeSchemaErrors returns the schemas of crd, by field path (such as
// spec.versions[0].schema.openAPIV3Schema, or spec.validation.openAPIV3Schema
// for the one the API server moves there when every version has it), whose
// rules the API server compiles none of: as it does, those that are not
// structural or whose defaults are not valid, which it gives no CEL
// context. The defaults are validated as the API server validates them, for
// the Kubernetes version of the check. Only a schema with one of problems,
// the API server's errors, under it is looked at: one without has none of
// those, and the API server's validation validated its defaults already, so
// they are not validated a second time. The error is ctx's, or says the
// version cannot be set (see UnderVersion).
func (r *reporter) wholeSchemaErrors(ctx context.Context, crd *apiextensions.CustomResourceDefinition, problems kfield.ErrorList) ([]string, error) {
	type schema struct {
		field string
		props *apiextensions.JSONSchemaProps
	}
	var schemas []schema
	if v := crd.Spec.Validation; v != nil && v.OpenAPIV3Schema != nil {
		schemas = append(schemas, schema{"spec.validation.openAPIV3Schema", v.OpenAPIV3Schema})
	}
	for i, v := range crd.Spec.Versions {
		if v.Schema != nil && v.Schema.OpenAPIV3Schema != nil {
			schemas = append(schemas, schema{fmt.Sprintf("spec.versions[%d].schema.openAPIV3Schema", i), v.Schema.OpenAPIV3Schema})
		}
	}
	var out []string
	for _, s := range schemas {
		if !slices.ContainsFunc(problems, func(fe *kfield.Error) bool { return underError(s.field, []string{fe.Field}) }) {
			continue
		}
		path := kfield.NewPath(s.field)
		ss, err := structuralschema.NewStructural(s.props)
		bad := err != nil || len(structuralschema.ValidateStructural(path, ss)) > 0
		if !bad {
			var problems kfield.ErrorList
			var defaultsErr error
			if err := UnderVersion(r.version, func() {
				problems, defaultsErr = structuraldefaulting.ValidateDefaults(ctx, path, ss, true, true)
			}); err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			bad = defaultsErr != nil || len(problems) > 0
		}
		if bad {
			out = append(out, s.field)
		}
	}
	return out, nil
}

// rulesOf returns the expressions of exprs in the schema at field, one of
// those wholeSchemaErrors returns: the schema every version shares, at
// spec.validation, holds those of every version.
func rulesOf(exprs map[string]extract.Expression, field string) []extract.Expression {
	var out []extract.Expression
	for path, e := range exprs {
		if strings.HasPrefix(path, field+".") || field == "spec.validation.openAPIV3Schema" && strings.HasPrefix(path, "spec.versions[") {
			out = append(out, e)
		}
	}
	return out
}

// listItemsErrors are the messages of the errors the API server finds for a
// list, with x-kubernetes-list-type set or map, in the schema of its items,
// and records at the list, not at the items: it skips the rules of the list
// and of the nodes above it, but compiles those of the items.
var listItemsErrors = []string{
	"cannot be nullable when x-kubernetes-list-type is ",
	"this property is in x-kubernetes-list-map-keys, so it ",
	"must be atomic as item of a list with x-kubernetes-list-type=set",
	"must have a schema if x-kubernetes-list-type is map",
	"must only have a single schema if x-kubernetes-list-type is map",
	"must be object if parent array's x-kubernetes-list-type is map",
	"must be a scalar type if parent array's x-kubernetes-list-type is map",
}

// errorNode is the field path where the API server records the schema
// error fe, whose rules and those above it it does not compile: fe's own,
// but the list's for an error about the schema of the list's items (see
// listItemsErrors).
func errorNode(fe *kfield.Error) string {
	if slices.ContainsFunc(listItemsErrors, func(m string) bool { return strings.Contains(fe.Detail, m) }) {
		if i := strings.LastIndex(fe.Field, ".items"); i >= 0 {
			return fe.Field[:i]
		}
	}
	return fe.Field
}

// uncompiledBy returns the expressions of exprs that the API server does not
// compile for the schema error recorded at field, in a schema it compiles rules of:
// those of the rules whose schema node holds the error, at it or below it
// (see isSchemaError). An error under spec.validation, where the API server
// moves the schema every version shares, holds the rules at that place in
// each version.
func uncompiledBy(exprs map[string]extract.Expression, field string) []extract.Expression {
	shared, isShared := strings.CutPrefix(field, "spec.validation.")
	var out []extract.Expression
	for path, e := range exprs {
		node, _, ok := strings.Cut(path, ".x-kubernetes-validations[")
		if !ok {
			continue
		}
		if underError(node, []string{field}) {
			out = append(out, e)
			continue
		}
		if _, schema, ok := strings.Cut(node, ".schema."); isShared && ok && strings.HasPrefix(node, "spec.versions[") && underError(schema, []string{shared}) {
			out = append(out, e)
		}
	}
	return out
}

// expressionsAt returns the expressions that the API server error at field
// refers to: the rule or messageExpression at that place, or for any other
// problem with an x-kubernetes-validations entry (a missing message, say) its
// rule. When every version has the same schema the API server moves it to
// spec.validation, so an error there belongs to the expression at that place in
// each version.
func expressionsAt(exprs map[string]extract.Expression, field string) []extract.Expression {
	if before, after, ok := strings.Cut(field, ".x-kubernetes-validations["); ok {
		if index, _, ok := strings.Cut(after, "]"); ok && !strings.HasSuffix(field, ".rule") && !strings.HasSuffix(field, ".messageExpression") {
			field = before + ".x-kubernetes-validations[" + index + "].rule"
		}
	}
	if e, ok := exprs[field]; ok {
		return []extract.Expression{e}
	}
	rest, ok := strings.CutPrefix(field, "spec.validation.")
	if !ok {
		return nil
	}
	var found []extract.Expression
	for path, e := range exprs {
		if strings.HasPrefix(path, "spec.versions[") && strings.HasSuffix(path, ".schema."+rest) {
			found = append(found, e)
		}
	}
	return found
}

// validationValue reports the API server error of errField, with errDetail, on the YAML value of the
// fieldPath, reason or message it is about, and says whether it did. A value
// that is not in the file, such as a message the rule needs, is left to the
// rule.
func (r *reporter) validationValue(doc *yaml.Node, errField, errDetail string) bool {
	schema, name, ok := validationField(errField)
	if !ok {
		return false
	}
	reported := false
	for _, l := range valueAt(doc, errField) {
		detail := errDetail
		if name == "fieldPath" && l.value.Kind == yaml.ScalarNode && detail == "must be a valid path" {
			if s := valueAt(doc, schema); len(s) > 0 {
				detail = missingPart(s[0].value, l.value.Value, detail)
			}
		}
		reported = r.reportValue(l, detail) || reported
	}
	return reported
}

// schemaNode reports detail, the API server's error about the schema at field,
// such as a schema that is not structural, on the key of the schema node. When
// the file has no value at field, as with a missing type, the error goes to
// the nearest schema node above it that the file has.
func (r *reporter) schemaNode(doc *yaml.Node, field, detail string) {
	if !strings.Contains(field, "openAPIV3Schema") {
		return
	}
	for ; field != ""; field = parentField(field) {
		reported := false
		for _, l := range valueAt(doc, field) {
			reported = l.key != nil && r.reportValue(located{l.key, l.key, l.key}, detail) || reported
		}
		if reported {
			return
		}
	}
}

// parentField returns the field path of the node that holds field: it drops
// the last step, a name or a bracketed key.
func parentField(field string) string {
	last := 0
	for i := 0; i < len(field); {
		if field[i] == '.' {
			i++
			last = i - 1
		} else {
			last = i
		}
		i += stepLen(field[i:])
	}
	return field[:last]
}

// isVersionRootMessage reports whether detail is one of the errors the API
// server adds for the root of a version's schema outside the schema walk.
func isVersionRootMessage(detail string) bool {
	return strings.Contains(detail, "nullable cannot be true at the root") ||
		strings.Contains(detail, "of the schema if the status subresource is enabled")
}

// ruleOfGuessedSchema reports whether fe is about an x-kubernetes-validations
// entry of a schema whose wrong-typed type read replaced with object. The
// rule was compiled against the guessed type, so its errors are false; the
// wrong type is already reported. The errors of the schema's other fields and
// of its children stay.
func (r *reporter) ruleOfGuessedSchema(fe *kfield.Error) bool {
	shared, isShared := strings.CutPrefix(fe.Field, "spec.validation.")
	for _, g := range r.guessed {
		if strings.HasPrefix(fe.Field, g+".x-kubernetes-validations[") {
			return true
		}
		// Versions that share a schema are validated as spec.validation.
		if _, schema, ok := strings.Cut(g, ".schema."); isShared && ok &&
			strings.HasPrefix(shared, schema+".x-kubernetes-validations[") {
			return true
		}
	}
	return false
}
