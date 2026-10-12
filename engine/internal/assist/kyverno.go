package assist

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/kyverno"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// newKyverno builds the Assistant of a Kyverno ValidatingPolicy from Kyverno's
// own CEL environment: the variables and functions it declares, and no others.
func newKyverno() (*Assistant, error) {
	env, err := kyverno.Env()
	if err != nil {
		return nil, err
	}
	funcs, macros := catalogOf(env)
	a := &Assistant{funcs: funcs, macros: macros, byName: make(map[string]function, len(funcs))}
	for _, f := range funcs {
		a.byName[f.name] = f
	}
	for _, v := range env.Variables() {
		if strings.HasSuffix(v.Name(), "_version") {
			// Declared by a function library to say which version it is; no policy uses it.
			continue
		}
		a.vars = append(a.vars, kyvernoVariable(v.Name(), typeName(v.Type())))
	}
	slices.SortFunc(a.vars, func(x, y variable) int { return strings.Compare(x.name, y.name) })
	return a, nil
}

// isKyverno reports whether e is in a Kyverno ValidatingPolicy.
func isKyverno(doc *Document, e extract.Expression) bool {
	if e.Document >= len(doc.roots) {
		return false
	}
	root := doc.roots[e.Document]
	return root["kind"] == "ValidatingPolicy" && root["apiVersion"] == "policies.kyverno.io/v1"
}

// kyvernoVariable describes the variable name of Kyverno's environment, which
// has the CEL type typ.
func kyvernoVariable(name, typ string) variable {
	v := variable{name: name, typeName: typ, celType: typ}
	switch name {
	case "object":
		v.desc, v.schema, v.celType = "The object being admitted; null on DELETE.", unknownType(), ""
	case "oldObject":
		v.desc, v.schema, v.celType = "The existing object; null on CREATE.", unknownType(), ""
	case "namespaceObject":
		v.desc, v.schema, v.celType = "The namespace the object belongs to; null for cluster-scoped objects.", unknownType(), ""
	case "request":
		v.desc, v.schema, v.celType = "The attributes of the admission request.", requestType(true), ""
	case "variables":
		v.desc, v.celType = "The policy variables of spec.variables, by name.", ""
	case "exceptions":
		v.desc, v.celType = "What the policy exceptions that apply allow.", ""
		v.schema = obj(map[string]any{
			"allowedImages": map[string]any{"type": "array", "items": str(""), "description": "Images the exceptions allow."},
			"allowedValues": map[string]any{"type": "array", "items": str(""), "description": "Values the exceptions allow."},
		}, "")
	case "resource":
		v.desc = "Reads Kubernetes resources: resource.Get(...), resource.List(...) and resource.Post(...)."
	case "http":
		v.desc = "Makes HTTP calls: http.Get(...), http.Post(...) and http.Client(...)."
	case "image":
		v.desc = "Reads image metadata from a registry: image.GetMetadata(...)."
	case "globalContext":
		v.desc = "Reads the entries of a GlobalContextEntry: globalContext.Get(...)."
	case "json":
		v.desc = "Parses JSON: json.unmarshal(...)."
	case "yaml":
		v.desc = "Parses YAML: yaml.parse(...)."
	}
	return v
}

// kyvernoScope declares the variables of Kyverno's environment for the
// expression e of the ValidatingPolicy root: object and oldObject typed from the
// kinds the policy matches, and variables only for the expressions that may use
// them, as for a ValidatingAdmissionPolicy.
func (a *Assistant) kyvernoScope(root map[string]any, e extract.Expression) []variable {
	spec, _ := root["spec"].(map[string]any)
	kinds, typed := matchedKinds(spec)
	var vars []variable
	for _, v := range a.vars {
		switch {
		case v.name == "variables":
			if strings.HasPrefix(e.FieldPath, "spec.matchConditions[") {
				continue
			}
			v.schema = variablesType(spec, e)
		case typed && (v.name == "object" || v.name == "oldObject"):
			v.typeName, v.schema = kinds.name, kinds.schema
		}
		vars = append(vars, v)
	}
	return vars
}

// variableLimit is the number of variables of spec.variables that e may use:
// the ones before e when e is one of them, and -1, every variable, otherwise.
func variableLimit(e extract.Expression) int {
	limit := -1
	if rest, ok := strings.CutPrefix(e.FieldPath, "spec.variables["); ok {
		limit, _ = strconv.Atoi(strings.SplitN(rest, "]", 2)[0])
	}
	return limit
}

// variablesType returns the schema of the variables variable for e: a property
// for each variable of spec.variables, only those before e when e is one of them.
func variablesType(spec map[string]any, e extract.Expression) schema {
	limit := variableLimit(e)
	props := map[string]any{}
	list, _ := spec["variables"].([]any)
	for i, item := range list {
		if limit >= 0 && i >= limit {
			break
		}
		if m, ok := item.(map[string]any); ok {
			if name, ok := m["name"].(string); ok {
				props[name] = map[string]any{"description": fmt.Sprintf("Policy variable %s, defined in spec.variables.", name), "x-kubernetes-preserve-unknown-fields": true}
			}
		}
	}
	return schema{"type": "object", "properties": props}
}

