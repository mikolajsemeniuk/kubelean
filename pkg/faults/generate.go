package faults

import "fmt"

// Generate produces n deterministic instances of the class (seed = index).
func (c FaultClass) Generate(n int) []Instance {
	out := make([]Instance, 0, n)
	for i := range n {
		truth := GroundTruth{
			Label:          c.Label,
			OffendingField: c.OffendingField,
			Goal:           c.Goal,
			Category:       c.Category,
			Provenance:     c.Provenance,
		}
		name := fmt.Sprintf("%s-%03d", c.Label, i)
		p := newParams(i, c.Difficulty)
		resources := c.Render(p)
		in := Instance{Name: name, Resources: resources, Truth: truth}

		out = append(out, in)
	}

	return out
}

// GenerateAll produces n instances of every class in the catalog.
func GenerateAll(n int) []Instance {
	var out []Instance
	for _, v := range Catalog() {
		out = append(out, v.Generate(n)...)
	}

	return out
}

// GenerateByDifficulty produces n instances of every class matching d.
func GenerateByDifficulty(n int, d Difficulty) []Instance {
	var out []Instance
	for _, v := range Catalog() {
		if v.Difficulty == d {
			out = append(out, v.Generate(n)...)
		}
	}

	return out
}
