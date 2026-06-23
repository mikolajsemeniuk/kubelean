// Package eval scores RCA accuracy of a local LLM over distilled vs raw
// Kubernetes resources. Correctness is automatic and deterministic: the model
// must emit a closed-set root_cause_label, exact-matched against the injected
// ground truth (no human, no LLM judge).
package eval

// Labels is the closed set the model must choose from — the root_cause_label
// vocabulary (see the fault taxonomy in CLAUDE.md). Keeping it fixed and shared
// across instances makes the classification non-trivial and the scoring
// exact-match.
var Labels = []string{
	"CrashLoopBackOff",
	"OOMKilled",
	"ImagePullBackOff_BadImage",
	"ImagePullBackOff_NoAuth",
	"LivenessProbeFailure",
	"ReadinessProbeFailure",
	"Pending_InsufficientResources",
	"Pending_UnsatisfiableAffinity",
	"PVC_Unbound",
	"CreateContainerConfigError",
	"Service_SelectorMismatch",
	"NetworkPolicy_BlocksIngress",
	"Scaling_ZeroReplicas",
	"RBAC_Forbidden",
}
