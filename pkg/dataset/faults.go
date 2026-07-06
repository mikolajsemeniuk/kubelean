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
	// Broadened 2026-07-04: the old text enumerated only 5 reference types while
	// the catalog injects ~12, so unlisted-type scenarios (RBAC, HPA/VPA, storage,
	// priorityClass, Ingress backends) never got the procedural check the listed
	// ones did — a prompt-cue asymmetry that confounded the gate ("7B can't do
	// RBAC" vs "the prompt never said to check roleRef"). Still procedural, still
	// no hint at which reference is broken. Prompt change ⇒ FULL re-run before
	// producing any new shards (never mix with pre-2026-07-04 data); smoke first.
	{FaultRefNotFound, "a reference by name points to an object or key that no manifest provides in the referencing object's namespace — check every reference the manifests use: envFrom secretRef.name / configMapRef.name, a key in valueFrom, volume sources (configMap.name, secret.secretName, persistentVolumeClaim.claimName), each volumeMounts name against the pod's volumes, serviceAccountName, imagePullSecrets, priorityClassName, storageClassName, an HPA/VPA scaleTargetRef or targetRef name, a RoleBinding/ClusterRoleBinding roleRef and subjects, and an Ingress backend service name"},
	// Both selector/port descriptions were widened 2026-07 for the Ingress/PDB/
	// NetworkPolicy scenarios; descriptions are fragile (see lessons) — any
	// further edit needs a make smoke before a full run.
	{FaultSelectorMismatch, "the key/value pairs under spec.selector.matchLabels are not identical to those under spec.template.metadata.labels (compare each value, not just the keys), or a selector on another object (a Service spec.selector, a PodDisruptionBudget or NetworkPolicy matchLabels) does not equal the labels of the pods it targets"},
	{FaultPortMismatch, "a Service's spec.ports targetPort does not equal the containerPort exposed by the pods its selector matches, or an Ingress backend port number does not equal any port of the Service it routes to — connections reach a port where nothing is listening"},
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
