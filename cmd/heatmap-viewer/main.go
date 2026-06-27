// Command heatmap-viewer runs the same ablation experiment as cmd/heatmapv2 but,
// instead of emitting LaTeX, collects the saliency data as JSON in memory and —
// once the experiment finishes — serves it over a small HTTP server, rendered as
// an interactive table in the browser. A scratch tool for eyeballing results
// (including the model's prediction per ablation) while iterating.
//
//	go run ./cmd/heatmap-viewer [flags]   # then open the printed URL
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/mikolajsemeniuk/kubelean/pkg/dataset"
	"github.com/mikolajsemeniuk/kubelean/pkg/heatmap"
	"github.com/mikolajsemeniuk/kubelean/pkg/providers"
)

//go:embed index.html
var page string

type diagnosis struct {
	FaultClass     string `json:"fault_class"`
	OffendingField string `json:"offending_field"`
}

// Row is one ablation result. Predicted/Offending are the model's raw answer,
// kept so the viewer can show *why* a field scored as it did.
type Row struct {
	Scenario  string  `json:"scenario"`
	Kind      string  `json:"kind"`
	Field     string  `json:"field"`
	Saliency  float64 `json:"saliency"`
	Predicted string  `json:"predicted"`
	Offending string  `json:"offending"`
}

type Data struct {
	Model string `json:"model"`
	Rows  []Row  `json:"rows"`
}

var (
	host, model, addr string
	seed              int64
	temp              float64

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
	// the fault_class vocabulary, so scoring is a plain string compare.
	systemPrompt = `You are a senior Kubernetes SRE performing root-cause analysis.
You are given one or more manifests as returned by "kubectl get -o yaml".
At most one root-cause fault is present; the manifests may also be healthy.
fault_class must be one of: SelectorLabelMismatch, SecretRefNotFound, NoFaultFound.
Set offending_field to the YAML path most responsible, or "none".`
)

func main() {
	flag.StringVar(&host, "host", "http://localhost:11434", "Ollama host")
	flag.StringVar(&model, "model", "qwen2.5:7b-instruct", "model name")
	flag.StringVar(&addr, "addr", ":8080", "HTTP listen address")
	flag.Int64Var(&seed, "seed", 0, "sampling seed")
	flag.Float64Var(&temp, "temp", 0, "sampling temperature")
	flag.Parse()

	ctx := context.Background()
	client := providers.NewOllama(host)

	var rows []Row
	for _, s := range dataset.Scenarios() {
		in := providers.ChatInput{
			Model:   model,
			Prompt:  systemPrompt + "\n\nManifests:\n" + s.YAML,
			Format:  schema,
			Options: providers.ChatOptions{Temperature: temp, Seed: seed},
		}
		res, err := client.Chat(ctx, in)
		if err != nil {
			log.Fatalf("%s baseline: %v", s.Name, err)
		}

		var base diagnosis
		if err := json.Unmarshal([]byte(res.Response), &base); err != nil {
			log.Fatalf("%s baseline: %v", s.Name, err)
		}

		correct := base.FaultClass == s.FaultClass
		targets, err := heatmap.Keys(s.YAML)
		if err != nil {
			log.Fatalf("%s keys: %v", s.Name, err)
		}

		fmt.Printf("%s: baseline correct=%v, ablating %d targets…\n", s.Name, correct, len(targets))
		for _, t := range targets {
			reduced, err := heatmap.Remove(s.YAML, t)
			if err != nil {
				log.Fatalf("%s remove %s: %v", s.Name, t.Pointer, err)
			}

			in.Prompt = systemPrompt + "\n\nManifests:\n" + reduced
			res, err := client.Chat(ctx, in)
			if err != nil {
				log.Fatalf("%s diagnose %s: %v", s.Name, t.Pointer, err)
			}

			var d diagnosis
			if err := json.Unmarshal([]byte(res.Response), &d); err != nil {
				log.Fatalf("%s diagnose %s: %v", s.Name, t.Pointer, err)
			}

			rows = append(rows, Row{
				Scenario:  s.Name,
				Kind:      t.Kind,
				Field:     t.Pointer,
				Saliency:  b2f(correct) - b2f(d.FaultClass == s.FaultClass),
				Predicted: d.FaultClass,
				Offending: d.OffendingField,
			})
		}
	}

	payload, err := json.Marshal(Data{Model: model, Rows: rows})
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/api/data", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, page)
	})

	fmt.Printf("\nserving %d rows on http://localhost%s\n", len(rows), addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
