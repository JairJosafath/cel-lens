package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/JairJosafath/cel-lens/engine/internal/hover"
	"github.com/JairJosafath/cel-lens/engine/internal/inline"
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// hoverOptions are what the client says of its hovers, in
// initializationOptions.hover. The zero value is the default: no command
// links, and the problems' messages written in the hover.
type hoverOptions struct {
	// commands are the ids of the commands the client runs from a link in a
	// hover (hover.commands); a hover links to no other command.
	commands []string
	// fixes is true when the client shows the message of a problem in the
	// hover itself, so the hover gives only its fixes (hover.problems:
	// fixes); false for message, the default.
	fixes bool
	// sent is true when the client sent hover in initializationOptions, in
	// any form; initialize then says what it honours (see echo).
	sent bool
}

// linkCommands are the commands a hover links to, each when the client
// lists it in hover.commands (see links).
func linkCommands() []string { return []string{hover.ApplyFixCommand, hover.PickVersionCommand} }

// links reports whether a hover writes a link to the command id: only to one
// of linkCommands, and only when the client lists it. Every link a hover
// writes is asked of it, and the echo of initialize lists the ids it says
// yes to, so the two cannot disagree.
func (o hoverOptions) links(id string) bool {
	return slices.Contains(linkCommands(), id) && slices.Contains(o.commands, id)
}

// echo is what initialize answers of the hover option the client sent, as
// capabilities.experimental.celLens.hover: commands, the ids of
// hover.commands the engine writes links to, in the client's order, each
// once; and problems, message or fixes, as the hover writes problems. A
// client that trusts a hover's links only for the ids it lists and reads
// here knows the engine escapes file text, as an engine without the field
// (0.33.0 and older) did not.
func (o hoverOptions) echo() map[string]any {
	commands := []string{}
	for _, id := range o.commands {
		if o.links(id) && !slices.Contains(commands, id) {
			commands = append(commands, id)
		}
	}
	problems := "message"
	if o.fixes {
		problems = "fixes"
	}
	return map[string]any{"commands": commands, "problems": problems}
}

