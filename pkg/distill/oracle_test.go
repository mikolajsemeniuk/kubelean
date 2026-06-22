package distill

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestWithoutRemovesLeafInArray(t *testing.T) {
	root := map[string]any{
		"spec": map[string]any{
			"containers": []any{
				map[string]any{"name": "a", "image": "img:1"},
			},
		},
	}
	// remove spec.containers[0].image
	path := []pathSeg{{key: "spec"}, {key: "containers"}, {idx: 0}, {key: "image"}}
	out := without(root, path).(map[string]any)

	cs, _, _ := unstructured.NestedSlice(out, "spec", "containers")
	if _, ok := cs[0].(map[string]any)["image"]; ok {
		t.Error("image should have been removed")
	}
	if cs[0].(map[string]any)["name"] != "a" {
		t.Error("sibling 'name' must be preserved")
	}
	// input not mutated
	in, _, _ := unstructured.NestedSlice(root, "spec", "containers")
	if _, ok := in[0].(map[string]any)["image"]; !ok {
		t.Error("without must not mutate the input")
	}
}

func TestOracleSaliencyFindsDecisiveField(t *testing.T) {
	// The model "diagnoses" OOMKilled iff the deciding field is present.
	diag := func(_ context.Context, y string) (string, error) {
		if strings.Contains(y, "OOMKilled") {
			return "OOMKilled", nil
		}
		return "Unknown", nil
	}

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "p", "namespace": "ns"},
		"spec":       map[string]any{"dnsPolicy": "ClusterFirst"},
		"status": map[string]any{
			"reason": "OOMKilled",
			"phase":  "Running",
		},
	}}

	sal, err := OracleSaliency(context.Background(), obj, "OOMKilled", diag, 1)
	if err != nil {
		t.Fatalf("OracleSaliency: %v", err)
	}

	var decisive float64
	var other float64
	for _, fs := range sal {
		switch fs.Path {
		case "status.reason":
			decisive = fs.Saliency
		case "spec.dnsPolicy":
			other = fs.Saliency
		}
	}
	if decisive <= 0 {
		t.Errorf("status.reason should be salient (>0), got %.2f", decisive)
	}
	if other != 0 {
		t.Errorf("spec.dnsPolicy should be non-salient (0), got %.2f", other)
	}
}
