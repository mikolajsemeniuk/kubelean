package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// VLLM talks to a vLLM OpenAI-compatible server at Host (e.g.
// http://localhost:12000). It implements the same Chat/Digest shape as
// Ollama using the shared ChatInput/ChatOptions/ChatOutput types, so it's a
// drop-in swap in cmd/heatmap — trial() takes a ChatClient and never knows
// which backend it's actually talking to.
type VLLM struct {
	Host       string
	httpClient *http.Client
}

// NewVLLM mirrors NewOllama's transport tuning: a connection pool sized for
// real concurrency (runParallel fires many goroutines at the same Host at
// once), rather than Go's default of 2 idle conns per host.
func NewVLLM(host string) *VLLM {
	transport := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     4 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	return &VLLM{
		Host: host,
		httpClient: &http.Client{
			Transport: transport,
		},
	}
}

type chatCompletionRequest struct {
	Model              string          `json:"model"`
	Messages           []chatMessage   `json:"messages"`
	Temperature        float64         `json:"temperature"`
	Seed               int64           `json:"seed,omitempty"`
	MaxTokens          int             `json:"max_tokens,omitempty"`
	ResponseFormat     *responseFormat `json:"response_format,omitempty"`
	ChatTemplateKwargs map[string]any  `json:"chat_template_kwargs,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string `json:"name"`
	Schema any    `json:"schema"`
	Strict bool   `json:"strict"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// Chat turns ChatInput.Prompt into a single user message, ChatInput.Format
// (when set — it's the same map[string]any schema main.go already builds for
// Ollama) into a strict JSON-schema response_format, and returns the model's
// raw text in ChatOutput.Response. That return type is identical to Ollama's,
// so trial() needs no changes to unmarshal it.
//
// NumCtx is intentionally not sent: vLLM fixes the context window at server
// startup via --max-model-len, there's no per-request equivalent. Make sure
// --max-model-len on the server is >= your -num-ctx flag.
func (v *VLLM) Chat(ctx context.Context, in ChatInput) (ChatOutput, error) {
	body := chatCompletionRequest{
		Model:       in.Model,
		Messages:    []chatMessage{{Role: "user", Content: in.Prompt}},
		Temperature: in.Options.Temperature,
		Seed:        in.Options.Seed,
		MaxTokens:   in.Options.NumPredict,
	}
	// enable_thinking=false is needed only by hybrid-thinking families (Qwen3,
	// GLM); plain Jinja templates ignore the unknown kwarg, but vLLM's
	// MistralTokenizer rejects the whole request over ANY chat_template_kwargs
	// ("chat_template is not supported for Mistral tokenizers"), so it must
	// not be sent unconditionally.
	if strings.HasPrefix(in.Model, "qwen3") || strings.HasPrefix(in.Model, "glm") {
		body.ChatTemplateKwargs = map[string]any{"enable_thinking": false}
	}
	if in.Format != nil {
		body.ResponseFormat = &responseFormat{
			Type:       "json_schema",
			JSONSchema: jsonSchema{Name: "diagnosis", Schema: in.Format, Strict: true},
		}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return ChatOutput{}, fmt.Errorf("vllm: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.Host+"/v1/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return ChatOutput{}, fmt.Errorf("vllm: build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	res, err := v.httpClient.Do(req)
	if err != nil {
		return ChatOutput{}, fmt.Errorf("vllm: http: %w", err)
	}

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ChatOutput{}, fmt.Errorf("vllm: status %d", res.StatusCode)
	}

	var parsed chatCompletionResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return ChatOutput{}, fmt.Errorf("vllm: decode response: %w", err)
	}

	if len(parsed.Choices) == 0 {
		return ChatOutput{}, fmt.Errorf("vllm: no choices in response")
	}

	return ChatOutput{Response: parsed.Choices[0].Message.Content}, nil
}

// Digest has no real vLLM equivalent — the OpenAI-compatible API doesn't
// expose a weights hash like Ollama's /api/tags digest. This confirms the
// model is actually being served (hits /v1/models) and returns its id, which
// catches a typo'd -model flag but does not pin exact weights the way
// Ollama's digest does.
func (v *VLLM) Digest(ctx context.Context, model string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.Host+"/v1/models", nil)
	if err != nil {
		return "", fmt.Errorf("vllm: build models request: %w", err)
	}

	res, err := v.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vllm: models http: %w", err)
	}

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("vllm: models status %d: %s", res.StatusCode, string(raw))
	}

	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("vllm: decode models: %w", err)
	}

	for _, m := range out.Data {
		if m.ID == model {
			return m.ID, nil
		}
	}

	return "", fmt.Errorf("vllm: model %q not found on server", model)
}
