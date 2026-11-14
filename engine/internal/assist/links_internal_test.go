package assist

import (
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// TestKubeDocsHost fails for a supported Kubernetes version without a docs
// host: kubernetes.io for 1.37, v1-NN.docs.kubernetes.io for the others
// (#57).
func TestKubeDocsHost(t *testing.T) {
	t.Parallel()
	for _, v := range kubeversion.Supported() {
		host, ok := kubeDocsHost(v)
		want := "https://v1-" + strings.TrimPrefix(v.String(), "1.") + ".docs.kubernetes.io"
		if v.String() == "1.37" {
			want = "https://kubernetes.io"
		}
		if !ok || host != want {
			t.Errorf("kubeDocsHost(%s) = %q, %t, want %q", v, host, ok, want)
		}
	}
	if host, ok := kubeDocsHost(kubeversion.Default()); !ok || host != "https://kubernetes.io" {
		t.Errorf("kubeDocsHost(the default, %s) = %q, %t, want kubernetes.io: the default is the current release", kubeversion.Default(), host, ok)
	}
}

// declaredNames returns every function and macro the environments declare:
// Kubernetes's at each supported version, then Kyverno's, sorted, each once.
func declaredNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, v := range kubeversion.Supported() {
		fs, ms, err := buildCatalog(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			if !internal(f.name) {
				names = append(names, f.name)
			}
		}
		for _, m := range ms {
			names = append(names, m.name)
		}
	}
	k, err := newKyverno()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range k.funcs {
		if !internal(f.name) {
			names = append(names, f.name)
		}
	}
	for _, m := range k.macros {
		names = append(names, m.name)
	}
	return slices.Compact(slices.Sorted(slices.Values(names)))
}

// TestEveryFunctionHasLink lists the functions and macros the environments
// declare, Kubernetes's at each supported version and Kyverno's, and fails
// on one without a pinned link, or with one that names no release (#57).
func TestEveryFunctionHasLink(t *testing.T) {
	t.Parallel()
	for _, name := range declaredNames(t) {
		d, ok := funcDocs(name, "")
		if !ok {
			t.Errorf("%q has no docs link", name)
			continue
		}
		for _, v := range kubeversion.Supported() {
			l, ok := d.page.link(d.title, v)
			if !ok || l.release == "" || !strings.HasPrefix(l.url, "https://") || !strings.Contains(l.title, "`"+name+"`") {
				t.Errorf("funcDocs(%q) at %s = %+v, %t, want a titled https link with its release", name, v, l, ok)
			}
			if strings.Contains(l.url, "/master/") || strings.Contains(l.url, "cel-go/cel#") || strings.Contains(l.url, "cel-go/ext#") {
				t.Errorf("funcDocs(%q) = %q, want a pinned page", name, l.url)
			}
		}
	}
}

// TestFuncDocsReceiver checks that a name in several libraries links the
// one of its receiver (#57).
func TestFuncDocsReceiver(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, recv, want string }{
		{"indexOf", "string", celGoExtPage + "#hdr-IndexOf-Strings"},
		{"indexOf", "list(int)", "/docs/reference/using-api/cel/#kubernetes-list-library"},
		{"reverse", "list(int)", celGoExtPage + "#hdr-Reverse-Lists"},
		{"reverse", "string", celGoExtPage + "#hdr-Reverse-Strings"},
		{"compareTo", "kubernetes.Semver", "#kubernetes-semver-library"},
		{"compareTo", "kubernetes.Quantity", "#kubernetes-quantity-library"},
		{"ip", "net.CIDR", "#kubernetes-cidr-library"},
		{"ip", "", "#kubernetes-ip-address-library"},
		{"Get", "resource.Context", kyvernoLibsPage + "#resource-library"},
		{"Get", "http.Context", kyvernoLibsPage + "#http-library"},
		{"Get", "", kyvernoLibsPage},
		{"format", "string", celGoExtPage + "#hdr-Format-Strings"},
		{"format.dns1123Label", "", "#kubernetes-format-library"},
		{"math.bitShiftLeft", "", celGoExtPage + "#hdr-Math_BitShiftLeft-Math"},
		{"unwrapOpt", "", celGoPage + "#hdr-Unwrap___UnwrapOpt-OptionalTypes"},
	}
	for _, tt := range tests {
		d, ok := funcDocs(tt.name, tt.recv)
		l, _ := d.page.link(d.title, kubeversion.Default())
		if !ok || !strings.HasSuffix(l.url, tt.want) {
			t.Errorf("funcDocs(%q, %q) = %q, want it to end with %q", tt.name, tt.recv, l.url, tt.want)
		}
	}
}
