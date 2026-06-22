package distill

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func l3Pod(node, ip, reason string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "p-" + node, "namespace": "ns"},
		"spec": map[string]any{
			"dnsPolicy": "ClusterFirst", // constant across corpus
			"nodeName":  node,           // varies across corpus
		},
		"status": map[string]any{
			"podIP":      ip,     // varies
			"lastReason": reason, // varies (the "diagnostic" field)
		},
	}}
}

func TestL3DropsConstantKeepsVarying(t *testing.T) {
	corpus := []*unstructured.Unstructured{
		l3Pod("a", "10.0.0.1", "Error"),
		l3Pod("b", "10.0.0.2", "OOMKilled"),
		l3Pod("c", "10.0.0.3", "Completed"),
	}
	stats := BuildCorpusStats(corpus)

	out := DistillL3(l3Pod("a", "10.0.0.1", "Error"), stats, 0.0)

	if _, found, _ := unstructured.NestedString(out.Object, "spec", "dnsPolicy"); found {
		t.Error("constant field spec.dnsPolicy should have been dropped")
	}
	if v, _, _ := unstructured.NestedString(out.Object, "spec", "nodeName"); v != "a" {
		t.Errorf("varying field spec.nodeName should be kept, got %q", v)
	}
	if v, _, _ := unstructured.NestedString(out.Object, "status", "lastReason"); v != "Error" {
		t.Errorf("varying field status.lastReason should be kept, got %q", v)
	}
	// apiVersion/kind are constant across the corpus but must survive (protected).
	if out.GetKind() != "Pod" || out.GetAPIVersion() != "v1" {
		t.Error("protected apiVersion/kind must not be entropy-dropped")
	}
}

func TestL3SingletonCorpusKeepsEverything(t *testing.T) {
	// n < 2 → no discriminating power → nothing is dropped.
	stats := BuildCorpusStats([]*unstructured.Unstructured{l3Pod("a", "10.0.0.1", "Error")})
	out := DistillL3(l3Pod("a", "10.0.0.1", "Error"), stats, 0.0)
	if _, found, _ := unstructured.NestedString(out.Object, "spec", "dnsPolicy"); !found {
		t.Error("with a singleton corpus no field should be dropped")
	}
}
