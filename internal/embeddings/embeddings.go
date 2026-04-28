package embeddings

import (
	"context"
	"crypto/sha256"
)

type Provider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	Dimension() int
}

type HashProvider struct {
	dim int
}

func NewHashProvider(dim int) HashProvider {
	if dim <= 0 {
		dim = 768
	}
	return HashProvider{dim: dim}
}

func (p HashProvider) Dimension() int { return p.dim }

func (p HashProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]float32, p.dim)
	seed := []byte(text)
	for i := 0; i < p.dim; i += 32 {
		sum := sha256.Sum256(append(seed, byte(i%256)))
		for j := 0; j < len(sum) && i+j < p.dim; j++ {
			out[i+j] = float32(int(sum[j])-128) / 128
		}
	}
	return out, nil
}
