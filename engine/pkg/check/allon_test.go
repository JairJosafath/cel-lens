package check_test

import (
	"github.com/JairJosafath/cel-lens/engine/pkg/check"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// allOn switches on every experimental feature, for the tests of what they do.
func allOn() check.Option {
	return check.WithFeatures(feature.Of(feature.Known()...))
}
