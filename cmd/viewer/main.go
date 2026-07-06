// Command viewer is a throwaway local heatmap browser. It reads the JSONL shards
// and serves three views on :8080, all using the SAME decision pipeline as
// cmd/render (dual gate, seed-paired McNemar + BH, negative-control fragility
// floor), so the browser never disagrees with the paper tables:
//
//	/            per-Kind heatmaps: field × scenario, dense by construction —
//	             a Kind's fields only column against the scenarios that contain
//	             that Kind, instead of one sparse global grid.
//	/manifest    the heatmap painted ON the K8s object: the scenario's YAML with
//	             each measured line coloured by its verdict.
//	/confidence  the CI tables (Newcombe / Wilson), verdict per cell.
//
// Colours: signal = strong orange; measured-but-no-signal = grey (darker =
// higher raw saliency, i.e. destabilization); negative-control cells = grey
// with a dashed border (they define the floor); deciding loci = dark slate
// with * (tautological, never scored). It re-reads on every request, so you
// can produce more data and just refresh.
//
//	go run ./cmd/viewer        # then open http://localhost:8080
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mikolajsemeniuk/kubelean/pkg/dataset"
	"github.com/mikolajsemeniuk/kubelean/pkg/heatmap"
	"gopkg.in/yaml.v3"
)

