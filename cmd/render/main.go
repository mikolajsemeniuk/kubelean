// Command render reads the raw per-trial JSONL shards produced by cmd/heatmap and
// renders paper/heatmap.gen.tex. Accuracy and saliency are computed here, not at
// produce time, so the slow model calls are never repeated when the methodology
// changes.
//
// It partitions fields into two populations (the flip): a field whose removal
// deletes the fault is a deciding-field locus — its expected answer is NOT the
// scenario's fault but NoFaultFound, since the fault is gone. Scoring those as
// "missed the fault" would mechanically yield saliency 1.00 and falsely read as
// signal. So deciding loci go to a separate control table (how often the model
// recognizes the fault is gone); the main saliency table holds only non-deciding
// fields. Deciding loci are resolved from each scenario's ground truth via
// heatmap.ResolveLeaves.
//
//	go run ./cmd/render -in data -out paper
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mikolajsemeniuk/kubelean/pkg/dataset"
	"github.com/mikolajsemeniuk/kubelean/pkg/heatmap"
)

const noFault = "NoFaultFound"

// locus is a resolved deciding-field location plus its ground-truth role:
// hides == true means removal only hides the fault's counter-evidence (the
// reference still dangles) instead of deleting the fault.
type locus struct {
	heatmap.Locus
	hides bool
}

// cell aggregates all reduced trials for one (scenario, doc, field).
type cell struct {
	scenario     string
	kind         string
	field        string
	valid        bool
	deciding     bool
	hides        bool // deciding subpopulation: evidence-hiding, not fault-deleting
	total        int
	matchFault   int            // answer == scenario fault (saliency uses this)
	matchNoFault int            // answer == NoFaultFound (control "recognized" uses this)
	seedFault    map[int64]bool // per-seed correctness — pairs with the baseline's seeds for McNemar
}

// fdr is the Benjamini–Hochberg false-discovery rate at which a cell counts as
// signal: with ~150 simultaneous cells, a raw per-cell 0.05 would admit ~7
// false positives by chance alone; BH bounds the expected false fraction instead.
const fdr = 0.05

