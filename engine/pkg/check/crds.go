package check

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sort"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// CRDs is the schemas of the custom resources that CustomResourceDefinitions
// define, by file. A policy whose spec.paramKind names one of them gets
// params typed from it (see WithCRDs). The zero value is an empty set. It is
// not safe for concurrent use while it changes; Check only reads it.
type CRDs struct {
	files map[string][]crdKind
}

// crdKind is one version of one custom resource kind.
type crdKind struct {
	apiVersion, kind string
	schema           map[string]any
}

// Set records the CustomResourceDefinitions in data, the text of file, in place
// of those it recorded for file before. Documents that do not parse or are no
// apiextensions.k8s.io/v1 CustomResourceDefinition are ignored, and so are
// versions without a schema. An empty data, or one without a
// CustomResourceDefinition, forgets the file. It reports whether the schemas of
// the set changed. A cancelled ctx leaves the set as it was and reports false.
func (c *CRDs) Set(ctx context.Context, file string, data []byte) bool {
	var found []crdKind
	if bytes.Contains(data, []byte("CustomResourceDefinition")) {
		docs, _, err := extract.Documents(ctx, file, data)
		if err != nil {
			return false
		}
		for _, d := range docs {
			var root map[string]any
			if d.Node.Decode(&root) == nil {
				found = append(found, crdKinds(root)...)
			}
		}
	}
	if reflect.DeepEqual(c.files[file], found) || len(found) == 0 && len(c.files[file]) == 0 {
		return false
	}
	if len(found) == 0 {
		delete(c.files, file)
		return true
	}
	if c.files == nil {
		c.files = map[string][]crdKind{}
	}
	c.files[file] = found
	return true
}

// Forget drops what the set recorded for file. It reports whether the schemas
// of the set changed.
func (c *CRDs) Forget(file string) bool {
	_, had := c.files[file]
	delete(c.files, file)
	return had
}

// Schema returns the OpenAPI schema, as YAML decodes it, of the custom
// resource kind with the given API version, such as "example.com/v1". When
// several files define it, the one whose name sorts first wins. The returned
// root is a copy that adds apiVersion, kind and the full metadata the API
// server publishes. It is false for a kind no recorded
// CustomResourceDefinition defines.
func (c *CRDs) Schema(apiVersion, kind string) (map[string]any, bool) {
	if c == nil {
		return nil, false
	}
	files := make([]string, 0, len(c.files))
	for f := range c.files {
		files = append(files, f)
	}
	slices.Sort(files)
	for _, f := range files {
		for _, k := range c.files[f] {
			if k.apiVersion == apiVersion && k.kind == kind {
				return withTypeMeta(k.schema), true
			}
		}
	}
	return nil, false
}

// crdKinds returns the kinds, one per version with a schema, that the
// CustomResourceDefinition root defines; none if root is anything else.
func crdKinds(root map[string]any) []crdKind {
	if root["apiVersion"] != "apiextensions.k8s.io/v1" || root["kind"] != "CustomResourceDefinition" {
		return nil
	}
	spec, _ := root["spec"].(map[string]any)
	names, _ := spec["names"].(map[string]any)
	group, _ := spec["group"].(string)
	kind, _ := names["kind"].(string)
	versions, _ := spec["versions"].([]any)
	if kind == "" {
		return nil
	}
	var out []crdKind
	for _, v := range versions {
		ver, _ := v.(map[string]any)
		name, _ := ver["name"].(string)
		sch, _ := ver["schema"].(map[string]any)
		open, _ := sch["openAPIV3Schema"].(map[string]any)
		if name == "" || open == nil {
			continue
		}
		apiVersion := name
		if group != "" {
			apiVersion = group + "/" + name
		}
		out = append(out, crdKind{apiVersion: apiVersion, kind: kind, schema: open})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].apiVersion < out[j].apiVersion })
	return out
}

// withTypeMeta returns the root schema of a custom resource as the API server
// publishes it: the schema of the CRD plus apiVersion, kind and the full
// ObjectMeta (built from metav1.ObjectMeta), which the CRD itself leaves out. The input is not changed.
func withTypeMeta(root map[string]any) map[string]any {
	str := map[string]any{"type": "string"}
	meta := objectMeta()
	out := make(map[string]any, len(root)+1)
	for k, v := range root {
		out[k] = v
	}
	props := map[string]any{"apiVersion": str, "kind": str, "metadata": meta}
	if old, ok := root["properties"].(map[string]any); ok {
		for k, v := range old {
			if k != "apiVersion" && k != "kind" && k != "metadata" {
				props[k] = v
			}
		}
	}
	out["properties"] = props
	return out
}

// objectMeta is the schema of metav1.ObjectMeta in the form of a CRD schema.
func objectMeta() map[string]any {
	data, err := json.Marshal(matchkind.ObjectMeta())
	if err != nil {
		return map[string]any{"type": "object"}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{"type": "object"}
	}
	return out
}
