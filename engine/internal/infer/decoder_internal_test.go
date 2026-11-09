package infer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JairJosafath/cel-lens/engine/pkg/extract"
)

// TestDecoderStopsWhenCancelled checks that a cancelled decoding stops
// within ctxEvery values, so a request replaced while it decodes does not
// go on (review of #17).
func TestDecoderStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	text := "[" + strings.Repeat("1, ", 3*ctxEvery) + "1]"
	docs, _, err := extract.Documents(t.Context(), "", []byte(text))
	if err != nil || len(docs) != 1 {
		t.Fatalf("Documents = %d, %v", len(docs), err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	d := newDecoder(docs)
	if _, err := d.value(ctx, docs[0].Node, 0); !errors.Is(err, context.Canceled) || d.values > ctxEvery {
		t.Errorf("value with a cancelled context = %v after %d values, want context.Canceled within %d", err, d.values, ctxEvery)
	}
}
