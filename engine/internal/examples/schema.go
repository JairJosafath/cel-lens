package examples

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel/model"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/objectmeta"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	apiservervalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	apiextensionsfeatures "k8s.io/apiextensions-apiserver/pkg/features"
	"k8s.io/apimachinery/pkg/api/operation"
	metavalidation "k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utiljson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/cel/common"
	apiserverfeatures "k8s.io/apiserver/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"

	"github.com/JairJosafath/cel-lens/engine/internal/yamlfield"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

// generatedSuffix stands for the five characters the API server adds to a
// metadata.generateName to make a name: always the same, so the messages
// about the name are.
const generatedSuffix = "xxxxx"

// storedVersion is the metadata.resourceVersion an update has when neither
// it nor the object it updates names one: the API server's validation of an
// update requires one, which kubectl apply takes from the object stored.
const storedVersion = "1"

// Check checks each of objs as the API server checks a custom resource
// before its CEL rules, in their order, and records what it refuses in its
// Problems and the object the CEL rules would get in its Object. An update
// (Old) is checked as an update from the object of its Old, which must come
// before it in objs, as Find returns them; when the API server refuses that
// one, there is no object to update, and it is checked as a create, as
// kubectl apply -f would create it. The error is ctx's.
func Check(ctx context.Context, objs []*Object) error {
	for _, o := range objs {
		if err := ctx.Err(); err != nil {
			return err
		}
		o.check(ctx)
	}
	return nil
}

// check is Check for o.
func (o *Object) check(ctx context.Context) {
	o.Problems, o.Object, o.Errors, o.DecodeFailed, o.RulesSkipped = nil, nil, nil, false, false
	js, err := toJSON(o.node)
	if err != nil {
		o.DecodeFailed = true
		o.problem("", err.Error())
		return
	}
	var obj map[string]any
	if err := utiljson.Unmarshal(js, &obj); err != nil {
		o.DecodeFailed = true
		o.problem("", err.Error())
		return
	}
	if !o.decode(obj) {
		o.DecodeFailed = true
		return
	}
	defaulting.Default(obj, o.version.structural)
	old := o.old()
	o.prepare(obj, old)
	o.Object = obj
	o.Errors = o.validate(ctx, obj, old)
	for _, fe := range o.Errors {
		o.problem(fe.Field, fe.Error())
		o.RulesSkipped = o.RulesSkipped || blocking(fe)
	}
}

// old is the object o is an update from: that of its Old, when the API
// server accepts it; nil for a create, and for an update from an example
// the schema refuses, which kubectl apply -f would create instead. Once Run
// paired o by what the rules refuse too (see paired), it is that of prev,
// nil for a create.
func (o *Object) old() map[string]any {
	if o.paired {
		if o.prev == nil {
			return nil
		}
		return o.prev.Object
	}
	if o.Old != nil && o.Old.Object != nil && len(o.Old.Problems) == 0 {
		return o.Old.Object
	}
	return nil
}

// blocking reports whether the API server runs no CEL rule of an object
// whose validation returned fe (hasBlockingErr of customResourceStrategy in
// k8s.io/apiextensions-apiserver's pkg/registry/customresource).
func blocking(fe *field.Error) bool {
	return slices.Contains([]field.ErrorType{
		field.ErrorTypeNotSupported, field.ErrorTypeRequired, field.ErrorTypeTooLong, field.ErrorTypeTooMany, field.ErrorTypeTypeInvalid,
	}, fe.Type)
}

// decode does to obj what the API server's decoding of a custom resource
// does (unstructuredSchemaCoercer in k8s.io/apiextensions-apiserver's
// customresource_handler.go), with strict field validation, kubectl's
// default: it prunes the fields the schema does not know, reporting each,
// and reads metadata. It reports false when the API server cannot decode
// the object at all, which it then refuses alone.
func (o *Object) decode(obj map[string]any) bool {
	s := o.version.structural
	meta, found, unknown, err := objectmeta.GetObjectMetaWithOptions(obj, objectmeta.ObjectMetaOptions{ReturnUnknownFieldPaths: true})
	if err != nil {
		o.problem("metadata", err.Error())
		return false
	}
	unknown = append(unknown, pruning.PruneWithOptions(obj, s, true, structuralschema.UnknownFieldPathOptions{TrackUnknownFieldPaths: true})...)
	defaulting.PruneNonNullableNullsWithoutDefaults(obj, s)
	ferr, paths := objectmeta.CoerceWithOptions(nil, obj, s, false, objectmeta.CoerceOptions{ReturnUnknownFieldPaths: true})
	if ferr != nil {
		o.problem(ferr.Field, ferr.Error())
		return false
	}
	unknown = append(unknown, paths...)
	if found {
		if err := objectmeta.SetObjectMeta(obj, meta); err != nil {
			o.problem("metadata", err.Error())
			return false
		}
	}
	for _, p := range unknown {
		o.problem(p, fmt.Sprintf("unknown field %q", p))
	}
	o.DecodeFailed = len(unknown) > 0
	return true
}

