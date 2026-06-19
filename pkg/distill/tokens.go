package distill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TokenCounter counts the tokens a piece of text costs in an LLM's context
// window. Implementations must be deterministic for a fixed backend.
type TokenCounter interface {
	Count(ctx context.Context, text string) (int, error)
}

// OllamaCounter counts tokens using a model's own tokenizer via the Ollama
// /api/generate endpoint. It reads prompt_eval_count with num_predict=0, which
// is the exact prompt tokenization for that model and is unaffected by any
// generation.
type OllamaCounter struct {
	BaseURL string
	Model   string
	Client  *http.Client
}

// NewOllamaCounter returns a counter targeting a local Ollama at the default
// address for the given model.
func NewOllamaCounter(model string) *OllamaCounter {
	return &OllamaCounter{
		BaseURL: "http://localhost:11434",
		Model:   model,
		Client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (o *OllamaCounter) Count(ctx context.Context, text string) (int, error) {
	in := map[string]any{
		"model":   o.Model,
		"prompt":  text,
		"stream":  false,
		"options": map[string]any{"num_predict": 0},
	}

	body, err := json.Marshal(in)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := o.Client.Do(req)
	if err != nil {
		return 0, err
	}

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ollama: unexpected status %d", res.StatusCode)
	}

	var out struct {
		PromptEvalCount int `json:"prompt_eval_count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return 0, err
	}

	return out.PromptEvalCount, nil
}

// FakeCounter is a deterministic stand-in for unit tests: it counts
// whitespace-separated words, so tests need no running Ollama. It preserves the
// ordering L0 >= L1 >= L2 in token cost, which is what the tests assert.
type FakeCounter struct{}

func (FakeCounter) Count(_ context.Context, text string) (int, error) {
	return len(strings.Fields(text)), nil
}
