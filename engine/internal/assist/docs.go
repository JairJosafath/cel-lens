package assist

import "strings"

// Where the hover of a function or macro sends the reader.
const (
	celSpecURL = "https://github.com/google/cel-spec/blob/master/doc/langdef.md"
	k8sCELURL  = "https://kubernetes.io/docs/reference/using-api/cel/"
	celGoURL   = "https://pkg.go.dev/github.com/google/cel-go/cel"
)

// Link titles of the docs pages.
const (
	celSpecTitle = "CEL language definition"
	k8sCELTitle  = "Kubernetes CEL reference"
	celGoTitle   = "CEL optional values"
)

// section is a heading of a docs page, with the title of the link to it.
type section struct{ page, title, anchor string }

// sections returns the heading that documents the function called name, if any. It maps a function to the heading that documents it, for the functions
// that have one. Every anchor exists on its page, checked on 2026-10-09:
//   - celSpecURL: regular-expressions, list-operators, string-functions and
//     presence-and-comprehension-macros are headings of the CEL language definition.
//   - k8sCELURL: kubernetes-list-library and kubernetes-quantity-library are
//     headings of the Kubernetes CEL page.
//   - celGoURL: the hdr-*-OptionalTypes anchors are the headings, under Optional
//     Types, of the cel-go package documentation.
func sections(name string) (section, bool) {
	switch name {
	case "matches":
		return section{celSpecURL, celSpecTitle, "regular-expressions"}, true
	case "size":
		return section{celSpecURL, celSpecTitle, "list-operators"}, true
	case "startsWith":
		return section{celSpecURL, celSpecTitle, "string-functions"}, true
	case "all":
		return section{celSpecURL, celSpecTitle, "presence-and-comprehension-macros"}, true
	case "sum":
		return section{k8sCELURL, k8sCELTitle, "kubernetes-list-library"}, true
	case "quantity", "isQuantity", "isLessThan":
		return section{k8sCELURL, k8sCELTitle, "kubernetes-quantity-library"}, true
	case "orValue":
		return section{celGoURL, celGoTitle, "hdr-OrValue-OptionalTypes"}, true
	case "optMap":
		return section{celGoURL, celGoTitle, "hdr-OptMap-OptionalTypes"}, true
	case "optFlatMap":
		return section{celGoURL, celGoTitle, "hdr-OptFlatMap-OptionalTypes"}, true
	case "hasValue":
		return section{celGoURL, celGoTitle, "hdr-HasValue-OptionalTypes"}, true
	case "value":
		return section{celGoURL, celGoTitle, "hdr-Value-OptionalTypes"}, true
	case "or":
		return section{celGoURL, celGoTitle, "hdr-Or-OptionalTypes"}, true
	case "optional.of":
		return section{celGoURL, celGoTitle, "hdr-Optional_Of-OptionalTypes"}, true
	case "optional.none":
		return section{celGoURL, celGoTitle, "hdr-Optional_None-OptionalTypes"}, true
	}
	return section{}, false
}

// example is a short, valid expression using a function or macro, and what it
// returns.
type example struct {
	expr, result string
}

// standard reports whether name is one of the functions and macros of the CEL language definition. Every other function belongs to a Kubernetes library or a
// CEL extension that kubernetes.io documents.
func standard(name string) bool {
	switch name {
	case "has", "all", "exists", "exists_one", "map", "filter",
		"size", "contains", "startsWith", "endsWith", "matches", "int",
		"uint", "double", "string", "bytes", "bool", "duration",
		"timestamp", "type", "dyn", "getFullYear", "getMonth", "getDayOfMonth",
		"getDayOfWeek", "getDayOfYear", "getDate", "getHours", "getMinutes", "getSeconds",
		"getMilliseconds":
		return true
	}
	return false
}

// docsURL returns the page, at the function's section when it has one, that documents
// the function or macro called name.
func docsURL(name string) (title, url string) {
	if sec, ok := sections(name); ok {
		return sec.title, sec.page + "#" + sec.anchor
	}
	if standard(name) {
		return celSpecTitle, celSpecURL
	}
	return k8sCELTitle, k8sCELURL
}

