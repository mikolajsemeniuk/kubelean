package distill

import (
	"context"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// L4 — goal-conditioned distillation via embedding-grounding. Unlike L2/L3 (which
// are fault-blind), L4 keeps the fields whose serialized form embeds close to the
// SYMPTOM the agent is diagnosing: the observable error text in status (condition
// reasons/messages, container waiting/terminated reasons) plus the goal hint.
// status itself is the symptom (the "question") and is always kept; spec and
// metadata fields are the candidate evidence, scored and pruned against it. This
// is query/fault-aware, so it can keep a field L2 statically drops (e.g.
// imagePullPolicy for an ImagePull fault) and drop a field L2 statically keeps.

var timestampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T`)

// DistillL4 returns a distilled deep copy of obj: L1 lossless strip, then prune
// every spec/metadata field whose cosine similarity to the symptom anchor is
// below threshold. status is never pruned (it is the symptom). identity fields
// (apiVersion/kind, metadata.name/namespace, container name) are always kept. The
// input is never mutated; embed is called once per anchor and once per candidate
// field.
func DistillL4(ctx context.Context, obj *unstructured.Unstructured, goal string, embed EmbedFunc, threshold float64) (*unstructured.Unstructured, error) {
	out := obj.DeepCopy()
	stripLossless(out)

	anchor := symptomAnchor(out, goal)
	// If status carries no symptom text, there is nothing reliable to ground
	// against (e.g. the healthy pod in a bundle) — fall back to the L1 result.
	if strings.TrimSpace(strings.TrimSuffix(anchor, goal)) == "" {
		return out, nil
	}
	av, err := embed(ctx, anchor)
	if err != nil {
		return nil, err
	}

	keep := func(key string, val any) (bool, error) {
		txt, err := ToYAML(&unstructured.Unstructured{Object: map[string]any{key: val}})
		if err != nil {
			return false, err
		}
		vec, err := embed(ctx, txt)
		if err != nil {
			return false, err
		}
		return cosine(av, vec) >= threshold, nil
	}

	if md, found, _ := unstructured.NestedMap(out.Object, "metadata"); found {
		if err := pruneMapByGrounding(md, map[string]bool{"name": true, "namespace": true}, keep); err != nil {
			return nil, err
		}
		_ = unstructured.SetNestedMap(out.Object, md, "metadata")
	}

	if spec, found, _ := unstructured.NestedMap(out.Object, "spec"); found {
		if err := pruneMapByGrounding(spec, map[string]bool{"containers": true, "initContainers": true}, keep); err != nil {
			return nil, err
		}
		for _, ck := range []string{"containers", "initContainers"} {
			cs, ok, _ := unstructured.NestedSlice(spec, ck)
			if !ok {
				continue
			}
			for i, c := range cs {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				if err := pruneMapByGrounding(cm, map[string]bool{"name": true}, keep); err != nil {
					return nil, err
				}
				cs[i] = cm
			}
			_ = unstructured.SetNestedSlice(spec, cs, ck)
		}
		_ = unstructured.SetNestedMap(out.Object, spec, "spec")
	}

	return out, nil
}

// pruneMapByGrounding deletes every non-protected key whose value fails the keep
// test (low grounding to the symptom).
func pruneMapByGrounding(m map[string]any, protected map[string]bool, keep func(string, any) (bool, error)) error {
	for k, v := range m {
		if protected[k] {
			continue
		}
		ok, err := keep(k, v)
		if err != nil {
			return err
		}
		if !ok {
			delete(m, k)
		}
	}
	return nil
}

// symptomAnchor builds the grounding query from the observable error text in
// status (reasons, messages, phase) plus the goal hint. Booleans, nulls, and
// timestamps are dropped as non-symptomatic noise.
func symptomAnchor(o *unstructured.Unstructured, goal string) string {
	var parts []string
	if status, found, _ := unstructured.NestedMap(o.Object, "status"); found {
		collectSymptomStrings(status, &parts)
	}
	parts = append(parts, goal)
	return strings.Join(parts, " ")
}

func collectSymptomStrings(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		for _, cv := range t {
			collectSymptomStrings(cv, out)
		}
	case []any:
		for _, el := range t {
			collectSymptomStrings(el, out)
		}
	case string:
		s := strings.TrimSpace(t)
		if s == "" || s == "True" || s == "False" || s == "null" || timestampRe.MatchString(s) {
			return
		}
		*out = append(*out, s)
	}
}
