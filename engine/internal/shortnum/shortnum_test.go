package shortnum_test

import (
	"math"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/internal/shortnum"
)

func TestFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   uint64
		want string
	}{
		{0, "0"},
		{9999, "9999"},
		{12_000, "12K"},
		{1_048_576, "1M"},
		{6_200_000, "6.2M"},
		{10_000_000, "10M"},
		{2_400_000_000, "2.4B"},
		{1_300_000_000_000, "1.3T"},
		{math.MaxUint64, "1.8e+19"},
	}
	for _, tt := range tests {
		if got := shortnum.Format(tt.in); got != tt.want {
			t.Errorf("Format(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
