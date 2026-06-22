package distill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"
)

// EmbedFunc maps text to a dense vector. L4 uses it to score how well each field
// grounds the symptom. Kept as a function type so tests can inject a deterministic
// fake with no running Ollama.
type EmbedFunc func(ctx context.Context, text string) ([]float64, error)

// Embedder produces embeddings via a local Ollama embedding model
// (e.g. nomic-embed-text), the same machinery LGS used for grounding.
type Embedder struct {
	BaseURL string
	Model   string
	Client  *http.Client
}

// NewEmbedder targets a local Ollama at the default address for the given model.
func NewEmbedder(model string) *Embedder {
	return &Embedder{
		BaseURL: "http://localhost:11434",
		Model:   model,
		Client:  &http.Client{Timeout: 120 * time.Second},
	}
}

// Embed returns the embedding of text. It satisfies EmbedFunc via e.Embed.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float64, error) {
	body, err := json.Marshal(map[string]any{"model": e.Model, "prompt": text})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.BaseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := e.Client.Do(req)
	if err != nil {
		return nil, err
	}

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embeddings: unexpected status %d", res.StatusCode)
	}

	var out struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embedding) == 0 {
		return nil, fmt.Errorf("ollama embeddings: empty vector (is %q an embedding model?)", e.Model)
	}
	return out.Embedding, nil
}

// cosine is the cosine similarity of a and b; 0 if either is zero or sizes differ.
func cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
