package distill

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// fakeEmbed maps text to a bag-of-keywords vector, so cosine similarity reflects
// shared vocabulary — enough to test that L4 keeps symptom-grounded fields.
func fakeEmbed(vocab []string) EmbedFunc {
	return func(_ context.Context, text string) ([]float64, error) {
		text = strings.ToLower(text)
		v := make([]float64, len(vocab))
		for i, w := range vocab {
			v[i] = float64(strings.Count(text, w))
		}
		return v, nil
	}
}

func TestL4KeepsGroundedDropsUnrelated(t *testing.T) {
	embed := fakeEmbed([]string{"oomkilled", "memory", "image", "dns"})

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "p", "namespace": "ns"},
		"spec": map[string]any{
			"dnsPolicy": "ClusterFirst", // unrelated to an OOM symptom -> drop
			"containers": []any{map[string]any{
				"name":      "app",
				"resources": map[string]any{"limits": map[string]any{"memory": "64Mi"}}, // memory -> grounds OOM -> keep
				"extra":     "image pull stuff",                                         // image, not in the symptom -> drop
			}},
		},
		"status": map[string]any{
			"containerStatuses": []any{map[string]any{
				"lastState": map[string]any{"terminated": map[string]any{"reason": "OOMKilled"}},
			}},
		},
	}}

	out, err := DistillL4(context.Background(), obj, "memory", embed, 0.5)
	if err != nil {
		t.Fatalf("DistillL4: %v", err)
	}

	if _, found, _ := unstructured.NestedString(out.Object, "spec", "dnsPolicy"); found {
		t.Error("ungrounded spec.dnsPolicy should be dropped")
	}
	cs, _, _ := unstructured.NestedSlice(out.Object, "spec", "containers")
	c0 := cs[0].(map[string]any)
	if _, ok := c0["resources"]; !ok {
		t.Error("symptom-grounded resources (memory) should be kept")
	}
	if _, ok := c0["extra"]; ok {
		t.Error("ungrounded container field 'extra' should be dropped")
	}
	if c0["name"] != "app" {
		t.Error("identity field container name must be kept")
	}
	// status is the symptom and must survive untouched.
	if _, found, _ := unstructured.NestedSlice(out.Object, "status", "containerStatuses"); !found {
		t.Error("status (the symptom) must never be pruned")
	}
	if out.GetKind() != "Pod" {
		t.Error("apiVersion/kind must be kept")
	}
}

func TestL4NoSymptomFallsBackToL1(t *testing.T) {
	embed := fakeEmbed([]string{"x"})
	// A healthy resource (no status symptom) — L4 should not prune spec.
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "p", "namespace": "ns", "uid": "abc"},
		"spec":       map[string]any{"dnsPolicy": "ClusterFirst"},
	}}
	out, err := DistillL4(context.Background(), obj, "connectivity", embed, 0.5)
	if err != nil {
		t.Fatalf("DistillL4: %v", err)
	}
	if _, found, _ := unstructured.NestedString(out.Object, "spec", "dnsPolicy"); !found {
		t.Error("with no symptom, L4 should fall back to L1 and keep spec")
	}
	// L1 part still applied: uid stripped.
	if _, found, _ := unstructured.NestedString(out.Object, "metadata", "uid"); found {
		t.Error("L4 should still apply the L1 lossless strip (uid)")
	}
}
