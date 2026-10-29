package assist

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"k8s.io/apiserver/pkg/cel/environment"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// function is a CEL function, with every way to call it.
type function struct {
	name string
	desc string
	// global lists the overloads called as name(args).
	global []overload
	// members lists the overloads called as receiver.name(args).
	members []member
}

// overload is one signature of a function.
type overload struct {
	// params holds the type of each parameter, the receiver first for a member.
	params []string
	sig    string
	// result is the type the overload returns.
	result string
}

// member is one overload called on a receiver.
type member struct {
	recv types.Kind
	// recvType is the name of the receiver's type.
	recvType string
	// optional is true when the receiver is an optional of any type.
	optional bool
	overload
}

// macro is one form of a CEL macro: syntax the parser expands.
type macro struct {
	name string
	// args counts the arguments, not the receiver.
	args int
	// member is true for receiver.name(...), false for name(...).
	member    bool
	sig, desc string
	// recv is the kinds of receiver the macro is called on.
	recv receivers
}

// accepts reports whether the macro can be called on a receiver of the CEL
// type named n, or on an optional when optional is true. An unknown type,
// "dyn", accepts every macro that has a receiver.
func (m macro) accepts(n string, optional bool) bool {
	switch {
	case optional:
		return m.recv&onOptionals != 0
	case n == "dyn":
		return m.recv != 0
	case strings.HasPrefix(n, "list"):
		return m.recv&onLists != 0
	case strings.HasPrefix(n, "map"):
		return m.recv&onMaps != 0
	}
	return false
}

// receivers is a set of the kinds of receiver a macro is called on.
type receivers uint8

// The kinds of receiver a macro form accepts.
const (
	onLists receivers = 1 << iota
	onMaps
	onOptionals
)

// macroForm is how a macro form is written for readers. The environment lists
// the macros by name and argument count but documents none, so a form it
// declares and this table lacks fails TestEveryFunctionHasExampleAndDocs.
type macroForm struct {
	sig, desc string
	recv      receivers
}

// macroForms returns the forms, keyed by the key of the macro in the environment: name, number
// of arguments and whether it has a receiver.
func macroForms() map[string]macroForm {
	return map[string]macroForm{
		"has:1:false":              {"has(e.f) -> bool", "Tests whether field f is present on e, without failing when it is absent.", 0},
		"all:2:true":               {"range.all(x, predicate) -> bool", "True when the predicate holds for every element of a list, or every key of a map.", onLists | onMaps},
		"all:3:true":               {"range.all(i, v, predicate) -> bool", "True when the predicate holds for every index and element of a list, or key and value of a map.", onLists | onMaps},
		"exists:2:true":            {"range.exists(x, predicate) -> bool", "True when the predicate holds for at least one element of a list, or key of a map.", onLists | onMaps},
		"exists:3:true":            {"range.exists(i, v, predicate) -> bool", "True when the predicate holds for at least one index and element of a list, or key and value of a map.", onLists | onMaps},
		"exists_one:2:true":        {"range.exists_one(x, predicate) -> bool", "True when the predicate holds for exactly one element of a list, or key of a map.", onLists | onMaps},
		"exists_one:3:true":        {"range.exists_one(i, v, predicate) -> bool", "True when the predicate holds for exactly one index and element of a list, or key and value of a map.", onLists | onMaps},
		"existsOne:3:true":         {"range.existsOne(i, v, predicate) -> bool", "True when the predicate holds for exactly one index and element of a list, or key and value of a map.", onLists | onMaps},
		"map:2:true":               {"range.map(x, transform) -> list", "Builds a list by applying the transform to each element of a list, or key of a map.", onLists | onMaps},
		"map:3:true":               {"range.map(x, filter, transform) -> list", "Builds a list by applying the transform to each element the filter accepts.", onLists | onMaps},
		"filter:2:true":            {"range.filter(x, predicate) -> list", "Keeps the elements for which the predicate holds.", onLists | onMaps},
		"optMap:2:true":            {"opt.optMap(x, transform) -> optional", "Applies the transform to the value of an optional; an empty optional stays empty.", onOptionals},
		"optFlatMap:2:true":        {"opt.optFlatMap(x, transform) -> optional", "Applies a transform that returns an optional to the value of an optional; an empty optional stays empty.", onOptionals},
		"transformList:3:true":     {"range.transformList(i, v, transform) -> list", "Builds a list by applying the transform to each index and element of a list, or key and value of a map.", onLists | onMaps},
		"transformList:4:true":     {"range.transformList(i, v, filter, transform) -> list", "Builds a list by applying the transform to each index and element the filter accepts.", onLists | onMaps},
		"transformMap:3:true":      {"range.transformMap(i, v, transform) -> map", "Builds a map with the keys of a map, or the indexes of a list, and the transform of each value.", onLists | onMaps},
		"transformMap:4:true":      {"range.transformMap(i, v, filter, transform) -> map", "Builds a map like transformMap, with only the entries the filter accepts.", onLists | onMaps},
		"transformMapEntry:3:true": {"range.transformMapEntry(i, v, transform) -> map", "Builds a map from the single-entry maps the transform returns for each index and element, or key and value.", onLists | onMaps},
		"transformMapEntry:4:true": {"range.transformMapEntry(i, v, filter, transform) -> map", "Builds a map like transformMapEntry, with only the entries the filter accepts.", onLists | onMaps},
		"sortBy:2:true":            {"list.sortBy(x, key) -> list", "A copy of the list sorted by the key computed for each element.", onLists},
	}
}