func main() {
	in := flag.String("in", "data", "root directory of JSONL shards (a per-model subdirectory is appended)")
	model := flag.String("model", "qwen2.5:7b-instruct", "model whose shards to browse — selects data/<model>/")
	gate := flag.Float64("gate", 0.8, "min baseline accuracy for a faulty scenario to be scored (mirrors cmd/render)")
	fragileMax := flag.Float64("fragile", 0.3, "max negative-control floor for signal eligibility (mirrors cmd/render)")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	dir := filepath.Join(*in, modelDir(*model))

	serve := func(render func(io.Writer, *view, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			v, err := load(dir, *gate, *fragileMax)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			render(w, v, r)
		}
	}

	http.HandleFunc("/", serve(writeKinds))
	http.HandleFunc("/manifest", serve(writeManifest))
	http.HandleFunc("/confidence", serve(writeConfidence))

	log.Printf("kubelean viewer on http://localhost%s (reading %s)", *addr, dir)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// modelDir renders a model name as a directory component (":" and "/" are not
// filesystem-safe).
func modelDir(model string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(model)
}

// cell aggregates all reduced trials of one (scenario, doc, field), plus the
// per-cell evidence behind its verdict.
type cell struct {
	scenario, kind, field string
	doc                   int
	valid, deciding       bool
	total, matchFault     int
	matchNoFault          int
	seedFault             map[int64]bool
	b, c                  int     // discordant seeds vs the baseline
	q                     float64 // BH-adjusted McNemar p (scored cells only)
	tested                bool    // part of the BH family (scored, non-deciding)
}

// view is everything the three pages need, computed once per request with the
// same rules as cmd/render.
type view struct {
	cells      map[string]*cell // key: scenario\x00doc\x00field
	order      []string
	baseHit    map[string]int
	baseTot    map[string]int
	faultClass map[string]string
	twinName   map[string]string
	isTwin     map[string]bool
	gated      map[string]string // scenario → reason ("" = scored)
	floor      map[string]float64
	fragileMax float64
}

const fdrLevel = 0.05

func (v *view) saliency(c *cell) float64 {
	return frac(v.baseHit[c.scenario], v.baseTot[c.scenario]) - frac(c.matchFault, c.total)
}

// verdict mirrors cmd/render: control / signal / fragile / destab / no.
// Deciding cells and cells of unscored scenarios always come back "no" (they
// are not tested). "fragile" = would be signal, but the scenario's own floor
// exceeds fragileMax, so the whole map is destabilization-dominated there.
func (v *view) verdict(c *cell) string {
	if negControl(c.field) {
		return "control"
	}
	if !c.tested || c.q > fdrLevel {
		return "no"
	}
	if v.saliency(c) <= v.floor[c.scenario] {
		return "destab"
	}
	if v.floor[c.scenario] > v.fragileMax {
		return "fragile"
	}
	return "signal"
}

func load(dir string, gate, fragileMax float64) (*view, error) {
	recs, err := readShards(dir)
	if err != nil {
		return nil, err
	}

	// concrete deciding loci per scenario, normalized like the record fields
	deciding := map[string][]heatmap.Locus{}
	for _, s := range dataset.All() {
		for _, df := range s.DecidingFields {
			ls, _ := heatmap.ResolveLeaves(s.YAML, df.Kind, df.Path)
			for i := range ls {
				ls[i].Pointer = heatmap.NormalizeKey(ls[i].Pointer)
			}
			deciding[s.Name] = append(deciding[s.Name], ls...)
		}
	}

	v := &view{
		cells:      map[string]*cell{},
		baseHit:    map[string]int{},
		baseTot:    map[string]int{},
		faultClass: map[string]string{},
		twinName:   map[string]string{},
		isTwin:     map[string]bool{},
		gated:      map[string]string{},
		floor:      map[string]float64{},
		fragileMax: fragileMax,
	}
	for _, s := range dataset.All() {
		if s.TwinOf != "" {
			v.twinName[s.TwinOf] = s.Name
			v.isTwin[s.Name] = true
		}
	}

	baseSeed := map[string]map[int64]bool{}
	for _, r := range recs {
		v.faultClass[r.Scenario] = r.FaultClass
		correct := r.Answer != nil && *r.Answer == r.FaultClass
		if r.Variant == "baseline" {
			v.baseTot[r.Scenario]++
			if correct {
				v.baseHit[r.Scenario]++
			}
			if baseSeed[r.Scenario] == nil {
				baseSeed[r.Scenario] = map[int64]bool{}
			}
			baseSeed[r.Scenario][r.Seed] = correct
			continue
		}
		field, doc := "", -1
		if r.Field != nil {
			field = *r.Field
		}
		if r.Doc != nil {
			doc = *r.Doc
		}
		key := r.Scenario + "\x00" + fmt.Sprint(doc) + "\x00" + field
		c := v.cells[key]
		if c == nil {
			c = &cell{
				scenario: r.Scenario, kind: r.Kind, field: field, doc: doc,
				valid: r.Valid, deciding: decides(doc, field, deciding[r.Scenario]),
				seedFault: map[int64]bool{},
			}
			v.cells[key] = c
			v.order = append(v.order, key)
		}
		c.total++
		if correct {
			c.matchFault++
		}
		c.seedFault[r.Seed] = correct
		if r.Answer != nil && *r.Answer == dataset.FaultNoFault {
			c.matchNoFault++
		}
	}

	// dual gate, as in cmd/render: baseline threshold AND twin J CI excluding 0
	for s, fc := range v.faultClass {
		if fc == dataset.FaultNoFault {
			continue
		}
		var reasons []string
		if frac(v.baseHit[s], v.baseTot[s]) < gate {
			reasons = append(reasons, "baseline")
		}
		if t := v.twinName[s]; v.baseTot[t] > 0 {
			twinFP := v.baseTot[t] - v.baseHit[t]
			if _, jlo, _ := newcombe(v.baseHit[s], v.baseTot[s], twinFP, v.baseTot[t]); jlo <= 0 {
				reasons = append(reasons, "twin")
			}
		}
		if len(reasons) > 0 {
			v.gated[s] = strings.Join(reasons, "+")
		}
	}

	// negative-control fragility floor per scored scenario
	for _, key := range v.order {
		c := v.cells[key]
		if c.deciding || v.faultClass[c.scenario] == dataset.FaultNoFault || v.gated[c.scenario] != "" || !negControl(c.field) {
			continue
		}
		if sal := v.saliency(c); sal > v.floor[c.scenario] {
			v.floor[c.scenario] = sal
		}
	}

	// seed-paired exact McNemar + BH over the scored non-deciding population
	var tested []string
	var ps []float64
	for _, key := range v.order {
		c := v.cells[key]
		if c.deciding || v.faultClass[c.scenario] == dataset.FaultNoFault || v.gated[c.scenario] != "" {
			continue
		}
		c.b, c.c = discordant(baseSeed[c.scenario], c.seedFault)
		c.tested = true
		tested = append(tested, key)
		ps = append(ps, mcnemar(c.b, c.c))
	}
	for i, q := range bhAdjust(ps) {
		v.cells[tested[i]].q = q
	}

	return v, nil
}

func decides(doc int, field string, loci []heatmap.Locus) bool {
	for _, l := range loci {
		if l.Doc == doc && (field == l.Pointer || strings.HasPrefix(l.Pointer, field+"/")) {
			return true
		}
	}
	return false
}

// ---------- page 1: per-Kind heatmaps ----------

// kindGrid is one dense heatmap: the fields of one Kind against the scored
// scenarios that contain a document of that Kind.
type kindGrid struct {
	kind      string
	scenarios []string
	fields    []string
	at        map[string]*cell // field\x00scenario
	max       float64
}

func writeKinds(w io.Writer, v *view, r *http.Request) {
	// Default mirrors Table 1: scored scenarios only — which after the dual
	// gate can be a handful of Kinds. ?all=1 adds the gated faulty scenarios
	// as muted columns (their cells are never "signal": untested ⇒ grey), so
	// the full Kind coverage stays explorable without pretending it is scored.
	all := r.URL.Query().Get("all") == "1"

	grids := map[string]*kindGrid{}
	for _, key := range v.order {
		c := v.cells[key]
		if v.faultClass[c.scenario] == dataset.FaultNoFault {
			continue // healthy cells measure hallucination (Table 3b), not saliency
		}
		if !all && v.gated[c.scenario] != "" {
			continue
		}
		g := grids[c.kind]
		if g == nil {
			g = &kindGrid{kind: c.kind, at: map[string]*cell{}}
			grids[c.kind] = g
		}
		k := c.field + "\x00" + c.scenario
		// Two same-Kind docs in one scenario would collide; keep the stronger cell.
		if old := g.at[k]; old == nil || v.saliency(c) > v.saliency(old) {
			g.at[k] = c
		}
		if !c.deciding && v.saliency(c) > g.max {
			g.max = v.saliency(c)
		}
	}

	var kinds []*kindGrid
	for _, g := range grids {
		seen := map[string]bool{}
		for k := range g.at {
			parts := strings.SplitN(k, "\x00", 2)
			if !seen["f"+parts[0]] {
				seen["f"+parts[0]] = true
				g.fields = append(g.fields, parts[0])
			}
			if !seen["s"+parts[1]] {
				seen["s"+parts[1]] = true
				g.scenarios = append(g.scenarios, parts[1])
			}
		}
		sort.Strings(g.scenarios)
		// hottest fields first, ties by name
		heat := map[string]float64{}
		for k, c := range g.at {
			f := strings.SplitN(k, "\x00", 2)[0]
			if !c.deciding && v.saliency(c) > heat[f] {
				heat[f] = v.saliency(c)
			}
		}
		sort.Slice(g.fields, func(i, j int) bool {
			if heat[g.fields[i]] != heat[g.fields[j]] {
				return heat[g.fields[i]] > heat[g.fields[j]]
			}
			return g.fields[i] < g.fields[j]
		})
		kinds = append(kinds, g)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if kinds[i].max != kinds[j].max {
			return kinds[i].max > kinds[j].max
		}
		return kinds[i].kind < kinds[j].kind
	})

	head(w, "kubelean signal heatmap")
	if all {
		fmt.Fprint(w, `<h1>signal heatmaps — one per Kind, ALL faulty scenarios</h1>
<p class=meta>gated columns are muted and can never be signal (their saliency is unscored) —
<a href="/">back to scored only</a></p>`)
	} else {
		fmt.Fprintf(w, `<h1>signal heatmaps — one per Kind, scored scenarios only (%d Kinds)</h1>
<p class=meta>only Kinds appearing in gate-surviving scenarios show up here —
<a href="/?all=1">show all faulty scenarios</a> (incl. gated) for the full Kind coverage</p>`, len(kinds))
	}
	legend(w)
	gatedNote(w, v)

	for _, g := range kinds {
		fmt.Fprintf(w, "<h2>%s <small>(%d fields × %d scenarios)</small></h2>", html.EscapeString(g.kind), len(g.fields), len(g.scenarios))
		fmt.Fprint(w, "<table><tr><th class=f>Field</th>")
		for _, s := range g.scenarios {
			if why := v.gated[s]; why != "" {
				fmt.Fprintf(w, `<th class=g title="GATED (%s) · baseline %.2f — saliency unscored"><a href="/manifest?scenario=%s">%s</a></th>`,
					html.EscapeString(why), frac(v.baseHit[s], v.baseTot[s]), url.QueryEscape(s), html.EscapeString(s))
				continue
			}
			fmt.Fprintf(w, `<th title="baseline %.2f · floor %.2f"><a href="/manifest?scenario=%s">%s</a></th>`,
				frac(v.baseHit[s], v.baseTot[s]), v.floor[s], url.QueryEscape(s), html.EscapeString(s))
		}
		fmt.Fprint(w, "</tr>")
		for _, f := range g.fields {
			fmt.Fprintf(w, "<tr><td class=f>%s</td>", html.EscapeString(f))
			for _, s := range g.scenarios {
				c := g.at[f+"\x00"+s]
				if c == nil {
					fmt.Fprint(w, "<td class=e>·</td>")
					continue
				}
				writeCell(w, v, c)
			}
			fmt.Fprint(w, "</tr>")
		}
		fmt.Fprint(w, "</table>")
	}
	fmt.Fprint(w, "</body></html>")
}

