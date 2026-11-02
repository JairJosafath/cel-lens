package check

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/matchkind"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// CRDs is the schemas of the custom resources that CustomResourceDefinitions
// define, by file. A policy whose spec.paramKind names one of them gets
// params typed from it (see WithCRDs). The zero value is an empty set. It is
// not safe for concurrent use while it changes; Check only reads it.
type CRDs struct {
	files map[string][]crdKind
	// touched are the files Set or Forget changed since Track, nil when not tracking.
	touched map[string]bool
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
	if c.touched != nil {
		c.touched[file] = true
	}
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

// Track starts recording the files that Set, Forget or Keep touch, until Fill
// or Untrack. Fill skips them, so a file that changed or went away while from
// was being read is not brought back.
func (c *CRDs) Track() {
	c.touched = map[string]bool{}
}

// Keep marks file as touched for the tracking Track started, so Fill leaves
// what c has for it, such as the text of an open document. It does nothing
// when not tracking.
func (c *CRDs) Keep(file string) {
	if c.touched != nil {
		c.touched[file] = true
	}
}

// Untrack stops the recording Track started, without a Fill.
func (c *CRDs) Untrack() {
	c.touched = nil
}

// Fill records the files of from that c did not touch since Track, replacing
// what c has for them, ends the tracking, and reports whether that changed
// the schemas of c. A file Set, Forgotten or Kept after Track wins over from.
// A nil from changes nothing.
func (c *CRDs) Fill(from *CRDs) bool {
	touched := c.touched
	c.touched = nil
	if from == nil {
		return false
	}
	added := false
	for f, kinds := range from.files {
		if touched[f] || reflect.DeepEqual(c.files[f], kinds) {
			continue
		}
		if c.files == nil {
			c.files = map[string][]crdKind{}
		}
		c.files[f] = kinds
		added = true
	}
	return added
}

// Forget drops what the set recorded for file. It reports whether the schemas
// of the set changed.
func (c *CRDs) Forget(file string) bool {
	if c.touched != nil {
		c.touched[file] = true
	}
	_, had := c.files[file]
	delete(c.files, file)
	return had
}

// ForgetUnder drops what the set recorded for every file whose name is below
// dir, a slash-separated path, except those for which keep reports true. It
// reports whether the schemas of the set changed.
func (c *CRDs) ForgetUnder(dir string, keep func(file string) bool) bool {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	changed := false
	for file := range c.files {
		if strings.HasPrefix(file, prefix) && !keep(file) {
			changed = c.Forget(file) || changed
		}
	}
	return changed
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
