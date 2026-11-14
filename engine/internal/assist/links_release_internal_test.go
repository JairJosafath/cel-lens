package assist

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// celSpecRaw is the raw Markdown of celSpecPage, whose headings give the
// page's heading ids, as GitHub makes them.
const celSpecRaw = "https://raw.githubusercontent.com/google/cel-spec/v0.25.2/doc/langdef.md"

// TestLinks is the release check of the hover's links (#57), which needs
// the network and runs only with CEL_LENS_LINKS_TEST=1: it fetches every
// page the Docs: paragraph links, once, for the host of each supported
// Kubernetes version, and fails on a status of 400 or more, on a heading id
// missing from its page (a GitHub page's from its raw Markdown's
// headings), and when the newest supported version's v1-NN.docs.kubernetes.io
// answers while the table still sends that version to kubernetes.io.
func TestLinks(t *testing.T) {
	if os.Getenv("CEL_LENS_LINKS_TEST") != "1" {
		t.Skip("the link check fetches the docs pages; set CEL_LENS_LINKS_TEST=1 to run it")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	pages := linkedPages(t)
	urls := make([]string, 0, len(pages))
	for u := range pages {
		urls = append(urls, u)
	}
	slices.Sort(urls)
	for _, u := range urls {
		anchors := pages[u]
		body, status, err := fetch(t.Context(), client, u)
		switch {
		case err != nil:
			t.Errorf("GET %s: %v", u, err)
		case status >= 400:
			t.Errorf("GET %s: status %d", u, status)
		}
		ids := htmlIDs(body)
		if u == celSpecPage {
			// A GitHub page's heading ids are its raw Markdown's headings,
			// checked whatever the page answered.
			raw, rawStatus, err := fetch(t.Context(), client, celSpecRaw)
			if err != nil || rawStatus >= 400 {
				t.Errorf("GET %s: status %d, %v", celSpecRaw, rawStatus, err)
				continue
			}
			ids = markdownIDs(raw)
		} else if err != nil || status >= 400 {
			continue
		}
		var missing []string
		for _, a := range anchors {
			if a != "" && !ids[a] {
				missing = append(missing, a)
			}
		}
		if len(missing) > 0 {
			t.Errorf("%s has no heading %q", u, missing)
		}
		t.Logf("%s: %d, %d headings checked", u, status, len(anchors))
	}
	newest := slices.MaxFunc(kubeversion.Supported(), func(a, b kubeversion.Version) int { return a.Minor() - b.Minor() })
	if host, _ := kubeDocsHost(newest); host == "https://kubernetes.io" {
		own := "https://v1-" + strings.TrimPrefix(newest.String(), "1.") + ".docs.kubernetes.io/"
		if _, status, err := fetch(t.Context(), client, own); err == nil && status < 400 {
			t.Errorf("GET %s: status %d, so Kubernetes %s has docs of its own: kubeDocsHost must send it there, not to kubernetes.io", own, status, newest)
		} else {
			t.Logf("%s does not answer (%v, status %d): %s stays on kubernetes.io", own, err, status, newest)
		}
	}
}

// linkedPages returns every page the Docs: paragraph can link to, for the
// host of each supported Kubernetes version, without its fragment, with the
// headings linked on it.
func linkedPages(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	add := func(p page, v kubeversion.Version) {
		u, _, ok := p.at(v)
		if !ok {
			t.Errorf("no docs host for Kubernetes %s", v)
			return
		}
		base, anchor, _ := strings.Cut(u, "#")
		if !slices.Contains(out[base], anchor) {
			out[base] = append(out[base], anchor)
		}
	}
	crd := map[string]any{"kind": "CustomResourceDefinition"}
	vap := map[string]any{"kind": "ValidatingAdmissionPolicy"}
	mutating := map[string]any{"kind": "MutatingAdmissionPolicy"}
	fields := []struct {
		root    map[string]any
		e       extract.Expression
		kyverno bool
	}{
		{crd, extract.Expression{Kind: extract.KindRule, FieldPath: "spec.versions[0].schema.openAPIV3Schema.x-kubernetes-validations[0].rule"}, false},
		{crd, extract.Expression{Kind: extract.KindMessageExpression, FieldPath: "spec.versions[0].schema.openAPIV3Schema.x-kubernetes-validations[0].messageExpression"}, false},
		{vap, extract.Expression{FieldPath: "spec.validations[0].expression"}, false},
		{vap, extract.Expression{FieldPath: "spec.validations[0].messageExpression"}, false},
		{vap, extract.Expression{FieldPath: "spec.matchConditions[0].expression"}, false},
		{vap, extract.Expression{FieldPath: "spec.variables[0].expression"}, false},
		{vap, extract.Expression{FieldPath: "spec.auditAnnotations[0].valueExpression"}, false},
		{mutating, extract.Expression{FieldPath: "spec.mutations[0].applyConfiguration.expression"}, false},
		{mutating, extract.Expression{FieldPath: "spec.mutations[0].jsonPatch.expression"}, false},
		{mutating, extract.Expression{FieldPath: "spec.variables[0].expression"}, false},
		{vap, extract.Expression{FieldPath: "spec.validations[0].expression"}, true},
		{vap, extract.Expression{FieldPath: "spec.validations[0].messageExpression"}, true},
		{vap, extract.Expression{FieldPath: "spec.auditAnnotations[0].valueExpression"}, true},
	}
	receivers := []string{"", "list(int)", "string", "kubernetes.Semver", "net.CIDR", "resource.Context", "http.Context", "globalcontext.Context"}
	for _, v := range kubeversion.Supported() {
		for _, f := range fields {
			for _, oldSelf := range []bool{false, true} {
				for _, heavy := range []bool{false, true} {
					for _, d := range ruleDocs(f.root, f.e, f.kyverno, oldSelf, heavy) {
						add(d.page, v)
					}
				}
			}
		}
		for _, name := range declaredNames(t) {
			for _, recv := range receivers {
				if d, ok := funcDocs(name, recv); ok {
					add(d.page, v)
				}
			}
		}
	}
	return out
}

// fetch GETs u and returns its body and status.
func fetch(ctx context.Context, client *http.Client, u string) (body []byte, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "cel-lens-link-check")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	body, err = io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if cerr := resp.Body.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read %s: %w", u, err)
	}
	return body, resp.StatusCode, nil
}

