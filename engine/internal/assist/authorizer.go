package assist

import (
	"strings"
	"unicode"
)

// The CEL types of the authorizer library of the API server, and of
// authorizer.requestResource.
const (
	authorizerType    = "kubernetes.authorization.Authorizer"
	resourceCheckType = "kubernetes.authorization.ResourceCheck"
)

// requestResourceVar is the name the API server declares for the authorizer
// that checks the resource of the request.
const requestResourceVar = "requestResource"

const requestResourceDesc = "Checks the resource of the request: its group, resource, subresource, namespace and name are already set."

// maxVariableDepth caps how many policy variables and groups opaqueFrom
// follows, so that variables defined by each other stay cheap.
const maxVariableDepth = 8

// opaqueBefore returns the opaque CEL type of the expression that ends before
// the dot at line[dot]: a variable of an opaque type, such as authorizer, or a
// policy variable whose expression has one (variables.rc), then any number of
// method calls (authorizer.group('apps').resource('pods')) and, on the
// authorizer, .requestResource, all of it possibly in parentheses. Each call
// gives the type its overload in the catalog returns. It reports false for any
// other expression.
func (a *Assistant) opaqueBefore(line []rune, dot int, vars []variable) (string, bool) {
	t, _, ok := a.opaqueFrom(line, dot, vars, 0)
	return t, ok
}

// opaqueFrom is opaqueBefore that also returns the column where the expression
// starts. depth counts the policy variables and groups followed.
func (a *Assistant) opaqueFrom(line []rune, dot int, vars []variable, depth int) (string, int, bool) {
	if depth > maxVariableDepth {
		return "", 0, false
	}
	type segment struct {
		name   string
		called bool
		typ    string // set for a parenthesised group
	}
	var rev []segment
	p := dot
	for {
		for p > 0 && unicode.IsSpace(line[p-1]) {
			p-- // a call on the next line: the dot starts it
		}
		called := false
		if p > 0 && line[p-1] == ')' {
			open := parenOpen(line, p-1)
			if open < 0 {
				return "", 0, false
			}
			if !calledFrom(line, open) {
				end := p - 1
				for end > open+1 && (line[end-1] == ' ' || line[end-1] == '\t') {
					end--
				}
				first := open + 1
				for first < end && (line[first] == ' ' || line[first] == '\t') {
					first++
				}
				t, start, ok := a.opaqueFrom(line[:end], end, vars, depth+1)
				if !ok || start != first {
					return "", 0, false
				}
				rev = append(rev, segment{typ: t})
				p = open
				break
			}
			called, p = true, open
		}
		q := p
		for q > 0 && identRune(line[q-1]) {
			q--
		}
		if q == p {
			return "", 0, false
		}
		rev = append(rev, segment{name: string(line[q:p]), called: called})
		p = q
		if p == 0 || line[p-1] != '.' {
			break
		}
		p--
	}
	first, rest := rev[len(rev)-1], rev[:len(rev)-1]
	cur := first.typ
	if cur == "" && !first.called {
		for _, v := range vars {
			if v.name != first.name {
				continue
			}
			cur = v.celType
			if len(rest) == 0 || rest[len(rest)-1].called {
				continue
			}
			if src, ok := v.sources[rest[len(rest)-1].name]; ok {
				t, ok := a.opaqueOfSource(src, vars, depth+1)
				if !ok {
					return "", 0, false
				}
				cur, rest = t, rest[:len(rest)-1]
			}
		}
	}
	if cur == "" {
		return "", 0, false
	}
	for i := len(rest) - 1; i >= 0; i-- {
		st := rest[i]
		switch {
		case !st.called && cur == authorizerType && st.name == requestResourceVar:
			cur = resourceCheckType
		case st.called:
			cur = a.resultOn(st.name, cur)
		default:
			return "", 0, false
		}
		if cur == "" {
			return "", 0, false
		}
	}
	return cur, p, true
}

// opaqueOfSource returns the opaque type of src, the CEL text of a policy
// variable.
func (a *Assistant) opaqueOfSource(src string, vars []variable, depth int) (string, bool) {
	line := []rune(strings.Join(strings.Fields(src), " "))
	t, start, ok := a.opaqueFrom(line, len(line), vars, depth)
	return t, ok && start == 0
}

// resultOn returns the type the method name returns when called on a receiver
// of the opaque type recv, or "" when recv has no such method.
func (a *Assistant) resultOn(name, recv string) string {
	for _, m := range a.byName[name].members {
		if m.recvType == recv && strings.Contains(m.result, ".") {
			return m.result
		}
	}
	return ""
}
