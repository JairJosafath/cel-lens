package check

import (
	"crypto/sha256"
	"slices"
	"sync"

	kfield "k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/JairJosafath/cel-lens/engine/pkg/kubeversion"
)

// Validations keeps what the API server's validation of the
// CustomResourceDefinitions of one file found, so that Check and Costs of the
// same text at the same Kubernetes version run it once between them: the
// first of them to validate a document keeps the result, and the other reads
// it. A result is kept for the text it was made from, by its SHA-256, and for
// the version; a text other than the one kept, given to Check or Costs with
// the same Validations, drops what was kept. The validation Check runs with the
// types of WithAssumedTypes placed is never kept. The zero value is ready to
// use, and a Validations is safe for concurrent use.
type Validations struct {
	mu   sync.Mutex
	text [sha256.Size]byte
	kept map[validationKey]validated
}

// WithValidations makes Check and Costs keep the results of the API server's
// validation of a CustomResourceDefinition in v, and read them from v when the
// same text was validated at the same version, rather than validate it again.
func WithValidations(v *Validations) Option {
	return func(s *settings) { s.validations = v }
}

// validationKey is a validated document: the Kubernetes version and the
// index of the document in its file.
type validationKey struct {
	minor, doc int
}

// validated is what a validation of one CustomResourceDefinition found: the
// API server's errors, copied, and the schemas it compiles no rule of (see
// wholeSchemaErrors).
type validated struct {
	problems []kfield.Error
	whole    []string
}

// textSum is the key of a file's text in a Validations.
func textSum(data []byte) [sha256.Size]byte { return sha256.Sum256(data) }

// get returns what was kept for document doc of the text whose sum is text,
// at version v.
func (vs *Validations) get(text [sha256.Size]byte, v kubeversion.Version, doc int) (kfield.ErrorList, []string, bool) {
	if vs == nil {
		return nil, nil, false
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	got, ok := vs.kept[validationKey{v.Minor(), doc}]
	if !ok || vs.text != text {
		return nil, nil, false
	}
	return errorList(got.problems), slices.Clone(got.whole), true
}

// put keeps problems and whole for document doc of the text whose sum is
// text, at version v, and drops what was kept for another text. It keeps
// copies, so the caller may change its own.
func (vs *Validations) put(text [sha256.Size]byte, v kubeversion.Version, doc int, problems kfield.ErrorList, whole []string) {
	if vs == nil {
		return
	}
	kept := validated{problems: make([]kfield.Error, len(problems)), whole: slices.Clone(whole)}
	for i, fe := range problems {
		kept.problems[i] = *fe
	}
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.kept == nil || vs.text != text {
		vs.kept, vs.text = map[validationKey]validated{}, text
	}
	vs.kept[validationKey{v.Minor(), doc}] = kept
}

// errorList returns copies of errs as an ErrorList.
func errorList(errs []kfield.Error) kfield.ErrorList {
	out := make(kfield.ErrorList, len(errs))
	for i := range errs {
		fe := errs[i]
		out[i] = &fe
	}
	return out
}
