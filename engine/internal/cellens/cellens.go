// Package cellens reads .cellens files: the types cel-lens assumes for values
// Kubernetes leaves dyn.
//
// A .cellens file is YAML. Its one key, types, lists entries; each names a
// value in a YAML file at or below the .cellens file's folder, and the type to
// assume there:
//
//	types:
//	  - file: config/crd/bases/widgets.yaml
//	    document: CustomResourceDefinition widgets.example.com
//	    version: v1
//	    path: spec.maxSurge
//	    type: int
//	  - file: policies/limits.yaml
//	    document: ValidatingAdmissionPolicy limits
//	    variable: params
//	    type: "{name: string, replicas: int}"
//
// Parse reads a file and reports every problem in it with its line; nothing
// here reads the workspace or knows which values are dyn.
package cellens

import (
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// FileName is the name of the files this package reads.
const FileName = ".cellens"

// CRDKind is the kind of a CustomResourceDefinition document.
const CRDKind = "CustomResourceDefinition"

// Entry is one entry of a .cellens file: a value and the type to assume for it.
// Exactly one of Type and Kind is set.
type Entry struct {
	// Line is the entry's line in the file, from 1.
	Line int
	// File is the YAML file the value is in, slash-separated and relative to
	// the .cellens file's folder, never outside it.
	File string
	// Document is the document of File the value is in.
	Document Document
	// Version is a CustomResourceDefinition's served version; empty for a policy.
	Version string
	// Variable is a policy's variable: params, object, oldObject or
	// variables.<name>; empty for a CustomResourceDefinition.
	Variable string
	// Path leads from the object's root (a CRD) or the variable (a policy) to
	// the value.
	Path Path
	// Type is the type written with type:.
	Type *Type
	// Kind is the kind written with kind:, a built-in kind or a CRD's.
	Kind *KindRef
}

// Document names one document of a YAML file by its kind and metadata.name.
type Document struct {
	Kind string
	Name string
}

// KindRef names a kind by its apiVersion, such as apps/v1 or example.com/v1,
// and its kind.
type KindRef struct {
	APIVersion string
	Kind       string
}

// Problem is something wrong in a .cellens file, at a line and column from 1.
type Problem struct {
	Line    int
	Column  int
	Message string
}

func (p Problem) String() string { return fmt.Sprintf("%d:%d: %s", p.Line, p.Column, p.Message) }

// Parse reads a .cellens file. It returns the entries that are valid and a
// problem for everything else, ordered by line and column; an entry with a
// problem is left out. An empty file, or one with only comments, has neither.
func Parse(data []byte) ([]Entry, []Problem) {
	entries, problems := parse(data)
	slices.SortStableFunc(problems, func(a, b Problem) int {
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Column - b.Column
	})
	return entries, problems
}

func parse(data []byte) ([]Entry, []Problem) {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, []Problem{notYAML(err)}
	}
	var problems []Problem
	var extra yaml.Node
	switch err := dec.Decode(&extra); {
	case err == nil:
		problems = append(problems, Problem{Line: extra.Line, Column: 1, Message: "a second YAML document: a .cellens holds one"})
	case !errors.Is(err, io.EOF):
		problems = append(problems, notYAML(err))
	}
	if len(doc.Content) == 0 {
		return nil, problems
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, append(problems, at(root, "a .cellens is a mapping with the key types"))
	}
	var list *yaml.Node
	seen := map[string]int{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		switch {
		case key.Value != "types":
			problems = append(problems, at(key, fmt.Sprintf("unknown key %q: a .cellens has only types", key.Value)))
		case seen[key.Value] > 0:
			problems = append(problems, twice(key, seen[key.Value]))
		default:
			seen[key.Value] = key.Line
			list = value
		}
	}
	if list == nil || list.Tag == "!!null" {
		return nil, problems
	}
	if list.Kind != yaml.SequenceNode {
		return nil, append(problems, at(list, "types is a list of entries"))
	}
	src := sourceLines(data)
	var entries []Entry
	firstLine := map[string]int{}
	for _, item := range list.Content {
		e, ps := parseEntry(item, src)
		if len(ps) > 0 {
			problems = append(problems, ps...)
			continue
		}
		key := e.valueKey()
		if line, ok := firstLine[key]; ok {
			problems = append(problems, at(item, fmt.Sprintf("a second entry for the value of line %d: keep one", line)))
			continue
		}
		firstLine[key] = e.Line
		entries = append(entries, e)
	}
	return entries, problems
}

