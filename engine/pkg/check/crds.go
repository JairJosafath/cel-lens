package check

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"

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
	// forgotten are the folder prefixes ForgetUnder dropped since Track.
	forgotten []string
	// roots, listed and partial describe a scan, for Fill: the folders it read,
	// the files it found there, and the folders it could not list fully.
	roots   []string
	listed  map[string]bool
	partial []string
	// skipped are the folders the scan did not enter.
	skipped []skippedDir
}

// skippedDir is a folder a scan did not enter, and how to tell whether a file
// below it is still there.
type skippedDir struct {
	prefix string
	exists func(file string) bool
}

// crdKind is one version of one custom resource kind.
type crdKind struct {
	apiVersion, kind, resource string
	schema                     map[string]any
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
	c.forgotten = nil
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
	c.forgotten = nil
}

// ScanRoots makes c the result of a scan of the folders root, slash-separated
// paths, for Fill: Fill then drops the files below them that the scan did not
// list. List names a file the scan found, and Incomplete says it could not list
// everything below a folder, which stops Fill from dropping the files below it.
func (c *CRDs) ScanRoots(roots []string) {
	c.roots = roots
	c.listed = map[string]bool{}
}

// List records that the scan found file, whether or not it could read it.
func (c *CRDs) List(file string) {
	if c.listed != nil {
		c.listed[file] = true
	}
}

// SkipDir records that the scan did not enter dir, a slash-separated path, such
// as a hidden folder. Fill keeps the files of c below dir for which exists
// reports true, because the scan cannot say whether they are gone.
func (c *CRDs) SkipDir(dir string, exists func(file string) bool) {
	c.skipped = append(c.skipped, skippedDir{strings.TrimSuffix(dir, "/") + "/", exists})
}

// Incomplete records that the scan did not list every file below dir, a
// slash-separated path, because it hit a limit or could not read dir. Fill
// keeps the files of c below dir, whose fate is unknown, and still drops the
// unlisted files below the other scanned folders.
func (c *CRDs) Incomplete(dir string) {
	c.partial = append(c.partial, strings.TrimSuffix(dir, "/")+"/")
}

// Fill records the files of from that c did not touch since Track, replacing
// what c has for them, ends the tracking, and reports whether that changed
// the schemas of c. A file Set, Forgotten or Kept after Track wins over from.
// When from is the result of a scan (see ScanRoots), Fill also drops
// the files of c below the scanned folders that the scan did not list, except
// those touched since Track, those below a folder the scan skipped (see
// SkipDir) that are still there, and those below a folder it left incomplete
// (see Incomplete). A nil from changes nothing.
func (c *CRDs) Fill(from *CRDs) bool {
	touched, forgotten := c.touched, c.forgotten
	c.touched, c.forgotten = nil, nil
	if from == nil {
		return false
	}
	added := false
	if from.listed != nil {
		for f := range c.files {
			if touched[f] || from.listed[f] || from.skippedPresent(f) || from.incompleteAt(f) {
				continue
			}
			for _, root := range from.roots {
				if strings.HasPrefix(f, strings.TrimSuffix(root, "/")+"/") {
					delete(c.files, f)
					added = true
					break
				}
			}
		}
	}
	for f, kinds := range from.files {
		if touched[f] || reflect.DeepEqual(c.files[f], kinds) || slices.ContainsFunc(forgotten, func(p string) bool { return strings.HasPrefix(f, p) }) {
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

// UnderSkipped lists the files of c, sorted, that are below a folder the scan
// from skipped (see SkipDir) and still exist. The scan never read them, so
// the caller reads them to follow their edits. A nil from lists none.
func (c *CRDs) UnderSkipped(from *CRDs) []string {
	if from == nil {
		return nil
	}
	var files []string
	for f := range c.files {
		if from.skippedPresent(f) {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	return files
}

// incompleteAt reports whether file is below a folder the scan left incomplete.
func (c *CRDs) incompleteAt(file string) bool {
	return slices.ContainsFunc(c.partial, func(p string) bool { return strings.HasPrefix(file, p) })
}

// skippedPresent reports whether file is below a folder the scan skipped and
// still exists.
func (c *CRDs) skippedPresent(file string) bool {
	return slices.ContainsFunc(c.skipped, func(d skippedDir) bool {
		return strings.HasPrefix(file, d.prefix) && d.exists(file)
	})
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
// reports whether the schemas of the set changed. While tracking (see Track),
// Fill also skips the files below dir, even those the set did not hold, and
// leaves what the set has for the kept ones.
func (c *CRDs) ForgetUnder(dir string, keep func(file string) bool) bool {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	if c.touched != nil {
		c.forgotten = append(c.forgotten, prefix)
	}
	changed := false
	for file := range c.files {
		if !strings.HasPrefix(file, prefix) {
			continue
		}
		if keep(file) {
			c.Keep(file)
		} else {
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
	k, ok := c.find(func(k crdKind) bool { return k.apiVersion == apiVersion && k.kind == kind })
	if !ok {
		return nil, false
	}
	return withTypeMeta(k.schema), true
}

// Resolve is matchkind.Resolve for rules that may also name the custom
// resources of the CRDs in c, by their plural name. Their kinds have the schema
// Schema returns. A nil c resolves built-in kinds only.
func (c *CRDs) Resolve(rules []matchkind.Rule) ([]matchkind.Kind, bool) {
	return matchkind.ResolveWith(rules, func(gvr schema.GroupVersionResource) (matchkind.Kind, bool) {
		k, ok := c.find(func(k crdKind) bool { return k.resource == gvr.Resource && k.apiVersion == gvr.GroupVersion().String() })
		if !ok {
			return matchkind.Kind{}, false
		}
		data, err := json.Marshal(withTypeMeta(k.schema))
		if err != nil {
			return matchkind.Kind{}, false
		}
		s := new(spec.Schema)
		if json.Unmarshal(data, s) != nil {
			return matchkind.Kind{}, false
		}
		return matchkind.Kind{GVK: gvr.GroupVersion().WithKind(k.kind), Schema: s}, true
	})
}

// find returns the first kind for which match is true, in the order of the file
// names.
func (c *CRDs) find(match func(crdKind) bool) (crdKind, bool) {
	if c == nil {
		return crdKind{}, false
	}
	files := make([]string, 0, len(c.files))
	for f := range c.files {
		files = append(files, f)
	}
	slices.Sort(files)
	for _, f := range files {
		for _, k := range c.files[f] {
			if match(k) {
				return k, true
			}
		}
	}
	return crdKind{}, false
}

// crdKinds returns the kinds, one per version with a schema, that the
// CustomResourceDefinition root defines; none if root is anything else.
func crdKinds(root map[string]any) []crdKind {
	if root["apiVersion"] != "apiextensions.k8s.io/v1" || root["kind"] != "CustomResourceDefinition" {
		return nil
	}
	crdSpec, _ := root["spec"].(map[string]any)
	names, _ := crdSpec["names"].(map[string]any)
	group, _ := crdSpec["group"].(string)
	kind, _ := names["kind"].(string)
	resource, _ := names["plural"].(string)
	versions, _ := crdSpec["versions"].([]any)
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
		out = append(out, crdKind{apiVersion: apiVersion, kind: kind, resource: resource, schema: open})
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
