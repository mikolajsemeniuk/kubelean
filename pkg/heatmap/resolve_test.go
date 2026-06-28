package heatmap

import "testing"

func TestResolveLeaves(t *testing.T) {
	// deployYAML's container has envFrom [configMapRef, secretRef], so the
	// secretRef path must resolve to index 1 ONLY — the configMapRef sibling at
	// index 0 has no secretRef and must be filtered out (the load-bearing case).
	loci, err := ResolveLeaves(deployYAML, "Deployment", "spec.template.spec.containers[].envFrom[].secretRef.name")
	if err != nil {
		t.Fatal(err)
	}
	if len(loci) != 1 || loci[0].Doc != 0 ||
		loci[0].Pointer != "/spec/template/spec/containers/0/envFrom/1/secretRef/name" {
		t.Fatalf("secretRef.name resolved to %+v, want only envFrom/1", loci)
	}

	// A flat path resolves to one pointer.
	loci, _ = ResolveLeaves(deployYAML, "Deployment", "spec.selector.matchLabels.app")
	if len(loci) != 1 || loci[0].Pointer != "/spec/selector/matchLabels/app" {
		t.Fatalf("matchLabels.app resolved to %+v", loci)
	}

	// Kind filters documents: deployYAML has no Secret, so nothing resolves.
	if loci, _ := ResolveLeaves(deployYAML, "Secret", "metadata.name"); len(loci) != 0 {
		t.Fatalf("metadata.name should not resolve in a Deployment-only stream, got %+v", loci)
	}

	// metadata.name does resolve when the Kind matches.
	if loci, _ := ResolveLeaves(deployYAML, "Deployment", "metadata.name"); len(loci) != 1 || loci[0].Pointer != "/metadata/name" {
		t.Fatalf("Deployment metadata.name resolved to %+v", loci)
	}

	// A path that does not exist resolves to nothing.
	if loci, _ := ResolveLeaves(deployYAML, "Deployment", "spec.nonexistent.field"); len(loci) != 0 {
		t.Fatalf("missing path should resolve to nothing, got %+v", loci)
	}
}
