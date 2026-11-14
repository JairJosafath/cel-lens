package lsp

import (
	"encoding/json"
	"slices"
	"testing"
)

// TestReadHoverOptions checks what readHoverOptions keeps of
// initializationOptions.hover: the commands listed, none by default, and
// fixes only for problems: fixes; a value of another form is dropped with
// one message for the log each (#52).
func TestReadHoverOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw      string
		commands []string
		fixes    bool
		messages int
	}{
		{"", nil, false, 0},
		{"null", nil, false, 0},
		{"{}", nil, false, 0},
		{`{"commands": ["cel-lens.applyFix", "cel-lens.pickKubernetesVersion"], "problems": "fixes"}`, []string{"cel-lens.applyFix", "cel-lens.pickKubernetesVersion"}, true, 0},
		{`{"commands": [], "problems": "message"}`, []string{}, false, 0},
		{`{"commands": null, "problems": null}`, nil, false, 0},
		{`{"commands": ["a", 1], "problems": "fixes"}`, nil, true, 1},
		{`{"commands": "a", "problems": "both"}`, nil, false, 2},
		{`{"problems": true}`, nil, false, 1},
		{`[1]`, nil, false, 1},
	}
	for _, tt := range tests {
		got, msgs := readHoverOptions(json.RawMessage(tt.raw))
		if !slices.Equal(got.commands, tt.commands) || (got.commands == nil) != (tt.commands == nil) || got.fixes != tt.fixes || len(msgs) != tt.messages {
			t.Errorf("readHoverOptions(%s) = %+v, %d messages, want commands %q, fixes %t, %d messages", tt.raw, got, len(msgs), tt.commands, tt.fixes, tt.messages)
		}
		for _, m := range msgs {
			if !m.log || m.typ != messageWarning {
				t.Errorf("readHoverOptions(%s) message %+v, want a warning for the log", tt.raw, m)
			}
		}
	}
}

// TestLinksAndEchoAgree checks that the echo of the hover option lists
// exactly the commands a hover writes links to (review of #64): one helper,
// links, decides both, over linkCommands, so a command added later is in
// both or in neither. Every command of linkCommands is linked when listed,
// and no other, however the client lists it.
func TestLinksAndEchoAgree(t *testing.T) {
	t.Parallel()
	others := []string{"cel-lens.setType", "workbench.action.terminal.new", "", "CEL-LENS.APPLYFIX"}
	lists := [][]string{nil, linkCommands(), others, append(append([]string(nil), others...), linkCommands()...)}
	for _, id := range linkCommands() {
		lists = append(lists, []string{id}, []string{id, id})
	}
	for _, listed := range lists {
		o := hoverOptions{commands: listed, sent: true}
		echoed, _ := o.echo()["commands"].([]string)
		for _, id := range append(append([]string(nil), listed...), linkCommands()...) {
			if got, want := o.links(id), slices.Contains(echoed, id); got != want {
				t.Errorf("hover.commands %q: links(%q) = %t, but the echo %q lists it: %t", listed, id, got, echoed, want)
			}
			if want := slices.Contains(linkCommands(), id) && slices.Contains(listed, id); o.links(id) != want {
				t.Errorf("hover.commands %q: links(%q) = %t, want %t", listed, id, o.links(id), want)
			}
		}
	}
}
