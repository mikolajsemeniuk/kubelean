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