// examples returns one example for every function and macro the Kubernetes CEL
// environment declares; docs_internal_test.go checks that each compiles in it.
func examples() map[string]example {
	return map[string]example{
		// Macros.
		"has":               {"has({'a': 1}.a)", "true"},
		"all":               {"[1, 2, 3].all(x, x > 0)", "true"},
		"exists":            {"[1, 2, 3].exists(x, x > 2)", "true"},
		"exists_one":        {"[1, 2, 3].exists_one(x, x > 2)", "true"},
		"map":               {"[1, 2, 3].map(x, x * 2)", "[2, 4, 6]"},
		"filter":            {"[1, 2, 3].filter(x, x > 1)", "[2, 3]"},
		"existsOne":         {"[1, 2, 3].existsOne(i, v, v > 2)", "true"},
		"optMap":            {"optional.of(2).optMap(x, x * 2).value()", "4"},
		"optFlatMap":        {"optional.of(2).optFlatMap(x, optional.of(x * 2)).value()", "4"},
		"transformList":     {"[1, 2, 3].transformList(i, v, v * 2)", "[2, 4, 6]"},
		"transformMap":      {"{'a': 1}.transformMap(k, v, v + 1)", "{'a': 2}"},
		"transformMapEntry": {"{'a': 1}.transformMapEntry(k, v, {v: k})", "{1: 'a'}"},
		"sortBy":            {"[{'n': 2}, {'n': 1}].sortBy(x, x.n)", "[{'n': 1}, {'n': 2}]"},

		// The CEL language definition.
		"size":            {"size([1, 2, 3])", "3"},
		"contains":        {"'kubernetes'.contains('net')", "true"},
		"startsWith":      {"'kubernetes'.startsWith('kube')", "true"},
		"endsWith":        {"'kubernetes'.endsWith('netes')", "true"},
		"matches":         {"'app-1'.matches('^app-[0-9]+$')", "true"},
		"int":             {"int('42')", "42"},
		"uint":            {"uint('42')", "42u"},
		"double":          {"double('1.5')", "1.5"},
		"string":          {"string(42)", "'42'"},
		"bytes":           {"bytes('abc')", "b'abc'"},
		"bool":            {"bool('true')", "true"},
		"duration":        {"duration('1h30m').getMinutes()", "90"},
		"timestamp":       {"timestamp('2024-05-06T07:08:09Z').getFullYear()", "2024"},
		"type":            {"type(1) == int", "true"},
		"dyn":             {"dyn(1) == 1.0", "true"},
		"getFullYear":     {"timestamp('2024-05-06T07:08:09Z').getFullYear()", "2024"},
		"getMonth":        {"timestamp('2024-05-06T07:08:09Z').getMonth()", "4 (months count from 0)"},
		"getDayOfMonth":   {"timestamp('2024-05-06T07:08:09Z').getDayOfMonth()", "5 (days count from 0)"},
		"getDayOfWeek":    {"timestamp('2024-05-06T07:08:09Z').getDayOfWeek()", "1 (Monday)"},
		"getDayOfYear":    {"timestamp('2024-05-06T07:08:09Z').getDayOfYear()", "126 (days count from 0)"},
		"getDate":         {"timestamp('2024-05-06T07:08:09Z').getDate()", "6"},
		"getHours":        {"timestamp('2024-05-06T07:08:09Z').getHours()", "7"},
		"getMinutes":      {"timestamp('2024-05-06T07:08:09Z').getMinutes()", "8"},
		"getSeconds":      {"timestamp('2024-05-06T07:08:09Z').getSeconds()", "9"},
		"getMilliseconds": {"duration('1.5s').getMilliseconds()", "1500"},

		// Lists, strings and sets.
		"isSorted":    {"[1, 2, 3].isSorted()", "true"},
		"sum":         {"[1, 2, 3].sum()", "6"},
		"min":         {"[3, 1, 2].min()", "1"},
		"max":         {"[3, 1, 2].max()", "3"},
		"indexOf":     {"'kubernetes'.indexOf('e')", "3"},
		"lastIndexOf": {"'kubernetes'.lastIndexOf('e')", "8"},
		"slice":       {"[1, 2, 3, 4].slice(1, 3)", "[2, 3]"},
		"sort":        {"[3, 1, 2].sort()", "[1, 2, 3]"},
		"first":       {"[1, 2].first().value()", "1"},
		"last":        {"[1, 2].last().value()", "2"},
		"distinct":    {"[1, 1, 2].distinct()", "[1, 2]"},
		"flatten":     {"[[1], [2, 3]].flatten()", "[1, 2, 3]"},
		"reverse":     {"[1, 2, 3].reverse()", "[3, 2, 1]"},
		"lists.range": {"lists.range(3)", "[0, 1, 2]"},
		"charAt":      {"'abc'.charAt(1)", "'b'"},
		"lowerAscii":  {"'ABC'.lowerAscii()", "'abc'"},
		"upperAscii":  {"'abc'.upperAscii()", "'ABC'"},
		"replace":     {"'a-b'.replace('-', '_')", "'a_b'"},
		"split":       {"'a,b'.split(',')", "['a', 'b']"},
		"substring":   {"'kubernetes'.substring(4)", "'rnetes'"},
		"trim":        {"' a '.trim()", "'a'"},
		"join":        {"['a', 'b'].join('-')", "'a-b'"},
		"format":      {"'%s has %d replicas'.format(['web', 3])", "'web has 3 replicas'"},
		"strings.quote": {
			"strings.quote('hi')", `'"hi"'`,
		},
		"sets.contains":   {"sets.contains([1, 2, 3], [1, 2])", "true"},
		"sets.equivalent": {"sets.equivalent([1, 2], [2, 1])", "true"},
		"sets.intersects": {"sets.intersects([1, 2], [2, 3])", "true"},
		"find":            {"'v1.37'.find('[0-9]+')", "'1'"},
		"findAll":         {"'v1.37'.findAll('[0-9]+')", "['1', '37']"},
		"optional.none":   {"optional.none().hasValue()", "false"},
		"optional.of":     {"optional.of(1).value()", "1"},
		"optional.unwrap": {"optional.unwrap([optional.of(1), optional.none()])", "[1]"},
		"unwrapOpt":       {"[optional.of(1), optional.none()].unwrapOpt()", "[1]"},
		"hasValue":        {"optional.of(1).hasValue()", "true"},
		"value":           {"optional.of(1).value()", "1"},
		"orValue":         {"optional.none().orValue(0)", "0"},
		"or":              {"optional.none().or(optional.of(1)).value()", "1"},
		"optional.ofNonZeroValue": {
			"optional.ofNonZeroValue('').hasValue()", "false",
		},

		// URLs.
		"url":            {"url('https://example.com/a?b=c').getHost()", "'example.com'"},
		"isURL":          {"isURL('https://example.com')", "true"},
		"getScheme":      {"url('https://example.com:8443/a?b=c').getScheme()", "'https'"},
		"getHost":        {"url('https://example.com:8443/a?b=c').getHost()", "'example.com:8443'"},
		"getHostname":    {"url('https://example.com:8443/a?b=c').getHostname()", "'example.com'"},
		"getPort":        {"url('https://example.com:8443/a?b=c').getPort()", "'8443'"},
		"getEscapedPath": {"url('https://example.com/a%20b').getEscapedPath()", "'/a%20b'"},
		"getQuery":       {"url('https://example.com/a?b=c').getQuery()", "{'b': ['c']}"},

		// Quantities.
		"quantity":           {"quantity('500Mi').asInteger()", "524288000"},
		"isQuantity":         {"isQuantity('500Mi')", "true"},
		"isLessThan":         {"quantity('500Mi').isLessThan(quantity('1Gi'))", "true"},
		"isGreaterThan":      {"quantity('500Mi').isGreaterThan(quantity('1Gi'))", "false"},
		"compareTo":          {"quantity('1Gi').compareTo(quantity('500Mi'))", "1"},
		"asInteger":          {"quantity('2Ki').asInteger()", "2048"},
		"asApproximateFloat": {"quantity('1500m').asApproximateFloat()", "1.5"},
		"isInteger":          {"quantity('2').isInteger()", "true"},
		"sign":               {"sign(quantity('-2'))", "-1"},
		"add":                {"quantity('1Gi').add(quantity('1Gi')).asInteger()", "2147483648"},
		"sub":                {"quantity('1Gi').sub(quantity('512Mi')).asInteger()", "536870912"},

		// IP addresses and CIDRs.
		"ip":                 {"ip('::1').family()", "6"},
		"isIP":               {"isIP('10.0.0.1')", "true"},
		"ip.isCanonical":     {"ip.isCanonical('2001:db8::1')", "true"},
		"cidr":               {"cidr('10.0.0.0/8').prefixLength()", "8"},
		"isCIDR":             {"isCIDR('10.0.0.0/8')", "true"},
		"containsIP":         {"cidr('10.0.0.0/8').containsIP('10.1.2.3')", "true"},
		"containsCIDR":       {"cidr('10.0.0.0/8').containsCIDR('10.1.0.0/16')", "true"},
		"masked":             {"cidr('10.1.2.3/8').masked()", "cidr('10.0.0.0/8')"},
		"prefixLength":       {"cidr('10.0.0.0/8').prefixLength()", "8"},
		"family":             {"ip('10.0.0.1').family()", "4"},
		"isLoopback":         {"ip('127.0.0.1').isLoopback()", "true"},
		"isUnspecified":      {"ip('0.0.0.0').isUnspecified()", "true"},
		"isGlobalUnicast":    {"ip('8.8.8.8').isGlobalUnicast()", "true"},
		"isLinkLocalUnicast": {"ip('169.254.1.1').isLinkLocalUnicast()", "true"},
		"isLinkLocalMulticast": {
			"ip('224.0.0.1').isLinkLocalMulticast()", "true",
		},

		// Semantic versions.
		"semver":   {"semver('1.2.3').major()", "1"},
		"isSemver": {"isSemver('1.2.3')", "true"},
		"major":    {"semver('1.2.3').major()", "1"},
		"minor":    {"semver('1.2.3').minor()", "2"},
		"patch":    {"semver('1.2.3').patch()", "3"},

		// Formats: validate returns the errors as an optional list, empty when the value is valid.
		"validate":            {"format.dns1123Label().validate('My_Name').hasValue()", "true (not a valid label)"},
		"format.named":        {"format.named('dns1123Label').value().validate('my-name').hasValue()", "false (valid)"},
		"format.byte":         {"format.byte().validate('aGVsbG8=').hasValue()", "false (valid)"},
		"format.date":         {"format.date().validate('2024-01-02').hasValue()", "false (valid)"},
		"format.datetime":     {"format.datetime().validate('2024-01-02T03:04:05Z').hasValue()", "false (valid)"},
		"format.dns1035Label": {"format.dns1035Label().validate('my-name').hasValue()", "false (valid)"},
		"format.dns1035LabelPrefix": {
			"format.dns1035LabelPrefix().validate('my-name-').hasValue()", "false (valid)",
		},
		"format.dns1123Label": {"format.dns1123Label().validate('my-name').hasValue()", "false (valid)"},
		"format.dns1123LabelPrefix": {
			"format.dns1123LabelPrefix().validate('my-name-').hasValue()", "false (valid)",
		},
		"format.dns1123Subdomain": {"format.dns1123Subdomain().validate('a.example.com').hasValue()", "false (valid)"},
		"format.dns1123SubdomainPrefix": {
			"format.dns1123SubdomainPrefix().validate('a.example.com-').hasValue()", "false (valid)",
		},
		"format.labelValue":    {"format.labelValue().validate('v1').hasValue()", "false (valid)"},
		"format.qualifiedName": {"format.qualifiedName().validate('example.com/name').hasValue()", "false (valid)"},
		"format.uri":           {"format.uri().validate('https://example.com').hasValue()", "false (valid)"},
		"format.uuid":          {"format.uuid().validate('123e4567-e89b-12d3-a456-426614174000').hasValue()", "false (valid)"},

		// The authorizer.
		"check":          {"authorizer.group('apps').resource('deployments').check('create').allowed()", "true when the user may create deployments"},
		"group":          {"authorizer.group('apps').resource('deployments').check('get').allowed()", "true when the user may get deployments"},
		"resource":       {"authorizer.group('').resource('pods').check('list').allowed()", "true when the user may list pods"},
		"subresource":    {"authorizer.group('').resource('pods').subresource('log').check('get').allowed()", "true when the user may read pod logs"},
		"name":           {"authorizer.group('').resource('pods').name('web').check('delete').allowed()", "true when the user may delete the pod web"},
		"path":           {"authorizer.path('/healthz').check('get').allowed()", "true when the user may get /healthz"},
		"serviceAccount": {"authorizer.serviceAccount('build', 'ci').group('').resource('pods').check('get').allowed()", "true when the service account may get pods"},
		"fieldSelector":  {"authorizer.group('').resource('pods').fieldSelector('spec.nodeName=n1').check('list').allowed()", "true when the user may list the pods of node n1"},
		"labelSelector":  {"authorizer.group('').resource('pods').labelSelector('app=web').check('list').allowed()", "true when the user may list the pods labelled app=web"},
		"allowed":        {"authorizer.path('/healthz').check('get').allowed()", "true when the request is allowed"},
		"reason":         {"authorizer.path('/healthz').check('get').reason()", "why the authorizer decided as it did, as a string"},
		"errored":        {"authorizer.path('/healthz').check('get').errored()", "true when the decision could not be made"},
		"error":          {"authorizer.path('/healthz').check('get').error()", "the error text when the decision could not be made"},
	}
}

// docsMarkdown renders the example and the link for the function or macro called name.
func docsMarkdown(name string) string {
	var b strings.Builder
	if ex, ok := examples()[name]; ok {
		b.WriteString("\n\n```cel\n" + ex.expr + " // -> " + ex.result + "\n```")
	}
	title, url := docsURL(name)
	b.WriteString("\n\n[" + title + "](" + url + ")")
	return b.String()
}