// kyvernoDescription returns the one-line description of a function that
// Kyverno's libraries declare, or "" for any other name. The libraries document
// none of them.
func kyvernoDescription(name string) string {
	switch name {
	case "Get", "get":
		return "Reads one resource, a global context entry or a URL, by the receiver: resource, globalContext or http. Kyverno resource, global context and HTTP libraries."
	case "List", "list":
		return "Lists the resources of a kind, in a namespace or the cluster, optionally by label. Kyverno resource library."
	case "Post", "post":
		return "Creates a resource, or sends an HTTP POST request with a body. Kyverno resource and HTTP libraries."
	case "Client", "client":
		return "An HTTP client with its own CA bundle, for the calls that follow. Kyverno HTTP library."
	case "GetMetadata", "getMetadata":
		return "The metadata of an image in its registry: manifest, config and digest. Kyverno image data library."
	case "ToGVR", "toGVR":
		return "The group, version and resource of an apiVersion and kind. Kyverno resource library."
	case "image":
		return "Parses a string as an image reference. Kyverno image library."
	case "isImage":
		return "True when the string is a valid image reference. Kyverno image library."
	case "registry":
		return "The registry of an image reference. Kyverno image library."
	case "repository":
		return "The repository of an image reference. Kyverno image library."
	case "path":
		return "The repository path of an image reference. Kyverno image library."
	case "tag":
		return "The tag of an image reference, or \"\". Kyverno image library."
	case "digest":
		return "The digest of an image reference, or \"\". Kyverno image library."
	case "identifier":
		return "The tag or digest of an image reference. Kyverno image library."
	case "identifierWithSeparator":
		return "The tag or digest of an image reference with its separator, : or @. Kyverno image library."
	case "containsDigest":
		return "True when the image reference has a digest. Kyverno image library."
	case "parseImageReference":
		return "Splits an image reference into registry, repository, tag and digest. Kyverno image library."
	case "parseServiceAccount", "user.parseServiceAccount":
		return "Splits a service account user name into its namespace and name. Kyverno user library."
	case "hash.md5", "md5":
		return "The MD5 hash of a string, in hexadecimal. Kyverno hash library."
	case "hash.sha1", "sha1":
		return "The SHA-1 hash of a string, in hexadecimal. Kyverno hash library."
	case "hash.sha256", "sha256":
		return "The SHA-256 hash of a string, in hexadecimal. Kyverno hash library."
	case "json.encode", "marshal":
		return "Encodes a value as a JSON string. Kyverno JSON library."
	case "unmarshal":
		return "Decodes a JSON string into a value. Kyverno JSON library."
	case "parse":
		return "Decodes a YAML string into a value. Kyverno YAML library."
	case "gzip.compress":
		return "Compresses a string with gzip, as base64. Kyverno gzip library."
	case "gzip.decompress":
		return "Decompresses a base64 gzip string. Kyverno gzip library."
	case "base64.encode":
		return "Encodes a string as base64. Kyverno base64 library."
	case "base64.decode":
		return "Decodes a base64 string. Kyverno base64 library."
	case "listObjToMap", "transform.listObjToMap":
		return "Builds a map from two lists of objects, keyed by a field of each. Kyverno transform library."
	case "lists.range":
		return "The list of integers from 0 up to, not including, the argument. Kyverno lists library."
	case "random", "random.random":
		return "A random string that matches a pattern such as \"[0-9a-z]{8}\". Kyverno random library."
	case "regex.extract":
		return "The first match of a regular expression in a string as an optional. Kyverno regex library."
	case "regex.extractAll":
		return "Every match of a regular expression in a string. Kyverno regex library."
	case "regex.replace":
		return "Replaces the matches of a regular expression in a string. Kyverno regex library."
	case "strings.quote":
		return "The string quoted and escaped as a CEL string literal. Kyverno strings library."
	case "time.now":
		return "The current time as a timestamp. Kyverno time library."
	case "time.toCron":
		return "The cron expression of a timestamp. Kyverno time library."
	case "time.truncate":
		return "A timestamp rounded down to a multiple of a duration. Kyverno time library."
	case "x509.decode":
		return "Decodes a PEM certificate. Kyverno x509 library."
	case "add":
		return "The quantity plus an integer or another quantity. Kubernetes quantity library."
	case "sub":
		return "The quantity minus an integer or another quantity. Kubernetes quantity library."
	case "error":
		return "The error message of the decision. Kubernetes authz library."
	case "ip.isCanonical":
		return "True when the string is an IP address in canonical form. Kubernetes IP library."
	case "isLinkLocalMulticast":
		return "True for a link-local multicast IP. Kubernetes IP library."
	case "math.abs":
		return "The absolute value of a number. Math extension."
	case "math.ceil":
		return "The smallest integer not less than a double. Math extension."
	case "math.floor":
		return "The largest integer not greater than a double. Math extension."
	case "math.round":
		return "A double rounded to the nearest integer, halves away from zero. Math extension."
	case "math.trunc":
		return "A double without its fraction. Math extension."
	case "math.sqrt":
		return "The square root of a number. Math extension."
	case "math.sign":
		return "-1, 0 or 1 by the sign of a number. Math extension."
	case "math.isNaN":
		return "True when a double is not a number. Math extension."
	case "math.isInf":
		return "True when a double is infinite. Math extension."
	case "math.isFinite":
		return "True when a double is neither infinite nor not a number. Math extension."
	case "math.bitAnd":
		return "The bitwise AND of two integers. Math extension."
	case "math.bitOr":
		return "The bitwise OR of two integers. Math extension."
	case "math.bitXor":
		return "The bitwise XOR of two integers. Math extension."
	case "math.bitNot":
		return "The bitwise NOT of an integer. Math extension."
	case "math.bitShiftLeft":
		return "An integer shifted left by a number of bits. Math extension."
	case "math.bitShiftRight":
		return "An integer shifted right by a number of bits. Math extension."
	}
	return ""
}
