package examples

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kjson "k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// FindBuiltIn returns the documents of data, the text of file, whose path
// below its workspace folder, slash-separated, is rel, whose apiVersion and
// kind are those of a built-in resource and for which want is true, in the
// order of the file, read as the API server reads them: what an admission
// policy may run on, and its params and namespaces. Their CRD is nil. Two of
// one kind, namespace and name are an update, as for Find. A document whose
// YAML does not parse is skipped. The error is ctx's.
//
// The API server decodes such a document into its Go type, with the strict
// field validation kubectl asks for: a field the type does not have is a
// problem, after which no admission policy runs on it. Its defaults and its
// validation are those of the API server's own registry
// (k8s.io/kubernetes), which the engine does not have: Object is the
// document as its type reads it, in its namespace (default when a
// namespaced one names none, none for a cluster-scoped one), named for its
// generateName as the API server names it.
func FindBuiltIn(ctx context.Context, file, rel string, data []byte, want func(apiVersion, kind string) bool) ([]*Object, error) {
	data = extract.UTF8(data)
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
		if apiVersion == "" || kind == "" || !want(apiVersion, kind) {
			continue
		}
		gv, err := schema.ParseGroupVersion(apiVersion)
		if err != nil {
			continue
		}
		_, namespaced, ok := matchkind.Resource(gv.WithKind(kind))
		if !ok {
			continue
		}
		o := &Object{File: file, Line: line, APIVersion: apiVersion, Kind: kind, Expect: byPath, node: d.Node, lines: lines, clusterScoped: !namespaced}
		o.Namespace, o.Name = nameOf(d.Node)
		o.readComments(lines)
		o.decodeBuiltIn(gv.WithKind(kind))
		if o.Name != "" {
			key := gv.Group + "\x00" + kind + "\x00" + o.requestNamespace() + "\x00" + o.Name
			o.Old = last[key]
			last[key] = o
		}
		out = append(out, o)
	}
	return out, nil
}

// decodeBuiltIn sets Object, or the problem that keeps the API server from
// decoding the document, of o, a document of the built-in kind gvk.
func (o *Object) decodeBuiltIn(gvk schema.GroupVersionKind) {
	js, err := toJSON(o.node)
	if err != nil {
		o.DecodeFailed = true
		o.problem("", err.Error())
		return
	}
	strict := kjson.NewSerializerWithOptions(kjson.DefaultMetaFactory, scheme.Scheme, scheme.Scheme, kjson.SerializerOptions{Strict: true})
	obj, _, err := strict.Decode(js, &gvk, nil)
	if err != nil {
		o.DecodeFailed = true
		o.problem("", err.Error())
		if obj == nil || !runtime.IsStrictDecodingError(err) {
			return
		}
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		o.DecodeFailed = true
		o.problem("", fmt.Sprintf("read the %s: %v", gvk.Kind, err))
		return
	}
	u := &unstructured.Unstructured{Object: m}
	u.SetAPIVersion(o.APIVersion)
	u.SetKind(o.Kind)
	if o.clusterScoped {
		u.SetNamespace("")
	} else {
		u.SetNamespace(o.requestNamespace())
	}
	if u.GetName() == "" && u.GetGenerateName() != "" {
		u.SetName(u.GetGenerateName() + generatedSuffix)
	}
	o.Object = u.Object
}

// requestNamespace is the namespace of the request for o: none for a
// cluster-scoped kind, and default, as kubectl takes it, for a namespaced
// one that names none.
func (o *Object) requestNamespace() string {
	switch {
	case o.CRD != nil:
		return o.namespace()
	case o.clusterScoped:
		return ""
	case o.Namespace == "":
		return metav1.NamespaceDefault
	}
	return o.Namespace
}
