package cellens

import "slices"

// Word is a keyword or a value that may be written at a place in a .cellens,
// with what it means in one line.
type Word struct {
	Name, Doc string
}

// schemaWords are the keywords of a schema that a .cellens offers, with what
// they do. A CRD's schema has others, such as default or allOf, which give a
// value no type, and x-kubernetes-validations, which a schema here refuses.
var schemaWords = map[string]string{
	"type":                                 "Its type: object, array, string, integer, number or boolean.",
	"description":                          "What the value is, shown in hover.",
	"nullable":                             "The value may be null.",
	"enum":                                 "The values it may have.",
	"format":                               "The format of the value; date-time makes a string a timestamp in CEL.",
	"properties":                           "The fields of the object, each with its schema.",
	"additionalProperties":                 "The schema of every value of a map.",
	"required":                             "The fields the object must have.",
	"maxProperties":                        "At most this many fields.",
	"minProperties":                        "At least this many fields.",
	"items":                                "The schema of every item of the list.",
	"maxItems":                             "At most this many items.",
	"minItems":                             "At least this many items.",
	"x-kubernetes-list-type":               "How the list merges: atomic, set or map.",
	"x-kubernetes-list-map-keys":           "The keys of the items of a list of type map.",
	"maxLength":                            "At most this many characters.",
	"minLength":                            "At least this many characters.",
	"pattern":                              "A regular expression the string matches.",
	"minimum":                              "The smallest value.",
	"maximum":                              "The largest value.",
	"exclusiveMinimum":                     "The value is more than minimum.",
	"exclusiveMaximum":                     "The value is less than maximum.",
	"multipleOf":                           "The value is a multiple of this.",
	"x-kubernetes-int-or-string":           "An integer or a string, without type: dyn in CEL.",
	"x-kubernetes-preserve-unknown-fields": "Keeps the fields it does not declare; CEL sees only the declared ones.",
	"x-kubernetes-embedded-resource":       "An object with apiVersion, kind and metadata.",
	"x-kubernetes-map-type":                "How the map merges: atomic or granular.",
}

// typeWords are the keywords each type of a schema offers, after those every
// schema offers.
var typeWords = map[string][]string{
	"object":  {"properties", "additionalProperties", "required", "maxProperties", "minProperties", "x-kubernetes-preserve-unknown-fields", "x-kubernetes-embedded-resource", "x-kubernetes-map-type"},
	"array":   {"items", "maxItems", "minItems", "x-kubernetes-list-type", "x-kubernetes-list-map-keys"},
	"string":  {"format", "maxLength", "minLength", "pattern"},
	"integer": {"format", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"},
	"number":  {"format", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"},
	"boolean": {},
}

// SchemaAt reports whether path, the keys and "-" from schema: down to a
// mapping, leads to a schema: schema: itself, the schema of a property, of
// items, of additionalProperties or of not, or an item of allOf, anyOf or
// oneOf. The mapping of properties, whose keys are names, is no schema.
func SchemaAt(path []string) bool {
	at := "schema"
	for _, k := range path {
		switch {
		case at == "names":
			at = "schema"
		case at == "list" && k == "-":
			at = "schema"
		case at == "schema" && k == "properties":
			at = "names"
		case at == "schema" && slices.Contains([]string{"items", "additionalProperties", "not"}, k):
		case at == "schema" && slices.Contains([]string{"allOf", "anyOf", "oneOf"}, k):
			at = "list"
		default:
			return false
		}
	}
	return at == "schema"
}

// SchemaKeywords returns the keywords a schema whose keys so far are keys,
// with their values as Spot has them, can take next: those of every schema,
// and those of its type; type first while it has none, with the extensions
// that stand for one. A keyword it has is left out, and so is
// additionalProperties beside properties, and the other way round.
func SchemaKeywords(keys map[string]string) []Word {
	names := []string{"description", "nullable", "enum"}
	switch t, typed := keys["type"]; {
	case typed:
		names = append(names, typeWords[t]...)
	case keys["x-kubernetes-int-or-string"] == "true":
	default:
		names = append([]string{"type", "x-kubernetes-int-or-string", "x-kubernetes-preserve-unknown-fields"}, names...)
	}
	var out []Word
	for _, n := range names {
		_, has := keys[n]
		_, props := keys["properties"]
		_, values := keys["additionalProperties"]
		if has || n == "additionalProperties" && props || n == "properties" && values || n == "required" && values {
			continue
		}
		out = append(out, Word{n, schemaWords[n]})
	}
	return out
}

// SchemaValues returns the values the keyword key of a schema whose keys are
// keys can have, when there are few: the types for type, the formats of its
// type for format, the merge kinds of x-kubernetes-list-type and
// x-kubernetes-map-type, and true and false for a keyword that is a boolean.
// Other keywords have none.
func SchemaValues(key string, keys map[string]string) []Word {
	switch key {
	case "type":
		return []Word{
			{"object", "An object: a struct with properties, or a map with additionalProperties."},
			{"array", "A list, with items."},
			{"string", "A string."},
			{"integer", "An int in CEL."},
			{"number", "A double in CEL."},
			{"boolean", "A bool in CEL."},
		}
	case "format":
		switch keys["type"] {
		case "string":
			return []Word{
				{"date-time", "A timestamp in CEL."},
				{"date", "A timestamp in CEL."},
				{"duration", "A duration in CEL."},
				{"byte", "Base64: bytes in CEL."},
				{"uri", "A URI: a string in CEL."},
				{"email", "An email address: a string in CEL."},
				{"hostname", "A host name: a string in CEL."},
				{"ipv4", "An IPv4 address: a string in CEL."},
				{"ipv6", "An IPv6 address: a string in CEL."},
				{"cidr", "A CIDR: a string in CEL."},
				{"uuid", "A UUID: a string in CEL."},
			}
		case "integer":
			return []Word{{"int32", "A 32-bit integer."}, {"int64", "A 64-bit integer."}}
		case "number":
			return []Word{{"float", "A 32-bit number."}, {"double", "A 64-bit number."}}
		}
	case "x-kubernetes-list-type":
		return []Word{
			{"atomic", "The list is replaced as a whole."},
			{"set", "Its items are unique scalars, merged as a set."},
			{"map", "Its items are objects merged by x-kubernetes-list-map-keys."},
		}
	case "x-kubernetes-map-type":
		return []Word{{"atomic", "The map is replaced as a whole."}, {"granular", "Its fields merge one by one."}}
	case "nullable", "exclusiveMinimum", "exclusiveMaximum", "x-kubernetes-int-or-string",
		"x-kubernetes-preserve-unknown-fields", "x-kubernetes-embedded-resource":
		return []Word{{"true", ""}, {"false", ""}}
	}
	return nil
}
