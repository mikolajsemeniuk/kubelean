// Command bench reports token reduction across distillation levels (L0 raw ->
// L1 lossless -> L2 static) on a representative generated fault resource, using
// a real model tokenizer (Ollama prompt_eval_count). It answers "how much
// smaller does distillation make the resource?" with no accuracy involved.
//
// Usage:
//
//	go run ./cmd/bench -model qwen2.5:7b-instruct
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/mikolajsemeniuk/kubelean/pkg/distill"
	"github.com/mikolajsemeniuk/kubelean/pkg/faults"
)

func main() {
	model := flag.String("model", "qwen2.5:7b-instruct", "Ollama model whose tokenizer counts tokens")
	flag.Parse()

	inst := faults.Catalog()[0].Generate(1)[0]
	obj := inst.Resources[0]

	counter := distill.NewOllamaCounter(*model)
	ctx := context.Background()

	levels := []struct {
		name  string
		level distill.Level
	}{
		{"L0 raw", distill.L0Raw},
		{"L1 lossless", distill.L1Lossless},
		{"L2 static", distill.L2StaticBuckets},
	}

	fmt.Printf("model: %s\nresource: %s (%s)\n\n", *model, inst.Name, inst.Truth.Label)
	fmt.Printf("%-14s %8s %8s %10s\n", "level", "tokens", "lines", "vs L0")

	var base int
	for i, l := range levels {
		in := distill.Distill(obj, distill.Profile{Level: l.level, Goal: distill.Goal(inst.Truth.Goal)})

		y, err := distill.ToYAML(in)
		if err != nil {
			log.Fatalf("serialize %s: %v", l.name, err)
		}

		tokens, err := counter.Count(ctx, y)
		if err != nil {
			log.Fatalf("count %s: %v (is `ollama serve` running?)", l.name, err)
		}

		if i == 0 {
			base = tokens
		}

		fmt.Printf("%-14s %8d %8d %9.0f%%\n", l.name, tokens, strings.Count(y, "\n"), 100*float64(tokens)/float64(base))
	}
}
