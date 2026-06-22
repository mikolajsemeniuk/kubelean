package distill

import (
	"fmt"
	"math"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// L3 — corpus-entropy saliency. Unlike L0..L2 (pure per-object transforms), L3
// scores each field by how much its value varies across a CORPUS of resources of
// the same kind: a field that is constant across the corpus is template /
// boilerplate (low entropy → drop); a field that varies carries instance-specific
// signal (high entropy → keep). The corpus MUST mix fault classes: within a
// single class the deciding field is constant and would be wrongly dropped, so
// saliency is only meaningful across classes (and matches the deployment reality
// where the fault class is unknown a priori).
//
// L3 is goal-blind: randomized-but-irrelevant fields (podIP, the suffix in the
// service-account volume name, image tags) are high-entropy and survive even
// though they carry no diagnostic value. That gap is exactly what goal-conditioned
// L4 is meant to close.

// CorpusStats holds, per resource kind, the value distribution of every leaf
// path observed across the corpus. Built once, reused for every DistillL3 call.
type CorpusStats struct {
	byKind map[string]*kindStats
}

type kindStats struct {
	n     int                       // number of corpus instances of this kind
	paths map[string]map[string]int // leaf path -> serialized value -> count
}

// BuildCorpusStats computes the per-kind leaf-path value distributions for the
// corpus. Each member is L1-stripped first, so server-managed noise never enters
// the statistics (and the paths line up with what DistillL3 sees).
func BuildCorpusStats(corpus []*unstructured.Unstructured) *CorpusStats {
	s := &CorpusStats{byKind: map[string]*kindStats{}}
	for _, o := range corpus {
		c := o.DeepCopy()
		stripLossless(c)

		kind := c.GetKind()
		ks := s.byKind[kind]
		if ks == nil {
			ks = &kindStats{paths: map[string]map[string]int{}}
			s.byKind[kind] = ks
		}
		ks.n++

		flat := map[string]string{}
		flatten(c.Object, "", flat)
		for p, val := range flat {
			vals := ks.paths[p]
			if vals == nil {
				vals = map[string]int{}
				ks.paths[p] = vals
			}
			vals[val]++
		}
	}
	return s
}

// DistillL3 returns a distilled deep copy of obj: L1 lossless strip, then every
// leaf whose normalized corpus entropy is at or below threshold (i.e. effectively
// constant across the corpus) is dropped. threshold is in [0,1]; 0 drops only
// perfectly-constant fields, higher values also drop near-constant ones. The
// input is never mutated.
func DistillL3(obj *unstructured.Unstructured, stats *CorpusStats, threshold float64) *unstructured.Unstructured {
	out := obj.DeepCopy()
	stripLossless(out)

	kind := out.GetKind()
	filtered, _ := stats.filterValue(out.Object, "", kind, threshold)
	if m, ok := filtered.(map[string]any); ok {
		out.Object = m
	}
	return out
}

// filterValue rebuilds v keeping only salient leaves. It returns the filtered
// value and whether it should be kept (scalars by entropy, containers if they
// retain at least one child). Array element positions are preserved while a slice
// is being filtered, so they stay aligned with the corpus path indices.
func (s *CorpusStats) filterValue(v any, path, kind string, threshold float64) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, cv := range t {
			fv, keep := s.filterValue(cv, joinKey(path, k), kind, threshold)
			if keep {
				out[k] = fv
			}
		}
		return out, len(out) > 0
	case []any:
		out := make([]any, 0, len(t))
		for i, el := range t {
			fv, keep := s.filterValue(el, fmt.Sprintf("%s[%d]", path, i), kind, threshold)
			if keep {
				out = append(out, fv)
			}
		}
		return out, len(out) > 0
	default:
		if isProtectedPath(path) {
			return v, true
		}
		return v, s.entropy(kind, path) > threshold
	}
}

// entropy is the Shannon entropy of the value distribution at path (absence
// counts as its own value), normalized to [0,1] by log2(n). A kind seen fewer
// than twice has no discriminating power, so its fields are treated as salient.
func (s *CorpusStats) entropy(kind, path string) float64 {
	ks := s.byKind[kind]
	if ks == nil || ks.n < 2 {
		return 1.0
	}

	counts := ks.paths[path]
	present := 0
	for _, c := range counts {
		present += c
	}

	n := float64(ks.n)
	h := 0.0
	for _, c := range counts {
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	if absent := ks.n - present; absent > 0 {
		p := float64(absent) / n
		h -= p * math.Log2(p)
	}

	max := math.Log2(n)
	if max == 0 {
		return 0
	}
	return h / max
}

// flatten walks v and records every scalar leaf as path -> serialized value,
// using dotted keys for maps and [i] indices for slices.
func flatten(v any, path string, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, cv := range t {
			flatten(cv, joinKey(path, k), out)
		}
	case []any:
		for i, el := range t {
			flatten(el, fmt.Sprintf("%s[%d]", path, i), out)
		}
	default:
		out[path] = fmt.Sprintf("%v", v)
	}
}

func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// isProtectedPath marks fields that keep the resource identifiable and must never
// be entropy-dropped (apiVersion/kind are constant across a kind → entropy 0).
func isProtectedPath(path string) bool {
	switch path {
	case "apiVersion", "kind", "metadata.name", "metadata.namespace":
		return true
	}
	return false
}
