package check_test

import (
	"context"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/check"
)

const snapshotCRD = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: widgets.example.com}
spec:
  group: example.com
  names: {plural: widgets, kind: Widget}
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema: {type: object, properties: {size: {type: integer}}}
`

// TestCRDsSnapshot checks that a snapshot of a set of CRDs keeps the
// schemas it had while the set changes, and that Equal compares the
// schemas, for the rule index of the language server, which checks files
// off its message loop while the loop changes the set (cel-lens-core#35).
func TestCRDsSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var set check.CRDs
	if !(&check.CRDs{}).Equal(nil) || !(*check.CRDs)(nil).Snapshot().Equal(nil) {
		t.Error("an empty set Equal(nil) = false, want true")
	}
	empty := set.Snapshot()
	set.Set(ctx, "/a.yaml", []byte(snapshotCRD))
	snap := set.Snapshot()
	if empty.Equal(snap) || !snap.Equal(&set) || len(snap.Files()) != 1 {
		t.Errorf("snapshot of one CRD: equal to the empty one %t, to the set %t, files %v; want false, true, [/a.yaml]", empty.Equal(snap), snap.Equal(&set), snap.Files())
	}
	set.Set(ctx, "/a.yaml", []byte(snapshotCRD+"# no change to the schema\n"))
	if !snap.Equal(set.Snapshot()) {
		t.Error("a snapshot after a change that keeps the schemas is not Equal, want Equal")
	}
	set.Set(ctx, "/b.yaml", []byte(snapshotCRD))
	set.Forget("/a.yaml")
	if got := snap.Files(); len(got) != 1 || got[0] != "/a.yaml" {
		t.Errorf("snapshot files after the set changed = %v, want [/a.yaml]", got)
	}
	if snap.Equal(set.Snapshot()) {
		t.Error("snapshots of other files are Equal, want not")
	}
	if _, ok := snap.Schema("example.com/v1", "Widget"); !ok {
		t.Error("snapshot lost the schema of Widget")
	}
}