func main() {
	in := flag.String("in", "data", "root directory of JSONL shards (a per-model subdirectory is appended)")
	out := flag.String("out", "paper", "root output directory (a per-model subdirectory is appended)")
	model := flag.String("model", "qwen2.5:7b-instruct", "model whose shards to render — selects data/<model>/ and paper/<model>/")
	gate := flag.Float64("gate", 0.8, "min baseline accuracy for a faulty scenario to be scored (m2 #12 gate)")
	fragileMax := flag.Float64("fragile", 0.3, "max negative-control floor for a scenario's cells to be eligible as signal; above it every BH-passing cell renders as 'fragile' (destabilization-dominated scenario)")
	flag.Parse()

	// Shards and artifacts are namespaced per model so a second model's run
	// never overwrites the first — the whole point of the future comparison.
	inDir := filepath.Join(*in, modelDir(*model))
	outDir := filepath.Join(*out, modelDir(*model))

	recs := readShards(inDir)
	if len(recs) == 0 {
		log.Fatalf("no records in %s — produce shards first (make run-<group> MODEL=%s)", inDir, *model)
	}

	// Resolve each scenario's deciding-field loci, then normalize their pointers to
	// the same canonical field-key form the records carry (array indices -> *), so
	// decides() compares like with like. Each locus keeps its ground-truth role:
	// fault-deleting vs evidence-hiding (dataset.DecidingField.Hides).
	deciding := map[string][]locus{}
	decidingNorms := map[string][][]string{} // normalized dotted paths, for offending_field scoring
	for _, s := range dataset.All() {
		for _, df := range s.DecidingFields {
			ls, err := heatmap.ResolveLeaves(s.YAML, df.Kind, df.Path)
			if err != nil {
				log.Fatalf("resolve %s %s: %v", s.Name, df.Path, err)
			}
			for i := range ls {
				ls[i].Pointer = heatmap.NormalizeKey(ls[i].Pointer)
				deciding[s.Name] = append(deciding[s.Name], locus{ls[i], df.Hides})
			}
			decidingNorms[s.Name] = append(decidingNorms[s.Name], normPath(df.Path))
		}
	}

	baseCorrect := map[string]int{}
	baseTotal := map[string]int{}
	baseSeed := map[string]map[int64]bool{} // scenario → seed → baseline correct (the McNemar pairing)
	locTotal := map[string]int{}            // faulty-scenario baseline trials with the correct class...
	locHit := map[string]int{}              // ...of which offending_field pointed at a deciding locus
	blame := map[string]map[string]int{}    // ...and what they actually blamed (top path shown in Table 6)
	cells := map[string]cell{}
	faultClass := map[string]string{}
	var order []string
	digests := map[string]bool{}

	for _, r := range recs {
		digests[r.ModelDigest] = true
		faultClass[r.Scenario] = r.FaultClass

		if r.Variant == "baseline" {
			baseTotal[r.Scenario]++
			correct := r.Answer != nil && *r.Answer == r.FaultClass
			if correct {
				baseCorrect[r.Scenario]++
			}
			if baseSeed[r.Scenario] == nil {
				baseSeed[r.Scenario] = map[int64]bool{}
			}
			baseSeed[r.Scenario][r.Seed] = correct

			// Localization: the class was right — did the model also blame a
			// deciding field, or did it guess the class off a healthy distractor?
			if correct && r.FaultClass != noFault {
				locTotal[r.Scenario]++
				var d struct {
					OffendingField string `json:"offending_field"`
				}
				if json.Unmarshal([]byte(r.Raw), &d) == nil {
					if locates(normPath(d.OffendingField), decidingNorms[r.Scenario]) {
						locHit[r.Scenario]++
					}
					if blame[r.Scenario] == nil {
						blame[r.Scenario] = map[string]int{}
					}
					blame[r.Scenario][d.OffendingField]++
				}
			}
			continue
		}

		field, doc := "", -1
		if r.Field != nil {
			field = *r.Field
		}
		if r.Doc != nil {
			doc = *r.Doc
		}

		key := fmt.Sprintf("%s\x00%d\x00%s", r.Scenario, doc, field)
		c, seen := cells[key]
		if !seen {
			dec, hides := decides(doc, field, deciding[r.Scenario])
			c = cell{
				scenario: r.Scenario, kind: r.Kind, field: field, valid: r.Valid,
				deciding: dec, hides: hides,
				seedFault: map[int64]bool{},
			}
			order = append(order, key)
		}
		c.total++
		correct := r.Answer != nil && *r.Answer == r.FaultClass
		if correct {
			c.matchFault++
		}
		c.seedFault[r.Seed] = correct
		if r.Answer != nil && *r.Answer == noFault {
			c.matchNoFault++
		}
		cells[key] = c
	}

	if len(digests) > 1 {
		log.Printf("warning: shards mix %d model digests — saliency across them is not comparable", len(digests))
	}

	// Pair each faulty scenario with its healthy twin (dataset is the source of
	// truth): twins are baseline-only NoFault bundles measuring the per-scenario
	// false-positive rate, so they are kept out of the saliency and control
	// populations and rendered as the discrimination table instead. Paired here,
	// before the gate, because the twin co-decides it.
	twinName := map[string]string{}
	isTwin := map[string]bool{}
	for _, s := range dataset.All() {
		if s.TwinOf != "" {
			twinName[s.TwinOf] = s.Name
			isTwin[s.Name] = true
		}
	}

	// #12 gate, both directions of competence: a faulty scenario is scored only
	// when the model (a) diagnoses the full manifest — baseline accuracy at or
	// above the threshold — AND (b) actually discriminates fault from health —
	// the 95% Newcombe CI of Youden's J against its twin excludes 0. Baseline
	// alone passes pure bias (lesson 1: a model that always answers this fault
	// on this bundle shape scores 1.00 yet flags the healthy twin every time),
	// and saliency measured on bias is "what shakes the reflex", not signal.
	// Scenarios without twin shards fall back to the baseline rule alone.
	// Healthy controls are exempt (judged by FP).
	gated := map[string]string{} // scenario → why it is excluded ("" = scored)
	for s, fc := range faultClass {
		if fc == dataset.FaultNoFault {
			continue
		}
		var reasons []string
		if frac(baseCorrect[s], baseTotal[s]) < *gate {
			reasons = append(reasons, "baseline")
		}
		if t := twinName[s]; baseTotal[t] > 0 {
			twinFP := baseTotal[t] - baseCorrect[t]
			if _, jlo, _ := newcombe(baseCorrect[s], baseTotal[s], twinFP, baseTotal[t]); jlo <= 0 {
				reasons = append(reasons, "twin")
			}
		}
		if len(reasons) > 0 {
			gated[s] = strings.Join(reasons, "+")
		}
	}

	// Negative-control fragility floor, per scored scenario. Server bookkeeping
	// fields (creation/condition timestamps, uid, resourceVersion, generation)
	// cannot encode a reference or selector fault by construction, so any
	// saliency they show is removal-induced destabilization (lesson 8), not
	// diagnostic signal. The healthy-bundle FP table cannot floor this — with no
	// marginal diagnosis to knock off, healthy fragility sits at ~0 — so each
	// scenario's floor is the max saliency over its own control cells, and a
	// cell counts as signal only if it clears BH AND exceeds that floor.
	floor := map[string]float64{}
	for _, key := range order {
		c := cells[key]
		if c.deciding || faultClass[c.scenario] == dataset.FaultNoFault || gated[c.scenario] != "" || !negControl(c.field) {
			continue
		}
		if sal := frac(baseCorrect[c.scenario], baseTotal[c.scenario]) - frac(c.matchFault, c.total); sal > floor[c.scenario] {
			floor[c.scenario] = sal
		}
	}

	// Paired significance for the saliency population: baseline and every
	// ablation share seeds 0..k-1, so each cell gets an exact McNemar p-value
	// from the discordant seeds, then Benjamini–Hochberg across the whole map.
	// "Signal" = q ≤ fdr; the Newcombe CI stays as the effect-size display.
	type sig struct {
		b, c int // discordant seeds: baseline-only / reduced-only correct
		p, q float64
	}
	sigs := map[string]*sig{}
	var sigKeys []string
	var ps []float64
	for _, key := range order {
		c := cells[key]
		if c.deciding || faultClass[c.scenario] == dataset.FaultNoFault || gated[c.scenario] != "" {
			continue
		}
		b, d := discordant(baseSeed[c.scenario], c.seedFault)
		sigs[key] = &sig{b: b, c: d, p: mcnemar(b, d)}
		sigKeys = append(sigKeys, key)
		ps = append(ps, sigs[key].p)
	}
	for i, q := range bhAdjust(ps) {
		sigs[sigKeys[i]].q = q
	}
	// verdict is the per-cell decision: "control" (negative-control field,
	// ineligible as signal by construction), "signal" (BH q ≤ fdr AND saliency
	// above the scenario's fragility floor AND the floor itself is low),
	// "fragile" (would be signal, but the scenario's floor exceeds -fragile:
	// when removing a TIMESTAMP knocks off 30%+ of diagnoses, no cell in that
	// scenario can be attributed to information rather than destabilization —
	// the K=40 run showed floors up to 0.90 on the RBAC scenarios), "destab"
	// (clears BH but not the floor), or "no". Only "signal" is bolded.
	verdict := func(key string) string {
		c := cells[key]
		if negControl(c.field) {
			return "control"
		}
		s := sigs[key]
		if s == nil || s.q > fdr {
			return "no"
		}
		if frac(baseCorrect[c.scenario], baseTotal[c.scenario])-frac(c.matchFault, c.total) <= floor[c.scenario] {
			return "destab"
		}
		if floor[c.scenario] > *fragileMax {
			return "fragile"
		}
		return "signal"
	}
	signal := func(key string) bool { return verdict(key) == "signal" }

	meta := recs[0]
	var b strings.Builder
	b.WriteString("% Auto-generated by cmd/render from data/*.jsonl — do not edit.\n")
	b.WriteString("% \\input-able fragment; requires \\usepackage{booktabs}.\n")
	fmt.Fprintf(&b, "%% model: %s @ %s ; k=%d ; temp=%.2f ; num_ctx=%d\n", meta.Model, meta.ModelDigest, meta.K, meta.Temp, meta.NumCtx)

	// Population 1: the saliency map — non-deciding fields only.
	b.WriteString("% Table 1 — saliency map (non-deciding fields). Saliency = baseline accuracy\n")
	b.WriteString("% − reduced accuracy, each a fraction over the cell's trials. Bold = signal:\n")
	fmt.Fprintf(&b, "%% seed-paired exact McNemar, Benjamini–Hochberg q <= %.2f across the %d\n", fdr, len(sigKeys))
	b.WriteString("% cells of the map, AND saliency above the scenario's negative-control\n")
	fmt.Fprintf(&b, "%% fragility floor, AND that floor <= %.2f (see fdr.gen.tex); see\n", *fragileMax)
	b.WriteString("% confidence.gen.tex for CIs, p, q and per-cell verdicts. Valid is a\n")
	b.WriteString("% covariate, not a gate.\n")
	b.WriteString("\\begin{tabular}{lllcr}\n\\toprule\nScenario & Kind & Field & Valid & Saliency \\\\\n\\midrule\n")
	for _, key := range order {
		c := cells[key]
		// A healthy (NoFault) scenario has no fault to lose, so its "saliency" is
		// just removal-induced hallucination, not signal — keep it out of the map.
		// Gated scenarios are excluded too: no diagnostic baseline, no saliency.
		if c.deciding || faultClass[c.scenario] == dataset.FaultNoFault || gated[c.scenario] != "" {
			continue
		}
		saliency, _, _ := newcombe(baseCorrect[c.scenario], baseTotal[c.scenario], c.matchFault, c.total)
		field := "\\texttt{" + escapeTeX(c.field) + "}"
		if signal(key) {
			field = "\\textbf{" + field + "}"
		}
		fmt.Fprintf(&b, "%s & %s & %s & %s & %.2f \\\\\n", escapeTeX(c.scenario), escapeTeX(c.kind), field, validMark(c.valid), saliency)
	}
	b.WriteString("\\bottomrule\n\\end{tabular}\n\n")

	// Population 2, split by locus role (see dataset.DecidingField.Hides): only
	// the fault-deleting loci carry an unambiguous expected answer.
	b.WriteString("% Table 2a — control: fault-deleting loci. Removing these deletes the fault\n")
	b.WriteString("% itself (the dangling reference site, or one side of a same-document\n")
	b.WriteString("% comparison), so expected flips to NoFaultFound; Recognized = fraction that\n")
	b.WriteString("% returned it (the rest hallucinated the now-absent fault). Not saliency —\n")
	b.WriteString("% these are not in the map.\n")
	b.WriteString("\\begin{tabular}{lllcr}\n\\toprule\nScenario & Kind & Field & Valid & Recognized \\\\\n\\midrule\n")
	for _, key := range order {
		c := cells[key]
		if !c.deciding || c.hides || gated[c.scenario] != "" {
			continue
		}
		recognized := frac(c.matchNoFault, c.total)
		fmt.Fprintf(&b, "%s & %s & \\texttt{%s} & %s & %.2f \\\\\n", escapeTeX(c.scenario), escapeTeX(c.kind), escapeTeX(c.field), validMark(c.valid), recognized)
	}
	b.WriteString("\\bottomrule\n\\end{tabular}\n\n")

	b.WriteString("% Table 2b — control: evidence-hiding loci. Removing these (the target\n")
	b.WriteString("% object's name, the far side of a cross-document comparison) only hides the\n")
	b.WriteString("% fault's counter-evidence — the reference still dangles. There is no single\n")
	b.WriteString("% correct answer: NoFaultFound is right under the prompt's charitable\n")
	b.WriteString("% absent-field convention, the original fault under a strict reading. Both\n")
	b.WriteString("% rates are reported; do not pool this population with Table 2a.\n")
	b.WriteString("\\begin{tabular}{lllcrr}\n\\toprule\nScenario & Kind & Field & Valid & NoFault rate & Fault rate \\\\\n\\midrule\n")
	for _, key := range order {
		c := cells[key]
		if !c.deciding || !c.hides || gated[c.scenario] != "" {
			continue
		}
		fmt.Fprintf(&b, "%s & %s & \\texttt{%s} & %s & %.2f & %.2f \\\\\n",
			escapeTeX(c.scenario), escapeTeX(c.kind), escapeTeX(c.field), validMark(c.valid),
			frac(c.matchNoFault, c.total), frac(c.matchFault, c.total))
	}
	b.WriteString("\\bottomrule\n\\end{tabular}\n")

	// Population 3: controls — standalone healthy (NoFault) scenarios; report
	// their baseline rate of correctly saying NoFaultFound (1 − FP rate). Twins
	// are NoFault too but belong to the paired discrimination table (Table 5),
	// not here.
	var controls []string
	for s, fc := range faultClass {
		if fc == dataset.FaultNoFault && !isTwin[s] {
			controls = append(controls, s)
		}
	}
	sort.Strings(controls)
	if len(controls) > 0 {
		b.WriteString("\n% Table 3 — controls: healthy scenarios, baseline NoFaultFound rate (1 − FP rate).\n")
		b.WriteString("\\begin{tabular}{lr}\n\\toprule\nScenario & NoFaultFound rate \\\\\n\\midrule\n")
		for _, s := range controls {
			fmt.Fprintf(&b, "%s & %.2f \\\\\n", escapeTeX(s), frac(baseCorrect[s], baseTotal[s]))
		}
		b.WriteString("\\bottomrule\n\\end{tabular}\n")
	}

	// Table 3b — removal-induced hallucination on healthy bundles: per ablated
	// field, the fraction of trials that claimed a fault once the field was
	// gone. This is the model's per-field fragility floor — a faulty-scenario
	// saliency within this floor is destabilization, not diagnostic signal
	// (lesson 8). Unparseable answers count as false positives.
	wrote3b := false
	for _, key := range order {
		c := cells[key]
		if faultClass[c.scenario] != dataset.FaultNoFault {
			continue
		}
		if !wrote3b {
			b.WriteString("\n% Table 3b — healthy bundles: per-field removal-induced hallucination rate\n")
			b.WriteString("% (fraction of reduced trials NOT answering NoFaultFound). The fragility\n")
			b.WriteString("% floor for interpreting borderline saliency cells (lesson 8).\n")
			b.WriteString("\\begin{tabular}{lllcr}\n\\toprule\nScenario & Kind & Field & Valid & FP rate \\\\\n\\midrule\n")
			wrote3b = true
		}
		fmt.Fprintf(&b, "%s & %s & \\texttt{%s} & %s & %.2f \\\\\n",
			escapeTeX(c.scenario), escapeTeX(c.kind), escapeTeX(c.field), validMark(c.valid),
			frac(c.total-c.matchNoFault, c.total))
	}
	if wrote3b {
		b.WriteString("\\bottomrule\n\\end{tabular}\n")
	}

	// Excluded by the #12 gate: faulty scenarios the model diagnoses too rarely
	// (baseline) or without discriminating fault from health (twin). Reported
	// for honesty — and as the motivation for the multi-model comparison.
	var below []string
	for s := range gated {
		below = append(below, s)
	}
	sort.Strings(below)
	if len(below) > 0 {
		fmt.Fprintf(&b, "\n%% Table 4 — excluded by the gate. baseline: accuracy on the full manifest\n%% below %.2f. twin: the 95%% Newcombe CI of Youden's J does not exclude 0 —\n", *gate)
		b.WriteString("% the model answers the same whether or not the fault is present (lesson 1\n")
		b.WriteString("% bias), so its saliency would measure what shakes the reflex, not signal.\n")
		b.WriteString("\\begin{tabular}{llrcl}\n\\toprule\nScenario & Fault & Baseline acc & J & Excluded by \\\\\n\\midrule\n")
		for _, s := range below {
			j := "--"
			if t := twinName[s]; baseTotal[t] > 0 {
				twinFP := baseTotal[t] - baseCorrect[t]
				jv, jlo, jhi := newcombe(baseCorrect[s], baseTotal[s], twinFP, baseTotal[t])
				j = fmt.Sprintf("%.2f [%.2f, %.2f]", jv, jlo, jhi)
			}
			fmt.Fprintf(&b, "%s & %s & %.2f & %s & %s \\\\\n",
				escapeTeX(s), escapeTeX(faultClass[s]), frac(baseCorrect[s], baseTotal[s]), j, escapeTeX(gated[s]))
		}
		b.WriteString("\\bottomrule\n\\end{tabular}\n")
	}

	// Table 6 — was the right class right for the right reason? Among baseline
	// trials whose fault_class was correct, the fraction whose offending_field
	// pointed at a deciding locus (lenient suffix match, see locates). A high
	// baseline with a low rate here means the class was guessed off the bundle
	// shape, not diagnosed from the fault. Gated scenarios included: their few
	// correct trials are exactly the ones worth auditing.
	var locNames []string
	for s := range locTotal {
		locNames = append(locNames, s)
	}
	sort.Strings(locNames)
	if len(locNames) > 0 {
		b.WriteString("\n% Table 6 — localization: baseline trials with the correct fault_class,\n")
		b.WriteString("% fraction whose offending_field pointed at a deciding locus (suffix match\n")
		b.WriteString("% on the last two path segments; document identity is not attributable in a\n")
		b.WriteString("% free-form path). This measures the model's ability to REPORT the fault's\n")
		b.WriteString("% address, not whether detection is anchored on it — Table 2a shows whether\n")
		b.WriteString("% the diagnosis collapses once the locus is removed. Low rate here + high\n")
		b.WriteString("% Recognized there = the model detects the anomaly but misreports the path\n")
		b.WriteString("% (e.g. blames the healthy sibling reference). Top blamed = the most\n")
		b.WriteString("% frequent offending_field among those trials, with its share.\n")
		b.WriteString("\\begin{tabular}{lrrrl}\n\\toprule\nScenario & Correct baselines & Localized & Rate & Top blamed \\\\\n\\midrule\n")
		for _, s := range locNames {
			top, n := "", 0
			for path, count := range blame[s] {
				if count > n || (count == n && path < top) {
					top, n = path, count
				}
			}
			fmt.Fprintf(&b, "%s & %d & %d & %.2f & \\texttt{%s} (%.2f) \\\\\n",
				escapeTeX(s), locTotal[s], locHit[s], frac(locHit[s], locTotal[s]),
				escapeTeX(top), frac(n, locTotal[s]))
		}
		b.WriteString("\\bottomrule\n\\end{tabular}\n")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	path := filepath.Join(outDir, "heatmap.gen.tex")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		log.Fatalf("write %s: %v", path, err)
	}
	fmt.Printf("wrote %s (%d cells from %d trials)\n", path, len(order), len(recs))

	// Second artifact: the full (unreduced) baseline manifests per scenario, so the
	// paper can show what the agent sees before reduction. The YAML comes from the
	// dataset, scoped to the scenarios actually present in the shards.
	byName := map[string]dataset.Scenario{}
	for _, s := range dataset.All() {
		byName[s.Name] = s
	}
	names := make([]string, 0, len(faultClass))
	for s := range faultClass {
		names = append(names, s)
	}
	sort.Strings(names)

	var bl strings.Builder
	bl.WriteString("% Auto-generated by cmd/render — full baseline manifests per scenario.\n")
	bl.WriteString("% \\input-able; each block is plain verbatim (no package needed).\n")
	written := 0
	for _, n := range names {
		s, ok := byName[n]
		if !ok {
			continue
		}
		fmt.Fprintf(&bl, "%% scenario: %s — fault: %s\n", n, s.FaultClass)
		bl.WriteString("\\begin{verbatim}\n")
		bl.WriteString(strings.TrimRight(s.YAML, "\n"))
		bl.WriteString("\n")
		bl.WriteString("\\end{verbatim}\n\n")
		written++
	}

	blPath := filepath.Join(outDir, "baseline.gen.tex")
	if err := os.WriteFile(blPath, []byte(bl.String()), 0o644); err != nil {
		log.Fatalf("write %s: %v", blPath, err)
	}
	fmt.Printf("wrote %s (%d baselines)\n", blPath, written)

	// Third artifact: the confidence-interval view. Point fractions over k trials
	// are noisy; this attaches a 95% interval to each cell — Newcombe for the
	// saliency difference, Wilson for the single-proportion control rates — so a
	// reader can separate signal from sampling noise at our small k.
	var ci strings.Builder
	ci.WriteString("% Auto-generated by cmd/render — 95% confidence intervals.\n")
	ci.WriteString("% Saliency shows its Newcombe interval (the effect size); the Verdict column\n")
	ci.WriteString("% is the McNemar+BH+fragility-floor decision — evidence in fdr.gen.tex.\n")
	ci.WriteString("% Control rates use Wilson (single proportion).\n")
	ci.WriteString("% \\input-able fragment; requires \\usepackage{booktabs}.\n")
	fmt.Fprintf(&ci, "%% model: %s @ %s ; k=%d ; temp=%.2f ; num_ctx=%d\n", meta.Model, meta.ModelDigest, meta.K, meta.Temp, meta.NumCtx)

	fmt.Fprintf(&ci, "%% Table A — saliency with 95%% CI (non-deciding fields). Verdict: signal =\n%% BH q <= %.2f AND saliency above the scenario's negative-control fragility\n%% floor AND floor <= %.2f; fragile = the scenario's floor exceeds that bound\n%% (destabilization-dominated); destab = clears BH only; control = a-priori\n%% bookkeeping field (ineligible as signal, defines the floor). Full evidence\n%% in fdr.gen.tex.\n", fdr, *fragileMax)
	ci.WriteString("\\begin{tabular}{lllrcc}\n\\toprule\nScenario & Kind & Field & Saliency & 95\\% CI & Verdict \\\\\n\\midrule\n")
	for _, key := range order {
		c := cells[key]
		if c.deciding || faultClass[c.scenario] == dataset.FaultNoFault || gated[c.scenario] != "" {
			continue
		}
		sal, low, high := newcombe(baseCorrect[c.scenario], baseTotal[c.scenario], c.matchFault, c.total)
		field, mark := "\\texttt{"+escapeTeX(c.field)+"}", verdict(key)
		if mark == "signal" {
			field = "\\textbf{" + field + "}"
		}
		fmt.Fprintf(&ci, "%s & %s & %s & %.2f & [%.2f, %.2f] & %s \\\\\n",
			escapeTeX(c.scenario), escapeTeX(c.kind), field, sal, low, high, mark)
	}
	ci.WriteString("\\bottomrule\n\\end{tabular}\n\n")

	ci.WriteString("% Table B1 — control: fault-deleting loci, Recognized (fraction returning\n% NoFaultFound) with 95% Wilson CI.\n")
	ci.WriteString("\\begin{tabular}{lllrc}\n\\toprule\nScenario & Kind & Field & Recognized & 95\\% CI \\\\\n\\midrule\n")
	for _, key := range order {
		c := cells[key]
		if !c.deciding || c.hides || gated[c.scenario] != "" {
			continue
		}
		lo, hi := wilson(c.matchNoFault, c.total)
		fmt.Fprintf(&ci, "%s & %s & \\texttt{%s} & %.2f & [%.2f, %.2f] \\\\\n",
			escapeTeX(c.scenario), escapeTeX(c.kind), escapeTeX(c.field), frac(c.matchNoFault, c.total), lo, hi)
	}
	ci.WriteString("\\bottomrule\n\\end{tabular}\n\n")

	ci.WriteString("% Table B2 — control: evidence-hiding loci; no single correct answer (see\n% heatmap.gen.tex Table 2b), so both readings carry 95% Wilson CIs.\n")
	ci.WriteString("\\begin{tabular}{lllrcrc}\n\\toprule\nScenario & Kind & Field & NoFault rate & 95\\% CI & Fault rate & 95\\% CI \\\\\n\\midrule\n")
	for _, key := range order {
		c := cells[key]
		if !c.deciding || !c.hides || gated[c.scenario] != "" {
			continue
		}
		nlo, nhi := wilson(c.matchNoFault, c.total)
		flo, fhi := wilson(c.matchFault, c.total)
		fmt.Fprintf(&ci, "%s & %s & \\texttt{%s} & %.2f & [%.2f, %.2f] & %.2f & [%.2f, %.2f] \\\\\n",
			escapeTeX(c.scenario), escapeTeX(c.kind), escapeTeX(c.field),
			frac(c.matchNoFault, c.total), nlo, nhi, frac(c.matchFault, c.total), flo, fhi)
	}
	ci.WriteString("\\bottomrule\n\\end{tabular}\n")

	if len(controls) > 0 {
		ci.WriteString("\n% Table C — controls: healthy scenarios, NoFaultFound rate with 95% Wilson CI.\n")
		ci.WriteString("\\begin{tabular}{lrc}\n\\toprule\nScenario & NoFaultFound rate & 95\\% CI \\\\\n\\midrule\n")
		for _, s := range controls {
			lo, hi := wilson(baseCorrect[s], baseTotal[s])
			fmt.Fprintf(&ci, "%s & %.2f & [%.2f, %.2f] \\\\\n", escapeTeX(s), frac(baseCorrect[s], baseTotal[s]), lo, hi)
		}
		ci.WriteString("\\bottomrule\n\\end{tabular}\n")
	}

	wroteC2 := false
	for _, key := range order {
		c := cells[key]
		if faultClass[c.scenario] != dataset.FaultNoFault {
			continue
		}
		if !wroteC2 {
			ci.WriteString("\n% Table C2 — healthy bundles: per-field removal-induced hallucination rate\n% with 95% Wilson CI (see heatmap.gen.tex Table 3b).\n")
			ci.WriteString("\\begin{tabular}{lllrc}\n\\toprule\nScenario & Kind & Field & FP rate & 95\\% CI \\\\\n\\midrule\n")
			wroteC2 = true
		}
		fp := c.total - c.matchNoFault
		lo, hi := wilson(fp, c.total)
		fmt.Fprintf(&ci, "%s & %s & \\texttt{%s} & %.2f & [%.2f, %.2f] \\\\\n",
			escapeTeX(c.scenario), escapeTeX(c.kind), escapeTeX(c.field), frac(fp, c.total), lo, hi)
	}
	if wroteC2 {
		ci.WriteString("\\bottomrule\n\\end{tabular}\n")
	}

	ciPath := filepath.Join(outDir, "confidence.gen.tex")
	if err := os.WriteFile(ciPath, []byte(ci.String()), 0o644); err != nil {
		log.Fatalf("write %s: %v", ciPath, err)
	}
	fmt.Printf("wrote %s\n", ciPath)

	// Fourth artifact: the FDR decision table — every saliency cell ranked by
	// evidence strength, with the raw discordant counts behind each p-value and
	// the fragility floor behind each verdict, so both decisions (multiple
	// comparison AND destabilization) are fully auditable from one fragment.
	nSignal := 0
	for _, key := range sigKeys {
		if verdict(key) == "signal" {
			nSignal++
		}
	}
	ranked := append([]string(nil), sigKeys...)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, z := sigs[ranked[i]], sigs[ranked[j]]
		if a.q != z.q {
			return a.q < z.q
		}
		return a.p < z.p
	})

	var fd strings.Builder
	fd.WriteString("% Auto-generated by cmd/render — the decision table behind every bold cell.\n")
	fd.WriteString("% Seed-paired exact McNemar per saliency cell; b/c = discordant seeds\n")
	fd.WriteString("% (baseline-only correct / reduced-only correct); q = Benjamini–Hochberg\n")
	fd.WriteString("% adjusted p across all cells below. Floor = the scenario's fragility floor:\n")
	fd.WriteString("% the max saliency over its negative-control cells (server bookkeeping\n")
	fd.WriteString("% fields, verdict \"control\" — by construction they carry no diagnostic\n")
	fd.WriteString("% signal, so what they show is removal-induced destabilization, lesson 8).\n")
	fd.WriteString("% Verdict: signal = q <= FDR and saliency > floor AND the floor itself is\n")
	fmt.Fprintf(&fd, "%% low (<= %.2f); fragile = would be signal but the scenario's floor exceeds\n", *fragileMax)
	fd.WriteString("% that bound — the scenario is destabilization-dominated and none of its\n")
	fd.WriteString("% cells is attributable to information; destab = clears BH but not the\n")
	fd.WriteString("% floor. Rows sorted by evidence strength.\n")
	fmt.Fprintf(&fd, "%% m = %d cells tested; FDR level %.2f; signal cells: %d.\n", len(sigKeys), fdr, nSignal)
	fd.WriteString("% \\input-able fragment; requires \\usepackage{booktabs}.\n")
	fmt.Fprintf(&fd, "%% model: %s @ %s ; k=%d ; temp=%.2f ; num_ctx=%d\n", meta.Model, meta.ModelDigest, meta.K, meta.Temp, meta.NumCtx)
	fd.WriteString("\\begin{tabular}{lllrrrrrrc}\n\\toprule\nScenario & Kind & Field & Saliency & b & c & p & q & Floor & Verdict \\\\\n\\midrule\n")
	for _, key := range ranked {
		c := cells[key]
		s := sigs[key]
		sal, _, _ := newcombe(baseCorrect[c.scenario], baseTotal[c.scenario], c.matchFault, c.total)
		field, mark := "\\texttt{"+escapeTeX(c.field)+"}", verdict(key)
		if mark == "signal" {
			field = "\\textbf{" + field + "}"
		}
		fmt.Fprintf(&fd, "%s & %s & %s & %.2f & %d & %d & %.3f & %.3f & %.2f & %s \\\\\n",
			escapeTeX(c.scenario), escapeTeX(c.kind), field, sal, s.b, s.c, s.p, s.q, floor[c.scenario], mark)
	}
	fd.WriteString("\\bottomrule\n\\end{tabular}\n")

	fdrPath := filepath.Join(outDir, "fdr.gen.tex")
	if err := os.WriteFile(fdrPath, []byte(fd.String()), 0o644); err != nil {
		log.Fatalf("write %s: %v", fdrPath, err)
	}
	fmt.Printf("wrote %s (%d cells, %d signal)\n", fdrPath, len(sigKeys), nSignal)

	// Fifth artifact: the twin discrimination table. Each faulty scenario is
	// paired with its healthy twin (same bundle, anomaly fixed, expected
	// NoFaultFound). Baseline accuracy alone cannot separate diagnosis from
	// bias (lesson 1): a model that always suspects this fault on this bundle
	// shape scores a perfect baseline AND flags the healthy twin. Youden's
	// J = sensitivity + specificity − 1 = baseline accuracy − twin FP rate
	// makes that one number: 1 = perfect discrimination, 0 = pure bias. J is a
	// difference of two independent proportions, so it gets a Newcombe 95% CI;
	// the scenario discriminates only when that CI excludes 0 (bold).
	var pairs []string
	for s, fc := range faultClass {
		if fc != dataset.FaultNoFault && baseTotal[twinName[s]] > 0 {
			pairs = append(pairs, s)
		}
	}
	sort.Strings(pairs)

	var tw strings.Builder
	tw.WriteString("% Auto-generated by cmd/render — twin discrimination.\n")
	tw.WriteString("% Baseline acc = fault recognized on the faulty bundle (sensitivity).\n")
	tw.WriteString("% Twin NoFault = NoFaultFound on the same bundle with the anomaly fixed\n")
	tw.WriteString("% (specificity); its complement is the per-scenario false-positive rate.\n")
	tw.WriteString("% J = Youden's index (sensitivity + specificity − 1): 1 = perfect\n")
	tw.WriteString("% discrimination, 0 = pure bias (the model answers the same regardless of\n")
	tw.WriteString("% whether the fault is present). Bold = the 95% Newcombe CI of J excludes 0.\n")
	tw.WriteString("% Rates carry 95% Wilson CIs. \\input-able; requires \\usepackage{booktabs}.\n")
	fmt.Fprintf(&tw, "%% model: %s @ %s ; k=%d ; temp=%.2f ; num_ctx=%d\n", meta.Model, meta.ModelDigest, meta.K, meta.Temp, meta.NumCtx)
	if len(pairs) == 0 {
		tw.WriteString("% no twin shards in the data yet — produce them with make run-<group>.\n")
	}
	tw.WriteString("\\begin{tabular}{llrcrcrc}\n\\toprule\n")
	tw.WriteString("Scenario & Fault & Baseline acc & 95\\% CI & Twin NoFault & 95\\% CI & J & 95\\% CI \\\\\n\\midrule\n")
	for _, s := range pairs {
		t := twinName[s]
		twinFP := baseTotal[t] - baseCorrect[t] // trials on the healthy twin that still claimed a fault
		blo, bhi := wilson(baseCorrect[s], baseTotal[s])
		tlo, thi := wilson(baseCorrect[t], baseTotal[t])
		j, jlo, jhi := newcombe(baseCorrect[s], baseTotal[s], twinFP, baseTotal[t])
		name := escapeTeX(s)
		if jlo > 0 {
			name = "\\textbf{" + name + "}"
		}
		fmt.Fprintf(&tw, "%s & %s & %.2f & [%.2f, %.2f] & %.2f & [%.2f, %.2f] & %.2f & [%.2f, %.2f] \\\\\n",
			name, escapeTeX(faultClass[s]),
			frac(baseCorrect[s], baseTotal[s]), blo, bhi,
			frac(baseCorrect[t], baseTotal[t]), tlo, thi,
			j, jlo, jhi)
	}
	tw.WriteString("\\bottomrule\n\\end{tabular}\n")

	twPath := filepath.Join(outDir, "twins.gen.tex")
	if err := os.WriteFile(twPath, []byte(tw.String()), 0o644); err != nil {
		log.Fatalf("write %s: %v", twPath, err)
	}
	fmt.Printf("wrote %s (%d twin pairs)\n", twPath, len(pairs))

	// Sixth artifact family: the three paper figures (see figures.go) — the
	// cross-scenario field profile, the TikZ contrast map, and the flagship
	// manifest painted by verdict. Same populations, same decision rule.
	fc := &figCtx{
		cells: cells, order: order, faultClass: faultClass, gated: gated,
		baseCorrect: baseCorrect, baseTotal: baseTotal, floor: floor, verdict: verdict,
	}
	fc.writeFieldProfile(outDir)
	fc.writeHeatmapFig(outDir)
	fc.writeManifestFig(outDir)
}

