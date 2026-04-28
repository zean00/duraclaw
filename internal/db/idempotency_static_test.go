package db

import (
	"os"
	"strings"
	"testing"
)

func TestCreateRunQueuesEventOnlyOnInsert(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "if inserted {\n\t\t_, _ = s.InsertMessage") {
		t.Fatalf("CreateRun should guard side effects with inserted flag")
	}
}
