package embeddings

import (
	"context"
	"errors"
	"testing"
)

func TestHashProviderDimensionAndCancellation(t *testing.T) {
	provider := NewHashProvider(0)
	if provider.Dimension() != 768 {
		t.Fatalf("dimension=%d", provider.Dimension())
	}
	small := NewHashProvider(4)
	vec, err := small.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 4 || vec[0] == 0 {
		t.Fatalf("vec=%#v", vec)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := small.Embed(ctx, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
