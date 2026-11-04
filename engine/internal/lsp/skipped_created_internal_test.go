package lsp

import (
	"path/filepath"
	"testing"
)

func TestSkippedCreated(t *testing.T) {
	t.Parallel()
	root := filepath.FromSlash("/ws/root")
	inner := filepath.FromSlash("/ws/root/sub")
	tests := []struct {
		name    string
		folders []string
		path    string
		want    bool
	}{
		{"root itself", []string{root}, "/ws/root", false},
		{"root itself, hidden name", []string{"/ws/.hidden"}, "/ws/.hidden", false},
		{"visible child", []string{root}, "/ws/root/pkg", false},
		{"hidden child directly under the root", []string{root}, "/ws/root/.git", true},
		{"node_modules directly under the root", []string{root}, "/ws/root/node_modules", true},
		{"below a hidden child", []string{root}, "/ws/root/.git/objects", true},
		{"outside every root, visible", []string{root}, "/other/pkg", false},
		{"outside every root, hidden", []string{root}, "/other/.cache", true},
		{"no roots, hidden", nil, "/other/.cache", true},
		{"no roots, visible", nil, "/other/pkg", false},
		{"nested roots, outer skips", []string{root, inner}, "/ws/root/sub/.x", true},
		{"nested roots, inner root itself", []string{root, inner}, "/ws/root/sub", false},
		{"nested roots, only the outer contains it and skips", []string{root, inner}, "/ws/root/.hid/sub", true},
		{"nested roots, inner reaches it past a hidden outer element", []string{filepath.FromSlash("/ws"), filepath.FromSlash("/ws/.a")}, "/ws/.a/b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ss := &session{folders: tt.folders}
			path := filepath.FromSlash(tt.path)
			if got := ss.skippedCreated(path); got != tt.want {
				t.Errorf("skippedCreated(%q) with folders %q = %v, want %v", path, tt.folders, got, tt.want)
			}
		})
	}
}