// description returns the short descriptions of the functions the CEL
// environment declares without documentation, or "" for any other name.
func description(name string) string {
	switch name {
	case "size":
		return "The number of elements of a list or map, characters of a string or bytes of a bytes value."
	case "contains":
		return "True when the string contains the given substring."
	case "startsWith":
		return "True when the string starts with the given prefix."
	case "endsWith":
		return "True when the string ends with the given suffix."
	case "matches":
		return "True when the string matches the RE2 regular expression, anywhere in it."
	case "int":
		return "Converts a value to int."
	case "uint":
		return "Converts a value to uint."
	case "double":
		return "Converts a value to double."
	case "string":
		return "Converts a value to string."
	case "bytes":
		return "Converts a string to bytes."
	case "bool":
		return "Converts a string to bool."
	case "duration":
		return "Parses a duration such as \"1h30m\"."
	case "timestamp":
		return "Parses an RFC 3339 timestamp."
	case "type":
		return "The type of a value."
	case "dyn":
		return "Treats a value as dynamically typed."
	case "isSorted":
		return "True when the list is in ascending order. Kubernetes list library."
	case "sum":
		return "The sum of the elements of a list. Kubernetes list library."
	case "min":
		return "The smallest element of a non-empty list. Kubernetes list library."
	case "max":
		return "The largest element of a non-empty list. Kubernetes list library."
	case "indexOf":
		return "The index of the first occurrence of a value in a string or list, or -1."
	case "lastIndexOf":
		return "The index of the last occurrence of a value in a string or list, or -1."
	case "slice":
		return "The sub-list from the start index up to, not including, the end index."
	case "sort":
		return "A sorted copy of the list. Kubernetes list library."
	case "first":
		return "The first element of a list as an optional."
	case "last":
		return "The last element of a list as an optional."
	case "charAt":
		return "The character at an index of a string. Strings extension."
	case "lowerAscii":
		return "The string with ASCII letters lowercased. Strings extension."
	case "upperAscii":
		return "The string with ASCII letters uppercased. Strings extension."
	case "replace":
		return "Replaces occurrences of a substring in a string. Strings extension."
	case "split":
		return "Splits a string at a separator into a list. Strings extension."
	case "substring":
		return "The part of a string between two indexes. Strings extension."
	case "trim":
		return "The string without leading and trailing white space. Strings extension."
	case "join":
		return "Joins a list of strings with a separator. Strings extension."
	case "format":
		return "Formats a string with printf-style verbs (%s, %d, %f, %e, %b, %x, %o). Strings extension."
	case "find":
		return "The first match of a regular expression in a string, or \"\". Kubernetes regex library."
	case "findAll":
		return "Every match of a regular expression in a string. Kubernetes regex library."
	case "url":
		return "Parses a string as a URL. Kubernetes URL library."
	case "isURL":
		return "True when the string is a valid URL. Kubernetes URL library."
	case "getScheme":
		return "The scheme of a URL. Kubernetes URL library."
	case "getHost":
		return "The host, with port, of a URL. Kubernetes URL library."
	case "getHostname":
		return "The host name of a URL. Kubernetes URL library."
	case "getPort":
		return "The port of a URL, or \"\". Kubernetes URL library."
	case "getEscapedPath":
		return "The escaped path of a URL. Kubernetes URL library."
	case "getQuery":
		return "The query parameters of a URL as a map of lists. Kubernetes URL library."
	case "quantity":
		return "Parses a Kubernetes resource quantity such as \"500Mi\". Kubernetes quantity library."
	case "isQuantity":
		return "True when the string is a valid quantity. Kubernetes quantity library."
	case "isLessThan":
		return "True when the quantity is less than the argument. Kubernetes quantity library."
	case "isGreaterThan":
		return "True when the quantity is greater than the argument. Kubernetes quantity library."
	case "compareTo":
		return "-1, 0 or 1 when the quantity or semver is less than, equal to or greater than the argument."
	case "asInteger":
		return "The quantity as int. Kubernetes quantity library."
	case "asApproximateFloat":
		return "The quantity as double, possibly losing precision. Kubernetes quantity library."
	case "isInteger":
		return "True when the quantity fits an int. Kubernetes quantity library."
	case "sign":
		return "-1, 0 or 1 by the sign of the quantity. Kubernetes quantity library."
	case "ip":
		return "Parses an IP address. Kubernetes IP library."
	case "isIP":
		return "True when the string is an IP address. Kubernetes IP library."
	case "cidr":
		return "Parses a CIDR such as \"10.0.0.0/8\". Kubernetes IP library."
	case "isCIDR":
		return "True when the string is a CIDR. Kubernetes IP library."
	case "containsIP":
		return "True when the CIDR contains the IP address or CIDR. Kubernetes IP library."
	case "containsCIDR":
		return "True when the CIDR contains the other CIDR. Kubernetes IP library."
	case "masked":
		return "The CIDR with host bits cleared. Kubernetes IP library."
	case "prefixLength":
		return "The prefix length of a CIDR. Kubernetes IP library."
	case "family":
		return "4 or 6, the IP address family. Kubernetes IP library."
	case "isLoopback":
		return "True for a loopback IP. Kubernetes IP library."
	case "isUnspecified":
		return "True for an unspecified IP (0.0.0.0 or ::). Kubernetes IP library."
	case "isGlobalUnicast":
		return "True for a global unicast IP. Kubernetes IP library."
	case "isLinkLocalUnicast":
		return "True for a link-local unicast IP. Kubernetes IP library."
	case "semver":
		return "Parses a semantic version. Kubernetes semver library."
	case "isSemver":
		return "True when the string is a semantic version. Kubernetes semver library."
	case "major":
		return "The major version of a semver. Kubernetes semver library."
	case "minor":
		return "The minor version of a semver. Kubernetes semver library."
	case "patch":
		return "The patch version of a semver. Kubernetes semver library."
	case "labelSelector":
		return "Parses a label selector. Kubernetes selector library."
	case "fieldSelector":
		return "Parses a field selector. Kubernetes selector library."
	case "validate":
		return "Validates a name or selector string, returning the errors as an optional list. Kubernetes format library."
	case "check":
		return "Checks the request against the authorizer and returns the decision. Kubernetes authz library."
	case "group":
		return "Sets the API group to check. Kubernetes authz library."
	case "resource":
		return "Sets the resource to check. Kubernetes authz library."
	case "subresource":
		return "Sets the subresource to check. Kubernetes authz library."
	case "namespace":
		return "Sets the namespace to check. Kubernetes authz library."
	case "name":
		return "Sets the name of the object to check. Kubernetes authz library."
	case "serviceAccount":
		return "Checks as the given service account. Kubernetes authz library."
	case "allowed":
		return "True when the decision allows the request. Kubernetes authz library."
	case "reason":
		return "The reason of the decision. Kubernetes authz library."
	case "errored":
		return "True when the decision could not be made. Kubernetes authz library."
	case "hasValue":
		return "True when the optional holds a value."
	case "value":
		return "The value of an optional; an error when it is empty."
	case "orValue":
		return "The value of an optional, or the default when it is empty."
	case "or":
		return "This optional, or the other one when this is empty."
	case "distinct":
		return "The list without duplicates. Lists extension."
	case "flatten":
		return "Flattens nested lists. Lists extension."
	case "reverse":
		return "The list or string in reverse order."
	case "getFullYear":
		return "The year of a timestamp."
	case "getMonth":
		return "The month of a timestamp, from 0."
	case "getDayOfMonth":
		return "The day of the month of a timestamp, from 0."
	case "getDayOfWeek":
		return "The day of the week of a timestamp, from 0 for Sunday."
	case "getDayOfYear":
		return "The day of the year of a timestamp, from 0."
	case "getDate":
		return "The day of the month of a timestamp, from 1."
	case "getHours":
		return "The hours of a timestamp or duration."
	case "getMinutes":
		return "The minutes of a timestamp or duration."
	case "getSeconds":
		return "The seconds of a timestamp or duration."
	case "getMilliseconds":
		return "The milliseconds of a timestamp or duration."
	}
	return ""
}

