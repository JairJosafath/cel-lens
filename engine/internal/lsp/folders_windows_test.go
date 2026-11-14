package lsp

import "testing"

// TestSameFolderWindows checks that a Windows folder path is one folder with
// or without a trailing backslash and with its drive letter in either case,
// as VS Code spells it file:///c%3A/repo and another client file:///C:/repo/
// (#38).
func TestSameFolderWindows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "#38 drive letter case", a: `C:\repo`, b: `c:\repo`, want: true},
		{name: "#38 trailing backslash", a: `C:\repo\`, b: `C:\repo`, want: true},
		{name: "#38 both", a: `c:\repo\`, b: `C:\repo`, want: true},
		{name: "another folder", a: `C:\repo`, b: `C:\repo2`},
		{name: "another drive", a: `C:\repo`, b: `D:\repo`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sameFolder(tt.a, tt.b); got != tt.want {
				t.Errorf("sameFolder(%q, %q) = %t, want %t", tt.a, tt.b, got, tt.want)
			}
		})
	}
	for uri, want := range map[string]string{"file:///c%3A/repo/": `c:\repo`, "file:///C:/repo": `C:\repo`} {
		if got, ok := folderPath(uri); !ok || got != want {
			t.Errorf("folderPath(%q) = %q, %t, want %q, true", uri, got, ok, want)
		}
	}
}
