package embeddings

import (
	"context"
	"testing"
)

func TestHashProviderDeterministic(t *testing.T) {
	p := NewHashProvider(8)
	a, err := p.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 8 || len(b) != 8 {
		t.Fatalf("bad dimensions")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("not deterministic")
		}
	}
}
