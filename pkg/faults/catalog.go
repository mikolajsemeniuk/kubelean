package faults

import (
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FaultClass renders instances of one fault class. Difficulty is intrinsic:
// single-Pod classes name the cause in the status (Easy, keyword-extractable),
// while bundle classes leave the symptom resource healthy and put the cause in a
// sibling resource (Hard, the root cause must be inferred) — the regime where
// distillation can lift accuracy above a sub-100% baseline (H1 headline).
type FaultClass struct {
	Label          string
	Category       string // Cloud-OpsBench category (derived taxonomy)
	Goal           string // L4 goal-condition
	Scope          string // "single" | "bundle"
	Difficulty     Difficulty
	OffendingField string
	Provenance     string
	Render         func(p Params) []*unstructured.Unstructured
}

func (p Params) image() string {
	return fmt.Sprintf("%s/%s:%s", p.Registry, p.App, p.Tag)
}

// Catalog is the full fault-class catalog, anchored to the Cloud-OpsBench
// taxonomy (derived, cited) and OperAID scenarios (MIT, github.com/
// EricssonResearch/operaid): the Config/Scaling/Network classes are derived by
// hand from OperAID's scenario_{2_configmap,3_upf_scale,1_netpol} faults.
func Catalog() []FaultClass {
	return []FaultClass{
		{
			Label:          "CrashLoopBackOff",
			Category:       "4 Runtime",
			Goal:           "crashloop",
			Scope:          "single",
			Difficulty:     Easy,
			OffendingField: "status.containerStatuses[0].lastState.terminated",
			Provenance:     "Cloud-OpsBench Runtime",
			Render:         renderCrashLoop,
		},
		{
			Label:          "OOMKilled",
			Category:       "4 Runtime",
			Goal:           "oom",
			Scope:          "single",
			Difficulty:     Easy,
			OffendingField: "status.containerStatuses[0].lastState.terminated.reason",
			Provenance:     "Cloud-OpsBench Runtime", Render: renderOOM,
		},
		{
			Label:          "ImagePullBackOff_BadImage",
			Category:       "3 Startup",
			Goal:           "imagepull",
			Scope:          "single",
			Difficulty:     Easy,
			OffendingField: "spec.containers[0].image",
			Provenance:     "Cloud-OpsBench Startup",
			Render:         renderImagePullBadImage,
		},
		{
			Label:          "ImagePullBackOff_NoAuth",
			Category:       "3 Startup",
			Goal:           "imagepull",
			Scope:          "single",
			Difficulty:     Hard,
			OffendingField: "spec.imagePullSecrets",
			Provenance:     "Cloud-OpsBench Startup",
			Render:         renderImagePullNoAuth,
		},
		{
			Label:          "ReadinessProbeFailure",
			Category:       "4 Runtime",
			Goal:           "probe",
			Scope:          "single",
			Difficulty:     Hard,
			OffendingField: "spec.containers[0].readinessProbe",
			Provenance:     "Cloud-OpsBench Runtime", Render: renderReadiness,
		},
		{
			Label:          "Pending_InsufficientResources",
			Category:       "2 Scheduling",
			Goal:           "scheduling",
			Scope:          "single",
			Difficulty:     Easy,
			OffendingField: "spec.containers[0].resources.requests",
			Provenance:     "Cloud-OpsBench Scheduling", Render: renderPendingResources,
		},
		{
			Label:          "CreateContainerConfigError",
			Category:       "3 Startup",
			Goal:           "config",
			Scope:          "single",
			Difficulty:     Easy,
			OffendingField: "spec.containers[0].envFrom",
			Provenance:     "OperAID scenario_2_configmap (MIT)", Render: renderConfigError,
		},
		{
			Label:          "Scaling_ZeroReplicas",
			Category:       "6 Performance",
			Goal:           "scaling",
			Scope:          "single",
			Difficulty:     Hard,
			OffendingField: "spec.replicas",
			Provenance:     "OperAID scenario_3_scale (MIT)", Render: renderScaleZero,
		},
		{
			Label:          "Service_SelectorMismatch",
			Category:       "5 Service routing",
			Goal:           "connectivity",
			Scope:          "bundle",
			Difficulty:     Hard,
			OffendingField: "Service.spec.selector",
			Provenance:     "Cloud-OpsBench Service routing", Render: renderServiceMismatch,
		},
		{
			Label:          "NetworkPolicy_BlocksIngress",
			Category:       "5 Service routing",
			Goal:           "connectivity",
			Scope:          "bundle",
			Difficulty:     Hard,
			OffendingField: "NetworkPolicy.spec.ingress",
			Provenance:     "OperAID scenario_1_netpol (MIT)", Render: renderNetpolBlock,
		},
	}
}

func renderCrashLoop(p Params) []*unstructured.Unstructured {
	pod := basePod(p)
	setConditions(pod, []any{condition("Ready", "False", "ContainersNotReady", "containers with unready status: ["+p.App+"]")})
	setContainerStatus(pod, map[string]any{
		"name": p.App, "image": p.image(), "ready": false, "started": false, "restartCount": int64(3 + p.rng.Intn(8)),
		"state":     map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff", "message": "back-off 5m0s restarting failed container=" + p.App}},
		"lastState": map[string]any{"terminated": map[string]any{"reason": "Error", "exitCode": int64(1), "finishedAt": "2026-06-15T09:14:00Z"}},
	})

	return []*unstructured.Unstructured{pod}
}

func renderOOM(p Params) []*unstructured.Unstructured {
	pod := basePod(p)
	setConditions(pod, []any{condition("Ready", "False", "ContainersNotReady", "containers with unready status: ["+p.App+"]")})
	setContainerStatus(pod, map[string]any{
		"name": p.App, "image": p.image(), "ready": false, "started": false, "restartCount": int64(4 + p.rng.Intn(9)),
		"state":     map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff", "message": "back-off restarting failed container=" + p.App}},
		"lastState": map[string]any{"terminated": map[string]any{"reason": "OOMKilled", "exitCode": int64(137), "finishedAt": "2026-06-15T09:14:00Z"}},
	})

	return []*unstructured.Unstructured{pod}
}

func renderImagePullBadImage(p Params) []*unstructured.Unstructured {
	pod := basePod(p)
	bad := p.image() + "-hotfix"
	firstContainer(pod, func(c map[string]any) { c["image"] = bad })
	setPhase(pod, "Pending")
	setConditions(pod, []any{condition("Ready", "False", "ContainersNotReady", "containers with unready status: ["+p.App+"]")})
	setContainerStatus(pod, map[string]any{
		"name": p.App, "image": bad, "imageID": "", "ready": false, "started": false, "restartCount": int64(0),
		"state": map[string]any{"waiting": map[string]any{"reason": "ImagePullBackOff", "message": "Back-off pulling image \"" + bad + "\""}},
	})
	return []*unstructured.Unstructured{pod}
}

func renderImagePullNoAuth(p Params) []*unstructured.Unstructured {
	pod := basePod(p)
	setPhase(pod, "Pending")
	setConditions(pod, []any{condition("Ready", "False", "ContainersNotReady", "containers with unready status: ["+p.App+"]")})
	// Generic ErrImagePull symptom; the deciding evidence is the MISSING
	// imagePullSecrets on a private registry, not the (generic) message.
	setContainerStatus(pod, map[string]any{
		"name": p.App, "image": p.image(), "imageID": "", "ready": false, "started": false, "restartCount": int64(0),
		"state": map[string]any{"waiting": map[string]any{"reason": "ErrImagePull", "message": "failed to pull and unpack image: 401 Unauthorized"}},
	})
	return []*unstructured.Unstructured{pod}
}

func renderReadiness(p Params) []*unstructured.Unstructured {
	pod := basePod(p)
	firstContainer(pod, func(c map[string]any) {
		c["readinessProbe"] = map[string]any{
			"httpGet":             map[string]any{"path": "/ready", "port": int64(8080), "scheme": "HTTP"},
			"initialDelaySeconds": int64(3), "periodSeconds": int64(10), "timeoutSeconds": int64(1), "failureThreshold": int64(3),
		}
	})
	setConditions(pod, []any{condition("Ready", "False", "ContainersNotReady", "containers with unready status: ["+p.App+"]")})
	setContainerStatus(pod, map[string]any{
		"name": p.App, "image": p.image(), "ready": false, "started": true, "restartCount": int64(0),
		"state": map[string]any{"running": map[string]any{"startedAt": "2026-06-15T09:12:34Z"}},
	})
	return []*unstructured.Unstructured{pod}
}

func renderPendingResources(p Params) []*unstructured.Unstructured {
	pod := basePod(p)
	unstructured.RemoveNestedField(pod.Object, "spec", "nodeName")
	firstContainer(pod, func(c map[string]any) {
		c["resources"] = map[string]any{
			"requests": map[string]any{"cpu": "16", "memory": "64Gi"},
			"limits":   map[string]any{"cpu": "16", "memory": "64Gi"},
		}
	})
	setPhase(pod, "Pending")
	setConditions(pod, []any{condition("PodScheduled", "False", "Unschedulable", "0/5 nodes are available: 5 Insufficient cpu, 5 Insufficient memory")})
	unstructured.RemoveNestedField(pod.Object, "status", "containerStatuses")
	unstructured.RemoveNestedField(pod.Object, "status", "hostIP")
	unstructured.RemoveNestedField(pod.Object, "status", "podIP")
	return []*unstructured.Unstructured{pod}
}

func renderConfigError(p Params) []*unstructured.Unstructured {
	pod := basePod(p)
	missing := p.App + "-extra-config-missing"
	firstContainer(pod, func(c map[string]any) {
		c["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": missing}}}
	})
	setPhase(pod, "Pending")
	setConditions(pod, []any{condition("Ready", "False", "ContainersNotReady", "containers with unready status: ["+p.App+"]")})
	setContainerStatus(pod, map[string]any{
		"name": p.App, "image": p.image(), "ready": false, "started": false, "restartCount": int64(0),
		"state": map[string]any{"waiting": map[string]any{"reason": "CreateContainerConfigError", "message": "configmap \"" + missing + "\" not found"}},
	})
	return []*unstructured.Unstructured{pod}
}

