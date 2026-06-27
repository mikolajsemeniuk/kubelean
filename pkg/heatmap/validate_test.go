package heatmap

import (
	"strings"
	"testing"
)

func removeOrFail(t *testing.T, src, pointer string) string {
	t.Helper()
	out, err := Remove(src, Target{Doc: 0, Kind: "Deployment", Pointer: pointer})
	if err != nil {
		t.Fatalf("remove %s: %v", pointer, err)
	}
	return out
}

func TestValid(t *testing.T) {
	// The unmodified Deployment is valid.
	if ok, probs, err := Valid(deployYAML); err != nil || !ok {
		t.Fatalf("baseline should be valid, got ok=%v probs=%v err=%v", ok, probs, err)
	}

	// Removing the selector (via its atomic matchLabels, which collapses the
	// whole selector away) drops a required field -> invalid.
	noSelector := removeOrFail(t, deployYAML, "/spec/selector/matchLabels")
	if ok, probs, _ := Valid(noSelector); ok {
		t.Error("deployment without selector should be invalid")
	} else if !strings.Contains(strings.Join(probs, ";"), "/spec/selector") {
		t.Errorf("expected a selector problem, got %v", probs)
	}

	// Removing the only container collapses the containers list -> invalid.
	noContainers := removeOrFail(t, deployYAML, "/spec/template/spec/containers/0")
	if ok, _, _ := Valid(noContainers); ok {
		t.Error("deployment without containers should be invalid")
	}

	// Removing a container's required name -> invalid.
	noName := removeOrFail(t, deployYAML, "/spec/template/spec/containers/0/name")
	if ok, _, _ := Valid(noName); ok {
		t.Error("container without name should be invalid")
	}

	// Removing an OPTIONAL field (image) stays valid — only required fields gate.
	noImage := removeOrFail(t, deployYAML, "/spec/template/spec/containers/0/image")
	if ok, probs, _ := Valid(noImage); !ok {
		t.Errorf("removing optional image should stay valid, got %v", probs)
	}
}
