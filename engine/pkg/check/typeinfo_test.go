package check

import (
	"testing"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/cel/environment"
)

// itemsNode is the node for the items, of the type given, of a list of up to
// 100000 items called name. Its rule does not read self, so every such node
// has the same rules.
func itemsNode(name, typ string) *schemaNode {
	limit := int64(100000)
	items := &apiextensions.JSONSchemaProps{
		Type:         typ,
		XValidations: apiextensions.ValidationRules{{Rule: "[1,2,3,4,5,6,7,8,9,10].all(x, x > 0)"}},
	}
	list := &apiextensions.JSONSchemaProps{Type: "array", MaxItems: &limit, Items: &apiextensions.JSONSchemaPropsOrArray{Schema: items}}
	root := &apiextensions.JSONSchemaProps{Type: "object", Properties: map[string]apiextensions.JSONSchemaProps{name: *list}}
	rootNode := &schemaNode{schema: root, cel: validation.RootCELContext(root), path: field.NewPath(name)}
	listNode := &schemaNode{schema: list, parent: rootNode, cel: rootNode.cel.ChildPropertyContext(list, name), path: rootNode.path.Child("list")}
	return &schemaNode{schema: items, parent: listNode, cel: listNode.cel.ChildItemsContext(items), path: listNode.path.Key("*"), element: true}
}

// TestCompileNoTypeInfo checks that a node without type information gets no
// cost, though a node with the same rules and a fixed cardinality has one.
func TestCompileNoTypeInfo(t *testing.T) {
	t.Parallel()
	c := &compiler{envSet: environment.MustBaseEnvSet(environment.DefaultCompatibilityVersion())}
	typed := itemsNode("typed", "string")
	if got, err := c.compile(typed); err != nil || len(got) != 1 {
		t.Fatalf("compile(typed) = %v, %v, want one cost", got, err)
	}
	untyped := itemsNode("untyped", "bogus")
	if info, err := untyped.cel.TypeInfo(); err == nil && info != nil {
		t.Fatalf("test setup: the node has type information %v", info)
	}
	if got, err := c.compile(untyped); err != nil || got != nil {
		t.Errorf("compile(untyped) = %v, %v, want no cost", got, err)
	}
}
