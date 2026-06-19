package faults

import (
	"fmt"
	"math/rand"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// GroundTruth is the injected oracle for one generated instance: the closed-set
// label to recover, the single deciding field (localization + L5 leave-one-out
// target), the L4 goal-condition, the Cloud-OpsBench category, and provenance
// (which prior-art benchmark the class is grounded in).
type GroundTruth struct {
	Label          string
	OffendingField string
	Goal           string
	Category       string
	Provenance     string
}

// Instance is one generated benchmark case: one or more Kubernetes resources
// (single-element for single-scope faults, >1 for bundles) plus the oracle.
type Instance struct {
	Name      string
	Resources []*unstructured.Unstructured
	Truth     GroundTruth
}

// Difficulty controls whether the root cause is spelled out in the status (Easy:
// label appears ~verbatim, keyword-extractable) or must be inferred from the
// configuration / a sibling resource (Hard: symptom is generic).
type Difficulty int

const (
	Easy Difficulty = iota
	Hard
)

func (d Difficulty) String() string {
	if d == Hard {
		return "hard"
	}
	return "easy"
}

// Params carries the deterministic randomization for one rendered instance.
type Params struct {
	Seed       int
	Difficulty Difficulty
	rng        *rand.Rand
	App        string
	Namespace  string
	Registry   string
	Tag        string
	Hash       string
}

var (
	appNames   = []string{"payment-api", "image-resizer", "notify-worker", "auth-svc", "cart-api", "search-indexer", "ledger", "session-store", "billing", "gateway"}
	namespaces = []string{"prod", "media", "messaging", "payments", "platform", "search", "core"}
	registries = []string{"registry.internal", "ghcr.io/acme", "docker.io/acme", "quay.io/acme"}
)

// newParams derives reproducible cosmetic values from seed so two instances of
// the same class differ but a given seed always reproduces the same instance.
func newParams(seed int, d Difficulty) Params {
	r := rand.New(rand.NewSource(int64(seed)*2654435761 + 1))
	return Params{
		Seed:       seed,
		Difficulty: d,
		rng:        r,
		App:        appNames[r.Intn(len(appNames))],
		Namespace:  namespaces[r.Intn(len(namespaces))],
		Registry:   registries[r.Intn(len(registries))],
		Tag:        fmt.Sprintf("%d.%d.%d", r.Intn(4)+1, r.Intn(9), r.Intn(9)),
		Hash:       fmt.Sprintf("%x", r.Int63())[:9],
	}
}

func (p Params) podName() string {
	return fmt.Sprintf("%s-%s-%s", p.App, p.Hash[:8], randSuffix(p.rng, 5))
}

func randSuffix(r *rand.Rand, n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}
