package distill

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// containerDefaultFields are per-container fields the API server injects with
// well-known defaults. A static policy drops them unconditionally. Note that
// imagePullPolicy is RCA-critical for ImagePull faults: dropping it here is
// exactly the kind of static over-pruning that goal-conditioned L4 will fix,
// which is the point of the static-vs-goal-conditioned ablation (H3).
var containerDefaultFields = []string{
	"terminationMessagePath",
	"terminationMessagePolicy",
	"imagePullPolicy",
}

// statusKeepKeys are the RCA-critical status fields kept at L2. Everything else
// under status is dropped as boilerplate. Unlike kubectl-neat (which strips the
// whole status), we keep the fields the field-class policy marks always-keep:
// conditions, container statuses (restartCount, lastState.terminated, ...).
var statusKeepKeys = map[string]bool{
	"phase":                 true,
	"conditions":            true,
	"containerStatuses":     true,
	"initContainerStatuses": true,
}

const defaultTokenMountPath = "/var/run/secrets/kubernetes.io/serviceaccount"

// keepAnnotations is the allow-list of annotation keys L2 retains. Everything
// else is boilerplate (human-oriented notes, controller bookkeeping, stale
// incident annotations) that both inflates tokens and acts as a semantic
// distractor for RCA. Goal-conditioned L4 may reinstate specific keys.
var keepAnnotations = map[string]bool{}

// stripStaticBuckets applies the L2 transform in place (on top of L1).
func stripStaticBuckets(o *unstructured.Unstructured) {
	m := o.Object
	stripPodDefaults(m)
	stripServiceAccountPlumbing(m)
	stripAnnotations(m)
	pruneStatus(m)
}

// stripAnnotations removes every annotation not in keepAnnotations, dropping the
// annotations map if it empties. This is the rule that removes semantic
// distractors, not just token bloat.
func stripAnnotations(m map[string]interface{}) {
	ann, found, err := unstructured.NestedMap(m, "metadata", "annotations")
	if !found || err != nil {
		return
	}
	for k := range ann {
		if !keepAnnotations[k] {
			delete(ann, k)
		}
	}
	if len(ann) == 0 {
		unstructured.RemoveNestedField(m, "metadata", "annotations")
		return
	}
	_ = unstructured.SetNestedMap(m, ann, "metadata", "annotations")
}

// stripPodDefaults removes spec-level and per-container fields whose value
// equals the API server's injected default.
func stripPodDefaults(m map[string]interface{}) {
	stripDefaultString(m, "Always", "spec", "restartPolicy")
	stripDefaultString(m, "ClusterFirst", "spec", "dnsPolicy")
	stripDefaultString(m, "default-scheduler", "spec", "schedulerName")
	stripDefaultNumber(m, 30, "spec", "terminationGracePeriodSeconds")
	removeIfEmptyMap(m, "spec", "securityContext")

	forEachContainer(m, func(c map[string]interface{}) {
		for _, f := range containerDefaultFields {
			delete(c, f)
		}
		removeIfEmptyMap(c, "resources")
	})
}

// stripServiceAccountPlumbing removes the default projected service-account
// token volume and its matching mounts, which the server injects into every
// pod and which carry no diagnostic signal.
func stripServiceAccountPlumbing(m map[string]interface{}) {
	if volumes, found, err := unstructured.NestedSlice(m, "spec", "volumes"); found && err == nil {
		kept := volumes[:0]
		for _, v := range volumes {
			if vm, ok := v.(map[string]interface{}); ok {
				if name, _, _ := unstructured.NestedString(vm, "name"); strings.HasPrefix(name, "kube-api-access-") {
					continue
				}
			}
			kept = append(kept, v)
		}
		setOrRemoveSlice(m, kept, "spec", "volumes")
	}

	forEachContainer(m, func(c map[string]interface{}) {
		mounts, found, err := unstructured.NestedSlice(c, "volumeMounts")
		if !found || err != nil {
			return
		}
		kept := mounts[:0]
		for _, mt := range mounts {
			if mtm, ok := mt.(map[string]interface{}); ok {
				if mp, _, _ := unstructured.NestedString(mtm, "mountPath"); mp == defaultTokenMountPath {
					continue
				}
			}
			kept = append(kept, mt)
		}
		setOrRemoveSlice(c, kept, "volumeMounts")
	})
}

// pruneStatus keeps only the RCA-critical status fields.
func pruneStatus(m map[string]interface{}) {
	status, found, err := unstructured.NestedMap(m, "status")
	if !found || err != nil {
		return
	}
	for k := range status {
		if !statusKeepKeys[k] {
			delete(status, k)
		}
	}
	if len(status) == 0 {
		unstructured.RemoveNestedField(m, "status")
		return
	}
	_ = unstructured.SetNestedMap(m, status, "status")
}

// --- small helpers over unstructured maps ---

// forEachContainer applies fn to each container map under spec.containers and
// spec.initContainers, writing the mutated slice back.
func forEachContainer(m map[string]interface{}, fn func(c map[string]interface{})) {
	for _, key := range []string{"containers", "initContainers"} {
		containers, found, err := unstructured.NestedSlice(m, "spec", key)
		if !found || err != nil {
			continue
		}
		for i, c := range containers {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			fn(cm)
			containers[i] = cm
		}
		_ = unstructured.SetNestedSlice(m, containers, "spec", key)
	}
}

func stripDefaultString(m map[string]interface{}, def string, path ...string) {
	if v, found, err := unstructured.NestedString(m, path...); found && err == nil && v == def {
		unstructured.RemoveNestedField(m, path...)
	}
}

func stripDefaultNumber(m map[string]interface{}, def float64, path ...string) {
	v, found, err := unstructured.NestedFieldNoCopy(m, path...)
	if !found || err != nil {
		return
	}
	if f, ok := toFloat(v); ok && f == def {
		unstructured.RemoveNestedField(m, path...)
	}
}

func removeIfEmptyMap(m map[string]interface{}, path ...string) {
	if v, found, err := unstructured.NestedMap(m, path...); found && err == nil && len(v) == 0 {
		unstructured.RemoveNestedField(m, path...)
	}
}

// setOrRemoveSlice writes s back at path, or removes the field if s is empty.
func setOrRemoveSlice(m map[string]interface{}, s []interface{}, path ...string) {
	if len(s) == 0 {
		unstructured.RemoveNestedField(m, path...)
		return
	}
	_ = unstructured.SetNestedSlice(m, s, path...)
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
