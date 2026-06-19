// Command matrix runs the generated benchmark across distillation profiles and
// reports RCA accuracy + tokens per profile and per difficulty. It compares:
//
//	L0     raw -o yaml
//	L2     structure-aware static distillation
//	rand   random-drop to L2's token budget (the H2 control)
//
// If L2 >> rand at equal tokens, the gain is from WHICH fields are kept, not
// from cutting volume.
//
// Usage:
//
//	go run ./cmd/matrix -model qwen2.5:7b-instruct -n 3 -k 3 -difficulty all
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/mikolajsemeniuk/kubelean/pkg/distill"
	"github.com/mikolajsemeniuk/kubelean/pkg/eval"
	"github.com/mikolajsemeniuk/kubelean/pkg/faults"
)

type acc struct{ correct, total, tokenSum, cells int }

func (a *acc) pct() float64 {
	if a.total == 0 {
		return 0
	}
	return 100 * float64(a.correct) / float64(a.total)
}
func (a *acc) meanTok() int {
	if a.cells == 0 {
		return 0
	}
	return a.tokenSum / a.cells
}

func main() {
	model := flag.String("model", "qwen2.5:7b-instruct", "Ollama model")
	n := flag.Int("n", 3, "instances per class")
	k := flag.Int("k", 3, "repeats per instance per profile")
	temp := flag.Float64("temp", 0.4, "sampling temperature")
	diff := flag.String("difficulty", "all", "all | easy | hard")
	volume := flag.Int("volume", 0, "structural-noise level (managedFields bloat) added before distillation")
	mislead := flag.Int("mislead", 0, "semantic-distractor level (stale annotations) added before distillation")
	flag.Parse()

	var instances []faults.Instance
	switch *diff {
	case "all":
		instances = faults.GenerateAll(*n)
	case "easy":
		instances = faults.GenerateByDifficulty(*n, faults.Easy)
	case "hard":
		instances = faults.GenerateByDifficulty(*n, faults.Hard)
	default:
		fatal("difficulty must be all|easy|hard")
	}

	client := eval.NewRCAClient(*model, *temp)
	ctx := context.Background()

	profiles := []string{"L0", "L2", "rand"}
	overall := map[string]*acc{}
	byDiff := map[string]*acc{} // key "difficulty/profile"
	byClass := map[string]*acc{}
	for _, p := range profiles {
		overall[p] = &acc{}
	}

	fmt.Printf("model=%s  n=%d  k=%d  temp=%.2f  difficulty=%s  volume=%d  mislead=%d  instances=%d\n\n",
		*model, *n, *k, *temp, *diff, *volume, *mislead, len(instances))

	for _, inst := range instances {
		res := inflateAll(inst.Resources, *volume, *mislead)
		// frac for the random-drop budget is computed locally from byte lengths
		// (no Ollama round-trip); exact token counts come from the RCA responses.
		l0y := render(res, "L0", inst.Truth.Goal)
		frac := 0.6
		if len(l0y) > 0 {
			frac = float64(len(render(res, "L2", inst.Truth.Goal))) / float64(len(l0y))
		}
		for _, p := range profiles {
			y := renderProfile(res, p, inst.Truth.Goal, frac)
			hit, promptTok := 0, 0
			for i := 0; i < *k; i++ {
				d, ptok, _, err := client.Diagnose(ctx, y)
				if err != nil {
					fatal("diagnose %s: %v", inst.Name, err)
				}
				promptTok = ptok
				if strings.EqualFold(strings.TrimSpace(d.RootCauseLabel), inst.Truth.Label) {
					hit++
				}
			}
			add(overall[p], hit, *k, promptTok)
			add(get(byDiff, instDiff(inst)+"/"+p), hit, *k, promptTok)
			add(get(byClass, inst.Truth.Label+"/"+p), hit, *k, promptTok)
		}
	}

	fmt.Println("== overall ==")
	fmt.Printf("%-6s %-10s %-8s\n", "prof", "acc", "tok")
	for _, p := range profiles {
		fmt.Printf("%-6s %-9.1f%% %-8d\n", p, overall[p].pct(), overall[p].meanTok())
	}

	fmt.Println("\n== by difficulty ==")
	for _, d := range []string{"easy", "hard"} {
		fmt.Printf("[%s]  ", d)
		for _, p := range profiles {
			a := byDiff[d+"/"+p]
			if a == nil {
				continue
			}
			fmt.Printf("%s=%.0f%%(%dtok) ", p, a.pct(), a.meanTok())
		}
		fmt.Println()
	}

	fmt.Println("\n== by class (L0 -> L2 -> rand) ==")
	for _, fc := range faults.Catalog() {
		l0, l2, rd := byClass[fc.Label+"/L0"], byClass[fc.Label+"/L2"], byClass[fc.Label+"/rand"]
		if l0 == nil {
			continue
		}
		fmt.Printf("  %-30s %-4s  %3.0f%% -> %3.0f%% -> %3.0f%%\n", fc.Label, fc.Difficulty, l0.pct(), l2.pct(), rd.pct())
	}
}

func instDiff(inst faults.Instance) string {
	for _, fc := range faults.Catalog() {
		if fc.Label == inst.Truth.Label {
			return fc.Difficulty.String()
		}
	}
	return "easy"
}

// inflateAll adds the requested noise to every resource in a bundle before
// distillation, so L0 sees the noise, L2 strips what it can, and random-drop is
// compared on the same noisy input.
func inflateAll(res []*unstructured.Unstructured, volume, mislead int) []*unstructured.Unstructured {
	if volume == 0 && mislead == 0 {
		return res
	}
	out := make([]*unstructured.Unstructured, len(res))
	for i, r := range res {
		out[i] = faults.Inflate(r, volume, mislead, i)
	}
	return out
}

func render(res []*unstructured.Unstructured, profile, goal string) string {
	return renderProfile(res, profile, goal, 0.6)
}

// renderProfile serializes a bundle under a profile into one ---joined document.
func renderProfile(res []*unstructured.Unstructured, profile, goal string, frac float64) string {
	parts := make([]string, 0, len(res))
	for i, r := range res {
		var out *unstructured.Unstructured
		switch profile {
		case "L0":
			out = r
		case "L2":
			out = distill.Distill(r, distill.Profile{Level: distill.L2StaticBuckets, Goal: distill.Goal(goal)})
		case "rand":
			out = distill.RandomDrop(r, frac, int64(i+1))
		}
		y, err := distill.ToYAML(out)
		if err != nil {
			fatal("serialize: %v", err)
		}
		parts = append(parts, y)
	}
	return strings.Join(parts, "---\n")
}

func add(a *acc, hit, k, t int) {
	a.correct += hit
	a.total += k
	a.tokenSum += t
	a.cells++
}

func get(m map[string]*acc, key string) *acc {
	if m[key] == nil {
		m[key] = &acc{}
	}
	return m[key]
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "matrix: "+format+"\n", args...)
	os.Exit(1)
}