func writeCell(w io.Writer, v *view, c *cell) {
	sal := v.saliency(c)
	verdict := v.verdict(c)
	tip := fmt.Sprintf("%s · saliency %.2f · b=%d c=%d q=%.3f · %s · valid=%v", c.field, sal, c.b, c.c, c.q, verdict, c.valid)
	if c.deciding {
		fmt.Fprintf(w, `<td class=d title="%s · deciding locus (injected fault, tautological)">*</td>`, html.EscapeString(c.field))
		return
	}
	fmt.Fprintf(w, `<td class=%s style="background:%s;color:%s" title="%s">%.2f</td>`,
		verdictClass(verdict), cellBG(verdict, sal), cellFG(verdict, sal), html.EscapeString(tip), sal)
}

func verdictClass(verdict string) string {
	if verdict == "control" {
		return "c"
	}
	return "m"
}

// cellBG: signal = strong orange (slightly deeper with saliency); everything
// else grey, darker with raw saliency so destabilization is visible but muted.
func cellBG(verdict string, sal float64) string {
	sal = math.Max(0, math.Min(1, sal))
	if verdict == "signal" {
		return fmt.Sprintf("hsl(24,94%%,%d%%)", 62-int(14*sal))
	}
	return fmt.Sprintf("hsl(220,9%%,%d%%)", 96-int(42*sal))
}

