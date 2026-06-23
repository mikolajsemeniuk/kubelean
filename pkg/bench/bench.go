// Package bench is the shared experiment harness behind the cmd/ entry points.
// Each command (tokens, accuracy, perclass, noise) renders generated faults under
// distillation profiles and scores RCA; the per-profile rendering, the
// instance×profile×repeat sweep, the accuracy accumulator, and the LaTeX helpers
// live here so the commands stay thin and emit one paper/<name>.gen.tex each.
package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/mikolajsemeniuk/kubelean/pkg/distill"
	"github.com/mikolajsemeniuk/kubelean/pkg/faults"
)

// Profile names understood by Renderer.Bundle.
const (
	L0   = "L0"
	L1   = "L1"
	L2   = "L2"
	L3   = "L3"
	L4   = "L4"
	Rand = "rand"
)

// Renderer serializes a resource bundle under any distillation profile. It holds
// the corpus-entropy model (for L3) and the embedder + thresholds (for L4), built
// once and reused across instances.
type Renderer struct {
	Stats    *distill.CorpusStats
	Embed    distill.EmbedFunc // nil unless L4 is used
	L3Thresh float64
	L4Thresh float64
}

// NewRenderer builds the L3 corpus model from corpus and configures L4. Pass a
// nil embed when L4 is not requested.
func NewRenderer(corpus []*unstructured.Unstructured, embed distill.EmbedFunc, l3thresh, l4thresh float64) *Renderer {
	return &Renderer{
		Stats:    distill.BuildCorpusStats(corpus),
		Embed:    embed,
		L3Thresh: l3thresh,
		L4Thresh: l4thresh,
	}
}

// Corpus flattens the resources of every instance into one slice (the L3 model
// and random-drop budget are computed over it).
func Corpus(instances []faults.Instance) []*unstructured.Unstructured {
	var c []*unstructured.Unstructured
	for _, in := range instances {
		c = append(c, in.Resources...)
	}
	return c
}

// Inflate returns a copy of instances with structural (volume = managedFields
// bloat) and semantic (mislead = stale distractor annotations) noise added to
// every resource, for the robustness sweeps. The originals are not mutated.
func Inflate(instances []faults.Instance, volume, mislead int) []faults.Instance {
	if volume == 0 && mislead == 0 {
		return instances
	}
	out := make([]faults.Instance, len(instances))
	for i, in := range instances {
		res := make([]*unstructured.Unstructured, len(in.Resources))
		for j, r := range in.Resources {
			res[j] = faults.Inflate(r, volume, mislead, j)
		}
		out[i] = faults.Instance{Name: in.Name, Resources: res, Truth: in.Truth}
	}
	return out
}

// Frac is the L2/L0 byte ratio for a bundle — the budget random-drop must hit.
func (r *Renderer) Frac(res []*unstructured.Unstructured, goal string) float64 {
	l0, err := r.Bundle(context.Background(), res, L0, goal, 0.6)
	if err != nil || len(l0) == 0 {
		return 0.6
	}
	l2, err := r.Bundle(context.Background(), res, L2, goal, 0.6)
	if err != nil {
		return 0.6
	}
	return float64(len(l2)) / float64(len(l0))
}

// Bundle serializes res under profile into one ---joined document.
func (r *Renderer) Bundle(ctx context.Context, res []*unstructured.Unstructured, profile, goal string, frac float64) (string, error) {
	parts := make([]string, 0, len(res))
	for i, obj := range res {
		var out *unstructured.Unstructured
		var err error
		switch profile {
		case L0:
			out = obj
		case L1:
			out = distill.Distill(obj, distill.Profile{Level: distill.L1Lossless, Goal: distill.Goal(goal)})
		case L2:
			out = distill.Distill(obj, distill.Profile{Level: distill.L2StaticBuckets, Goal: distill.Goal(goal)})
		case L3:
			out = distill.DistillL3(obj, r.Stats, r.L3Thresh)
		case L4:
			if r.Embed == nil {
				return "", fmt.Errorf("profile L4 requires an embedder")
			}
			out, err = distill.DistillL4(ctx, obj, goal, r.Embed, r.L4Thresh)
			if err != nil {
				return "", err
			}
		case Rand:
			out = distill.RandomDrop(obj, frac, int64(i+1))
		default:
			return "", fmt.Errorf("unknown profile %q", profile)
		}
		y, err := distill.ToYAML(out)
		if err != nil {
			return "", err
		}
		parts = append(parts, y)
	}
	return strings.Join(parts, "---\n"), nil
}