// buildCatalog lists the functions, by name, and the macro forms the API server's
// CEL environment of version v declares. Operators and internal functions are
// left out. A macro the environment declares and macroForms lacks is left out.
func buildCatalog(v kubeversion.Version) ([]function, []macro, error) {
	env, err := v.EnvSet().Env(environment.NewExpressions)
	if err != nil {
		return nil, nil, fmt.Errorf("build CEL environment: %w", err)
	}
	funcs, macros := catalogOf(env)
	return funcs, macros, nil
}

// catalogOf lists the functions and macro forms env declares, as buildCatalog does.
func catalogOf(env *cel.Env) ([]function, []macro) {
	var ms []macro
	forms := macroForms()
	for _, m := range env.Macros() {
		if form, ok := forms[m.MacroKey()]; ok {
			ms = append(ms, macro{m.Function(), m.ArgCount(), m.IsReceiverStyle(), form.sig, form.desc, form.recv})
		}
	}
	slices.SortFunc(ms, func(a, b macro) int {
		return cmp.Or(strings.Compare(a.name, b.name), cmp.Compare(a.args, b.args))
	})
	var out []function
	for name, decl := range env.Functions() {
		if strings.HasPrefix(name, "_") || strings.Contains(name, "@") {
			continue
		}
		f := function{name: name, desc: decl.Description()}
		if f.desc == "" {
			f.desc = cmp.Or(description(name), kyvernoDescription(name))
		}
		for _, o := range decl.OverloadDecls() {
			args := o.ArgTypes()
			result := typeName(o.ResultType())
			if o.IsMemberFunction() && len(args) > 0 {
				sig := fmt.Sprintf("%s.%s(%s) -> %s", typeName(args[0]), name, typeList(args[1:]), result)
				f.members = append(f.members, member{args[0].Kind(), typeName(args[0]), args[0].TypeName() == "optional_type", overload{paramNames(args), sig, result}})
				continue
			}
			f.global = append(f.global, overload{paramNames(args), fmt.Sprintf("%s(%s) -> %s", name, typeList(args), result), result})
		}
		slices.SortFunc(f.global, func(a, b overload) int { return strings.Compare(a.sig, b.sig) })
		f.global = slices.CompactFunc(f.global, func(a, b overload) bool { return a.sig == b.sig })
		slices.SortFunc(f.members, func(a, b member) int { return strings.Compare(a.sig, b.sig) })
		f.members = slices.CompactFunc(f.members, func(a, b member) bool { return a.sig == b.sig })
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, ms
}

// protobufNames writes the protobuf well-known types as the CEL docs do.
var protobufNames = strings.NewReplacer("google.protobuf.Duration", "duration", "google.protobuf.Timestamp", "timestamp")

func typeName(t *types.Type) string { return protobufNames.Replace(t.String()) }

func paramNames(ts []*types.Type) []string {
	s := make([]string, len(ts))
	for i, t := range ts {
		s[i] = typeName(t)
	}
	return s
}

func typeList(ts []*types.Type) string {
	s := make([]string, len(ts))
	for i, t := range ts {
		s[i] = typeName(t)
	}
	return strings.Join(s, ", ")
}

// accepts reports whether the overload can be called on a receiver of the
// given kind, where types.UnspecifiedKind stands for an unknown receiver.
func (m member) accepts(kind types.Kind) bool {
	return m.anyType() || kind == types.UnspecifiedKind || m.recv == kind
}

// anyType reports whether the overload is declared on any type, not on one.
func (m member) anyType() bool {
	return m.recv == types.DynKind || m.recv == types.AnyKind || m.recv == types.TypeParamKind
}

// acceptsOptional reports whether the overload can be called on an optional
// value: one declared on optionals, or on any type.
func (m member) acceptsOptional() bool {
	return m.optional || m.anyType()
}

// onType reports whether the overload is declared on the opaque type named
// celType, or on any type, or celType is empty.
func (m member) onType(celType string) bool {
	return celType == "" || m.anyType() || m.recvType == celType
}
