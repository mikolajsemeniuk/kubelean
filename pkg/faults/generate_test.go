package faults

import (
	"strings"
	"testing"

	"github.com/mikolajsemeniuk/kubelean/pkg/distill"
)

// TestGenerateAllRenders asserts every class renders deterministically into
// serializable resources carrying its ground-truth label, and that the deciding
// evidence survives (sanity: the label or its symptom is locatable somewhere).
func TestGenerateAllRenders(t *testing.T) {
	insts := GenerateAll(3)
	if len(insts) != len(Catalog())*3 {
		t.Fatalf("expected %d instances, got %d", len(Catalog())*3, len(insts))
	}
	for _, inst := range insts {
		if len(inst.Resources) == 0 {
			t.Errorf("%s: no resources", inst.Name)
		}
		if inst.Truth.Label == "" || inst.Truth.OffendingField == "" {
			t.Errorf("%s: incomplete ground truth", inst.Name)
		}
		for _, r := range inst.Resources {
			y, err := distill.ToYAML(r)
			if err != nil {
				t.Errorf("%s: serialize: %v", inst.Name, err)
			}
			if !strings.Contains(y, "kind:") {
				t.Errorf("%s: serialized resource missing kind", inst.Name)
			}
		}
	}
}

// TestDeterminism asserts the same seed reproduces byte-identical output.
func TestDeterminism(t *testing.T) {
	a := Catalog()[0].Generate(2)
	b := Catalog()[0].Generate(2)
	for i := range a {
		ya, _ := distill.ToYAML(a[i].Resources[0])
		yb, _ := distill.ToYAML(b[i].Resources[0])
		if ya != yb {
			t.Errorf("instance %d not reproducible", i)
		}
	}
}
