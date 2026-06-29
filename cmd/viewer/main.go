// Command viewer is a throwaway local heatmap browser. It reads the JSONL shards
// and serves, on :8080, a field × scenario grid coloured by signal — green = noise
// (safe to drop), red = carries diagnostic signal. Signal is the raw saliency
// (baseline accuracy − reduced accuracy = how often removing the field changed the
// diagnosis). Deciding-field loci (the injected faults) are marked *, since their
// signal is tautological. It re-reads on every request, so you can produce more
// data and just refresh.
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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mikolajsemeniuk/kubelean/pkg/dataset"
	"github.com/mikolajsemeniuk/kubelean/pkg/heatmap"
)

type point struct {
	signal   float64
	valid    bool
	deciding bool
}

type row struct {
	kind  string
	field string
	cells map[string]point // by scenario
	max   float64
}

func main() {
	dir := flag.String("in", "data", "directory of JSONL shards")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		scenarios, rows, err := aggregate(*dir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		writePage(w, scenarios, rows)
	})

	http.HandleFunc("/confidence", func(w http.ResponseWriter, r *http.Request) {
		sals, ctls, fps, err := confAggregate(*dir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		writeConfidence(w, sals, ctls, fps)
	})

	log.Printf("kubelean viewer on http://localhost%s (reading %s)", *addr, *dir)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// aggregate reads the shards and returns the sorted scenario columns and the field
// rows, each carrying its per-scenario signal.
func aggregate(dir string) ([]string, []row, error) {
	recs, err := readShards(dir)
	if err != nil {
		return nil, nil, err
	}

	// concrete deciding loci per scenario
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

	baseHit := map[string]int{}
	baseTot := map[string]int{}
	type agg struct {
		kind            string
		valid, deciding bool
		hit, total      int
	}
	cells := map[string]*agg{} // key: scenario\x00doc\x00field
	scenSet := map[string]bool{}

	for _, r := range recs {
		scenSet[r.Scenario] = true
		correct := r.Answer != nil && *r.Answer == r.FaultClass
		if r.Variant == "baseline" {
			baseTot[r.Scenario]++
			if correct {
				baseHit[r.Scenario]++
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
		a := cells[key]
		if a == nil {
			a = &agg{kind: r.Kind, valid: r.Valid, deciding: decides(doc, field, deciding[r.Scenario])}
			cells[key] = a
		}
		a.total++
		if correct {
			a.hit++
		}
	}

	// fold into field rows
	rows := map[string]*row{}
	for key, a := range cells {
		parts := strings.SplitN(key, "\x00", 3)
		scenario, field := parts[0], parts[2]
		accBase := frac(baseHit[scenario], baseTot[scenario])
		signal := accBase - frac(a.hit, a.total)

		rk := a.kind + "\x00" + field
		rr := rows[rk]
		if rr == nil {
			rr = &row{kind: a.kind, field: field, cells: map[string]point{}}
			rows[rk] = rr
		}
		rr.cells[scenario] = point{signal: signal, valid: a.valid, deciding: a.deciding}
		if signal > rr.max {
			rr.max = signal
		}
	}

	scenarios := make([]string, 0, len(scenSet))
	for s := range scenSet {
		scenarios = append(scenarios, s)
	}
	sort.Strings(scenarios)

	out := make([]row, 0, len(rows))
	for _, rr := range rows {
		out = append(out, *rr)
	}
	// hottest fields first; ties by name for stable ordering
	sort.Slice(out, func(i, j int) bool {
		if out[i].max != out[j].max {
			return out[i].max > out[j].max
		}
		if out[i].kind != out[j].kind {
			return out[i].kind < out[j].kind
		}
		return out[i].field < out[j].field
	})
	return scenarios, out, nil
}

func writePage(w io.Writer, scenarios []string, rows []row) {
	fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8">
<title>kubelean signal heatmap</title>
<style>
 body{font:13px/1.4 ui-monospace,Menlo,Consolas,monospace;margin:24px;color:#222}
 h1{font-size:16px}
 .legend{margin:8px 0 16px;color:#444}
 .sw{display:inline-block;width:14px;height:14px;vertical-align:middle;border:1px solid #999}
 table{border-collapse:collapse}
 th,td{border:1px solid #ddd;padding:4px 8px;text-align:center}
 th.f,td.f{text-align:left;white-space:nowrap;font-size:12px}
 td.e{background:#f4f4f4;color:#bbb}
 .s{font-weight:bold}
</style></head><body>`)

	fmt.Fprint(w, nav)
	fmt.Fprintf(w, "<h1>kubelean signal heatmap — %d fields × %d scenarios</h1>", len(rows), len(scenarios))
	fmt.Fprintf(w, `<div class=legend>
 signal = baseline − reduced accuracy (how often removing the field changed the diagnosis):
 <span class=sw style="background:%s"></span> 0.00 noise (drop)
 <span class=sw style="background:%s"></span> 0.50
 <span class=sw style="background:%s"></span> 1.00 carries signal
 &nbsp;·&nbsp; <b>*</b> = injected fault locus (tautological) &nbsp;·&nbsp; reload to refresh
</div>`, heat(0), heat(0.5), heat(1))

	fmt.Fprint(w, "<table><tr><th class=f>Kind &nbsp; Field</th>")
	for _, s := range scenarios {
		fmt.Fprintf(w, "<th>%s</th>", html.EscapeString(s))
	}
	fmt.Fprint(w, "</tr>")

	for _, r := range rows {
		fmt.Fprintf(w, "<tr><td class=f><b>%s</b> &nbsp; %s</td>", html.EscapeString(r.kind), html.EscapeString(r.field))
		for _, s := range scenarios {
			p, ok := r.cells[s]
			if !ok {
				fmt.Fprint(w, "<td class=e>·</td>")
				continue
			}
			star := ""
			if p.deciding {
				star = `<span class=s>*</span>`
			}
			fmt.Fprintf(w, `<td style="background:%s" title="valid=%v deciding=%v">%.2f%s</td>`,
				heat(p.signal), p.valid, p.deciding, p.signal, star)
		}
		fmt.Fprint(w, "</tr>")
	}
	fmt.Fprint(w, "</table></body></html>")
}

func decides(doc int, field string, loci []heatmap.Locus) bool {
	for _, l := range loci {
		if l.Doc == doc && (field == l.Pointer || strings.HasPrefix(l.Pointer, field+"/")) {
			return true
		}
	}
	return false
}

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

// heat maps signal 0..1 to green..red.
func heat(s float64) string {
	if s < 0 {
		s = 0
	}
	if s > 1 {
		s = 1
	}
	return fmt.Sprintf("hsl(%d,75%%,72%%)", int(120*(1-s)))
}

const nav = `<nav style="margin:0 0 14px"><a href="/">signal heatmap</a> &nbsp;|&nbsp; <a href="/confidence">confidence (CI)</a></nav>`

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

type salCell struct {
	scenario, kind, field string
	valid, signal         bool
	sal, lo, hi           float64
}

type ctlCell struct {
	scenario, kind, field string
	valid                 bool
	rec, lo, hi           float64
}

type fpCell struct {
	scenario     string
	rate, lo, hi float64
}

// confAggregate computes the three confidence populations exactly as cmd/render:
// saliency (non-deciding, Newcombe), control recognition (deciding, Wilson) and
// healthy false-positive rate (Wilson).
func confAggregate(dir string) (sals []salCell, ctls []ctlCell, fps []fpCell, err error) {
	recs, err := readShards(dir)
	if err != nil {
		return nil, nil, nil, err
	}

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

	baseHit := map[string]int{}
	baseTot := map[string]int{}
	faultClass := map[string]string{}
	type agg struct {
		kind                            string
		valid, deciding                 bool
		total, matchFault, matchNoFault int
	}
	cells := map[string]*agg{}
	var order []string

	for _, r := range recs {
		faultClass[r.Scenario] = r.FaultClass
		correct := r.Answer != nil && *r.Answer == r.FaultClass
		if r.Variant == "baseline" {
			baseTot[r.Scenario]++
			if correct {
				baseHit[r.Scenario]++
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
		a := cells[key]
		if a == nil {
			a = &agg{kind: r.Kind, valid: r.Valid, deciding: decides(doc, field, deciding[r.Scenario])}
			cells[key] = a
			order = append(order, key)
		}
		a.total++
		if correct {
			a.matchFault++
		}
		if r.Answer != nil && *r.Answer == dataset.FaultNoFault {
			a.matchNoFault++
		}
	}

	for _, key := range order {
		a := cells[key]
		parts := strings.SplitN(key, "\x00", 3)
		scenario, field := parts[0], parts[2]
		switch {
		case a.deciding:
			lo, hi := wilson(a.matchNoFault, a.total)
			ctls = append(ctls, ctlCell{scenario, a.kind, field, a.valid, frac(a.matchNoFault, a.total), lo, hi})
		case faultClass[scenario] != dataset.FaultNoFault:
			sal, lo, hi := newcombe(baseHit[scenario], baseTot[scenario], a.matchFault, a.total)
			sals = append(sals, salCell{scenario, a.kind, field, a.valid, lo > 0, sal, lo, hi})
		}
		// healthy non-deciding cells are excluded from the map (mirrors render).
	}

	var controls []string
	for s, fc := range faultClass {
		if fc == dataset.FaultNoFault {
			controls = append(controls, s)
		}
	}
	sort.Strings(controls)
	for _, s := range controls {
		lo, hi := wilson(baseHit[s], baseTot[s])
		fps = append(fps, fpCell{s, frac(baseHit[s], baseTot[s]), lo, hi})
	}

	sort.Slice(sals, func(i, j int) bool { return less(sals[i].scenario, sals[i].field, sals[j].scenario, sals[j].field) })
	sort.Slice(ctls, func(i, j int) bool { return less(ctls[i].scenario, ctls[i].field, ctls[j].scenario, ctls[j].field) })
	return sals, ctls, fps, nil
}

func less(s1, f1, s2, f2 string) bool {
	if s1 != s2 {
		return s1 < s2
	}
	return f1 < f2
}

func writeConfidence(w io.Writer, sals []salCell, ctls []ctlCell, fps []fpCell) {
	fmt.Fprint(w, `<!doctype html><html><head><meta charset="utf-8">
<title>kubelean confidence</title>
<style>
 body{font:13px/1.4 ui-monospace,Menlo,Consolas,monospace;margin:24px;color:#222}
 h1{font-size:16px} h2{font-size:14px;margin-top:24px}
 .legend{margin:8px 0 16px;color:#444}
 table{border-collapse:collapse;margin-top:6px}
 th,td{border:1px solid #ddd;padding:4px 8px;text-align:center}
 th.f,td.f{text-align:left;white-space:nowrap;font-size:12px}
 td.sig{font-weight:bold}
 .yes{background:hsl(0,75%,82%)} .no{background:hsl(120,55%,90%)}
</style></head><body>`)
	fmt.Fprint(w, nav)
	fmt.Fprint(w, `<h1>kubelean — 95% confidence intervals</h1>
<div class=legend>Saliency CI = Newcombe (difference of two proportions); control rates = Wilson (single proportion).
 <b>Signal</b> = saliency CI lower bound &gt; 0 (real, not k-noise).</div>`)

	fmt.Fprintf(w, "<h2>Saliency map — non-deciding fields (%d)</h2>", len(sals))
	fmt.Fprint(w, "<table><tr><th class=f>Scenario</th><th class=f>Kind</th><th class=f>Field</th><th>Saliency</th><th>95% CI</th><th>Signal</th></tr>")
	for _, c := range sals {
		cls, sig, field := "no", "no", html.EscapeString(c.field)
		if c.signal {
			cls, sig, field = "yes", "yes", "<b>"+field+"</b>"
		}
		fmt.Fprintf(w, `<tr><td class=f>%s</td><td class=f>%s</td><td class=f>%s</td><td>%.2f</td><td>[%.2f, %.2f]</td><td class="sig %s">%s</td></tr>`,
			html.EscapeString(c.scenario), html.EscapeString(c.kind), field, c.sal, c.lo, c.hi, cls, sig)
	}
	fmt.Fprint(w, "</table>")

	fmt.Fprintf(w, "<h2>Control — deciding loci, Recognized (%d)</h2>", len(ctls))
	fmt.Fprint(w, "<table><tr><th class=f>Scenario</th><th class=f>Kind</th><th class=f>Field</th><th>Recognized</th><th>95% CI</th></tr>")
	for _, c := range ctls {
		fmt.Fprintf(w, `<tr><td class=f>%s</td><td class=f>%s</td><td class=f>%s</td><td>%.2f</td><td>[%.2f, %.2f]</td></tr>`,
			html.EscapeString(c.scenario), html.EscapeString(c.kind), html.EscapeString(c.field), c.rec, c.lo, c.hi)
	}
	fmt.Fprint(w, "</table>")

	fmt.Fprintf(w, "<h2>Control — healthy NoFaultFound rate (%d)</h2>", len(fps))
	fmt.Fprint(w, "<table><tr><th class=f>Scenario</th><th>NoFaultFound rate</th><th>95% CI</th></tr>")
	for _, c := range fps {
		fmt.Fprintf(w, `<tr><td class=f>%s</td><td>%.2f</td><td>[%.2f, %.2f]</td></tr>`,
			html.EscapeString(c.scenario), c.rate, c.lo, c.hi)
	}
	fmt.Fprint(w, "</table></body></html>")
}
