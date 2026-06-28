// Command heatmap is the m2 producer: for a group of scenarios it records raw
// per-trial RCA outcomes to a JSONL shard per scenario under -out. It removes
// each field (heatmap.Remove), asks the model k times (seeds 0..k-1), and writes
// one line per trial — no accuracy, no saliency, no LaTeX. Those are derived
// later by cmd/render from these shards, so a methodology change never re-runs
// the model. Each run overwrites its scenario shards.
//
//	go run ./cmd/heatmap -group secret-ref
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/mikolajsemeniuk/kubelean/pkg/dataset"
	"github.com/mikolajsemeniuk/kubelean/pkg/heatmap"
	"github.com/mikolajsemeniuk/kubelean/pkg/providers"
)

type diagnosis struct {
	FaultClass string `json:"fault_class"`
}

var (
	host, model, out      string
	group                 string
	k, numCtx, numPredict int
	temp                  float64

	// schema constrains the model to clean JSON with a fixed fault_class vocabulary
	// (mirrors the scenarios' FaultClass labels + NoFaultFound). Extend the enum when
	// you add scenarios.
	schema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"fault_class": map[string]any{
				"type": "string",
				"enum": []string{"SelectorLabelMismatch", "SecretRefNotFound", "NoFaultFound"},
			},
			"offending_field": map[string]any{"type": "string"},
		},
		"required": []string{"fault_class", "offending_field"},
	}

	// systemPrompt frames the root-cause task. The schema enforces the JSON shape and
	// the fault_class vocabulary, so scoring is a plain string compare. The "reduced"
	// framing is load-bearing: variants have fields removed for the ablation, and we
	// must stop the model from reading an absent field as a fault in itself.
	systemPrompt = `You are a senior Kubernetes SRE performing root-cause analysis.
You are given one or more manifests as returned by "kubectl get -o yaml".
The manifests may have been deliberately reduced — some fields removed to save context.
Treat every manifest as valid and well-formed: a missing field is not itself a fault.
Diagnose the root cause only from the information that is present.
At most one root-cause fault is present; the manifests may also be healthy.
fault_class must be one of: SelectorLabelMismatch, SecretRefNotFound, NoFaultFound.
Set offending_field to the YAML path most responsible, or "none".`
)

func main() {
	flag.StringVar(&host, "host", "http://localhost:11434", "Ollama host")
	flag.StringVar(&model, "model", "qwen2.5:7b-instruct", "model name")
	flag.StringVar(&out, "out", "data", "output directory for JSONL shards")
	flag.StringVar(&group, "group", "", "scenario group to produce")
	flag.IntVar(&k, "k", 10, "samples per variant (seed = 0..k-1)")
	flag.Float64Var(&temp, "temp", 0.7, "sampling temperature (>0 so seeds give varied draws)")
	flag.IntVar(&numCtx, "num-ctx", 8192, "context window — avoids silent truncation of multi-doc prompts")
	flag.IntVar(&numPredict, "num-predict", 256, "max output tokens")
	flag.Parse()

	scenarios := dataset.Scenarios(group)
	if len(scenarios) == 0 {
		log.Fatalf("no scenarios in group %q", group)
	}

	ctx := context.Background()
	client := providers.NewOllama(host)

	// Pin the exact weights for the paper: a tag can be re-pulled and change.
	digest, err := client.Digest(ctx, model)
	if err != nil {
		log.Printf("warning: could not read model digest: %v", err)
		digest = "unknown"
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		log.Fatal(err)
	}

	for _, s := range scenarios {
		start := time.Now()

		in := providers.ChatInput{
			Model:   model,
			Format:  schema,
			Options: providers.ChatOptions{Temperature: temp, NumCtx: numCtx, NumPredict: numPredict},
		}

		var recs []heatmap.Record

		// baseline: the full bundle, k trials.
		in.Prompt = systemPrompt + "\n\nManifests:\n" + s.YAML
		baseCorrect := 0
		for i := 0; i < k; i++ {
			r := trial(ctx, client, in, i, s, digest)
			r.Variant = "baseline"
			r.Valid = true
			if r.Answer != nil && *r.Answer == s.FaultClass {
				baseCorrect++
			}
			recs = append(recs, r)
		}

		targets, err := heatmap.Keys(s.YAML)
		if err != nil {
			log.Fatalf("%s keys: %v", s.Name, err)
		}

		fmt.Printf("%s [%s]: baseline %d/%d correct, ablating %d fields × k=%d…\n",
			s.Name, s.Group, baseCorrect, k, len(targets), k)

		invalid := 0
		for _, t := range targets {
			reduced, err := heatmap.Remove(s.YAML, t)
			if err != nil {
				log.Fatalf("%s remove %s: %v", s.Name, t.Pointer, err)
			}

			valid, _, err := heatmap.Valid(reduced)
			if err != nil {
				log.Fatalf("%s validate %s: %v", s.Name, t.Pointer, err)
			}
			if !valid {
				invalid++
			}

			in.Prompt = systemPrompt + "\n\nManifests:\n" + reduced
			doc, field := t.Doc, t.Pointer
			for i := 0; i < k; i++ {
				r := trial(ctx, client, in, i, s, digest)
				r.Variant = "reduced"
				r.Doc = &doc
				r.Kind = t.Kind
				r.Field = &field
				r.Category = string(t.Category)
				r.Valid = valid
				recs = append(recs, r)
			}
		}
		fmt.Printf("  %d/%d variants invalid (recorded, flagged in shard)\n", invalid, len(targets))

		path := filepath.Join(out, s.Name+".jsonl")
		f, err := os.Create(path)
		if err != nil {
			log.Fatalf("create %s: %v", path, err)
		}
		enc := json.NewEncoder(f)
		for _, r := range recs {
			if err := enc.Encode(r); err != nil {
				log.Fatalf("write %s: %v", path, err)
			}
		}
		if err := f.Close(); err != nil {
			log.Fatalf("close %s: %v", path, err)
		}

		d := time.Since(start)
		dur := fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
		fmt.Printf("wrote %s (%d trials) in %s\n", path, len(recs), dur)
	}
}

// trial runs one model call at the given seed and returns the raw record. Answer
// is nil when the response does not parse (a stopgap recorded for item #8).
func trial(ctx context.Context, client *providers.Ollama, in providers.ChatInput, seed int, s dataset.Scenario, digest string) heatmap.Record {
	in.Options.Seed = int64(seed)
	res, err := client.Chat(ctx, in)
	if err != nil {
		log.Fatalf("%s chat (seed %d): %v", s.Name, seed, err)
	}

	r := heatmap.Record{
		Scenario:    s.Name,
		Group:       s.Group,
		FaultClass:  s.FaultClass,
		Seed:        int64(seed),
		K:           k,
		Model:       model,
		ModelDigest: digest,
		Temp:        temp,
		NumCtx:      numCtx,
		Raw:         res.Response,
	}

	var d diagnosis
	if err := json.Unmarshal([]byte(res.Response), &d); err == nil {
		answer := d.FaultClass
		r.Answer = &answer
	}

	return r
}
