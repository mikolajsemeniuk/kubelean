// Package providers holds thin, generic clients for model runtimes. Ollama
// wraps a local Ollama server's HTTP API; it is intentionally dumb — prompt in,
// text out — so callers own prompt construction and response parsing.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Ollama talks to a local Ollama server at Host (e.g. http://localhost:11434).
//
// httpClient is intentionally NOT http.DefaultClient. The default transport's
// MaxIdleConnsPerHost is 2 — fine for occasional requests, but under real
// concurrency (many goroutines hitting the same Host at once, as cmd/heatmap's
// runParallel does) it forces the transport to keep opening and tearing down
// TCP connections instead of reusing a pool of them. That overhead scales
// *worse* than linearly as concurrency rises, which is why raising -parallel
// past a point can make throughput fall below a lower-parallelism baseline
// instead of merely plateauing: past a certain concurrency the connection
// churn cost outweighs whatever extra scheduling slack the server still had.
// MaxIdleConnsPerHost here is set generously above any -parallel value we'd
// realistically try, so the pool itself is never the bottleneck.
type Ollama struct {
	Host       string
	httpClient *http.Client
}

// NewOllama returns a client for the given host, with a transport whose
// connection pool is sized for real concurrency instead of the http.Default*
// pool, which is sized for casual, low-concurrency use.
func NewOllama(host string) *Ollama {
	transport := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	return &Ollama{
		Host: host,
		httpClient: &http.Client{
			Transport: transport,
			// No blanket client-side Timeout: a slow model response would
			// otherwise be indistinguishable from a genuinely hung request.
			// Callers already carry their own ctx for cancellation.
		},
	}
}

// ChatInput is a single /api/generate request. Set Stream false to get one
// response document; Options carries the determinism knobs. Format, when set to
// a JSON Schema, makes the model emit JSON conforming to it (Ollama structured
// output) — so callers get parseable JSON instead of free text.
type ChatInput struct {
	Model   string      `json:"model"`
	Prompt  string      `json:"prompt"`
	Format  any         `json:"format,omitempty"`
	Options ChatOptions `json:"options"`
	Stream  bool        `json:"stream"`
}

type ChatOptions struct {
	Temperature float64 `json:"temperature"`
	Seed        int64   `json:"seed"`
	// NumCtx must be set explicitly: Ollama's default context window silently
	// truncates a long (multi-document) prompt, so the model would diagnose a
	// half-cut manifest. NumPredict caps the (tiny JSON) answer. Both omitted
	// fall back to Ollama defaults.
	NumCtx     int `json:"num_ctx,omitempty"`
	NumPredict int `json:"num_predict,omitempty"`
}

type ChatOutput struct {
	Response string `json:"response"`
}

// Digest returns the content digest of a pulled model (from /api/tags). A tag
// like "qwen2.5:7b-instruct" can be re-pulled and change weights underneath you;
// recording the digest pins the exact model for reproducibility.
func (o *Ollama) Digest(ctx context.Context, model string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Host+"/api/tags", nil)
	if err != nil {
		return "", fmt.Errorf("ollama: build tags request: %w", err)
	}
	res, err := o.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama: tags http: %w", err)
	}

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama: tags status %d", res.StatusCode)
	}

	var out struct {
		Models []struct {
			Name   string `json:"name"`
			Model  string `json:"model"`
			Digest string `json:"digest"`
		} `json:"models"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("ollama: decode tags: %w", err)
	}
	for _, m := range out.Models {
		if m.Name == model || m.Model == model {
			return m.Digest, nil
		}
	}
	return "", fmt.Errorf("ollama: model %q not found in tags", model)
}

// Chat runs one prompt through the model and returns its raw text response.
func (o *Ollama) Chat(ctx context.Context, in ChatInput) (ChatOutput, error) {
	input, err := json.Marshal(in)
	if err != nil {
		return ChatOutput{}, fmt.Errorf("ollama: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.Host+"/api/generate", bytes.NewReader(input))
	if err != nil {
		return ChatOutput{}, fmt.Errorf("ollama: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.httpClient.Do(req)
	if err != nil {
		return ChatOutput{}, fmt.Errorf("ollama: http: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return ChatOutput{}, fmt.Errorf("ollama: status %d: %s", res.StatusCode, string(raw))
	}
	var out ChatOutput
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return ChatOutput{}, fmt.Errorf("ollama: decode response: %w", err)
	}
	return out, nil
}