// Diagnoser runs one RCA call and returns the model's label and the exact prompt
// token count. Injected so this package does not depend on pkg/eval.
type Diagnoser func(ctx context.Context, resourceYAML string) (label string, promptTokens int, err error)

// Result is one (instance, profile) cell after repeats RCA calls.
type Result struct {
	Label        string
	Difficulty   string
	Profile      string
	Correct      int
	Repeats      int
	PromptTokens int
}

// Progress carries optional callbacks ticked during a Run (nil-safe).
type Progress struct {
	Render func() // once per instance after its profiles are serialized
	RCA    func() // once per RCA repeat
}

// Run serializes every (instance, profile) up front (Pass 1, so an L4 embedder
// stays loaded instead of Ollama thrashing against the RCA model), then scores
// each cell with repeats RCA calls (Pass 2).
func Run(ctx context.Context, instances []faults.Instance, profiles []string, r *Renderer, diag Diagnoser, repeats int, prog Progress) ([]Result, error) {
	yamls := make([]map[string]string, len(instances))
	for i, inst := range instances {
		goal := inst.Truth.Goal
		frac := r.Frac(inst.Resources, goal)
		m := make(map[string]string, len(profiles))
		for _, p := range profiles {
			y, err := r.Bundle(ctx, inst.Resources, p, goal, frac)
			if err != nil {
				return nil, err
			}
			m[p] = y
		}
		yamls[i] = m
		if prog.Render != nil {
			prog.Render()
		}
	}

	out := make([]Result, 0, len(instances)*len(profiles))
	for i, inst := range instances {
		for _, p := range profiles {
			y := yamls[i][p]
			correct, tok := 0, 0
			for j := 0; j < repeats; j++ {
				label, ptok, err := diag(ctx, y)
				if err != nil {
					return nil, err
				}
				tok = ptok
				if strings.EqualFold(strings.TrimSpace(label), inst.Truth.Label) {
					correct++
				}
				if prog.RCA != nil {
					prog.RCA()
				}
			}
			out = append(out, Result{
				Label: inst.Truth.Label, Difficulty: DifficultyOf(inst.Truth.Label),
				Profile: p, Correct: correct, Repeats: repeats, PromptTokens: tok,
			})
		}
	}
	return out, nil
}

// Acc accumulates accuracy and mean tokens over a set of result cells.
type Acc struct {
	Correct, Repeats, TokenSum, Cells int
}

// Add folds one result cell in.
func (a *Acc) Add(r Result) {
	a.Correct += r.Correct
	a.Repeats += r.Repeats
	a.TokenSum += r.PromptTokens
	a.Cells++
}

// Pct is accuracy in percent.
func (a *Acc) Pct() float64 {
	if a.Repeats == 0 {
		return 0
	}
	return 100 * float64(a.Correct) / float64(a.Repeats)
}

// MeanTok is the mean prompt token count per cell.
func (a *Acc) MeanTok() int {
	if a.Cells == 0 {
		return 0
	}
	return a.TokenSum / a.Cells
}

// DifficultyOf returns the catalog difficulty ("easy"/"hard") for a label.
func DifficultyOf(label string) string {
	for _, fc := range faults.Catalog() {
		if fc.Label == label {
			return fc.Difficulty.String()
		}
	}
	return "easy"
}

// TexEscape escapes the LaTeX special characters that appear in labels and model
// names (notably the underscores in fault classes).
func TexEscape(s string) string {
	return strings.NewReplacer(
		`\`, `\textbackslash{}`, `_`, `\_`, `%`, `\%`, `&`, `\&`,
		`#`, `\#`, `$`, `\$`, `{`, `\{`, `}`, `\}`,
	).Replace(s)
}

// WriteFile writes content to path, creating the parent directory if needed.
func WriteFile(path, content string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
