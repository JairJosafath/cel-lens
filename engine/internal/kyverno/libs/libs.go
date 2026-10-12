// Package libs holds the Go types Kyverno's CEL environment exposes to
// expressions. CEL names them after this package, as Kyverno's own types are,
// so a message that names one reads as Kyverno's does.
package libs

// Exception is the type of the exceptions variable of a policy: what the
// policy exceptions that apply allow.
type Exception struct {
	AllowedImages []string `cel:"allowedImages"`
	AllowedValues []string `cel:"allowedValues"`
}
