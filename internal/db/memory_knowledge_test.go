package db

import "testing"

func TestPGVectorFormatting(t *testing.T) {
	got := pgVector([]float32{1, 0.5, -2})
	if got != "[1,0.5,-2]" {
		t.Fatalf("got %q", got)
	}
}
