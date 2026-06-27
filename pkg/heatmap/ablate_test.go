package heatmap

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const deployYAML = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: production
  labels:
    app: web
spec:
  replicas: 3
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web-frontend
    spec:
      containers:
        - name: web
          image: nginx:1.25
          envFrom:
            - configMapRef:
                name: web-config
            - secretRef:
                name: web-secret
          ports:
            - containerPort: 80
`

func byPointer(ts []Target) map[string]Target {
	m := make(map[string]Target, len(ts))
	for _, t := range ts {
		m[t.Pointer] = t
	}
	return m
}

func TestKeysTaxonomy(t *testing.T) {
	ts, err := Keys(deployYAML)
	if err != nil {
		t.Fatal(err)
	}
	got := byPointer(ts)

	// Scalar leaves are emitted.
	for _, p := range []string{
		"/apiVersion", "/kind", "/metadata/name", "/metadata/namespace",
		"/spec/replicas", "/spec/template/spec/containers/0/name",
		"/spec/template/spec/containers/0/image",
	} {
		if tg, ok := got[p]; !ok {
			t.Errorf("missing scalar %q", p)
		} else if tg.Category != CategoryScalar {
			t.Errorf("%q category = %q, want scalar", p, tg.Category)
		}
	}

	// Atomic maps are emitted whole, their inner keys are NOT (no husk-prone leaf).
	for _, p := range []string{"/metadata/labels", "/spec/selector/matchLabels", "/spec/template/metadata/labels"} {
		if tg, ok := got[p]; !ok {
			t.Errorf("missing atomic map %q", p)
		} else if tg.Category != CategoryAtomicMap {
			t.Errorf("%q category = %q, want atomic-map", p, tg.Category)
		}
	}
	for _, p := range []string{"/metadata/labels/app", "/spec/selector/matchLabels/app"} {
		if _, ok := got[p]; ok {
			t.Errorf("inner key of atomic map should not be emitted: %q", p)
		}
	}

	// Sequence elements are emitted as their own category.
	for _, p := range []string{"/spec/template/spec/containers/0", "/spec/template/spec/containers/0/envFrom/1"} {
		if tg, ok := got[p]; !ok {
			t.Errorf("missing seq-elem %q", p)
		} else if tg.Category != CategorySeqElem {
			t.Errorf("%q category = %q, want seq-elem", p, tg.Category)
		}
	}

	// Structural maps and whole sequences must NOT be emitted (they would smear).
	for _, p := range []string{
		"/metadata", "/spec", "/spec/selector", "/spec/template",
		"/spec/template/metadata", "/spec/template/spec",
		"/spec/template/spec/containers", "/spec/template/spec/containers/0/ports",
	} {
		if _, ok := got[p]; ok {
			t.Errorf("structural/sequence node should not be emitted: %q", p)
		}
	}
}

func TestIsAtomicMapByKind(t *testing.T) {
	// selector is atomic in a Service (flat label map) but structural in a
	// Deployment (it wraps matchLabels and must be descended).
	if !isAtomicMap("Service", "selector") {
		t.Error("Service selector should be atomic")
	}
	if isAtomicMap("Deployment", "selector") {
		t.Error("Deployment selector should be structural")
	}
	// Universals hold in any Kind.
	if !isAtomicMap("Deployment", "labels") || !isAtomicMap("ConfigMap", "data") {
		t.Error("universal atomic maps must hold in any Kind")
	}
	// Closed-schema maps are never atomic.
	if isAtomicMap("Deployment", "resources") || isAtomicMap("Pod", "secretRef") {
		t.Error("closed-schema maps must not be atomic")
	}
}

// TestRemoveCollapsesHusk is the load-bearing case: removing the sole child of a
// reference object must take the whole object (and its now-empty list entry)
// with it, leaving realistic YAML — no "secretRef: {}" breadcrumb.
func TestRemoveCollapsesHusk(t *testing.T) {
	out, err := Remove(deployYAML, Target{
		Doc: 0, Kind: "Deployment", Category: CategoryScalar,
		Pointer: "/spec/template/spec/containers/0/envFrom/1/secretRef/name",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "secretRef") {
		t.Fatalf("husk not collapsed, secretRef still present:\n%s", out)
	}
	// The sibling envFrom entry must survive.
	if !strings.Contains(out, "configMapRef") {
		t.Errorf("collapse removed too much:\n%s", out)
	}
	mustParse(t, out)
}

func TestRemoveKeepsSiblings(t *testing.T) {
	out, err := Remove(deployYAML, Target{
		Doc: 0, Kind: "Deployment", Category: CategoryScalar,
		Pointer: "/spec/template/spec/containers/0/image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "nginx") {
		t.Fatalf("image not removed:\n%s", out)
	}
	// Siblings with content are untouched — no spurious collapse.
	for _, want := range []string{"name: web", "containerPort: 80"} {
		if !strings.Contains(out, want) {
			t.Errorf("sibling %q dropped:\n%s", want, out)
		}
	}
	mustParse(t, out)
}

func TestRemoveAtomicMap(t *testing.T) {
	out, err := Remove(deployYAML, Target{
		Doc: 0, Kind: "Deployment", Category: CategoryAtomicMap,
		Pointer: "/spec/selector/matchLabels",
	})
	if err != nil {
		t.Fatal(err)
	}
	// matchLabels gone; with it now empty, selector collapses away too.
	if strings.Contains(out, "matchLabels") || strings.Contains(out, "selector") {
		t.Fatalf("atomic map / empty parent not collapsed:\n%s", out)
	}
	mustParse(t, out)
}

func mustParse(t *testing.T, src string) {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(src))
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if err != nil {
			if err.Error() == "EOF" {
				return
			}
			t.Fatalf("result does not parse: %v\n%s", err, src)
		}
	}
}
