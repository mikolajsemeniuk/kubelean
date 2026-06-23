// Command oracle computes the L5 leave-one-field-out diagnostic saliency map: for
// each fault class it drops every field in turn and remeasures RCA accuracy, so
// the field whose removal hurts most is the empirically decisive one. This is the
// gold saliency upper bound (and a validation of the injected ground-truth
// OffendingField). It is EXPENSIVE — one RCA pass per field — so it defaults to a
// tiny sample; raise -n / -k only when you can wait.
//
// Usage:
//
//	go run ./cmd/oracle -model qwen2.5:7b-instruct -n 1 -k 1
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/schollz/progressbar/v3"

	"github.com/mikolajsemeniuk/kubelean/pkg/bench"
	"github.com/mikolajsemeniuk/kubelean/pkg/distill"
	"github.com/mikolajsemeniuk/kubelean/pkg/eval"
	"github.com/mikolajsemeniuk/kubelean/pkg/faults"
)

type agg struct {
	sum  float64
	n    int
	base float64
}

// classResult is the aggregated oracle outcome for one fault class.
type classResult struct {
	fc       faults.FaultClass
	ranked   []distill.FieldSaliency // aggregated, sorted desc by saliency
	baseAcc  float64
	recovers bool // does a top field match the injected OffendingField?
}

func main() {
	model := flag.String("model", "qwen2.5:7b-instruct", "Ollama model")
	n := flag.Int("n", 1, "instances per class")
	k := flag.Int("k", 1, "RCA repeats per configuration")
	temp := flag.Float64("temp", 0.4, "sampling temperature")
	top := flag.Int("top", 3, "how many top-saliency fields to report per class")
	texOut := flag.String("tex", "paper/oracle.gen.tex", "write the LaTeX saliency table here (empty = skip)")
	flag.Parse()

	rc := eval.NewRCAClient(*model, *temp)
	diag := func(ctx context.Context, y string) (string, error) {
		d, _, _, err := rc.Diagnose(ctx, y)
		return d.RootCauseLabel, err
	}
	ctx := context.Background()

	var results []classResult

	bar := progressbar.NewOptions(len(faults.Catalog())*(*n),
		progressbar.OptionSetDescription("oracle"),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionShowCount(),
		progressbar.OptionShowElapsedTimeOnFinish(),
		progressbar.OptionSetPredictTime(true),
	)

	for _, fc := range faults.Catalog() {
		byPath := map[string]*agg{}
		for _, inst := range fc.Generate(*n) {
			if len(inst.Resources) != 1 {
				// Bundles have an ambiguous "which resource" question; oracle runs
				// on single-resource faults only.
				_ = bar.Add(1)
				continue
			}
			sal, err := distill.OracleSaliency(ctx, inst.Resources[0], inst.Truth.Label, diag, *k)
			if err != nil {
				fatal("oracle %s: %v", inst.Name, err)
			}
			for _, fs := range sal {
				a := byPath[fs.Path]
				if a == nil {
					a = &agg{}
					byPath[fs.Path] = a
				}
				a.sum += fs.Saliency
				a.n++
				a.base = fs.BaseAcc
			}
			_ = bar.Add(1)
		}

		ranked := make([]distill.FieldSaliency, 0, len(byPath))
		var base float64
		for path, a := range byPath {
			ranked = append(ranked, distill.FieldSaliency{Path: path, Saliency: a.sum / float64(a.n), BaseAcc: a.base})
			base = a.base
		}
		sort.Slice(ranked, func(i, j int) bool { return ranked[i].Saliency > ranked[j].Saliency })

		recovers := false
		for i := 0; i < *top && i < len(ranked); i++ {
			if fieldMatches(ranked[i].Path, fc.OffendingField) {
				recovers = true
			}
		}
		results = append(results, classResult{fc: fc, ranked: ranked, baseAcc: base, recovers: recovers})
	}

	fmt.Printf("\nmodel=%s  n=%d  k=%d  temp=%.2f\n\n", *model, *n, *k, *temp)
	fmt.Println("== L5 oracle saliency (top fields per class; * = matches injected OffendingField) ==")
	for _, r := range results {
		if len(r.ranked) == 0 {
			fmt.Printf("\n%s [%s]  (skipped: bundle / no single resource)\n", r.fc.Label, r.fc.Difficulty)
			continue
		}
		fmt.Printf("\n%s [%s]  baseAcc=%.0f%%  truth-field=%s%s\n",
			r.fc.Label, r.fc.Difficulty, 100*r.baseAcc, r.fc.OffendingField, recoverMark(r.recovers))
		for i := 0; i < *top && i < len(r.ranked); i++ {
			fs := r.ranked[i]
			mark := ""
			if fieldMatches(fs.Path, r.fc.OffendingField) {
				mark = " *"
			}
			fmt.Printf("    %+.2f  %s%s\n", fs.Saliency, fs.Path, mark)
		}
	}

	if *texOut != "" {
		if err := writeTeX(*texOut, *model, *n, *k, *temp, *top, results); err != nil {
			fatal("write tex: %v", err)
		}
		fmt.Printf("\nwrote LaTeX: %s\n", *texOut)
	}
}

