// Package faults generates RCA benchmark scenarios. Its core lever is Inflate:
// it adds server-faithful noise to a resource, scaled by a level knob, so the
// same injected fault can be presented at increasing degrees of bloat. The
// noise lives in fields the API server really populates (managedFields, junk
// annotations/labels, sidecar plumbing) — exactly the fields kubelean's L1/L2
// distillation strips — which makes "noise level" the X-axis for testing H1
// (does burying the deciding field in bloat degrade RCA?).
package faults

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Inflate returns a deep copy of obj with two independently-scaled kinds of
// noise, so the two mechanisms can be tested separately:
//
//   - volume: managedFields ballooning — the documented `-o yaml` bloat
//     (kubernetes#90933). It lives ENTIRELY in fields L1/L2 strip, so under
//     distillation it vanishes. This isolates the positional / "buried signal"
//     effect: raw accuracy should fall with volume while distilled stays flat.
//   - mislead: stale ops annotations that name OTHER fault classes, plus a
//     healthy sidecar. These survive L2 (it does not strip arbitrary
//     annotations), so they isolate distraction-by-content — and answer the
//     design question of whether distill should also strip annotations.
//
// seed varies cosmetic content. The injected fault's deciding field is never
// touched.
func Inflate(obj *unstructured.Unstructured, volume, mislead, seed int) *unstructured.Unstructured {
	out := obj.DeepCopy()
	m := out.Object
	if volume > 0 {
		// managedFields is the only large sink L2 strips in full, so volume
		// noise lives here exclusively → L2 output stays flat across volume.
		inflateManagedFields(m, volume)
	}

	if mislead > 0 {
		inflateAnnotations(m, mislead, seed)
		inflateLabels(m, mislead, seed)
		addSidecar(m, mislead, seed)
	}

	return out
}

// inflateManagedFields appends realistic managedFields entries — the single
// largest documented source of `-o yaml` bloat (kubernetes/kubernetes#90933).
func inflateManagedFields(m map[string]any, level int) {
	managers := []string{"kube-controller-manager", "kubelet", "kube-scheduler", "metrics-server", "cluster-autoscaler", "kyverno", "argocd-application-controller", "flux-source-controller"}
	existing, _, _ := unstructured.NestedSlice(m, "metadata", "managedFields")
	for i := 0; i < level*len(managers); i++ {
		mgr := managers[i%len(managers)]
		entry := map[string]any{
			"manager":    mgr,
			"operation":  "Update",
			"apiVersion": "v1",
			"time":       fmt.Sprintf("2026-06-1%dT0%d:%02d:%02dZ", i%9, i%9, i%60, i%60),
			"fieldsType": "FieldsV1",
			"fieldsV1": map[string]any{
				"f:metadata": map[string]any{
					"f:labels":      map[string]any{fmt.Sprintf("f:noise-%d", i): map[string]any{}},
					"f:annotations": map[string]any{fmt.Sprintf("f:ops.internal/seen-%d", i): map[string]any{}},
				},
				"f:status": map[string]any{
					"f:conditions":        map[string]any{},
					"f:containerStatuses": map[string]any{},
				},
			},
		}
		if i%3 == 0 {
			entry["subresource"] = "status"
		}
		existing = append(existing, entry)
	}
	_ = unstructured.SetNestedSlice(m, existing, "metadata", "managedFields")
}

// inflateAnnotations adds boilerplate ops annotations. Some intentionally
// mention OTHER fault classes (stale incident notes), a realistic distractor
// that L1/L2 do not specifically target but which co-occurs with the bloat.
func inflateAnnotations(m map[string]any, level, seed int) {
	ann, _, _ := unstructured.NestedStringMap(m, "metadata", "annotations")
	if ann == nil {
		ann = map[string]string{}
	}
	distractors := []string{
		"last-incident: 2026-05-30 ImagePullBackOff on registry mirror, resolved",
		"runbook: https://wiki.internal/runbooks/readiness-probe-flapping",
		"note: scheduling was tight last week, watch resource requests",
		"observed: occasional OOM on the metrics sidecar, unrelated",
	}
	for i := 0; i < level*4; i++ {
		key := fmt.Sprintf("ops.internal/seen-%d-%d", seed, i)
		ann[key] = distractors[(seed+i)%len(distractors)]
	}
	_ = unstructured.SetNestedStringMap(m, ann, "metadata", "annotations")
}

func inflateLabels(m map[string]any, level, seed int) {
	lab, _, _ := unstructured.NestedStringMap(m, "metadata", "labels")
	if lab == nil {
		lab = map[string]string{}
	}
	for i := 0; i < level*3; i++ {
		lab[fmt.Sprintf("noise-%d-%d", seed, i)] = fmt.Sprintf("v%d", (seed+i)%97)
	}
	_ = unstructured.SetNestedStringMap(m, lab, "metadata", "labels")
}

// addSidecar injects a healthy sidecar container plus its container status —
// common in real clusters (service mesh, log shippers) and pure noise for RCA.
func addSidecar(m map[string]any, level, seed int) {
	if level <= 0 {
		return
	}
	containers, _, _ := unstructured.NestedSlice(m, "spec", "containers")
	sidecar := map[string]any{
		"name":            "istio-proxy",
		"image":           "docker.io/istio/proxyv2:1.24.2",
		"imagePullPolicy": "IfNotPresent",
		"resources":       map[string]any{"limits": map[string]any{"cpu": "2", "memory": "1Gi"}, "requests": map[string]any{"cpu": "100m", "memory": "128Mi"}},
		"ports":           []any{map[string]any{"containerPort": int64(15090), "name": "http-envoy-prom", "protocol": "TCP"}},
	}
	containers = append(containers, sidecar)
	_ = unstructured.SetNestedSlice(m, containers, "spec", "containers")

	statuses, _, _ := unstructured.NestedSlice(m, "status", "containerStatuses")
	sidecarStatus := map[string]any{
		"name":         "istio-proxy",
		"image":        "docker.io/istio/proxyv2:1.24.2",
		"ready":        true,
		"started":      true,
		"restartCount": int64(0),
		"state":        map[string]any{"running": map[string]any{"startedAt": "2026-06-17T08:00:00Z"}},
	}
	statuses = append(statuses, sidecarStatus)
	_ = unstructured.SetNestedSlice(m, statuses, "status", "containerStatuses")
}
