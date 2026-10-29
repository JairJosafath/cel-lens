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

// snippet is a short, valid expression using a function or macro, and what it
// returns.
type snippet struct {
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

// example returns the example for the function or macro called name, if any. It
// covers every function and macro the Kubernetes CEL environment declares;
// docs_internal_test.go checks that each compiles in it.
func example(name string) (snippet, bool) {
	switch name {
	// Macros.
	case "has":
		return snippet{"has({'a': 1}.a)", "true"}, true
	case "all":
		return snippet{"[1, 2, 3].all(x, x > 0)", "true"}, true
	case "exists":
		return snippet{"[1, 2, 3].exists(x, x > 2)", "true"}, true
	case "exists_one":
		return snippet{"[1, 2, 3].exists_one(x, x > 2)", "true"}, true
	case "map":
		return snippet{"[1, 2, 3].map(x, x * 2)", "[2, 4, 6]"}, true
	case "filter":
		return snippet{"[1, 2, 3].filter(x, x > 1)", "[2, 3]"}, true
	case "existsOne":
		return snippet{"[1, 2, 3].existsOne(i, v, v > 2)", "true"}, true
	case "optMap":
		return snippet{"optional.of(2).optMap(x, x * 2).value()", "4"}, true
	case "optFlatMap":
		return snippet{"optional.of(2).optFlatMap(x, optional.of(x * 2)).value()", "4"}, true
	case "transformList":
		return snippet{"[1, 2, 3].transformList(i, v, v * 2)", "[2, 4, 6]"}, true
	case "transformMap":
		return snippet{"{'a': 1}.transformMap(k, v, v + 1)", "{'a': 2}"}, true
	case "transformMapEntry":
		return snippet{"{'a': 1}.transformMapEntry(k, v, {v: k})", "{1: 'a'}"}, true
	case "sortBy":
		return snippet{"[{'n': 2}, {'n': 1}].sortBy(x, x.n)", "[{'n': 1}, {'n': 2}]"}, true

	// The CEL language definition.
	case "size":
		return snippet{"size([1, 2, 3])", "3"}, true
	case "contains":
		return snippet{"'kubernetes'.contains('net')", "true"}, true
	case "startsWith":
		return snippet{"'kubernetes'.startsWith('kube')", "true"}, true
	case "endsWith":
		return snippet{"'kubernetes'.endsWith('netes')", "true"}, true
	case "matches":
		return snippet{"'app-1'.matches('^app-[0-9]+$')", "true"}, true
	case "int":
		return snippet{"int('42')", "42"}, true
	case "uint":
		return snippet{"uint('42')", "42u"}, true
	case "double":
		return snippet{"double('1.5')", "1.5"}, true
	case "string":
		return snippet{"string(42)", "'42'"}, true
	case "bytes":
		return snippet{"bytes('abc')", "b'abc'"}, true
	case "bool":
		return snippet{"bool('true')", "true"}, true
	case "duration":
		return snippet{"duration('1h30m').getMinutes()", "90"}, true
	case "type":
		return snippet{"type(1) == int", "true"}, true
	case "dyn":
		return snippet{"dyn(1) == 1.0", "true"}, true
	case "timestamp", "getFullYear":
		return snippet{"timestamp('2024-05-06T07:08:09Z').getFullYear()", "2024"}, true
	case "getMonth":
		return snippet{"timestamp('2024-05-06T07:08:09Z').getMonth()", "4 (months count from 0)"}, true
	case "getDayOfMonth":
		return snippet{"timestamp('2024-05-06T07:08:09Z').getDayOfMonth()", "5 (days count from 0)"}, true
	case "getDayOfWeek":
		return snippet{"timestamp('2024-05-06T07:08:09Z').getDayOfWeek()", "1 (Monday)"}, true
	case "getDayOfYear":
		return snippet{"timestamp('2024-05-06T07:08:09Z').getDayOfYear()", "126 (days count from 0)"}, true
	case "getDate":
		return snippet{"timestamp('2024-05-06T07:08:09Z').getDate()", "6"}, true
	case "getHours":
		return snippet{"timestamp('2024-05-06T07:08:09Z').getHours()", "7"}, true
	case "getMinutes":
		return snippet{"timestamp('2024-05-06T07:08:09Z').getMinutes()", "8"}, true
	case "getSeconds":
		return snippet{"timestamp('2024-05-06T07:08:09Z').getSeconds()", "9"}, true
	case "getMilliseconds":
		return snippet{"duration('1.5s').getMilliseconds()", "1500"}, true

	// Lists, strings and sets.
	case "isSorted":
		return snippet{"[1, 2, 3].isSorted()", "true"}, true
	case "sum":
		return snippet{"[1, 2, 3].sum()", "6"}, true
	case "min":
		return snippet{"[3, 1, 2].min()", "1"}, true
	case "max":
		return snippet{"[3, 1, 2].max()", "3"}, true
	case "indexOf":
		return snippet{"'kubernetes'.indexOf('e')", "3"}, true
	case "lastIndexOf":
		return snippet{"'kubernetes'.lastIndexOf('e')", "8"}, true
	case "slice":
		return snippet{"[1, 2, 3, 4].slice(1, 3)", "[2, 3]"}, true
	case "sort":
		return snippet{"[3, 1, 2].sort()", "[1, 2, 3]"}, true
	case "first":
		return snippet{"[1, 2].first().value()", "1"}, true
	case "last":
		return snippet{"[1, 2].last().value()", "2"}, true
	case "distinct":
		return snippet{"[1, 1, 2].distinct()", "[1, 2]"}, true
	case "flatten":
		return snippet{"[[1], [2, 3]].flatten()", "[1, 2, 3]"}, true
	case "reverse":
		return snippet{"[1, 2, 3].reverse()", "[3, 2, 1]"}, true
	case "lists.range":
		return snippet{"lists.range(3)", "[0, 1, 2]"}, true
	case "charAt":
		return snippet{"'abc'.charAt(1)", "'b'"}, true
	case "lowerAscii":
		return snippet{"'ABC'.lowerAscii()", "'abc'"}, true
	case "upperAscii":
		return snippet{"'abc'.upperAscii()", "'ABC'"}, true
	case "replace":
		return snippet{"'a-b'.replace('-', '_')", "'a_b'"}, true
	case "split":
		return snippet{"'a,b'.split(',')", "['a', 'b']"}, true
	case "substring":
		return snippet{"'kubernetes'.substring(4)", "'rnetes'"}, true
	case "trim":
		return snippet{"' a '.trim()", "'a'"}, true
	case "join":
		return snippet{"['a', 'b'].join('-')", "'a-b'"}, true
	case "format":
		return snippet{"'%s has %d replicas'.format(['web', 3])", "'web has 3 replicas'"}, true
	case "strings.quote":
		return snippet{"strings.quote('hi')", `'"hi"'`}, true
	case "sets.contains":
		return snippet{"sets.contains([1, 2, 3], [1, 2])", "true"}, true
	case "sets.equivalent":
		return snippet{"sets.equivalent([1, 2], [2, 1])", "true"}, true
	case "sets.intersects":
		return snippet{"sets.intersects([1, 2], [2, 3])", "true"}, true
	case "find":
		return snippet{"'v1.37'.find('[0-9]+')", "'1'"}, true
	case "findAll":
		return snippet{"'v1.37'.findAll('[0-9]+')", "['1', '37']"}, true
	case "optional.none":
		return snippet{"optional.none().hasValue()", "false"}, true
	case "optional.unwrap":
		return snippet{"optional.unwrap([optional.of(1), optional.none()])", "[1]"}, true
	case "unwrapOpt":
		return snippet{"[optional.of(1), optional.none()].unwrapOpt()", "[1]"}, true
	case "hasValue":
		return snippet{"optional.of(1).hasValue()", "true"}, true
	case "optional.of", "value":
		return snippet{"optional.of(1).value()", "1"}, true
	case "orValue":
		return snippet{"optional.none().orValue(0)", "0"}, true
	case "or":
		return snippet{"optional.none().or(optional.of(1)).value()", "1"}, true
	case "optional.ofNonZeroValue":
		return snippet{"optional.ofNonZeroValue('').hasValue()", "false"}, true

	// URLs.
	case "url":
		return snippet{"url('https://example.com/a?b=c').getHost()", "'example.com'"}, true
	case "isURL":
		return snippet{"isURL('https://example.com')", "true"}, true
	case "getScheme":
		return snippet{"url('https://example.com:8443/a?b=c').getScheme()", "'https'"}, true
	case "getHost":
		return snippet{"url('https://example.com:8443/a?b=c').getHost()", "'example.com:8443'"}, true
	case "getHostname":
		return snippet{"url('https://example.com:8443/a?b=c').getHostname()", "'example.com'"}, true
	case "getPort":
		return snippet{"url('https://example.com:8443/a?b=c').getPort()", "'8443'"}, true
	case "getEscapedPath":
		return snippet{"url('https://example.com/a%20b').getEscapedPath()", "'/a%20b'"}, true
	case "getQuery":
		return snippet{"url('https://example.com/a?b=c').getQuery()", "{'b': ['c']}"}, true

	// Quantities.
	case "quantity":
		return snippet{"quantity('500Mi').asInteger()", "524288000"}, true
	case "isQuantity":
		return snippet{"isQuantity('500Mi')", "true"}, true
	case "isLessThan":
		return snippet{"quantity('500Mi').isLessThan(quantity('1Gi'))", "true"}, true
	case "isGreaterThan":
		return snippet{"quantity('500Mi').isGreaterThan(quantity('1Gi'))", "false"}, true
	case "compareTo":
		return snippet{"quantity('1Gi').compareTo(quantity('500Mi'))", "1"}, true
	case "asInteger":
		return snippet{"quantity('2Ki').asInteger()", "2048"}, true
	case "asApproximateFloat":
		return snippet{"quantity('1500m').asApproximateFloat()", "1.5"}, true
	case "isInteger":
		return snippet{"quantity('2').isInteger()", "true"}, true
	case "sign":
		return snippet{"sign(quantity('-2'))", "-1"}, true
	case "add":
		return snippet{"quantity('1Gi').add(quantity('1Gi')).asInteger()", "2147483648"}, true
	case "sub":
		return snippet{"quantity('1Gi').sub(quantity('512Mi')).asInteger()", "536870912"}, true

	// IP addresses and CIDRs.
	case "ip":
		return snippet{"ip('::1').family()", "6"}, true
	case "isIP":
		return snippet{"isIP('10.0.0.1')", "true"}, true
	case "ip.isCanonical":
		return snippet{"ip.isCanonical('2001:db8::1')", "true"}, true
	case "isCIDR":
		return snippet{"isCIDR('10.0.0.0/8')", "true"}, true
	case "containsIP":
		return snippet{"cidr('10.0.0.0/8').containsIP('10.1.2.3')", "true"}, true
	case "containsCIDR":
		return snippet{"cidr('10.0.0.0/8').containsCIDR('10.1.0.0/16')", "true"}, true
	case "masked":
		return snippet{"cidr('10.1.2.3/8').masked()", "cidr('10.0.0.0/8')"}, true
	case "cidr", "prefixLength":
		return snippet{"cidr('10.0.0.0/8').prefixLength()", "8"}, true
	case "family":
		return snippet{"ip('10.0.0.1').family()", "4"}, true
	case "isLoopback":
		return snippet{"ip('127.0.0.1').isLoopback()", "true"}, true
	case "isUnspecified":
		return snippet{"ip('0.0.0.0').isUnspecified()", "true"}, true
	case "isGlobalUnicast":
		return snippet{"ip('8.8.8.8').isGlobalUnicast()", "true"}, true
	case "isLinkLocalUnicast":
		return snippet{"ip('169.254.1.1').isLinkLocalUnicast()", "true"}, true
	case "isLinkLocalMulticast":
		return snippet{"ip('224.0.0.1').isLinkLocalMulticast()", "true"}, true

	// Semantic versions.
	case "isSemver":
		return snippet{"isSemver('1.2.3')", "true"}, true
	case "semver", "major":
		return snippet{"semver('1.2.3').major()", "1"}, true
	case "minor":
		return snippet{"semver('1.2.3').minor()", "2"}, true
	case "patch":
		return snippet{"semver('1.2.3').patch()", "3"}, true

	// Formats: validate returns the errors as an optional list, empty when the value is valid.
	case "validate":
		return snippet{"format.dns1123Label().validate('My_Name').hasValue()", "true (not a valid label)"}, true
	case "format.named":
		return snippet{"format.named('dns1123Label').value().validate('my-name').hasValue()", "false (valid)"}, true
	case "format.byte":
		return snippet{"format.byte().validate('aGVsbG8=').hasValue()", "false (valid)"}, true
	case "format.date":
		return snippet{"format.date().validate('2024-01-02').hasValue()", "false (valid)"}, true
	case "format.datetime":
		return snippet{"format.datetime().validate('2024-01-02T03:04:05Z').hasValue()", "false (valid)"}, true
	case "format.dns1035Label":
		return snippet{"format.dns1035Label().validate('my-name').hasValue()", "false (valid)"}, true
	case "format.dns1035LabelPrefix":
		return snippet{"format.dns1035LabelPrefix().validate('my-name-').hasValue()", "false (valid)"}, true
	case "format.dns1123Label":
		return snippet{"format.dns1123Label().validate('my-name').hasValue()", "false (valid)"}, true
	case "format.dns1123LabelPrefix":
		return snippet{"format.dns1123LabelPrefix().validate('my-name-').hasValue()", "false (valid)"}, true
	case "format.dns1123Subdomain":
		return snippet{"format.dns1123Subdomain().validate('a.example.com').hasValue()", "false (valid)"}, true
	case "format.dns1123SubdomainPrefix":
		return snippet{"format.dns1123SubdomainPrefix().validate('a.example.com-').hasValue()", "false (valid)"}, true
	case "format.labelValue":
		return snippet{"format.labelValue().validate('v1').hasValue()", "false (valid)"}, true
	case "format.qualifiedName":
		return snippet{"format.qualifiedName().validate('example.com/name').hasValue()", "false (valid)"}, true
	case "format.uri":
		return snippet{"format.uri().validate('https://example.com').hasValue()", "false (valid)"}, true
	case "format.uuid":
		return snippet{"format.uuid().validate('123e4567-e89b-12d3-a456-426614174000').hasValue()", "false (valid)"}, true

	// The authorizer.
	case "check":
		return snippet{"authorizer.group('apps').resource('deployments').check('create').allowed()", "true when the user may create deployments"}, true
	case "group":
		return snippet{"authorizer.group('apps').resource('deployments').check('get').allowed()", "true when the user may get deployments"}, true
	case "resource":
		return snippet{"authorizer.group('').resource('pods').check('list').allowed()", "true when the user may list pods"}, true
	case "subresource":
		return snippet{"authorizer.group('').resource('pods').subresource('log').check('get').allowed()", "true when the user may read pod logs"}, true
	case "name":
		return snippet{"authorizer.group('').resource('pods').name('web').check('delete').allowed()", "true when the user may delete the pod web"}, true
	case "path":
		return snippet{"authorizer.path('/healthz').check('get').allowed()", "true when the user may get /healthz"}, true
	case "serviceAccount":
		return snippet{"authorizer.serviceAccount('build', 'ci').group('').resource('pods').check('get').allowed()", "true when the service account may get pods"}, true
	case "fieldSelector":
		return snippet{"authorizer.group('').resource('pods').fieldSelector('spec.nodeName=n1').check('list').allowed()", "true when the user may list the pods of node n1"}, true
	case "labelSelector":
		return snippet{"authorizer.group('').resource('pods').labelSelector('app=web').check('list').allowed()", "true when the user may list the pods labelled app=web"}, true
	case "allowed":
		return snippet{"authorizer.path('/healthz').check('get').allowed()", "true when the request is allowed"}, true
	case "reason":
		return snippet{"authorizer.path('/healthz').check('get').reason()", "why the authorizer decided as it did, as a string"}, true
	case "errored":
		return snippet{"authorizer.path('/healthz').check('get').errored()", "true when the decision could not be made"}, true
	case "error":
		return snippet{"authorizer.path('/healthz').check('get').error()", "the error text when the decision could not be made"}, true
	}
	return snippet{}, false
}

// docsMarkdown renders the example and the link for the function or macro called name.
func docsMarkdown(name string) string {
	var b strings.Builder
	if ex, ok := example(name); ok {
		b.WriteString("\n\n```cel\n" + ex.expr + " // -> " + ex.result + "\n```")
	}
	title, url := docsURL(name)
	b.WriteString("\n\n[" + title + "](" + url + ")")
	return b.String()
}
