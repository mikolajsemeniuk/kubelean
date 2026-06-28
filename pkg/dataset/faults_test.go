package dataset

import "testing"

// TestEveryScenarioClassIsInCatalog guards the footgun: a scenario using a class
// missing from the faults catalog would never appear in the enum, so the model
// could not output it and that scenario's baseline would silently be zero.
func TestEveryScenarioClassIsInCatalog(t *testing.T) {
	known := map[string]bool{}
	for _, c := range FaultClasses() {
		known[c] = true
	}
	for _, s := range All() {
		if !known[s.FaultClass] {
			t.Errorf("scenario %q uses class %q not in the faults catalog", s.Name, s.FaultClass)
		}
	}
}
