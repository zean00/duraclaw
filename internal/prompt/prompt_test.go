package prompt

import (
	"context"
	"errors"
	"testing"
)

type testContributor struct {
	slots []Slot
	err   error
}

func (c testContributor) Contribute(context.Context) ([]Slot, error) {
	return c.slots, c.err
}

func TestBuilderOrdersSlotsAndSkipsBlankContent(t *testing.T) {
	got, err := NewBuilder(
		testContributor{slots: []Slot{
			{Layer: LayerContext, Name: "b", Content: "context b"},
			{Layer: LayerSystem, Name: "z", Content: "system z"},
			{Layer: LayerContext, Name: "a", Content: "context a"},
			{Layer: LayerPolicy, Name: "blank", Content: " "},
		}},
	).Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "context a\n\ncontext b\n\nsystem z"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuilderReturnsContributorError(t *testing.T) {
	want := errors.New("boom")
	if _, err := NewBuilder(testContributor{err: want}).Build(context.Background()); !errors.Is(err, want) {
		t.Fatalf("err=%v", err)
	}
}
