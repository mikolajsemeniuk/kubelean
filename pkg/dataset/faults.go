package dataset

// Fault classes — the root-cause labels the model chooses from. Rule: one class =
// one distinct root cause an SRE would name, not one per component (a missing
// referenced object is Ref_NotFound whether it's a secret, configmap, pvc or sa).
// Each class carries a description, modelled like an MCP endpoint (name +
// description): the description is the *procedure to check*, not a hint at which
// answer applies, so the model can diagnose generic labels instead of relying on
// a specific label name as a cue. New classes land in the faults catalog as their
// group is implemented; keep them few — a 7B model cannot tell apart a long enum.
const (
	FaultSelectorMismatch = "SelectorMismatch"
	FaultRefNotFound      = "Ref_NotFound"
	FaultPortMismatch     = "PortMismatch"
	FaultNoFault          = "NoFaultFound"
)

// Fault is a class name and the check that defines it.
type Fault struct {
	Class       string
	Description string
}

// faults is the single source of truth for the label space, in prompt order. The
// schema enum is built from the names, the prompt from name + description.
var faults = []Fault{
	{FaultRefNotFound, "a name reference (secretRef.name, configMapRef.name, a key in valueFrom, a volume claimName, or serviceAccountName) points to an object or key that is not present in the manifests"},
	{FaultSelectorMismatch, "the key/value pairs under spec.selector.matchLabels are not identical to those under spec.template.metadata.labels (compare each value, not just the keys), or a Service spec.selector does not equal the pod labels"},
	{FaultPortMismatch, "a Service's spec.ports targetPort does not equal the containerPort exposed by the pods its selector matches, so connections to the Service reach a port where nothing is listening"},
	// The closing clause matters for the flip: manifests now carry status blocks,
	// so after a deciding field is removed the symptoms (unavailable replicas)
	// remain while the root cause is gone — NoFaultFound must mean "no root cause
	// identifiable", not "everything looks healthy", or the control is unanswerable.
	// A/B-smoked 2026-07 (k=3): rewording did not move baselines vs the old text.
	{FaultNoFault, "every reference resolves, every selector matches its target labels, and every Service targetPort matches a pod containerPort — none of the faults above is identifiable; a status reporting unavailability does not by itself identify one"},
}

// FaultClasses returns just the class names — used as the schema enum.
func FaultClasses() []string {
	out := make([]string, len(faults))
	for i, f := range faults {
		out[i] = f.Class
	}
	return out
}

// FaultLines returns "Class: description" lines — joined into the prompt.
func FaultLines() []string {
	out := make([]string, len(faults))
	for i, f := range faults {
		out[i] = f.Class + ": " + f.Description
	}

	return out
}
