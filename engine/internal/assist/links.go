package assist

import (
	"slices"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// The pinned docs the hover links to. Each page is the release the engine
// builds or checks with; every heading named below answered when it was
// fetched on 14 November, and the release check (CEL_LENS_LINKS_TEST=1 go
// test -run Links) fetches them again.
const (
	// celSpecPage is cel-spec's language definition at v0.25.2, the
	// cel.dev/expr the engine builds.
	celSpecPage    = "https://github.com/google/cel-spec/blob/v0.25.2/doc/langdef.md"
	celSpecVersion = "cel-spec v0.25.2"
	// celGoExtPage and celGoPage are cel-go v0.30.0's extensions and its
	// cel package, where the optional values are documented.
	celGoExtPage = "https://pkg.go.dev/github.com/google/cel-go@v0.30.0/ext"
	celGoPage    = "https://pkg.go.dev/github.com/google/cel-go@v0.30.0/cel"
	celGoVersion = "cel-go v0.30.0"
	// The Kyverno pages, for Kyverno 1.19, the version the engine checks
	// policies with.
	kyvernoLibsPage   = "https://release-1-19-0.kyverno.io/docs/policy-types/cel-libraries/"
	kyvernoPolicyPage = "https://release-1-19-0.kyverno.io/docs/policy-types/validating-policy/"
	kyvernoVersion    = "Kyverno 1.19"
	// The paths of the Kubernetes pages, below the host of the version
	// picked (see kubeDocsHost).
	k8sCELPath = "/docs/reference/using-api/cel/"
	k8sCRDPath = "/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/"
	k8sVAPPath = "/docs/reference/access-authn-authz/validating-admission-policy/"
	k8sMAPPath = "/docs/reference/access-authn-authz/mutating-admission-policy/"
)

// kubeDocsHost returns where the docs of Kubernetes version v are served:
// kubernetes.io for the current release, 1.37, and v1-NN.docs.kubernetes.io
// for the others. It is false for a version it has no host for; a test fails
// for any supported version without one. The engine release that adds 1.38
// moves 1.37 to v1-37.docs.kubernetes.io, which the release check asks for.
func kubeDocsHost(v kubeversion.Version) (string, bool) {
	switch v.String() {
	case "1.37":
		return "https://kubernetes.io", true
	case "1.36", "1.35", "1.34", "1.33":
		return "https://v1-" + strings.TrimPrefix(v.String(), "1.") + ".docs.kubernetes.io", true
	}
	return "", false
}

// docsLink is a link of the hover's Docs: paragraph: a title that names its
// subject, Markdown, and the release of what it documents.
type docsLink struct {
	title, url, release string
}

// markdown is the link as the hover writes it: "[Validation
// rules](url) (Kubernetes 1.36)".
func (l docsLink) markdown() string {
	return "[" + l.title + "](" + l.url + ") (" + l.release + ")"
}

// page is a docs page and, when not "", the heading on it.
type page struct {
	// kube is true for a page of the Kubernetes docs, whose url is a path
	// below the host of the version picked.
	kube        bool
	url, anchor string
	release     string
}

// at returns the address of p for Kubernetes version v, and the release it
// documents; false when v has no docs host.
func (p page) at(v kubeversion.Version) (url, release string, ok bool) {
	url, release = p.url, p.release
	if p.kube {
		host, found := kubeDocsHost(v)
		if !found {
			return "", "", false
		}
		url, release = host+p.url, "Kubernetes "+v.String()
	}
	if p.anchor != "" {
		url += "#" + p.anchor
	}
	return url, release, true
}

// link is the link titled title to p for Kubernetes version v.
func (p page) link(title string, v kubeversion.Version) (docsLink, bool) {
	url, release, ok := p.at(v)
	return docsLink{title: title, url: url, release: release}, ok
}

func kubePage(path, anchor string) page { return page{kube: true, url: path, anchor: anchor} }

// ruleDocs returns the pages of the Docs: paragraph for the expression e of
// root, by its field, as plan decision 8 lists them, with titles naming
// their subject: a CRD's rule (its transition rules when it reads oldSelf)
// and messageExpression; each field of a ValidatingAdmissionPolicy; a
// MutatingAdmissionPolicy's mutation, and its page for its match conditions
// and variables; a Kyverno ValidatingPolicy's page and its headings for
// messageExpression and audit annotations. heavy, true when the
// expression's estimated cost is near or over its budget, adds the page
// about cost: on the CRD page for a CRD, the Kubernetes CEL page's runtime
// budget for a policy.
func ruleDocs(root map[string]any, e extract.Expression, kyverno, readsOldSelf, heavy bool) []titled {
	if kyverno {
		var out []titled
		switch {
		case strings.HasSuffix(e.FieldPath, ".messageExpression"):
			out = append(out, titled{"Messages from messageExpression", page{url: kyvernoPolicyPage, anchor: "using-messageexpression-to-generate-dynamic-messages", release: kyvernoVersion}})
		case strings.HasPrefix(e.FieldPath, "spec.auditAnnotations["):
			out = append(out, titled{"Audit annotations", page{url: kyvernoPolicyPage, anchor: "using-auditannotations-to-add-custom-data", release: kyvernoVersion}})
		default:
			out = append(out, titled{"ValidatingPolicy", page{url: kyvernoPolicyPage, release: kyvernoVersion}})
		}
		return out
	}
	var out []titled
	switch root["kind"] {
	case "CustomResourceDefinition":
		switch {
		case e.Kind == extract.KindMessageExpression:
			out = append(out, titled{"The messageExpression field", kubePage(k8sCRDPath, "the-messageexpression-field")})
		case readsOldSelf:
			out = append(out, titled{"Transition rules", kubePage(k8sCRDPath, "transition-rules")})
		default:
			out = append(out, titled{"Validation rules", kubePage(k8sCRDPath, "validation-rules")})
		}
		if heavy {
			out = append(out, titled{"Resource use by validation functions", kubePage(k8sCRDPath, "resource-use-by-validation-functions")})
		}
		return out
	case "ValidatingAdmissionPolicy":
		switch {
		case strings.HasPrefix(e.FieldPath, "spec.matchConditions["):
			out = append(out, titled{"Match conditions", kubePage(k8sVAPPath, "matching-requests-matchconditions")})
		case strings.HasPrefix(e.FieldPath, "spec.variables["):
			out = append(out, titled{"Variable composition", kubePage(k8sVAPPath, "variable-composition")})
		case strings.HasPrefix(e.FieldPath, "spec.auditAnnotations["):
			out = append(out, titled{"Audit annotations", kubePage(k8sVAPPath, "audit-annotations")})
		case strings.HasSuffix(e.FieldPath, ".messageExpression"):
			out = append(out, titled{"Message expressions", kubePage(k8sVAPPath, "message-expression")})
		default:
			out = append(out, titled{"Validation expressions", kubePage(k8sVAPPath, "validation-expression")})
		}
	case "MutatingAdmissionPolicy":
		switch {
		case strings.HasSuffix(e.FieldPath, ".applyConfiguration.expression"):
			out = append(out, titled{"Apply configuration patches", kubePage(k8sMAPPath, "patch-type-apply-configuration")})
		case strings.HasSuffix(e.FieldPath, ".jsonPatch.expression"):
			out = append(out, titled{"JSON patches", kubePage(k8sMAPPath, "patch-type-json-patch")})
		default:
			// The page has no section for match conditions or variables.
			out = append(out, titled{"MutatingAdmissionPolicy", kubePage(k8sMAPPath, "")})
		}
	default:
		return nil
	}
	if heavy {
		out = append(out, titled{"Runtime cost budget", kubePage(k8sCELPath, "runtime-cost-budget")})
	}
	return out
}

// titled is a page with the title of the link to it.
type titled struct {
	title string
	page  page
}

// celSpec is the heading of cel-spec's language definition anchor.
func celSpec(anchor string) page {
	return page{url: celSpecPage, anchor: anchor, release: celSpecVersion}
}

// celGoExt is the heading hdr-<name>-<library> of cel-go's extensions.
func celGoExt(name, library string) page {
	return page{url: celGoExtPage, anchor: "hdr-" + name + "-" + library, release: celGoVersion}
}

// celGoOptional is the heading hdr-<name>-OptionalTypes of cel-go's cel
// package, which documents the optional values.
func celGoOptional(name string) page {
	return page{url: celGoPage, anchor: "hdr-" + name + "-OptionalTypes", release: celGoVersion}
}

// kubeLibrary is the heading of a Kubernetes library on the Kubernetes CEL
// page: kubernetes-<name>-library.
func kubeLibrary(name string) page { return kubePage(k8sCELPath, "kubernetes-"+name+"-library") }

// kyvernoLibrary is the heading of a Kyverno library, or the page of the
// libraries when anchor is "".
func kyvernoLibrary(anchor string) page {
	return page{url: kyvernoLibsPage, anchor: anchor, release: kyvernoVersion}
}

// funcDocs returns where the function or macro called name is documented,
// and the title of the link: its name and the library. recv is the type of
// the receiver of a member call, "" when it is not known or there is none;
// it tells apart the functions of one name in several libraries (indexOf of
// a string or a list, compareTo of a quantity or a semver, Get of
// Kyverno's resource, http or globalContext). It is false for a name no
// table holds.
func funcDocs(name, recv string) (titled, bool) {
	if p, title, ok := kyvernoDocs(name, recv); ok {
		return titled{"`" + name + "`, " + title, p}, true
	}
	if p, title, ok := kubeDocs(name, recv); ok {
		return titled{"`" + name + "`, " + title, p}, true
	}
	if p, title, ok := celDocs(name, recv); ok {
		return titled{"`" + name + "`, " + title, p}, true
	}
	return titled{}, false
}

// receiverOf returns the library a receiver type belongs to: the part of
// its name before the dot (kubernetes.Quantity gives Quantity, resource.Context
// gives resource), or the kind (list, string) for a CEL type.
func receiverOf(recv string) string {
	head, _, _ := strings.Cut(recv, "(")
	if pkg, typ, ok := strings.Cut(head, "."); ok {
		if pkg == "kubernetes" || pkg == "net" {
			return typ
		}
		return pkg
	}
	return head
}

// celDocs is funcDocs for cel-spec's standard definitions and cel-go's
// extensions and optional values.
func celDocs(name, recv string) (page, string, bool) {
	switch name {
	case "has", "all", "exists", "exists_one", "map", "filter":
		return celSpec("presence-and-comprehension-macros"), "CEL presence and comprehension macros", true
	case "size":
		return celSpec("list-operators"), "CEL list operators", true
	case "contains", "startsWith", "endsWith":
		return celSpec("string-functions"), "CEL string functions", true
	case "matches":
		return celSpec("regular-expressions"), "CEL regular expressions", true
	case "int", "uint", "double", "string", "bytes", "bool", "duration", "timestamp", "type", "dyn":
		return celSpec("types-and-conversions"), "CEL types and conversions", true
	case "getFullYear", "getMonth", "getDayOfMonth", "getDayOfWeek", "getDayOfYear", "getDate",
		"getHours", "getMinutes", "getSeconds", "getMilliseconds":
		return celSpec("datetime-functions"), "CEL date and time functions", true
	case "charAt", "format", "join", "lowerAscii", "replace", "split", "substring", "trim", "upperAscii",
		"indexOf", "lastIndexOf": // indexOf of a list is kubeDocs's
		return celGoExt(strings.ToUpper(name[:1])+name[1:], "Strings"), "cel-go strings extension", true
	case "strings.quote":
		return celGoExt("Strings_Quote", "Strings"), "cel-go strings extension", true
	case "reverse":
		if receiverOf(recv) == "list" {
			return celGoExt("Reverse", "Lists"), "cel-go lists extension", true
		}
		return celGoExt("Reverse", "Strings"), "cel-go strings extension", true
	case "lists.range":
		return celGoExt("Range", "Lists"), "cel-go lists extension", true
	case "distinct", "flatten", "slice", "sort", "sortBy":
		return celGoExt(strings.ToUpper(name[:1])+name[1:], "Lists"), "cel-go lists extension", true
	case "sets.contains", "sets.equivalent", "sets.intersects":
		rest := strings.TrimPrefix(name, "sets.")
		return celGoExt("Sets_"+strings.ToUpper(rest[:1])+rest[1:], "Sets"), "cel-go sets extension", true
	case "existsOne", "transformList", "transformMap", "transformMapEntry":
		return celGoExt(strings.ToUpper(name[:1])+name[1:], "TwoVarComprehensions"), "cel-go two-variable comprehensions", true
	case "cel.bind":
		return celGoExt("Cel_Bind", "Bindings"), "cel-go bindings", true
	case "base64.decode":
		return celGoExt("Base64_Decode", "Encoders"), "cel-go encoders", true
	case "base64.encode":
		return celGoExt("Base64_Encode", "Encoders"), "cel-go encoders", true
	case "json.encode":
		return celGoExt("JSON_Encode", "Encoders"), "cel-go encoders", true
	case "regex.replace":
		return celGoExt("Replace", "Regex"), "cel-go regex extension", true
	case "regex.extract":
		return celGoExt("Extract", "Regex"), "cel-go regex extension", true
	case "regex.extractAll":
		return celGoExt("Extract_All", "Regex"), "cel-go regex extension", true
	case "optional.of":
		return celGoOptional("Optional_Of"), "cel-go optional values", true
	case "optional.ofNonZeroValue":
		return celGoOptional("Optional_OfNonZeroValue"), "cel-go optional values", true
	case "optional.none":
		return celGoOptional("Optional_None"), "cel-go optional values", true
	case "optional.unwrap", "unwrapOpt":
		return celGoOptional("Unwrap___UnwrapOpt"), "cel-go optional values", true
	case "hasValue", "value", "or", "orValue", "optMap", "optFlatMap", "first", "last":
		return celGoOptional(strings.ToUpper(name[:1]) + name[1:]), "cel-go optional values", true
	}
	if rest, ok := strings.CutPrefix(name, "math."); ok {
		return celGoExt(mathHeading(rest), "Math"), "cel-go math extension", true
	}
	return page{}, "", false
}

// mathHeading is the name of the heading of math.<rest> in cel-go's
// extensions: Math_Abs for abs, Math_BitShiftLeft for bitShiftLeft.
func mathHeading(rest string) string {
	if rest == "" {
		return "Math"
	}
	return "Math_" + strings.ToUpper(rest[:1]) + rest[1:]
}

// kubeDocs is funcDocs for the Kubernetes libraries, at their heading on
// the Kubernetes CEL page.
func kubeDocs(name, recv string) (page, string, bool) {
	lib := ""
	switch name {
	case "isSorted", "sum", "min", "max":
		lib = "list"
	case "indexOf", "lastIndexOf":
		if receiverOf(recv) != "list" {
			return page{}, "", false // the strings extension's
		}
		lib = "list"
	case "find", "findAll":
		lib = "regex"
	case "url", "isURL", "getScheme", "getHost", "getHostname", "getPort", "getEscapedPath", "getQuery":
		lib = "url"
	case "isIP", "ip.isCanonical", "family", "isUnspecified", "isLoopback", "isLinkLocalMulticast", "isLinkLocalUnicast", "isGlobalUnicast":
		lib = "ip-address"
	case "ip":
		lib = "ip-address"
		if receiverOf(recv) == "CIDR" {
			lib = "cidr"
		}
	case "cidr", "isCIDR", "containsIP", "containsCIDR", "masked", "prefixLength":
		lib = "cidr"
	case "path", "group", "resource", "subresource", "namespace", "name", "check", "allowed", "reason",
		"errored", "error", "serviceAccount", "fieldSelector", "labelSelector":
		lib = "authorizer"
	case "validate":
		lib = "format"
	case "quantity", "isQuantity", "sign", "add", "sub", "isInteger", "asInteger", "asApproximateFloat":
		lib = "quantity"
	case "semver", "isSemver", "major", "minor", "patch":
		lib = "semver"
	case "compareTo", "isGreaterThan", "isLessThan":
		lib = "quantity"
		if receiverOf(recv) == "Semver" {
			lib = "semver"
		}
	default:
		if strings.HasPrefix(name, "format.") {
			lib = "format"
		}
	}
	if lib == "" {
		return page{}, "", false
	}
	title := "Kubernetes " + strings.ReplaceAll(lib, "-", " ") + " library"
	if lib == "url" || lib == "cidr" {
		title = "Kubernetes " + strings.ToUpper(lib) + " library"
	}
	if lib == "ip-address" {
		title = "Kubernetes IP address library"
	}
	return kubeLibrary(lib), title, true
}

// kyvernoDocs is funcDocs for the libraries Kyverno declares, at their
// heading on Kyverno's page of CEL libraries; gzip, which that page leaves
// out, at the page itself.
func kyvernoDocs(name, recv string) (page, string, bool) {
	var anchor, title string
	switch name {
	case "Get", "get", "List", "list", "Post", "post", "Client", "client":
		switch receiverOf(recv) {
		case "http":
			anchor, title = "http-library", "Kyverno HTTP library"
		case "globalcontext":
			anchor, title = "globalcontext-library", "Kyverno global context library"
		case "resource":
			anchor, title = "resource-library", "Kyverno resource library"
		default:
			// The receiver is not known: the page of the libraries.
			return kyvernoLibrary(""), "Kyverno CEL libraries", true
		}
	case "ToGVR", "toGVR":
		anchor, title = "resource-library", "Kyverno resource library"
	case "parseServiceAccount", "user.parseServiceAccount":
		anchor, title = "user-library", "Kyverno user library"
	case "image", "isImage", "containsDigest", "digest", "identifier", "identifierWithSeparator",
		"includes", "parseImageReference", "registry", "repository", "tag":
		anchor, title = "image-library", "Kyverno image library"
	case "GetMetadata", "getMetadata":
		anchor, title = "imagedata-library", "Kyverno image data library"
	case "hash.md5", "hash.sha1", "hash.sha256", "md5", "sha1", "sha256":
		anchor, title = "hash-library", "Kyverno hash library"
	case "random", "random.random":
		anchor, title = "random-library", "Kyverno random library"
	case "listObjToMap", "transform.listObjToMap":
		anchor, title = "transform-library", "Kyverno transform library"
	case "marshal", "unmarshal":
		anchor, title = "json-library", "Kyverno JSON library"
	case "parse":
		anchor, title = "yaml-library", "Kyverno YAML library"
	case "time.now", "time.toCron", "time.truncate":
		anchor, title = "time-functions", "Kyverno time functions"
	case "x509.decode":
		anchor, title = "x509-library", "Kyverno X.509 library"
	case "gzip.compress", "gzip.decompress":
		return kyvernoLibrary(""), "Kyverno CEL libraries", true
	default:
		return page{}, "", false
	}
	return kyvernoLibrary(anchor), title, true
}

// RuleDocs returns the Docs: paragraph's links for the expression e of doc,
// in Markdown, joined by " · ": the pinned page and heading of its kind of
// rule (see ruleDocs), for the Kubernetes version of a. heavy, true when
// the expression's estimated cost is near or over its budget, adds the page
// about cost. It is "" for an expression the check does not check.
func (a *Assistant) RuleDocs(doc *Document, e extract.Expression, heavy bool) string {
	root, b, checked := a.checked(doc, e)
	if !checked {
		return ""
	}
	oldSelf := false
	if root["kind"] == "CustomResourceDefinition" {
		doc.memoMu.Lock()
		oldSelf = slices.Contains(doc.memoOf(e.Start).readNames(a.exprs, e.Text), "oldSelf")
		doc.memoMu.Unlock()
	}
	var out []string
	for _, t := range ruleDocs(root, e, b.vars != nil, oldSelf, heavy) {
		if l, ok := t.page.link(t.title, a.version); ok {
			out = append(out, l.markdown())
		}
	}
	return strings.Join(out, " · ")
}
