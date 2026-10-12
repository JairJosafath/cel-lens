package assist

import (
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// Suggest finds the name that the misspelled field, function or macro in
// the range start to end of doc, a position in the file, was probably meant
// to be. Only names that are valid at that place are candidates: the fields of
// the schema of self and oldSelf (and of the other variables of the admission
// environment), the functions and macros that apply to the receiver, or the
// global functions and macros. It reports false when the range is not a
// single name after a dot or before an opening parenthesis, when the name is
// already valid, and unless exactly one candidate is closest within the edit
// distance limit, see distance.
func (a *Assistant) Suggest(doc *Document, start, end extract.Position) (string, bool) {
	if start.Line != end.Line {
		return "", false
	}
	s, ok := at(doc, start)
	if !ok {
		return "", false
	}
	a = a.forExpr(doc, s.expr)
	to := end.Column - 1 - s.base
	if s.col >= to || to > len(s.line) || inString(s.line, s.col) {
		return "", false
	}
	for _, r := range s.line[s.col:to] {
		if !identRune(r) {
			return "", false
		}
	}
	word := string(s.line[s.col:to])
	called := calledAt(s.line, to)
	dotted := s.col > 0 && s.line[s.col-1] == '.'
	var names []string
	switch {
	case dotted:
		chain, ok := chainBefore(s.line, s.col-1)
		if !ok {
			return "", false
		}
		vars := a.scope(doc, s.expr)
		recv := resolve(vars, chain)
		var items []Item
		switch {
		case called:
			if ns := namespace(chain, vars); ns != "" {
				items = a.namespaced(ns)
			}
			if items == nil && recv.found {
				items = a.members(recv.known, recv.optional, recv.celType)
			}
		case recv.found && recv.known != nil:
			for _, n := range recv.known.fieldNames() {
				items = append(items, Item{Label: n})
			}
		}
		for _, it := range items {
			names = append(names, it.Label)
		}
	case called:
		for _, it := range a.topLevel(nil) {
			names = append(names, it.Label)
		}
	}
	return closest(word, names)
}

// closest returns the one name of names nearest to word, when that is within
// the limit of distance and no other name is as near.
func closest(word string, names []string) (string, bool) {
	limit := distanceLimit(word)
	best, bestDist, tied := "", limit+1, false
	for _, n := range names {
		if n == word {
			return "", false
		}
		switch d := distance(word, n, limit); {
		case d < bestDist:
			best, bestDist, tied = n, d, false
		case d == bestDist && n != best:
			tied = true
		}
	}
	if bestDist > limit || tied {
		return "", false
	}
	return best, true
}

// distanceLimit is the largest edit distance at which a name of that length
// counts as close: one for short names, two from six characters, so that a
// slip is forgiven but a different word is not.
func distanceLimit(word string) int {
	if len([]rune(word)) >= 6 {
		return 2
	}
	return 1
}

// distance is the number of edits (insert, delete, replace one character, swap
// two neighbours) it takes to turn a into b, or limit+1 when it is more.
func distance(a, b string, limit int) int {
	x, y := []rune(a), []rune(b)
	if abs(len(x)-len(y)) > limit {
		return limit + 1
	}
	prev2 := make([]int, len(y)+1)
	prev := make([]int, len(y)+1)
	cur := make([]int, len(y)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(x); i++ {
		cur[0] = i
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && x[i-1] == y[j-2] && x[i-2] == y[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return min(prev[len(y)], limit+1)
}

func abs(n int) int { return max(n, -n) }