// --- Deployment renderer ---

func renderScaleZero(p Params) []*unstructured.Unstructured {
	dep := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{
			"name": p.App, "namespace": p.Namespace, "generation": int64(2),
			"uid":         fmt.Sprintf("%08x-0000-4000-8000-%012x", p.rng.Int31(), p.rng.Int63()&0xffffffffffff),
			"labels":      map[string]any{"app": p.App},
			"annotations": map[string]any{"deployment.kubernetes.io/revision": "3"},
		},
		"spec": map[string]any{
			"replicas": int64(0),
			"selector": map[string]any{"matchLabels": map[string]any{"app": p.App}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": p.App}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": p.App, "image": p.image()}}},
			},
		},
		"status": map[string]any{"observedGeneration": int64(2), "replicas": int64(0), "availableReplicas": int64(0)},
	}}
	return []*unstructured.Unstructured{dep}
}

// --- bundle renderers (symptom resource healthy, cause in sibling) ---

func renderServiceMismatch(p Params) []*unstructured.Unstructured {
	pod := basePod(p) // healthy, labels app:<App>
	svc := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"name": p.App, "namespace": p.Namespace, "labels": map[string]any{"app": p.App}},
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": map[string]any{"app": p.App + "-v2"}, // typo'd selector → matches no pods
			"ports":    []any{map[string]any{"name": "http", "port": int64(80), "targetPort": int64(8080), "protocol": "TCP"}},
		},
	}}
	return []*unstructured.Unstructured{svc, pod}
}

func renderNetpolBlock(p Params) []*unstructured.Unstructured {
	pod := basePod(p) // healthy app pod
	np := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "block-to-" + p.App, "namespace": p.Namespace},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": map[string]any{"app": p.App}},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{map[string]any{
				"from":  []any{map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app": "nrf"}}}},
				"ports": []any{map[string]any{"port": int64(7777), "protocol": "TCP"}},
			}},
		},
	}}
	return []*unstructured.Unstructured{np, pod}
}
