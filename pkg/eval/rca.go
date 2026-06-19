package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Diagnosis is the structured RCA output the model is forced to emit.
type Diagnosis struct {
	RootCauseLabel string `json:"root_cause_label"`
	OffendingField string `json:"offending_field"`
}

// RCAClient asks a local Ollama model for a structured root-cause diagnosis of
// a single Kubernetes resource.
type RCAClient struct {
	BaseURL     string
	Model       string
	Temperature float64
	Client      *http.Client
}

func NewRCAClient(model string, temperature float64) *RCAClient {
	return &RCAClient{
		BaseURL:     "http://localhost:11434",
		Model:       model,
		Temperature: temperature,
		Client:      &http.Client{Timeout: 5 * time.Minute},
	}
}

const systemPrompt = `You are a senior Kubernetes SRE performing root-cause analysis.
You are given the YAML of a single Kubernetes resource that is unhealthy.
Identify the SINGLE root cause and the SINGLE most decisive field that proves it.

Respond with ONLY a JSON object, no prose, of the form:
{"root_cause_label": "<one label from the allowed list>", "offending_field": "<dotted path to the deciding field>"}

The root_cause_label MUST be exactly one of the allowed labels:
%s`

func (c *RCAClient) prompt(resourceYAML string) string {
	allowed := "- " + strings.Join(Labels, "\n- ")
	sys := fmt.Sprintf(systemPrompt, allowed)
	return sys + "\n\nResource:\n```yaml\n" + resourceYAML + "\n```\n"
}

// Diagnose runs one RCA call and returns the parsed diagnosis, the exact prompt
// token count (read from the same response, so no extra tokenizer round-trip is
// needed), and the raw model output (for debugging / failure inspection).
func (c *RCAClient) Diagnose(ctx context.Context, resourceYAML string) (Diagnosis, int, string, error) {
	reqBody, err := json.Marshal(map[string]any{
		"model":   c.Model,
		"prompt":  c.prompt(resourceYAML),
		"stream":  false,
		"format":  "json",
		"options": map[string]any{"temperature": c.Temperature},
	})
	if err != nil {
		return Diagnosis{}, 0, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/generate", bytes.NewReader(reqBody))
	if err != nil {
		return Diagnosis{}, 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.Client.Do(req)
	if err != nil {
		return Diagnosis{}, 0, "", err
	}

	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Diagnosis{}, 0, "", fmt.Errorf("ollama: unexpected status %d", res.StatusCode)
	}

	var out struct {
		Response        string `json:"response"`
		PromptEvalCount int    `json:"prompt_eval_count"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return Diagnosis{}, 0, "", err
	}

	var d Diagnosis
	if err := json.Unmarshal([]byte(out.Response), &d); err != nil {
		// Model returned non-JSON despite format=json: treat as a wrong answer,
		// not a hard error, so one bad sample does not abort the run.
		return Diagnosis{}, out.PromptEvalCount, out.Response, nil
	}

	return d, out.PromptEvalCount, out.Response, nil
}
