package assist_test

import (
	"github.com/JairJosafath/cel-lens/engine/internal/assist"
	"github.com/JairJosafath/cel-lens/engine/pkg/feature"
)

// allOn switches on every experimental feature, for the tests of what they do.
func allOn() assist.NewOption {
	return assist.WithFeatures(feature.Of(feature.Known()...))
}