// decides reports whether the field at (doc, pointer) is an ancestor-or-equal
// of a concrete deciding locus in the same document, and whether that removal
// merely hides the fault's counter-evidence rather than deleting the fault.
// hides is true only when EVERY covered locus is evidence-hiding: if the field
// also spans a fault-deleting locus, the removal genuinely deletes the fault.
func decides(doc int, field string, loci []locus) (deciding, hides bool) {
	hides = true
	for _, l := range loci {
		if l.Doc == doc && (field == l.Pointer || strings.HasPrefix(l.Pointer, field+"/")) {
			deciding = true
			if !l.hides {
				hides = false
			}
		}
	}
	if !deciding {
		hides = false
	}

	return deciding, hides
}

func readShards(dir string) []heatmap.Record {
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		log.Fatal(err)
	}
	sort.Strings(paths)

	var recs []heatmap.Record
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			log.Fatal(err)
		}

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // raw responses can be long
		for sc.Scan() {
			if len(sc.Bytes()) == 0 {
				continue
			}
			var r heatmap.Record
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				log.Fatalf("%s: %v", p, err)
			}
			recs = append(recs, r)
		}
		if err := sc.Err(); err != nil {
			log.Fatal(err)
		}
		f.Close()
	}
	return recs
}

// modelDir renders a model name as a directory component (":" and "/" are not
// filesystem-safe): shards and artifacts for different models live side by
// side, so a 32b run never overwrites the 7b data.
func modelDir(model string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(model)
}