func notYAML(err error) Problem {
	return Problem{Line: errorLine(err), Column: 1, Message: "not YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}
}

func twice(key *yaml.Node, first int) Problem {
	return at(key, fmt.Sprintf("key %q appears twice (first at line %d): keep one", key.Value, first))
}

// valueKey identifies the value an entry names, to find two entries for one value.
func (e Entry) valueKey() string {
	return strings.Join([]string{e.File, e.Document.Kind, e.Document.Name, e.Version, e.Variable, e.Path.String()}, "\x00")
}

var entryKeys = []string{"file", "document", "version", "variable", "path", "type", "kind"}

func parseEntry(item *yaml.Node, src [][]rune) (Entry, []Problem) {
	e := Entry{Line: item.Line}
	if item.Kind != yaml.MappingNode {
		return e, []Problem{at(item, "an entry is a mapping with file, document, and type or kind")}
	}
	fields := map[string]*yaml.Node{}
	firstAt := map[string]int{}
	var problems []Problem
	for i := 0; i+1 < len(item.Content); i += 2 {
		key, value := item.Content[i], item.Content[i+1]
		switch {
		case !slices.Contains(entryKeys, key.Value):
			problems = append(problems, at(key, fmt.Sprintf("unknown key %q: an entry has file, document, version, variable, path, and type or kind", key.Value)))
		case firstAt[key.Value] > 0:
			problems = append(problems, twice(key, firstAt[key.Value]))
		case key.Value == "type" && value.Kind == yaml.MappingNode:
			problems = append(problems, at(value, `quote the type: YAML reads {...} as a mapping, so write type: "{name: string}"`))
		case value.Kind != yaml.ScalarNode:
			problems = append(problems, at(value, key.Value+" is a single value"))
		default:
			firstAt[key.Value] = key.Line
			fields[key.Value] = value
		}
	}
	if len(problems) > 0 {
		return e, problems
	}
	problems = append(problems, e.readTarget(item, fields)...)
	problems = append(problems, e.readValue(item, fields)...)
	problems = append(problems, e.readType(item, fields, src)...)
	return e, problems
}

// readTarget reads file and document.
func (e *Entry) readTarget(item *yaml.Node, fields map[string]*yaml.Node) []Problem {
	var problems []Problem
	if n, ok := fields["file"]; !ok || n.Value == "" {
		problems = append(problems, at(item, "the entry has no file: name the YAML file the value is in, relative to this .cellens"))
	} else if f := path.Clean(strings.TrimSpace(n.Value)); !isLocalFile(f) {
		problems = append(problems, at(n, fmt.Sprintf("file %q is not a file in this .cellens's folder or below it: name one with a relative, slash-separated path", n.Value)))
	} else {
		e.File = f
	}
	n, ok := fields["document"]
	if !ok || n.Value == "" {
		return append(problems, at(item, `the entry has no document: write its kind and metadata.name, such as "CustomResourceDefinition widgets.example.com"`))
	}
	words := strings.Fields(n.Value)
	if len(words) != 2 || !kindName.MatchString(words[0]) {
		return append(problems, at(n, fmt.Sprintf("document %q: write its kind and metadata.name, separated by a space", n.Value)))
	}
	if !slices.Contains(celKinds, words[0]) {
		return append(problems, at(n, fmt.Sprintf("document %q: cel-lens reads CEL in %s documents", n.Value, strings.Join(celKinds, ", "))))
	}
	e.Document = Document{Kind: words[0], Name: words[1]}
	return problems
}

// celKinds are the kinds of document whose CEL cel-lens reads.
var celKinds = []string{CRDKind, "ValidatingAdmissionPolicy", "MutatingAdmissionPolicy", "ValidatingPolicy"}

var (
	driveLetter = regexp.MustCompile(`^[A-Za-z]:`)
	version     = regexp.MustCompile(`^v\d+((alpha|beta)\d+)?$`)
	kindName    = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	apiVersion  = regexp.MustCompile(`^([a-z0-9]([-a-z0-9.]*[a-z0-9])?/)?v[0-9]+((alpha|beta)[0-9]+)?$`)
	variables   = regexp.MustCompile(`^(params|object|oldObject|variables\.[A-Za-z_][A-Za-z0-9_]*)$`)
)

// readValue reads version or variable, and path.
func (e *Entry) readValue(item *yaml.Node, fields map[string]*yaml.Node) []Problem {
	var problems []Problem
	versionNode, hasVersion := fields["version"]
	variable, hasVariable := fields["variable"]
	switch {
	case e.Document.Kind == "":
		// readTarget reported the document.
	case e.Document.Kind == CRDKind && hasVariable:
		problems = append(problems, at(variable, "variable is for policies: a CustomResourceDefinition's entry names the value with version and path"))
	case e.Document.Kind == CRDKind && (!hasVersion || versionNode.Value == ""):
		problems = append(problems, at(item, "a CustomResourceDefinition's entry needs version: the served version the value is in, such as v1"))
	case e.Document.Kind == CRDKind && !version.MatchString(versionNode.Value):
		problems = append(problems, at(versionNode, fmt.Sprintf("version %q: write a served version, such as v1 or v1beta1", versionNode.Value)))
	case e.Document.Kind == CRDKind:
		e.Version = versionNode.Value
	case hasVersion:
		problems = append(problems, at(versionNode, "version is for CustomResourceDefinitions: a policy's entry names the value with variable and path"))
	case !hasVariable || variable.Value == "":
		problems = append(problems, at(item, "a policy's entry needs variable: params, object, oldObject or variables.<name>"))
	case !variables.MatchString(variable.Value):
		problems = append(problems, at(variable, fmt.Sprintf("variable %q: use params, object, oldObject or variables.<name>", variable.Value)))
	default:
		e.Variable = variable.Value
	}
	if n, ok := fields["path"]; ok {
		p, err := ParsePath(n.Value)
		if err != nil {
			problems = append(problems, at(n, fmt.Sprintf("path %q: %v", n.Value, err)))
		}
		e.Path = p
	}
	return problems
}

// readType reads type or kind.
func (e *Entry) readType(item *yaml.Node, fields map[string]*yaml.Node, src [][]rune) []Problem {
	typ, hasType := fields["type"]
	kind, hasKind := fields["kind"]
	switch {
	case hasType && hasKind:
		return []Problem{at(kind, "the entry has both type and kind: keep one")}
	case hasType:
		t, err := ParseType(typ.Value)
		var te *TypeError
		if errors.As(err, &te) {
			return []Problem{typeProblem(typ, te, src)}
		}
		e.Type = t
	case hasKind:
		k, err := ParseKind(kind.Value)
		if err != nil {
			return []Problem{at(kind, err.Error())}
		}
		e.Kind = k
	default:
		return []Problem{at(item, `the entry has neither type nor kind: give the value a type, such as "{name: string}", or a kind, such as "apps/v1 Deployment"`)}
	}
	return nil
}

// ParseKind reads a kind as kind: takes it: its apiVersion and kind,
// separated by a space, such as "apps/v1 Deployment".
func ParseKind(s string) (*KindRef, error) {
	words := strings.Fields(s)
	if len(words) != 2 || !apiVersion.MatchString(words[0]) || !kindName.MatchString(words[1]) {
		return nil, fmt.Errorf(`kind %q: write the apiVersion and kind, such as "apps/v1 Deployment"`, s)
	}
	return &KindRef{APIVersion: words[0], Kind: words[1]}, nil
}

// valueKeys are the keys of an entry that name its value.
var valueKeys = []string{"file", "document", "version", "variable", "path"}

// ReadValue reads the keys of an entry that name its value, as Parse reads
// them in a file: f maps file, document, version, variable and path to what
// is written there, and a key that is missing or "" is not written; other
// keys of f are ignored. It returns the entry, without a type or kind, or what
// Parse would report wrong with those keys, as problems without a line or
// column.
func ReadValue(f map[string]string) (Entry, []Problem) {
	fields := map[string]*yaml.Node{}
	for _, k := range valueKeys {
		if v := f[k]; v != "" {
			fields[k] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
		}
	}
	item := &yaml.Node{Kind: yaml.MappingNode}
	var e Entry
	problems := e.readTarget(item, fields)
	problems = append(problems, e.readValue(item, fields)...)
	if len(problems) > 0 {
		return Entry{}, problems
	}
	return e, nil
}

// typeProblem places a type's problem on its character when the source
// line shows the type as written: a single-line plain or quoted value without
// escapes. Otherwise it stays on the value and the message names the character.
func typeProblem(n *yaml.Node, te *TypeError, src [][]rune) Problem {
	p := at(n, "type: "+te.Message)
	start := p.Column - 1
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		start++
	}
	text := []rune(n.Value)
	plainOrQuoted := n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) == 0
	if plainOrQuoted && n.Line >= 1 && n.Line <= len(src) {
		line := src[n.Line-1]
		if start >= 0 && start+len(text) <= len(line) && string(line[start:start+len(text)]) == n.Value {
			p.Column = start + te.Offset + 1
			return p
		}
	}
	p.Message = fmt.Sprintf("type: character %d: %s", te.Offset+1, te.Message)
	return p
}

// sourceLines splits the file into lines of characters, for columns.
func sourceLines(data []byte) [][]rune {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	out := make([][]rune, len(lines))
	for i, l := range lines {
		out[i] = []rune(l)
	}
	return out
}

// isLocalFile reports whether f, slash-separated and cleaned, names a file in
// the .cellens file's folder or below it on every system.
func isLocalFile(f string) bool {
	return f != "." && f != "" && !strings.Contains(f, "\\") && !driveLetter.MatchString(f) &&
		filepath.IsLocal(filepath.FromSlash(f))
}

func at(n *yaml.Node, message string) Problem {
	return Problem{Line: n.Line, Column: n.Column, Message: message}
}

var errorLineRE = regexp.MustCompile(`line (\d+)`)

// errorLine is the line a YAML error names, or 1.
func errorLine(err error) int {
	if m := errorLineRE.FindStringSubmatch(err.Error()); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			return n
		}
	}
	return 1
}
