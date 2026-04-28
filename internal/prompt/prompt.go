package prompt

import (
	"context"
	"sort"
	"strings"
)

type Layer string

const (
	LayerSystem     Layer = "system"
	LayerPolicy     Layer = "policy"
	LayerContext    Layer = "context"
	LayerCapability Layer = "capability"
)

type Slot struct {
	Layer   Layer
	Name    string
	Content string
	Source  string
}

type Contributor interface {
	Contribute(ctx context.Context) ([]Slot, error)
}

type Builder struct{ contributors []Contributor }

func NewBuilder(contributors ...Contributor) *Builder { return &Builder{contributors: contributors} }

func (b *Builder) Build(ctx context.Context) (string, error) {
	var slots []Slot
	for _, c := range b.contributors {
		add, err := c.Contribute(ctx)
		if err != nil {
			return "", err
		}
		slots = append(slots, add...)
	}
	sort.SliceStable(slots, func(i, j int) bool {
		if slots[i].Layer == slots[j].Layer {
			return slots[i].Name < slots[j].Name
		}
		return slots[i].Layer < slots[j].Layer
	})
	var out []string
	for _, s := range slots {
		if strings.TrimSpace(s.Content) != "" {
			out = append(out, s.Content)
		}
	}
	return strings.Join(out, "\n\n"), nil
}
