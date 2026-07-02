package dataset

// ServerMeta are the server-assigned metadata fields `kubectl get -o yaml`
// shows on every live object: creationTimestamp, generation, resourceVersion,
// uid. It is embedded in each Kind's Params; the zero value omits every field,
// so scenarios that do not opt in render byte-identical to before. Created is
// also reused as the timestamp of any status condition the template renders.
//
// Two realistic blocks are deliberately NOT modelled, both load-bearing:
//   - managedFields: kubectl strips them from get output since v1.21, so a
//     realistic agent never sees them (and their fieldsV1 mirror would leak a
//     removed field's former presence into every ablation variant).
//   - kubectl.kubernetes.io/last-applied-configuration: it embeds a JSON copy
//     of the whole spec, so a removed field would stay visible inside it and
//     every ablation would leak. It only exists under client-side apply.
type ServerMeta struct {
	Created         string // metadata.creationTimestamp
	Generation      int    // metadata.generation — only rendered by Kinds that track it
	ResourceVersion string
	UID             string
}

// Status selects the per-Kind status block a template renders: healthy (the
// object works), failing (the scenario's fault shows its symptoms), or ""
// (no status at all — the pre-decoration shape). Status text carries aggregate
// symptoms only — counts, phases, generic conditions — never root-cause
// detail: the real FailedGetScale message names the missing target, which
// would plant the ground-truth answer in a non-deciding field that survives
// removal of the deciding one.
const (
	StatusHealthy = "healthy"
	StatusFailing = "failing"
)
