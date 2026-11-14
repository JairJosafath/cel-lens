package hover

import "strings"

// ApplyFixCommand is the engine command that applies a quick fix; a fix links
// to it when the client runs it from a hover.
const ApplyFixCommand = "cel-lens.applyFix"

// Fix is a quick fix the hover offers.
type Fix struct {
	// Words name the fix in Markdown, with the text it takes from a file in
	// code spans, such as "Add `maxItems: 1000` to `spec.ports`".
	Words string
	// Title is the fix's code action title, in plain text, which the link
	// shows as its tooltip.
	Title string
	// Argument is the argument of ApplyFixCommand that applies the fix,
	// URI-encoded, "" when the client does not run the command from a
	// hover: the fix is then named in words.
	Argument string
}

// Markdown writes f: "Fix: " and a link to ApplyFixCommand with f.Argument,
// titled f.Title, when f has one; else "Fix: " and f.Words, then "(a quick
// fix)", so the reader knows where to find it.
func (f Fix) Markdown() string {
	if f.Argument == "" {
		return "Fix: " + f.Words + " (a quick fix)"
	}
	title := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", " ", "\r", " ").Replace(f.Title)
	return "Fix: [" + f.Words + "](command:" + ApplyFixCommand + "?" + f.Argument + ` "` + title + `")`
}

// Problem is a problem at the cursor: its message, as the check gives it,
// and its fixes.
type Problem struct {
	Message string
	Fixes   []Fix
}

// FixBlock writes the Fix block of the problems at the cursor, one paragraph
// each: with messages, the problem's message, escaped, then its fixes; without
// (for a client that shows the message in the same hover), only the fixes, so
// a problem without one adds nothing. It is "" when there is nothing to say.
func FixBlock(problems []Problem, messages bool) string {
	var paragraphs []string
	for _, p := range problems {
		var lines []string
		if messages {
			lines = append(lines, Escape(p.Message))
		}
		for _, f := range p.Fixes {
			lines = append(lines, f.Markdown())
		}
		if len(lines) > 0 {
			paragraphs = append(paragraphs, strings.Join(lines, "\n\n"))
		}
	}
	return strings.Join(paragraphs, "\n\n")
}