// negControl reports whether a field-key is an a-priori negative control:
// server-assigned bookkeeping — creation/condition timestamps, uid,
// resourceVersion, generation. By construction these cannot encode a reference,
// selector or port fault (the server stamps them regardless of spec content),
// so they are ineligible as "signal" no matter what the statistics say; the
// saliency they DO show estimates the scenario's removal-induced
// destabilization (lesson 8) and defines its fragility floor. This is the
// blank-sample logic of a lab assay, not field cherry-picking: the exclusion
// is justified by what the field is (server bookkeeping), never by how it
// scored.
func negControl(field string) bool {
	for _, s := range []string{
		"/creationTimestamp", "/metadata/generation", "/metadata/resourceVersion",
		"/metadata/uid", "/lastTransitionTime", "/lastUpdateTime", "/lastProbeTime",
	} {
		if strings.HasSuffix(field, s) {
			return true
		}
	}

	return false
}

// normPath splits a YAML path into lowercase segments, dropping array indices
// and wildcards, so the model's free-form offending_field
// ("spec.template.spec.containers[0].envFrom[1].secretRef.name") and the
// catalog's dotted deciding paths land in one comparable form.
func normPath(p string) []string {
	var segs []string
	for _, s := range strings.FieldsFunc(strings.ToLower(p), func(r rune) bool {
		return r == '.' || r == '/' || r == '[' || r == ']' || r == '"' || r == '\'' || r == ' '
	}) {
		if s == "" || s == "*" {
			continue
		}
		if _, err := strconv.Atoi(s); err == nil {
			continue
		}
		segs = append(segs, s)
	}

	return segs
}

