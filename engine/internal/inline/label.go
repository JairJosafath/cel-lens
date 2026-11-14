package inline

// Where the label of a rule comes from (see RuleLabel).
const (
	LabelMessage    = "message"
	LabelName       = "name"
	LabelKey        = "key"
	LabelExpression = "expression"
)

// RuleWords are what a rule's author wrote that can name it in a list of
// rules: its static message (a CRD's rule, a policy's validation), its name
// (a match condition, a variable) and its key (an audit annotation), each
// "" when the rule has none, and its expression. A messageExpression is
// CEL, not words, so it is not one of them.
type RuleWords struct {
	Message, Name, Key, Expression string
}

// RuleLabel returns the label a list of rules shows for a rule, and where it
// comes from: the first of its message, its name and its key that is not
// empty once its white space is one space (LabelMessage, LabelName,
// LabelKey), else its expression, on one line and cut past 60 characters
// with "…", as the warnings cut a rule (LabelExpression). A message, name or
// key is put on one line and never cut.
func RuleLabel(w RuleWords) (label, from string) {
	for _, c := range []struct{ text, from string }{
		{w.Message, LabelMessage}, {w.Name, LabelName}, {w.Key, LabelKey},
	} {
		if s := oneLine(c.text); s != "" {
			return s, c.from
		}
	}
	return cut(oneLine(w.Expression), maxRule), LabelExpression
}