func cellFG(verdict string, sal float64) string {
	if verdict == "signal" {
		return "#fff"
	}
	if sal > 0.6 {
		return "#f9fafb"
	}
	return "#374151"
}

// ---------- page 2: the heatmap painted on the K8s object ----------

// span is one painted line range of a scenario's YAML (1-based, inclusive).
type span struct {
	from, to int
	c        *cell
}

func writeManifest(w io.Writer, v *view, r *http.Request) {
	name := r.URL.Query().Get("scenario")
	var scen *dataset.Scenario
	var picker []dataset.Scenario
	for _, s := range dataset.All() {
		if s.TwinOf != "" {
			continue // twins run baseline-only: nothing to paint
		}
		picker = append(picker, s)
		exact := s.Name == name
		defaulted := name == "" && scen == nil && v.gated[s.Name] == "" && s.FaultClass != dataset.FaultNoFault
		if exact || defaulted {
			s := s
			scen = &s
		}
	}
	if scen != nil {
		name = scen.Name
	}

	head(w, "kubelean manifest heatmap")
	fmt.Fprint(w, "<h1>the heatmap on the K8s object</h1>")
	legend(w)

	fmt.Fprint(w, `<div class=picker>`)
	for _, s := range picker {
		cls := "pick"
		if v.gated[s.Name] != "" || s.FaultClass == dataset.FaultNoFault {
			cls = "pick dim"
		}
		if s.Name == name {
			cls += " cur"
		}
		fmt.Fprintf(w, `<a class="%s" href="/manifest?scenario=%s">%s</a> `, cls, url.QueryEscape(s.Name), html.EscapeString(s.Name))
	}
	fmt.Fprint(w, `</div>`)

	if scen == nil {
		fmt.Fprint(w, "<p>unknown scenario</p></body></html>")
		return
	}

	switch {
	case scen.FaultClass == dataset.FaultNoFault:
		fmt.Fprint(w, `<p class=warn>healthy control — its cells measure removal-induced hallucination (Table 3b), not saliency; greys show that rate.</p>`)
	case v.gated[scen.Name] != "":
		fmt.Fprintf(w, `<p class=warn>gated (%s) — saliency is not scored for this scenario; greys show raw values for exploration only.</p>`, html.EscapeString(v.gated[scen.Name]))
	default:
		fmt.Fprintf(w, `<p class=meta>baseline %.2f · fragility floor %.2f · fault %s</p>`,
			frac(v.baseHit[scen.Name], v.baseTot[scen.Name]), v.floor[scen.Name], html.EscapeString(scen.FaultClass))
	}

	lines := strings.Split(strings.TrimRight(scen.YAML, "\n"), "\n")
	byLine := map[int]*span{}
	for _, sp := range paint(scen, v) {
		for l := sp.from; l <= sp.to; l++ {
			byLine[l] = sp
		}
	}

	fmt.Fprint(w, "<pre class=yaml>")
	for i, line := range lines {
		sp := byLine[i+1]
		if sp == nil {
			fmt.Fprintf(w, "<span class=plain>%s</span>\n", html.EscapeString(line))
			continue
		}
		c := sp.c
		if c.deciding {
			fmt.Fprintf(w, `<span class=dl title="%s · deciding locus (injected fault)">%s  *</span>`+"\n",
				html.EscapeString(c.field), html.EscapeString(line))
			continue
		}
		sal := v.saliency(c)
		verdict := v.verdict(c)
		tip := fmt.Sprintf("%s · saliency %.2f · b=%d c=%d q=%.3f · %s", c.field, sal, c.b, c.c, c.q, verdict)
		fmt.Fprintf(w, `<span style="background:%s;color:%s" title="%s">%s  %.2f</span>`+"\n",
			cellBG(verdict, sal), cellFG(verdict, sal), html.EscapeString(tip), html.EscapeString(line), sal)
	}
	fmt.Fprint(w, "</pre></body></html>")
}

