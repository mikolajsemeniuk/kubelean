package faults

import "fmt"

// Generate produces n deterministic instances of the class (seed = index).
func (fc FaultClass) Generate(n int) []Instance {
	out := make([]Instance, 0, n)
	for i := range n {
		p := newParams(i, fc.Difficulty)
		out = append(out, Instance{
			Name:      fmt.Sprintf("%s-%03d", fc.Label, i),
			Resources: fc.Render(p),
			Truth: GroundTruth{
				Label: fc.Label, OffendingField: fc.OffendingField,
				Goal: fc.Goal, Category: fc.Category, Provenance: fc.Provenance,
			},
		})
	}

	return out
}

// GenerateAll produces n instances of every class in the catalog.
func GenerateAll(n int) []Instance {
	var out []Instance
	for _, fc := range Catalog() {
		out = append(out, fc.Generate(n)...)
	}

	return out
}

// GenerateByDifficulty produces n instances of every class matching d.
func GenerateByDifficulty(n int, d Difficulty) []Instance {
	var out []Instance
	for _, fc := range Catalog() {
		if fc.Difficulty == d {
			out = append(out, fc.Generate(n)...)
		}
	}

	return out
}
