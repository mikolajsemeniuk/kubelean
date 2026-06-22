package distill

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// L5 — oracle leave-one-field-out diagnostic saliency. For each leaf field, drop
// it and remeasure RCA accuracy; the drop in accuracy is the field's saliency.
// This is the gold saliency map (the upper bound a cheap proxy like L4 is judged
// against), not a deployable transform: it costs one RCA pass per field. Note the
// classic leave-one-out caveat — redundant fields (two places stating the same
// cause) each look non-salient because the other covers for it.

// Diagnoser runs one RCA call over a serialized resource and returns the model's
// root-cause label. Injected so this package does not depend on pkg/eval.
type Diagnoser func(ctx context.Context, resourceYAML string) (label string, err error)

// FieldSaliency is one field's contribution to RCA accuracy.
type FieldSaliency struct {
	Path     string  // dotted path with [i] indices, e.g. spec.containers[0].image
	Saliency float64 // baseAcc - accWithoutField; >0 means removing it hurt RCA
	BaseAcc  float64 // accuracy with the field present (same for every field)
}

// OracleSaliency computes the leave-one-field-out saliency of every leaf in obj
// (after an L1 strip) against truthLabel, running each configuration repeats
// times. Identity fields (apiVersion/kind, metadata.name/namespace) are never
// removed.
func OracleSaliency(ctx context.Context, obj *unstructured.Unstructured, truthLabel string, diag Diagnoser, repeats int) ([]FieldSaliency, error) {
	base := obj.DeepCopy()
	stripLossless(base)

	baseAcc, err := rcaAccuracy(ctx, base.Object, truthLabel, diag, repeats)
	if err != nil {
		return nil, err
	}

	var paths [][]pathSeg
	leafPaths(base.Object, nil, &paths)

	out := make([]FieldSaliency, 0, len(paths))
	for _, p := range paths {
		if isProtectedSegPath(p) {
			continue
		}
		reduced, ok := without(base.Object, p).(map[string]any)
		if !ok {
			continue
		}
		acc, err := rcaAccuracy(ctx, reduced, truthLabel, diag, repeats)
		if err != nil {
			return nil, err
		}
		out = append(out, FieldSaliency{Path: pathString(p), Saliency: baseAcc - acc, BaseAcc: baseAcc})
	}
	return out, nil
}

func rcaAccuracy(ctx context.Context, obj map[string]any, truth string, diag Diagnoser, repeats int) (float64, error) {
	y, err := ToYAML(&unstructured.Unstructured{Object: obj})
	if err != nil {
		return 0, err
	}
	hit := 0
	for i := 0; i < repeats; i++ {
		label, err := diag(ctx, y)
		if err != nil {
			return 0, err
		}
		if strings.EqualFold(strings.TrimSpace(label), truth) {
			hit++
		}
	}
	return float64(hit) / float64(repeats), nil
}

// --- structured path manipulation (handles both map keys and array indices) ---

// pathSeg is one step of a path: a map key, or (when key=="") an array index.
type pathSeg struct {
	key string
	idx int
}

func (s pathSeg) isIndex() bool { return s.key == "" }

// leafPaths collects the path to every scalar leaf in v.
func leafPaths(v any, prefix []pathSeg, out *[][]pathSeg) {
	switch t := v.(type) {
	case map[string]any:
		for k, cv := range t {
			leafPaths(cv, appendSeg(prefix, pathSeg{key: k}), out)
		}
	case []any:
		for i, el := range t {
			leafPaths(el, appendSeg(prefix, pathSeg{idx: i}), out)
		}
	default:
		*out = append(*out, appendSeg(prefix))
	}
}

// without returns a copy of v with the leaf at path removed. Maps and slices
// along the path are rebuilt so the input is never mutated.
func without(v any, path []pathSeg) any {
	if len(path) == 0 {
		return v
	}
	seg := path[0]

	if seg.isIndex() {
		s, ok := v.([]any)
		if !ok || seg.idx < 0 || seg.idx >= len(s) {
			return v
		}
		if len(path) == 1 {
			out := make([]any, 0, len(s)-1)
			out = append(out, s[:seg.idx]...)
			return append(out, s[seg.idx+1:]...)
		}
		out := make([]any, len(s))
		copy(out, s)
		out[seg.idx] = without(s[seg.idx], path[1:])
		return out
	}

	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		out[k] = val
	}
	if len(path) == 1 {
		delete(out, seg.key)
		return out
	}
	if child, ok := out[seg.key]; ok {
		out[seg.key] = without(child, path[1:])
	}
	return out
}

func appendSeg(prefix []pathSeg, segs ...pathSeg) []pathSeg {
	p := make([]pathSeg, 0, len(prefix)+len(segs))
	p = append(p, prefix...)
	return append(p, segs...)
}

func pathString(p []pathSeg) string {
	var b strings.Builder
	for i, s := range p {
		if s.isIndex() {
			fmt.Fprintf(&b, "[%d]", s.idx)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(s.key)
	}
	return b.String()
}

// isProtectedSegPath matches the same identity fields isProtectedPath protects,
// expressed as structured segments.
func isProtectedSegPath(p []pathSeg) bool {
	return isProtectedPath(pathString(p))
}