// fieldMatches reports whether an oracle path corresponds to the injected
// OffendingField. The truth field uses [0]-free notation (e.g.
// "spec.containers[0].image" vs catalog "spec.containers[0].image", or
// "status.containerStatuses[0].lastState.terminated.reason"); compare with array
// indices stripped so notation differences do not cause false misses.
func fieldMatches(oraclePath, truthField string) bool {
	a, b := stripIdx(oraclePath), stripIdx(truthField)
	if a == b {
		return true
	}
	// the truth field may name a subtree; count an oracle leaf beneath it as a match
	return strings.HasPrefix(a, b+".") || strings.HasPrefix(b, a+".")
}

func stripIdx(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch {
		case r == '[':
			skip = true
		case r == ']':
			skip = false
		case !skip:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func recoverMark(ok bool) string {
	if ok {
		return "  [recovered *]"
	}
	return "  [NOT in top]"
}

func writeTeX(path, model string, n, k int, temp float64, top int, results []classResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%% oracle.gen.tex — AUTO-GENERATED by cmd/oracle. DO NOT EDIT BY HAND.\n")
	fmt.Fprintf(&b, "%% Run: model=%s n=%d k=%d temp=%.2f top=%d\n", model, n, k, temp, top)
	fmt.Fprintf(&b, "%% Requires \\usepackage{booktabs} in the preamble.\n\n")
	fmt.Fprintf(&b, "\\begin{table}[t]\n  \\centering\n")
	fmt.Fprintf(&b, "  \\caption{L5 oracle leave-one-field-out saliency: the most decisive field per fault class and whether it recovers the injected deciding field (\\texttt{%s}, $n{=}%d$, $k{=}%d$).}\n", bench.TexEscape(model), n, k)
	fmt.Fprintf(&b, "  \\label{tab:oracle-saliency}\n")
	fmt.Fprintf(&b, "  \\begin{tabular}{llrc}\n    \\toprule\n")
	fmt.Fprintf(&b, "    Fault class & Top oracle field & Saliency & Recovers \\\\\n    \\midrule\n")
	for _, r := range results {
		if len(r.ranked) == 0 {
			fmt.Fprintf(&b, "    %s & \\multicolumn{3}{l}{\\emph{bundle (skipped)}} \\\\\n", bench.TexEscape(r.fc.Label))
			continue
		}
		t := r.ranked[0]
		rec := "no"
		if r.recovers {
			rec = "yes"
		}
		fmt.Fprintf(&b, "    %s & \\texttt{%s} & %+.2f & %s \\\\\n", bench.TexEscape(r.fc.Label), bench.TexEscape(t.Path), t.Saliency, rec)
	}
	fmt.Fprintf(&b, "    \\bottomrule\n  \\end{tabular}\n\\end{table}\n")

	return bench.WriteFile(path, b.String())
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "oracle: "+format+"\n", args...)
	os.Exit(1)
}
