package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikolajsemeniuk/kubelean/pkg/dataset"
	"github.com/mikolajsemeniuk/kubelean/pkg/heatmap"
	"gopkg.in/yaml.v3"
)

// TestWalkYAMLLineMapping guards the manifest-figure line math: a measured
// field's normalized pointer must map back to the exact global line of the
// scenario YAML it lives on, across the --- document boundaries.
func TestWalkYAMLLineMapping(t *testing.T) {
	t.Parallel()

	var scen *dataset.Scenario
	for _, s := range dataset.All() {
		if s.Name == "secret-ref-wrong-name" {
			s := s
			scen = &s
		}
	}
	if scen == nil {
		t.Fatal("secret-ref-wrong-name not in catalog")
	}
	lines := strings.Split(strings.TrimRight(scen.YAML, "\n"), "\n")

	got := map[string]int{} // normalized pointer (doc-qualified) → global start line
	offset := 0
	for doc, src := range splitDocs(scen.YAML) {
		var root yaml.Node
		if err := yaml.Unmarshal([]byte(src), &root); err != nil {
			t.Fatalf("doc %d: %v", doc, err)
		}
		if len(root.Content) > 0 {
			walkYAML(nil, root.Content[0], "", func(ptr string, from, _ int) {
				got[fmt.Sprintf("%d\x00%s", doc, heatmap.NormalizeKey(ptr))] = offset + from
			})
		}
		offset += strings.Count(src, "\n") + 2
	}

	// The faulty reference site lives in doc 0 (Deployment) on the line
	// "name: api-secret" under secretRef; the Secret's metadata.name in doc 2.
	for ptr, want := range map[string]string{
		"0\x00/spec/template/spec/containers/*/envFrom/*/secretRef/name": "name: api-secret",
		"0\x00/spec/template/spec/containers/*/ports/*/containerPort":    "containerPort: 8080",
		"2\x00/metadata/name": "name: api-secrets",
	} {
		line, ok := got[ptr]
		if !ok {
			t.Fatalf("pointer %q not visited", ptr)
		}
		if line < 1 || line > len(lines) {
			t.Fatalf("pointer %q mapped to out-of-range line %d", ptr, line)
		}
		if !strings.Contains(lines[line-1], want) {
			t.Errorf("pointer %q → line %d %q; want a line containing %q", ptr, line, lines[line-1], want)
		}
	}
}

// TestFiguresWrite smoke-tests the three writers against a synthetic scored
// cell set: files must be written, the flagship manifest must paint its
// signal line orange-boxed and its deciding line dark.
func TestFiguresWrite(t *testing.T) {
	t.Parallel()

	scenName := "secret-ref-wrong-name"
	sigField := "/spec/template/spec/containers/*/envFrom/*/secretRef/name"
	decField := "/spec/template/spec/containers/*/envFrom/*/configMapRef/name"
	sigKey := scenName + "\x000\x00" + sigField
	decKey := scenName + "\x000\x00" + decField
	ctlKey := scenName + "\x000\x00/metadata/creationTimestamp"

	fc := &figCtx{
		cells: map[string]cell{
			sigKey: {scenario: scenName, kind: "Deployment", field: sigField, total: 40, matchFault: 4},
			decKey: {scenario: scenName, kind: "Deployment", field: decField, deciding: true, total: 40},
			ctlKey: {scenario: scenName, kind: "Deployment", field: "/metadata/creationTimestamp", total: 40, matchFault: 36},
		},
		order:       []string{sigKey, decKey, ctlKey},
		faultClass:  map[string]string{scenName: dataset.FaultRefNotFound},
		gated:       map[string]string{},
		baseCorrect: map[string]int{scenName: 38},
		baseTotal:   map[string]int{scenName: 40},
		floor:       map[string]float64{scenName: 0.05},
		verdict: func(key string) string {
			switch key {
			case sigKey:
				return "signal"
			case ctlKey:
				return "control"
			}
			return "no"
		},
	}

	dir := t.TempDir()
	fc.writeFieldProfile(dir)
	fc.writeHeatmapFig(dir)
	fc.writeManifestFig(dir)

	profile := readFile(t, filepath.Join(dir, "fieldprofile.gen.tex"))
	if !strings.Contains(profile, "secretRef/name") || !strings.Contains(profile, "\\begin{longtable}") {
		t.Error("fieldprofile.gen.tex missing the signal field row or longtable env")
	}

	fig := readFile(t, filepath.Join(dir, "heatmapfig.gen.tex"))
	if !strings.Contains(fig, "tikzpicture") || !strings.Contains(fig, "$\\ast$") {
		t.Error("heatmapfig.gen.tex missing tikzpicture or the deciding-locus star")
	}

	man := readFile(t, filepath.Join(dir, "manifestfig.gen.tex"))
	if !strings.Contains(man, scenName) {
		t.Error("manifestfig.gen.tex did not pick the flagship scenario")
	}
	if !strings.Contains(man, "\\colorbox[HTML]{334155}") {
		t.Error("manifestfig.gen.tex missing the dark deciding-locus line")
	}
	// the signal line: orange background (red channel dominates) + white text
	if !strings.Contains(man, "\\color{white}") || !strings.Contains(man, "name:~api-secret~~0.85") {
		t.Error("manifestfig.gen.tex missing the orange signal line for secretRef.name")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
