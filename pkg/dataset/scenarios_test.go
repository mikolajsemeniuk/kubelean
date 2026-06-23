package dataset

import (
	"strings"
	"testing"
)

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func TestScenarios(t *testing.T) {
	scs := Scenarios()
	if len(scs) != 2 {
		t.Fatalf("want 2 scenarios, got %d", len(scs))
	}

	by := map[string]Scenario{}
	for _, s := range scs {
		if strings.TrimSpace(s.YAML) == "" {
			t.Fatalf("%s: empty YAML", s.Name)
		}
		if s.FaultClass == "" || len(s.DecidingFields) == 0 {
			t.Errorf("%s: missing ground truth", s.Name)
		}
		by[s.Name] = s
	}

	// Scenario 1: single document, label mismatch actually present.
	s1 := by["selector-label-mismatch"]
	if got := strings.Count(s1.YAML, "kind:"); got != 1 {
		t.Errorf("scenario 1 should be a single document, got %d:\n%s", got, s1.YAML)
	}
	mustContain(t, s1.YAML, "app: web", "app: web-frontend")

	// Scenario 2: three documents; the secretRef name differs from the Secret name.
	s2 := by["secret-ref-wrong-name"]
	if got := strings.Count(s2.YAML, "kind:"); got != 3 {
		t.Errorf("scenario 2 should have 3 documents, got %d:\n%s", got, s2.YAML)
	}
	mustContain(t, s2.YAML, "kind: ConfigMap", "kind: Secret", "name: api-secret", "name: api-secrets")
}