// prepare does to obj what the API server does to a custom resource before
// it validates it (rest.BeforeCreate and BeforeUpdate, and the strategy's
// PrepareForCreate and PrepareForUpdate): it puts it in its namespace, the
// default one, as kubectl does, when it names none, or none for a
// cluster-scoped kind, gives it a name for its generateName, gives an
// update the resourceVersion of the object it updates, as kubectl apply
// does, and, with the status subresource, drops the status of a create and
// keeps the old one for an update.
func (o *Object) prepare(obj, old map[string]any) {
	u := &unstructured.Unstructured{Object: obj}
	if old != nil && u.GetResourceVersion() == "" {
		rv := (&unstructured.Unstructured{Object: old}).GetResourceVersion()
		u.SetResourceVersion(cmp.Or(rv, storedVersion))
	}
	switch {
	case !o.CRD.namespaced:
		if u.GetNamespace() != "" {
			u.SetNamespace("")
		}
	case u.GetNamespace() == "":
		u.SetNamespace(metav1.NamespaceDefault)
	}
	if u.GetName() == "" && u.GetGenerateName() != "" {
		u.SetName(u.GetGenerateName() + generatedSuffix)
	}
	if !o.version.status {
		return
	}
	switch status, had := old["status"]; {
	case old == nil:
		delete(obj, "status")
	case had:
		obj["status"] = status
	default:
		delete(obj, "status")
	}
}

// validate returns what the API server's validation of a custom resource
// refuses in obj, but its CEL rules (customResourceStrategy.Validate and
// ValidateUpdate in k8s.io/apiextensions-apiserver's
// pkg/registry/customresource): its metadata, its schema, its embedded
// resources and its list types; for an update from old, with the
// ratcheting the API server has, so a field left as it was is not refused
// again.
func (o *Object) validate(ctx context.Context, obj, old map[string]any) field.ErrorList {
	sv := o.version
	beta := utilfeature.DefaultFeatureGate.Enabled(apiserverfeatures.DeclarativeValidationBeta)
	meta, errs := objectMeta(obj)
	if old == nil {
		if len(errs) == 0 {
			errs = metavalidation.ValidateObjectMetaDeclaratively(ctx, operation.Create, meta, nil, o.CRD.namespaced, metavalidation.NameIsDNSSubdomain, field.NewPath("metadata"), beta)
		}
		errs = append(errs, apiservervalidation.ValidateCustomResource(nil, obj, sv.validator)...)
		errs = append(errs, objectmeta.Validate(ctx, nil, obj, sv.structural, false)...)
		return append(errs, listtype.ValidateListSetsAndMaps(nil, sv.structural, obj)...)
	}
	if len(errs) == 0 {
		oldMeta, oldErrs := objectMeta(old)
		errs = oldErrs
		if len(errs) == 0 {
			errs = metavalidation.ValidateObjectMetaDeclaratively(ctx, operation.Update, meta, oldMeta, o.CRD.namespaced, nil, field.NewPath("metadata"), beta)
		}
	}
	var options []apiservervalidation.ValidationOption
	if utilfeature.DefaultFeatureGate.Enabled(apiextensionsfeatures.CRDValidationRatcheting) {
		options = append(options, apiservervalidation.WithRatcheting(common.NewCorrelatedObject(obj, old, &model.Structural{Structural: sv.structural})))
	}
	errs = append(errs, apiservervalidation.ValidateCustomResourceUpdate(nil, obj, old, sv.validator, options...)...)
	errs = append(errs, objectmeta.Validate(ctx, nil, obj, sv.structural, false)...)
	if oldErrs := listtype.ValidateListSetsAndMaps(nil, sv.structural, old); len(oldErrs) == 0 {
		errs = append(errs, listtype.ValidateListSetsAndMaps(nil, sv.structural, obj)...)
	}
	return errs
}

// objectMeta reads the metadata of obj as the API server's validation of a
// custom resource does.
func objectMeta(obj map[string]any) (*metav1.ObjectMeta, field.ErrorList) {
	raw := obj["metadata"]
	if raw == nil {
		return &metav1.ObjectMeta{}, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, field.ErrorList{field.Invalid(field.NewPath("metadata"), raw, fmt.Sprintf("expected map[string]interface{}, got %T", raw))}
	}
	meta := &metav1.ObjectMeta{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(m, meta); err != nil {
		return nil, field.ErrorList{field.Invalid(field.NewPath("metadata"), raw, err.Error())}
	}
	return meta, nil
}

// problem records msg as a problem of o at the field path, as the API
// server writes one, on the nearest key of the document that exists.
func (o *Object) problem(path, msg string) {
	start, end := o.at(path)
	if _, value := o.find(path); value != nil && strings.Contains(msg, "bool") {
		msg += yaml11Hint(value)
	}
	o.Problems = append(o.Problems, check.Diagnostic{File: o.File, Message: msg, Severity: check.SeverityError, Start: start, End: end})
}

// yaml11Hint is what to add to the API server's complaint about a boolean
// where it wants something else, at or below n: where the document has a
// plain scalar that kubectl reads as a boolean, "" when it has none.
func yaml11Hint(n *yaml.Node) string {
	if w := firstPlainBool(n); w != nil {
		return fmt.Sprintf(" (line %d: kubectl reads the unquoted %s as a boolean, as YAML 1.1 does; quote it if a string is meant)", w.Line, w.Value)
	}
	return ""
}

// firstPlainBool is the first scalar at or below n that yamlfield.PlainBool
// accepts, nil when there is none. Aliases are not followed.
func firstPlainBool(n *yaml.Node) *yaml.Node {
	if _, ok := yamlfield.PlainBool(n); ok {
		return n
	}
	for _, c := range n.Content {
		if w := firstPlainBool(c); w != nil {
			return w
		}
	}
	return nil
}
