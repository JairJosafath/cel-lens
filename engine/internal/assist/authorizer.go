package assist

import "strings"

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

// opaqueBefore returns the opaque CEL type of the expression that ends before
// the dot at line[dot]: a variable of an opaque type, such as authorizer, then
// any number of method calls (authorizer.group('apps').resource('pods')) and,
// on the authorizer, .requestResource. Each call gives the type its overload in
// the catalog returns. It reports false for any other expression.
func (a *Assistant) opaqueBefore(line []rune, dot int, vars []variable) (string, bool) {
	type segment struct {
		name   string
		called bool
	}
	var rev []segment
	p := dot
	for {
		called := false
		if p > 0 && line[p-1] == ')' {
			open := parenOpen(line, p-1)
			if open < 0 {
				return "", false
			}
			called, p = true, open
		}
		q := p
		for q > 0 && identRune(line[q-1]) {
			q--
		}
		if q == p {
			return "", false
		}
		rev = append(rev, segment{string(line[q:p]), called})
		p = q
		if p == 0 || line[p-1] != '.' {
			break
		}
		p--
	}
	cur := ""
	first := rev[len(rev)-1]
	for _, v := range vars {
		if v.name == first.name && !first.called {
			cur = v.celType
		}
	}
	if cur == "" {
		return "", false
	}
	for i := len(rev) - 2; i >= 0; i-- {
		st := rev[i]
		switch {
		case !st.called && cur == authorizerType && st.name == requestResourceVar:
			cur = resourceCheckType
		case st.called:
			cur = a.resultOn(st.name, cur)
		default:
			return "", false
		}
		if cur == "" {
			return "", false
		}
	}
	return cur, true
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