// locates reports whether the model's offending path points at any deciding
// path. The match is a lenient suffix: the model path must end with the
// deciding path's last two segments (or one, for single-segment paths) — so a
// bare "secretRef.name" credits the full spec.template...secretRef.name locus
// without demanding the whole prefix, while "metadata.name" never matches a
// secretRef locus. Kind is not encoded in free-form paths, so a metadata.name
// answer cannot be attributed to a specific document — a stated limitation.
func locates(model []string, decidings [][]string) bool {
	for _, d := range decidings {
		n := 2
		if len(d) < n {
			n = len(d)
		}
		if n == 0 || len(model) < n {
			continue
		}
		match := true
		for i := 1; i <= n; i++ {
			if model[len(model)-i] != d[len(d)-i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}

	return false
}

func frac(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// zCI is the standard-normal quantile for a 95% two-sided interval.
const zCI = 1.96

// wilson returns the 95% Wilson score interval [lo, hi] for x successes in n
// trials. Unlike the naive normal interval it never collapses to width 0 at
// x==0 or x==n and stays within [0,1] — the right tool for our small k and the
// many extreme (0/10, 10/10) rates.
func wilson(x, n int) (lo, hi float64) {
	if n == 0 {
		return 0, 0
	}
	p, nn := frac(x, n), float64(n)
	d := 1 + zCI*zCI/nn
	center := (p + zCI*zCI/(2*nn)) / d
	half := (zCI / d) * math.Sqrt(p*(1-p)/nn+zCI*zCI/(4*nn*nn))
	return math.Max(0, center-half), math.Min(1, center+half)
}

// discordant counts the seed-paired disagreements between the baseline and one
// reduced cell: b = seeds where only the baseline diagnosed the fault, c =
// seeds where only the reduced variant did. Concordant seeds carry no
// information about the difference and are ignored (that is McNemar's point).
func discordant(base, reduced map[int64]bool) (b, c int) {
	for seed, red := range reduced {
		bl, ok := base[seed]
		if !ok {
			continue
		}

		if bl && !red {
			b++
		}

		if !bl && red {
			c++
		}
	}

	return b, c
}

// mcnemar returns the two-sided exact McNemar p-value for a paired difference
// of proportions with b and c discordant pairs. Pairing is by sampling seed —
// the baseline and every ablation run the same seeds 0..k-1. Under H0 (the
// removal changes nothing) a discordant seed is baseline-only or reduced-only
// with equal probability even if the shared seed does not actually couple the
// two runs, so the test stays valid regardless; any real coupling only adds
// power. This is what lets small effects reach significance at a k where the
// unpaired Newcombe interval still straddles zero.
func mcnemar(b, c int) float64 {
	n := b + c
	if n == 0 {
		return 1
	}
	m := b
	m = min(m, c)

	// two-sided exact: 2·P(Bin(n,1/2) ≤ min(b,c)), capped at 1.
	var p float64
	for i := 0; i <= m; i++ {
		p += binomHalf(n, i)
	}
	p *= 2
	if p > 1 {
		p = 1
	}

	return p
}

// binomHalf is the Bin(n, 1/2) pmf at i, via log-gamma to stay exact-ish for
// any n we will ever see.
func binomHalf(n, i int) float64 {
	ln, _ := math.Lgamma(float64(n + 1))
	li, _ := math.Lgamma(float64(i + 1))
	lni, _ := math.Lgamma(float64(n - i + 1))

	return math.Exp(ln - li - lni - float64(n)*math.Ln2)
}

// bhAdjust returns Benjamini–Hochberg q-values for the given p-values: q_i is
// the smallest false-discovery rate at which p_i would still be declared
// significant. Bounds the expected fraction of false "signal" cells across the
// whole map, which a raw per-cell threshold does not.
func bhAdjust(ps []float64) []float64 {
	m := len(ps)
	idx := make([]int, m)
	for i := range idx {
		idx[i] = i
	}

	sort.Slice(idx, func(a, b int) bool { return ps[idx[a]] < ps[idx[b]] })

	out := make([]float64, m)
	prev := 1.0
	for r := m - 1; r >= 0; r-- {
		q := ps[idx[r]] * float64(m) / float64(r+1)
		if q > prev {
			q = prev
		}
		prev = q
		out[idx[r]] = q
	}

	return out
}

// newcombe returns the difference p1−p2 and its 95% Newcombe interval, built
// from the two Wilson intervals. We use it for saliency (baseline − reduced)
// as the effect-size display; the bold/signal decision is McNemar+BH above.
func newcombe(x1, n1, x2, n2 int) (diff, lo, hi float64) {
	p1, p2 := frac(x1, n1), frac(x2, n2)
	l1, u1 := wilson(x1, n1)
	l2, u2 := wilson(x2, n2)
	diff = p1 - p2
	lo = diff - math.Sqrt((p1-l1)*(p1-l1)+(u2-p2)*(u2-p2))
	hi = diff + math.Sqrt((u1-p1)*(u1-p1)+(p2-l2)*(p2-l2))
	return diff, lo, hi
}

func validMark(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}

func escapeTeX(s string) string {
	in := []string{
		"\\", "\\textbackslash{}",
		"_", "\\_", "%", "\\%", "&", "\\&", "#", "\\#",
		"$", "\\$", "{", "\\{", "}", "\\}",
		"~", "\\textasciitilde{}", "^", "\\textasciicircum{}",
	}

	return strings.NewReplacer(in...).Replace(s)
}