// paint maps the scenario's measured cells onto line ranges of its YAML: each
// document is parsed into a yaml.v3 AST (which carries line numbers), every
// node's JSON pointer is normalized with the same heatmap.NormalizeKey the
// producer used, and pointers that match a measured cell mark the node's whole
// block. No kernel logic is duplicated — the shards say what was measured, the
// AST says where it lives.
func paint(s *dataset.Scenario, v *view) []*span {
	var spans []*span
	offset := 0 // global line offset of the current document
	for doc, src := range splitDocs(s.YAML) {
		var root yaml.Node
		if err := yaml.Unmarshal([]byte(src), &root); err == nil && len(root.Content) > 0 {
			mark := func(ptr string, from, to int) {
				key := s.Name + "\x00" + fmt.Sprint(doc) + "\x00" + heatmap.NormalizeKey(ptr)
				if c := v.cells[key]; c != nil {
					spans = append(spans, &span{from: offset + from, to: offset + to, c: c})
				}
			}
			walk(nil, root.Content[0], "", mark)
		}
		offset += strings.Count(src, "\n") + 1 // the body lines...
		offset++                               // ...plus the --- separator line
	}
	return spans
}

// splitDocs splits a --- joined multi-doc stream into per-document sources so
// each document's AST line numbers can be offset back to global lines.
func splitDocs(yamlSrc string) []string {
	var docs []string
	var cur []string
	for _, line := range strings.Split(strings.TrimRight(yamlSrc, "\n"), "\n") {
		if strings.TrimSpace(line) == "---" {
			docs = append(docs, strings.Join(cur, "\n"))
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	return append(docs, strings.Join(cur, "\n"))
}

// walk visits every node, building its JSON pointer. For a mapping value the
// painted block starts at the KEY's line ("labels:") and runs to the deepest
// descendant, so removing-whole-map cells (atomic maps) highlight fully.
func walk(key, val *yaml.Node, ptr string, mark func(ptr string, from, to int)) {
	from := val.Line
	if key != nil {
		from = key.Line
	}
	mark(ptr, from, maxLine(val))

	switch val.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(val.Content); i += 2 {
			k, c := val.Content[i], val.Content[i+1]
			walk(k, c, ptr+"/"+k.Value, mark)
		}
	case yaml.SequenceNode:
		for i, c := range val.Content {
			walk(nil, c, fmt.Sprintf("%s/%d", ptr, i), mark)
		}
	}
}

func maxLine(n *yaml.Node) int {
	m := n.Line
	for _, c := range n.Content {
		if l := maxLine(c); l > m {
			m = l
		}
	}
	return m
}

// ---------- page 3: confidence intervals ----------

func writeConfidence(w io.Writer, v *view, _ *http.Request) {
	head(w, "kubelean confidence")
	fmt.Fprint(w, `<h1>kubelean — 95% confidence intervals</h1>
<div class=legend>Saliency CI = Newcombe (difference of two proportions); control rates = Wilson.
 Verdict = the paper rule: <b>signal</b> needs McNemar+BH q ≤ 0.05 AND saliency above the scenario's
 negative-control fragility floor.</div>`)

	fmt.Fprint(w, "<h2>Saliency — scored non-deciding cells</h2>")
	fmt.Fprint(w, "<table><tr><th class=f>Scenario</th><th class=f>Kind</th><th class=f>Field</th><th>Saliency</th><th>95% CI</th><th>q</th><th>Verdict</th></tr>")
	for _, key := range v.order {
		c := v.cells[key]
		if !c.tested {
			continue
		}
		sal, lo, hi := newcombe(v.baseHit[c.scenario], v.baseTot[c.scenario], c.matchFault, c.total)
		verdict, field := v.verdict(c), html.EscapeString(c.field)
		if verdict == "signal" {
			field = "<b>" + field + "</b>"
		}
		fmt.Fprintf(w, `<tr><td class=f>%s</td><td class=f>%s</td><td class=f>%s</td><td>%.2f</td><td>[%.2f, %.2f]</td><td>%.3f</td><td style="background:%s;color:%s">%s</td></tr>`,
			html.EscapeString(c.scenario), html.EscapeString(c.kind), field, sal, lo, hi, c.q,
			cellBG(verdict, sal), cellFG(verdict, sal), verdict)
	}
	fmt.Fprint(w, "</table>")

	fmt.Fprint(w, "<h2>Control — deciding loci, Recognized (NoFaultFound after removal)</h2>")
	fmt.Fprint(w, "<table><tr><th class=f>Scenario</th><th class=f>Kind</th><th class=f>Field</th><th>Recognized</th><th>95% CI</th></tr>")
	for _, key := range v.order {
		c := v.cells[key]
		if !c.deciding || v.gated[c.scenario] != "" {
			continue
		}
		lo, hi := wilson(c.matchNoFault, c.total)
		fmt.Fprintf(w, `<tr><td class=f>%s</td><td class=f>%s</td><td class=f>%s</td><td>%.2f</td><td>[%.2f, %.2f]</td></tr>`,
			html.EscapeString(c.scenario), html.EscapeString(c.kind), html.EscapeString(c.field), frac(c.matchNoFault, c.total), lo, hi)
	}
	fmt.Fprint(w, "</table>")

	fmt.Fprint(w, "<h2>Control — healthy NoFaultFound rate</h2>")
	fmt.Fprint(w, "<table><tr><th class=f>Scenario</th><th>NoFaultFound rate</th><th>95% CI</th></tr>")
	var controls []string
	for s, fc := range v.faultClass {
		if fc == dataset.FaultNoFault && !v.isTwin[s] {
			controls = append(controls, s)
		}
	}
	sort.Strings(controls)
	for _, s := range controls {
		lo, hi := wilson(v.baseHit[s], v.baseTot[s])
		fmt.Fprintf(w, `<tr><td class=f>%s</td><td>%.2f</td><td>[%.2f, %.2f]</td></tr>`,
			html.EscapeString(s), frac(v.baseHit[s], v.baseTot[s]), lo, hi)
	}
	fmt.Fprint(w, "</table></body></html>")
}

// ---------- shared page chrome ----------

func head(w io.Writer, title string) {
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">
<title>%s</title>
<style>
 body{font:13px/1.4 ui-monospace,Menlo,Consolas,monospace;margin:24px;color:#222}
 h1{font-size:16px} h2{font-size:14px;margin:22px 0 4px} h2 small{color:#888;font-weight:normal}
 .legend{margin:8px 0 14px;color:#444}
 .sw{display:inline-block;width:14px;height:14px;vertical-align:middle;border:1px solid #bbb}
 .sw.ctl{border:1px dashed #6b7280}
 table{border-collapse:collapse;margin-top:4px}
 th,td{border:1px solid #ddd;padding:3px 7px;text-align:center}
 th a{color:inherit;text-decoration:none} th a:hover{text-decoration:underline}
 th.f,td.f{text-align:left;white-space:nowrap;font-size:12px}
 th.g{color:#9ca3af;font-weight:normal} th.g a{color:#9ca3af}
 td.e{background:#fff;color:#d1d5db}
 td.c{border:1px dashed #6b7280}
 td.d{background:#334155;color:#fff;font-weight:bold}
 .gated{margin:6px 0 10px;color:#777;font-size:12px}
 .picker{margin:10px 0;max-width:1100px}
 .pick{color:#1d4ed8;text-decoration:none;margin-right:2px}
 .pick.dim{color:#9ca3af}
 .pick.cur{font-weight:bold;text-decoration:underline}
 .warn{color:#92400e;background:#fef3c7;display:inline-block;padding:3px 8px}
 .meta{color:#555}
 pre.yaml{border:1px solid #ddd;padding:10px 0;max-width:1100px;overflow-x:auto}
 pre.yaml span{display:block;padding:0 10px;white-space:pre}
 pre.yaml span.plain{color:#666}
 pre.yaml span.dl{background:#334155;color:#fff}
</style></head><body>
<nav style="margin:0 0 14px"><a href="/">per-Kind heatmaps</a> &nbsp;|&nbsp; <a href="/manifest">manifest view</a> &nbsp;|&nbsp; <a href="/confidence">confidence (CI)</a></nav>`, html.EscapeString(title))
}

func legend(w io.Writer) {
	fmt.Fprintf(w, `<div class=legend>
 verdict per cell (same rule as the paper: seed-paired McNemar + BH q ≤ %.2f, then the negative-control floor):
 <span class=sw style="background:%s"></span> <b>signal</b> (orange, deeper = higher saliency)
 <span class=sw style="background:%s"></span><span class=sw style="background:%s"></span> no signal / destab / fragile (grey, darker = higher raw saliency; "fragile" = the scenario's own floor is too high for any signal claim)
 <span class="sw ctl" style="background:%s"></span> negative control (defines the floor)
 <span class=sw style="background:#334155"></span> * deciding locus (injected fault, never scored)
 &nbsp;·&nbsp; hover any cell for b/c/q evidence &nbsp;·&nbsp; reload to refresh
</div>`, fdrLevel, cellBG("signal", 0.9), cellBG("no", 0.05), cellBG("no", 0.6), cellBG("control", 0.3))
}

func gatedNote(w io.Writer, v *view) {
	var out []string
	for s, why := range v.gated {
		out = append(out, fmt.Sprintf("%s (%s)", s, why))
	}
	if len(out) == 0 {
		return
	}
	sort.Strings(out)
	fmt.Fprintf(w, `<div class=gated>excluded by the gate: %s</div>`, html.EscapeString(strings.Join(out, " · ")))
}

// ---------- shards + statistics (duplicated from cmd/render by design:
// small pure helpers, per-cmd like frac) ----------

func readShards(dir string) ([]heatmap.Record, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	var recs []heatmap.Record
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			if len(sc.Bytes()) == 0 {
				continue
			}
			var r heatmap.Record
			if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
				f.Close()
				return nil, fmt.Errorf("%s: %w", p, err)
			}
			recs = append(recs, r)
		}
		f.Close()
	}
	return recs, nil
}

func frac(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// negControl mirrors cmd/render: server bookkeeping fields are a-priori
// noise, so their saliency defines the fragility floor instead of counting
// as signal.
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

func mcnemar(b, c int) float64 {
	n := b + c
	if n == 0 {
		return 1
	}
	m := min(b, c)
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

func binomHalf(n, i int) float64 {
	ln, _ := math.Lgamma(float64(n + 1))
	li, _ := math.Lgamma(float64(i + 1))
	lni, _ := math.Lgamma(float64(n - i + 1))
	return math.Exp(ln - li - lni - float64(n)*math.Ln2)
}

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

// zCI is the standard-normal quantile for a 95% two-sided interval; wilson and
// newcombe mirror cmd/render so the browser preview matches the paper artifact.
const zCI = 1.96

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

func newcombe(x1, n1, x2, n2 int) (diff, lo, hi float64) {
	p1, p2 := frac(x1, n1), frac(x2, n2)
	l1, u1 := wilson(x1, n1)
	l2, u2 := wilson(x2, n2)
	diff = p1 - p2
	lo = diff - math.Sqrt((p1-l1)*(p1-l1)+(u2-p2)*(u2-p2))
	hi = diff + math.Sqrt((u1-p1)*(u1-p1)+(p2-l2)*(p2-l2))
	return diff, lo, hi
}
