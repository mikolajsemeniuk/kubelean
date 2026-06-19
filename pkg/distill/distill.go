// Package distill implements saliency-budgeted distillation of live Kubernetes
// resource output for LLM agents.
//
// The core entry point is [Distill]: a pure, deterministic transform over an
// *unstructured.Unstructured that prunes/condenses a resource before it reaches
// an LLM. M0 implements the lossless and static-bucket levels (L0..L2); the
// corpus-entropy, goal-conditioned, and oracle levels (L3..L5) land later.
package distill

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// Level selects the distillation mechanism. Higher levels strip more, trading
// information for tokens.
type Level int

const (
	// L0Raw returns the object unchanged: the baseline `kubectl get -o yaml`.
	L0Raw Level = iota
	// L1Lossless removes server-managed fields that carry no diagnostic
	// information by construction (managedFields, resourceVersion, uid, ...).
	// Reduction here is a "free lunch": no information loss.
	L1Lossless
	// L2StaticBuckets adds static, role-based pruning of default-injected and
	// boilerplate fields (~= kubectl-neat). This is the prior-art baseline the
	// goal-conditioned levels must beat, not the contribution.
	L2StaticBuckets
)

// Goal is a fault-class hint for goal-conditioned distillation (L4+). At
// L0..L2 it is informational only; static levels do not branch on it. The
// concrete goal vocabulary lives with the generator (pkg/faults catalog).
type Goal string

// Profile is a distillation configuration.
type Profile struct {
	Level Level
	Goal  Goal
}

// Distill returns a distilled deep copy of obj according to p. The input is
// never mutated; the function is pure and deterministic.
func Distill(obj *unstructured.Unstructured, p Profile) *unstructured.Unstructured {
	out := obj.DeepCopy()
	switch p.Level {
	case L0Raw:
		// identity
	case L1Lossless:
		stripLossless(out)
	case L2StaticBuckets:
		stripLossless(out)
		stripStaticBuckets(out)
	}
	return out
}