// htmlID finds the id of an element of an HTML page.
var htmlID = regexp.MustCompile(`\bid=["']?([^"'\s>]+)`)

// htmlIDs returns the ids of the elements of an HTML page.
func htmlIDs(body []byte) map[string]bool {
	ids := map[string]bool{}
	for _, m := range htmlID.FindAllSubmatch(body, -1) {
		ids[string(m[1])] = true
	}
	return ids
}

// markdownIDs returns the ids GitHub gives the headings of a Markdown file:
// the heading's text in lower case, its spaces as hyphens, and every other
// character but letters, digits, hyphens and underscores left out.
func markdownIDs(md []byte) map[string]bool {
	ids := map[string]bool{}
	for line := range strings.SplitSeq(string(md), "\n") {
		text, ok := strings.CutPrefix(strings.TrimLeft(line, "#"), " ")
		if !ok || !strings.HasPrefix(line, "#") {
			continue
		}
		var b strings.Builder
		for _, r := range strings.ToLower(strings.TrimSpace(text)) {
			switch {
			case r == ' ':
				b.WriteRune('-')
			case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
				b.WriteRune(r)
			}
		}
		ids[b.String()] = true
	}
	return ids
}

// TestMarkdownIDs checks the ids GitHub gives headings, which TestLinks
// checks cel-spec's against.
func TestMarkdownIDs(t *testing.T) {
	t.Parallel()
	ids := markdownIDs([]byte("# Language Definition\n#### Date/Time Functions\n### Regular Expressions\nnot # a heading\n```\n"))
	for _, want := range []string{"language-definition", "datetime-functions", "regular-expressions"} {
		if !ids[want] {
			t.Errorf("markdownIDs() = %v, want %q", ids, want)
		}
	}
	if len(ids) != 3 {
		t.Errorf("markdownIDs() = %v, want three", ids)
	}
}
