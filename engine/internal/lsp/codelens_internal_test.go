package lsp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// TestCodeLensesKept checks that a codeLens request for a version of a
// document already answered costs nothing, and that the lenses are made again
// when the document changes, is checked again or a .cellens changes (#3).
func TestCodeLensesKept(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ss := newSession(t)
	open, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": cacheURI, "version": 1, "text": widget(t)}})
	if err := ss.didOpen(ctx, open); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"textDocument": map[string]any{"uri": cacheURI}})
	lenses := func() []any {
		t.Helper()
		got, err := ss.codeLens(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		list, ok := got.([]any)
		if !ok {
			t.Fatalf("codeLens = %T, want a list", got)
		}
		return list
	}
	first := lenses()
	if len(first) == 0 {
		t.Fatal("codeLens = none, want the lines of widget.yaml")
	}
	kept := []any{"kept"}
	ss.docs[cacheURI].codeLenses = kept
	if got := lenses(); !reflect.DeepEqual(got, kept) {
		t.Errorf("codeLens again = %v, want the lenses kept for this version", got)
	}
	for _, tc := range []struct {
		name   string
		change func()
	}{
		{"a .cellens changes", func() { ss.putLens("file:///.cellens", []byte("types: []\n"), true) }},
		{"the document is checked again", func() {
			if err := ss.publish(ctx, cacheURI); err != nil {
				t.Fatal(err)
			}
		}},
		{"the document changes", func() { change(t, ss, 2, widget(t)) }},
	} {
		ss.docs[cacheURI].codeLenses = kept
		tc.change()
		if got := lenses(); reflect.DeepEqual(got, kept) || !reflect.DeepEqual(got, first) {
			t.Errorf("codeLens after %s = %v, want the lenses made again", tc.name, got)
		}
	}
}
