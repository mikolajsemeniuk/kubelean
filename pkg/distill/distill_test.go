package distill

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// loadFixture parses the inlined golden fixture (see fixtures_test.go); name
// selects which one. The suite has no filesystem dependency.
func loadFixture(t *testing.T, name string) *unstructured.Unstructured {
	t.Helper()
	fixtures := map[string]string{"pod_crashloop.yaml": crashloopFixture}
	src, ok := fixtures[name]
	if !ok {
		t.Fatalf("unknown fixture %q", name)
	}
	obj, err := FromYAML([]byte(src))
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return obj
}

func has(t *testing.T, obj *unstructured.Unstructured, path ...string) bool {
	t.Helper()
	_, found, err := unstructured.NestedFieldNoCopy(obj.Object, path...)
	if err != nil {
		t.Fatalf("lookup %v: %v", path, err)
	}
	return found
}

func firstContainer(t *testing.T, obj *unstructured.Unstructured) map[string]any {
	t.Helper()
	cs, found, err := unstructured.NestedSlice(obj.Object, "spec", "containers")
	if !found || err != nil || len(cs) == 0 {
		t.Fatalf("spec.containers missing: found=%v err=%v", found, err)
	}
	cm, ok := cs[0].(map[string]any)
	if !ok {
		t.Fatalf("spec.containers[0] not a map")
	}
	return cm
}

func firstContainerStatus(t *testing.T, obj *unstructured.Unstructured) map[string]any {
	t.Helper()
	cs, found, err := unstructured.NestedSlice(obj.Object, "status", "containerStatuses")
	if !found || err != nil || len(cs) == 0 {
		t.Fatalf("status.containerStatuses missing: found=%v err=%v", found, err)
	}
	cm, ok := cs[0].(map[string]any)
	if !ok {
		t.Fatalf("status.containerStatuses[0] not a map")
	}
	return cm
}

// TestDistillPurity asserts Distill never mutates its input.
func TestDistillPurity(t *testing.T) {
	obj := loadFixture(t, "pod_crashloop.yaml")
	before, err := ToYAML(obj)
	if err != nil {
		t.Fatal(err)
	}
	_ = Distill(obj, Profile{Level: L2StaticBuckets})
	after, err := ToYAML(obj)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("Distill mutated its input")
	}
}

func TestL1Lossless(t *testing.T) {
	obj := loadFixture(t, "pod_crashloop.yaml")
	out := Distill(obj, Profile{Level: L1Lossless})

	// Server-managed noise must be gone.
	for _, path := range [][]string{
		{"metadata", "managedFields"},
		{"metadata", "resourceVersion"},
		{"metadata", "uid"},
		{"metadata", "generation"},
		{"metadata", "creationTimestamp"},
		{"metadata", "annotations", lastAppliedAnnotation},
	} {
		if has(t, out, path...) {
			t.Errorf("L1 should remove %v", path)
		}
	}

	// A non-noise annotation must survive (annotations map not nuked wholesale).
	if !has(t, out, "metadata", "annotations", "kubernetes.io/psp") {
		t.Errorf("L1 dropped a non-noise annotation")
	}

	// Always-keep / RCA-critical fields must remain untouched at L1.
	if !has(t, out, "spec", "containers") {
		t.Errorf("L1 dropped spec.containers")
	}
	cstat := firstContainerStatus(t, out)
	if r, _, _ := unstructured.NestedString(cstat, "lastState", "terminated", "reason"); r != "Error" {
		t.Errorf("L1 lost lastState.terminated.reason, got %q", r)
	}
}

func TestL2StaticBuckets(t *testing.T) {
	obj := loadFixture(t, "pod_crashloop.yaml")
	out := Distill(obj, Profile{Level: L2StaticBuckets})

	// Spec-level defaults removed.
	for _, path := range [][]string{
		{"spec", "restartPolicy"},
		{"spec", "dnsPolicy"},
		{"spec", "schedulerName"},
		{"spec", "terminationGracePeriodSeconds"},
		{"spec", "securityContext"},
	} {
		if has(t, out, path...) {
			t.Errorf("L2 should remove %v", path)
		}
	}

	// Per-container defaults removed.
	c := firstContainer(t, out)
	for _, f := range []string{"terminationMessagePath", "terminationMessagePolicy", "imagePullPolicy"} {
		if _, ok := c[f]; ok {
			t.Errorf("L2 should remove container.%s", f)
		}
	}

	// Default service-account volume + mount gone; real ones kept.
	vols, _, _ := unstructured.NestedSlice(out.Object, "spec", "volumes")
	if len(vols) != 1 {
		t.Errorf("L2 should leave exactly the 'config' volume, got %d", len(vols))
	}
	mounts, _, _ := unstructured.NestedSlice(c, "volumeMounts")
	if len(mounts) != 1 {
		t.Errorf("L2 should leave exactly the 'config' mount, got %d", len(mounts))
	}

	// Status boilerplate pruned...
	for _, path := range [][]string{
		{"status", "hostIP"},
		{"status", "podIP"},
		{"status", "qosClass"},
		{"status", "startTime"},
	} {
		if has(t, out, path...) {
			t.Errorf("L2 should prune %v", path)
		}
	}
	// ...but RCA-critical status kept.
	if !has(t, out, "status", "conditions") {
		t.Errorf("L2 dropped status.conditions")
	}
	cstat := firstContainerStatus(t, out)
	if rc, _, _ := unstructured.NestedFieldNoCopy(cstat, "restartCount"); rc == nil {
		t.Errorf("L2 dropped restartCount")
	}
	if reason, _, _ := unstructured.NestedString(cstat, "state", "waiting", "reason"); reason != "CrashLoopBackOff" {
		t.Errorf("L2 lost state.waiting.reason, got %q", reason)
	}
	if code, _, _ := unstructured.NestedFieldNoCopy(cstat, "lastState", "terminated", "exitCode"); code == nil {
		t.Errorf("L2 dropped lastState.terminated.exitCode")
	}

	// Always-keep container fields untouched.
	if img, _, _ := unstructured.NestedString(c, "image"); img == "" {
		t.Errorf("L2 dropped container.image")
	}
	if _, ok := c["env"]; !ok {
		t.Errorf("L2 dropped container.env")
	}
	if _, ok := c["livenessProbe"]; !ok {
		t.Errorf("L2 dropped container.livenessProbe")
	}
}

// TestTokenReductionOrdering asserts L0 >= L1 >= L2 in token cost, using the
// deterministic FakeCounter so the test needs no running Ollama.
func TestTokenReductionOrdering(t *testing.T) {
	obj := loadFixture(t, "pod_crashloop.yaml")
	ctx := context.Background()
	var fc FakeCounter

	count := func(level Level) int {
		out := Distill(obj, Profile{Level: level})
		y, err := ToYAML(out)
		if err != nil {
			t.Fatal(err)
		}
		n, err := fc.Count(ctx, y)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	l0, l1, l2 := count(L0Raw), count(L1Lossless), count(L2StaticBuckets)
	if !(l0 > l1 && l1 > l2) {
		t.Fatalf("expected strictly decreasing token cost L0>L1>L2, got %d, %d, %d", l0, l1, l2)
	}
	t.Logf("token cost (words): L0=%d L1=%d L2=%d (L2 is %.0f%% of L0)", l0, l1, l2, 100*float64(l2)/float64(l0))
}