// readHoverOptions reads raw, initializationOptions.hover: an object with
// commands, a list of command ids, and problems, message or fixes. A value
// of another form is ignored, and each such one gets a line for the
// client's log; absent or null, raw gives the defaults.
func readHoverOptions(raw json.RawMessage) (hoverOptions, []message) {
	var o hoverOptions
	if len(raw) == 0 || string(raw) == "null" {
		return o, nil
	}
	o.sent = true
	ignored := func(what string) message {
		return message{typ: messageWarning, text: "hover: " + what + ", ignored", log: true}
	}
	var p struct {
		Commands json.RawMessage `json:"commands"`
		Problems json.RawMessage `json:"problems"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return o, []message{ignored(fmt.Sprintf("%s is not an object with commands and problems", raw))}
	}
	var problems []message
	if len(p.Commands) > 0 && string(p.Commands) != "null" {
		if err := json.Unmarshal(p.Commands, &o.commands); err != nil {
			o.commands = nil
			problems = append(problems, ignored(fmt.Sprintf("commands %s is not a list of command ids", p.Commands)))
		}
	}
	if len(p.Problems) > 0 && string(p.Problems) != "null" {
		var v string
		switch err := json.Unmarshal(p.Problems, &v); {
		case err == nil && v == "message":
		case err == nil && v == "fixes":
			o.fixes = true
		default:
			problems = append(problems, ignored(fmt.Sprintf(`problems %s is neither "message" nor "fixes"`, p.Problems)))
		}
	}
	return o, problems
}

// hasError reports whether diags, the problems of a file, hold an error
// that starts inside the expression e, or that kept the check from
// compiling it.
func hasError(diags []check.Diagnostic, e extract.Expression) bool {
	for _, d := range diags {
		if d.Severity != check.SeverityError {
			continue
		}
		if !before(d.Start, e.Start) && !before(e.End, d.Start) || slices.Contains(d.Uncompiled, e.Start) {
			return true
		}
	}
	return false
}

// hover answers textDocument/hover on the message loop, estimating the costs
// of the document and sizing the fix of a near expression there when the
// hover needs them and they are not known (see answerHover, which works them
// out off the loop).
func (ss *session) hover(ctx context.Context, params json.RawMessage) (any, error) {
	var p positionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, badParams("textDocument/hover", err)
	}
	doc, ok := ss.docs[p.TextDocument.URI]
	if !ok || isLens(p.TextDocument.URI) {
		return ss.hoverAt(ctx, p, nil)
	}
	e, in := doc.parse(ctx, p.TextDocument.URI).Expression(fromPosition(doc.runes(), p.Position))
	if !in {
		return ss.hoverAt(ctx, p, ss.knownCosts(doc))
	}
	es, err := ss.estimateNow(ctx, p.TextDocument.URI, doc)
	if err != nil {
		return nil, err
	}
	if c, ok := es.of(e); ok {
		if err := ss.sizeNow(ctx, p.TextDocument.URI, doc, es, c); err != nil {
			return nil, err
		}
	}
	return ss.hoverAt(ctx, p, es)
}

// hoverAt is the hover at p, with the Cost: paragraph from costs, the
// estimates of the document's expressions and the fixes of the near ones,
// or none when costs is nil. Inside a CEL expression the check checks, every
// position gets the hover of package hover: the part under the cursor, when
// it is on a variable, field or function, then the problems at the cursor
// with their fixes (see fixBlock), then the rule's sections (what it checks,
// or returns, the variables it reads, its cost with the fix of a near one,
// for a CRD's rule what it does to its examples, and the pages that document
// it). What it checks is left out when the file's check has an error inside
// it. The range is the
// name of the part, or the expression off one. A position outside CEL, and
// one whose hover would say nothing, gets null.
func (ss *session) hoverAt(ctx context.Context, p positionParams, costs *estimates) (any, error) {
	if isLens(p.TextDocument.URI) {
		return ss.lensHover(ctx, p.TextDocument.URI, p.Position)
	}
	doc, ok := ss.docs[p.TextDocument.URI]
	if !ok {
		return nil, nil
	}
	lines := doc.runes()
	parsed := doc.parse(ctx, p.TextDocument.URI)
	pos := fromPosition(lines, p.Position)
	var sections hover.Sections
	h, onPart := ss.assist.Hover(parsed, pos)
	if onPart {
		sections.Part = h.Markdown
	}
	e, inExpr := parsed.Expression(pos)
	if inExpr {
		if rule, checked := ss.assist.Rule(parsed, e); checked {
			sections.Checks, sections.Returns, sections.Uses = rule.Checks, rule.Returns, rule.Uses
			if sections.Checks != "" {
				// What an expression with an error checks is not known.
				diags, err := ss.diagnostics(ctx, p.TextDocument.URI, doc)
				if err != nil {
					return nil, err
				}
				if hasError(diags, e) {
					sections.Checks = ""
				}
			}
		}
		fixBlock, err := ss.fixBlock(ctx, p.TextDocument.URI, doc, pos)
		if err != nil {
			return nil, err
		}
		sections.Fix = fixBlock
		if c, ok := costs.of(e); ok {
			o := hover.CostOptions{Version: costs.kube.String(), Pick: ss.hoverOpts.links(hover.PickVersionCommand)}
			if f, sized := costs.near[c.Start]; sized && isNear(c) {
				for _, lf := range limitFixes(lines, f.fixes, f.together) {
					o.Fixes = append(o.Fixes, ss.hoverFix(p.TextDocument.URI, doc, toRange(lines, c.Start, c.End), lf))
				}
			}
			sections.Cost = hover.Cost(c, o)
		}
		// A CRD's rule says what it does to the examples, as far as the
		// last check of the examples found it.
		if key := crdKey(p.TextDocument.URI); ss.knowsRules(key) {
			sections.Examples = inline.ExamplesHover(ss.ruleExamples(key, e))
		}
		var level costLevel // a nil *estimates in it would still be no nil interface
		if costs != nil {
			level = costs
		}
		sections.Docs = docsSection(ss.assist, parsed, e, h.Docs, level)
		if !onPart {
			h.Start, h.End = e.Start, e.End
		}
	}
	md := hover.Render(sections)
	if md == "" {
		return nil, nil
	}
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": md},
		"range":    toRange(lines, h.Start, h.End),
	}, nil
}

// fixBlock is the Fix block of the hover at pos in doc: each problem of the
// document's check whose range holds pos, with its message unless the client
// shows it itself (hover.problems: fixes), and its fixes (see hoverFix).
func (ss *session) fixBlock(ctx context.Context, uri string, doc *document, pos extract.Position) (string, error) {
	diags, err := ss.diagnostics(ctx, uri, doc)
	if err != nil {
		return "", err
	}
	lines := doc.runes()
	var problems []hover.Problem
	for _, d := range diags {
		if before(pos, d.Start) || before(d.End, pos) {
			continue
		}
		pr := hover.Problem{Message: d.Message}
		for _, f := range ss.fixesFor(ctx, uri, doc, lines, d) {
			pr.Fixes = append(pr.Fixes, ss.hoverFix(uri, doc, toRange(lines, d.Start, d.End), f))
		}
		problems = append(problems, pr)
	}
	return hover.FixBlock(problems, !ss.hoverOpts.fixes), nil
}

// applyFixArgs is the argument of cel-lens.applyFix: the document and its
// version, the range whose quick fixes it works out, and the title of the
// one to apply.
type applyFixArgs struct {
	URI     string   `json:"uri"`
	Version int      `json:"version"`
	Range   lspRange `json:"range"`
	Title   string   `json:"title"`
}

// hoverFix is f, a quick fix of the range r of doc at uri, as the hover
// offers it: with the argument of cel-lens.applyFix that applies it when the
// client runs that command from a hover, else in words.
func (ss *session) hoverFix(uri string, doc *document, r lspRange, f fix) hover.Fix {
	hf := hover.Fix{Words: f.words, Title: f.title}
	if ss.hoverOpts.links(hover.ApplyFixCommand) {
		arg, err := json.Marshal([]applyFixArgs{{URI: uri, Version: doc.version, Range: r, Title: f.title}})
		if err == nil {
			hf.Argument = uriComponent(string(arg))
		}
	}
	return hf
}

// uriComponent percent-encodes every byte of s but ASCII letters, digits and
// -_.~, so the result is one component of a URI that no Markdown character
// in it can end, parentheses included. decodeURIComponent reads it back.
func uriComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
