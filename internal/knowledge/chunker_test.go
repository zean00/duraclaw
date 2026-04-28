package knowledge

import "testing"

func TestChunkText(t *testing.T) {
	chunks := ChunkText("one\n\n"+"two two two", 6)
	if len(chunks) != 3 {
		t.Fatalf("chunks=%#v", chunks)
	}
	if chunks[0].Index != 0 || chunks[1].Index != 1 || chunks[2].Index != 2 {
		t.Fatalf("chunks=%#v", chunks)
	}
}
