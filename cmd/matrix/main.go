// Command matrix runs the generated benchmark across distillation profiles and
// reports RCA accuracy + tokens per profile and per difficulty. It compares:
//
//	L0     raw -o yaml
//	L1     lossless strip (server-managed noise; RCA-safe by construction)
//	L2     structure-aware static distillation
//	L3     corpus-entropy saliency (drop fields constant across the corpus)
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
	"path/filepath"
	"strings"
	"time"

	"github.com/schollz/progressbar/v3"
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
	l3thresh := flag.Float64("l3thresh", 0.0, "L3 entropy threshold in [0,1]: drop leaves at or below it (0 = only perfectly-constant)")
	texOut := flag.String("tex", "paper/matrix.gen.tex", "write LaTeX result tables to this path (empty = skip)")
	l4 := flag.Bool("l4", false, "include the L4 goal-conditioned profile (embedding-grounding; needs an embed model)")
	embedModel := flag.String("embed", "nomic-embed-text", "Ollama embedding model used by L4")
	l4thresh := flag.Float64("l4thresh", 0.5, "L4 cosine-similarity keep threshold in [0,1]")
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

	profiles := []string{"L0", "L1", "L2", "L3"}
	if *l4 {
		profiles = append(profiles, "L4")
	}
	profiles = append(profiles, "rand")
	overall := map[string]*acc{}
	byDiff := map[string]*acc{} // key "difficulty/profile"
	byClass := map[string]*acc{}
	for _, p := range profiles {
		overall[p] = &acc{}
	}

	fmt.Printf("model=%s  n=%d  k=%d  temp=%.2f  difficulty=%s  volume=%d  mislead=%d  instances=%d\n\n",
		*model, *n, *k, *temp, *diff, *volume, *mislead, len(instances))

	// Inflate every instance up front and build the L3 corpus model from the
	// actual L0 inputs (cross-class, per kind), so saliency reflects what the
	// model is fed. Stats are computed once and reused for every DistillL3 call.
	type prepared struct {
		inst faults.Instance
		res  []*unstructured.Unstructured
	}
	preps := make([]prepared, 0, len(instances))
	var corpus []*unstructured.Unstructured
	for _, inst := range instances {
		res := inflateAll(inst.Resources, *volume, *mislead)
		preps = append(preps, prepared{inst, res})
		corpus = append(corpus, res...)
	}
	stats := distill.BuildCorpusStats(corpus)

	// Pass 1: serialize every (instance, profile) up front. L4 needs the embedding
	// model; doing all groundings here keeps that model loaded, instead of Ollama
	// thrashing between the embed and RCA models per instance. Everything else is
	// a cheap pure transform.
	var embed distill.EmbedFunc
	if *l4 {
		embed = distill.NewEmbedder(*embedModel).Embed
		fmt.Printf("L4 grounding via %s (threshold %.2f)\n", *embedModel, *l4thresh)
	}
	yamls := make([]map[string]string, len(preps))
	renderBar := progressbar.NewOptions(len(preps),
		progressbar.OptionSetDescription("render"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionClearOnFinish(),
	)
	for i, pr := range preps {
		goal := pr.inst.Truth.Goal
		// frac for the random-drop budget is computed locally from byte lengths
		// (no Ollama round-trip); exact token counts come from the RCA responses.
		l0y := render(pr.res, "L0", goal, stats, *l3thresh)
		frac := 0.6
		if len(l0y) > 0 {
			frac = float64(len(render(pr.res, "L2", goal, stats, *l3thresh))) / float64(len(l0y))
		}
		m := make(map[string]string, len(profiles))
		for _, p := range profiles {
			if p == "L4" {
				m[p] = renderL4(ctx, pr.res, goal, embed, *l4thresh)
			} else {
				m[p] = renderProfile(pr.res, p, goal, frac, stats, *l3thresh)
			}
		}
		yamls[i] = m
		_ = renderBar.Add(1)
	}

	// Pass 2: one RCA call per (instance, profile, repeat). The bar writes to
	// stderr, so the final tables on stdout stay clean (and pipeable).
	total := len(preps) * len(profiles) * *k
	bar := progressbar.NewOptions(total,
		progressbar.OptionSetDescription("RCA"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionShowCount(),
		progressbar.OptionShowElapsedTimeOnFinish(),
		progressbar.OptionSetPredictTime(true),
		progressbar.OptionThrottle(100*time.Millisecond),
		progressbar.OptionClearOnFinish(),
	)
	for i, pr := range preps {
		inst := pr.inst
		for _, p := range profiles {
			y := yamls[i][p]
			hit, promptTok := 0, 0
			for j := 0; j < *k; j++ {
				d, ptok, _, err := client.Diagnose(ctx, y)
				if err != nil {
					fatal("diagnose %s: %v", inst.Name, err)
				}
				promptTok = ptok
				if strings.EqualFold(strings.TrimSpace(d.RootCauseLabel), inst.Truth.Label) {
					hit++
				}
				_ = bar.Add(1)
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

	fmt.Printf("\n== by class (%s) ==\n", strings.Join(profiles, " -> "))
	for _, fc := range faults.Catalog() {
		if byClass[fc.Label+"/"+profiles[0]] == nil {
			continue
		}
		fmt.Printf("  %-30s %-4s ", fc.Label, fc.Difficulty)
		for _, p := range profiles {
			fmt.Printf(" %3.0f%%", byClass[fc.Label+"/"+p].pct())
		}
		fmt.Println()
	}

	if *texOut != "" {
		run := runMeta{
			model: *model, n: *n, k: *k, temp: *temp, diff: *diff,
			volume: *volume, mislead: *mislead, l3thresh: *l3thresh, instances: len(instances),
		}
		if err := writeTeX(*texOut, run, profiles, overall, byDiff, byClass); err != nil {
			fatal("write tex: %v", err)
		}
		fmt.Printf("\nwrote LaTeX tables: %s\n", *texOut)
	}
}

// runMeta captures the experiment parameters recorded in the generated LaTeX.
type runMeta struct {
	model           string
	n, k            int
	temp            float64
	diff            string
	volume, mislead int
	l3thresh        float64
	instances       int
}

// writeTeX renders the overall, by-difficulty, and by-class result tables as
// booktabs LaTeX, ready to \input{} into the paper. The file is fully
// regenerated each run; do not hand-edit it.
func writeTeX(path string, m runMeta, profiles []string, overall, byDiff, byClass map[string]*acc) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%% matrix.gen.tex — AUTO-GENERATED by cmd/matrix. DO NOT EDIT BY HAND.\n")
	fmt.Fprintf(&b, "%% Regenerate with: make matrix\n")
	fmt.Fprintf(&b, "%% Run: model=%s n=%d k=%d temp=%.2f difficulty=%s volume=%d mislead=%d l3thresh=%.2f instances=%d\n",
		m.model, m.n, m.k, m.temp, m.diff, m.volume, m.mislead, m.l3thresh, m.instances)
	fmt.Fprintf(&b, "%% Requires \\usepackage{booktabs} in the preamble.\n\n")

	caption := fmt.Sprintf("(\\texttt{%s}, $n{=}%d$, $k{=}%d$, $T{=}%.2f$)", texEscape(m.model), m.n, m.k, m.temp)

	// --- overall ---
	fmt.Fprintf(&b, "\\begin{table}[t]\n  \\centering\n")
	fmt.Fprintf(&b, "  \\caption{RCA accuracy and mean prompt tokens per distillation profile %s.}\n", caption)
	fmt.Fprintf(&b, "  \\label{tab:matrix-overall}\n")
	fmt.Fprintf(&b, "  \\begin{tabular}{lrr}\n    \\toprule\n")
	fmt.Fprintf(&b, "    Profile & Acc. (\\%%) & Tokens \\\\\n    \\midrule\n")
	for _, p := range profiles {
		a := overall[p]
		fmt.Fprintf(&b, "    %s & %.1f & %d \\\\\n", texEscape(p), a.pct(), a.meanTok())
	}
	fmt.Fprintf(&b, "    \\bottomrule\n  \\end{tabular}\n\\end{table}\n\n")

	// --- by difficulty ---
	colSpec := "l" + strings.Repeat("r", len(profiles))
	fmt.Fprintf(&b, "\\begin{table}[t]\n  \\centering\n")
	fmt.Fprintf(&b, "  \\caption{RCA accuracy (\\%%) by difficulty %s.}\n", caption)
	fmt.Fprintf(&b, "  \\label{tab:matrix-bydifficulty}\n")
	fmt.Fprintf(&b, "  \\begin{tabular}{%s}\n    \\toprule\n", colSpec)
	fmt.Fprintf(&b, "    Difficulty")
	for _, p := range profiles {
		fmt.Fprintf(&b, " & %s", texEscape(p))
	}
	fmt.Fprintf(&b, " \\\\\n    \\midrule\n")
	for _, d := range []string{"easy", "hard"} {
		if byDiff[d+"/"+profiles[0]] == nil {
			continue
		}
		fmt.Fprintf(&b, "    %s", d)
		for _, p := range profiles {
			fmt.Fprintf(&b, " & %.0f", byDiff[d+"/"+p].pct())
		}
		fmt.Fprintf(&b, " \\\\\n")
	}
	fmt.Fprintf(&b, "    \\bottomrule\n  \\end{tabular}\n\\end{table}\n\n")

	// --- by class ---
	fmt.Fprintf(&b, "\\begin{table}[t]\n  \\centering\n")
	fmt.Fprintf(&b, "  \\caption{RCA accuracy (\\%%) per fault class across distillation profiles %s.}\n", caption)
	fmt.Fprintf(&b, "  \\label{tab:matrix-byclass}\n")
	fmt.Fprintf(&b, "  \\begin{tabular}{ll%s}\n    \\toprule\n", strings.Repeat("r", len(profiles)))
	fmt.Fprintf(&b, "    Fault class & Diff.")
	for _, p := range profiles {
		fmt.Fprintf(&b, " & %s", texEscape(p))
	}
	fmt.Fprintf(&b, " \\\\\n    \\midrule\n")
	for _, fc := range faults.Catalog() {
		if byClass[fc.Label+"/"+profiles[0]] == nil {
			continue
		}
		fmt.Fprintf(&b, "    %s & %s", texEscape(fc.Label), fc.Difficulty)
		for _, p := range profiles {
			fmt.Fprintf(&b, " & %.0f", byClass[fc.Label+"/"+p].pct())
		}
		fmt.Fprintf(&b, " \\\\\n")
	}
	fmt.Fprintf(&b, "    \\bottomrule\n  \\end{tabular}\n\\end{table}\n")

	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// texEscape escapes the LaTeX special characters that appear in our labels
// (fault classes carry underscores; model names may too).
func texEscape(s string) string {
	r := strings.NewReplacer(
		`\`, `\textbackslash{}`,
		`_`, `\_`,
		`%`, `\%`,
		`&`, `\&`,
		`#`, `\#`,
		`$`, `\$`,
		`{`, `\{`,
		`}`, `\}`,
	)
	return r.Replace(s)
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

func render(res []*unstructured.Unstructured, profile, goal string, stats *distill.CorpusStats, l3thresh float64) string {
	return renderProfile(res, profile, goal, 0.6, stats, l3thresh)
}

// renderL4 serializes a bundle under the goal-conditioned L4 profile, calling the
// embedder per resource. Kept separate from renderProfile because L4 needs the
// context and embed function and can fail.
func renderL4(ctx context.Context, res []*unstructured.Unstructured, goal string, embed distill.EmbedFunc, thresh float64) string {
	parts := make([]string, 0, len(res))
	for _, r := range res {
		out, err := distill.DistillL4(ctx, r, goal, embed, thresh)
		if err != nil {
			fatal("L4 grounding: %v", err)
		}
		y, err := distill.ToYAML(out)
		if err != nil {
			fatal("serialize: %v", err)
		}
		parts = append(parts, y)
	}
	return strings.Join(parts, "---\n")
}

// renderProfile serializes a bundle under a profile into one ---joined document.
func renderProfile(res []*unstructured.Unstructured, profile, goal string, frac float64, stats *distill.CorpusStats, l3thresh float64) string {
	parts := make([]string, 0, len(res))
	for i, r := range res {
		var out *unstructured.Unstructured
		switch profile {
		case "L0":
			out = r
		case "L1":
			out = distill.Distill(r, distill.Profile{Level: distill.L1Lossless, Goal: distill.Goal(goal)})
		case "L2":
			out = distill.Distill(r, distill.Profile{Level: distill.L2StaticBuckets, Goal: distill.Goal(goal)})
		case "L3":
			out = distill.DistillL3(r, stats, l3thresh)
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
