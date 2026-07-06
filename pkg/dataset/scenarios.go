package dataset

import (
	"strings"
	"text/template"
)

// Group names — the batch keys used to produce related scenarios together
// (make run-<group>). Defined here so scenarios and callers share one source of
// truth instead of magic strings.
const (
	GroupSelector   = "selector"
	GroupReferences = "references"
	GroupNetworking = "networking"
	GroupVolumes    = "volumes"
	GroupScaling    = "scaling"
	GroupRBAC       = "rbac"
	GroupHealthy    = "healthy"
)

// Scenario is one faulty instance of the m1 dataset: the rendered manifest(s)
// plus the ground truth used to score root-cause analysis in m2.
type Scenario struct {
	Name           string          // stable id
	Group          string          // batch/category id; run related scenarios together (make run-<group>)
	FaultClass     string          // ground-truth label
	DecidingFields []DecidingField // fault loci, Kind-qualified; encode the fault
	YAML           string          // rendered manifest(s); multi-document scenarios are --- joined
	TwinOf         string          // set on a healthy twin: the faulty scenario it mirrors; twins run baseline-only
}

// DecidingField is a ground-truth fault locus: the field whose value encodes the
// fault, qualified by the Kind of the document it lives in — so metadata.name in
// a Secret is not confused with metadata.name in a Deployment. Path is dotted
// with [] for array levels, e.g.
// spec.template.spec.containers[].envFrom[].secretRef.name. It is resolved to
// concrete pointers against a scenario's YAML by heatmap.ResolveLeaves.
//
// Hides splits the loci into the two populations of the control (the flip):
//
//   - Hides == false (fault-deleting): removing the field deletes the fault
//     itself. The dangling reference site (secretRef.name) is gone, or one side
//     of a same-document comparison (selector vs template labels) no longer
//     exists — the manifests are genuinely consistent again, so expecting
//     NoFaultFound is honest.
//   - Hides == true (evidence-hiding): removing the field only removes the
//     counter-evidence while the fault arguably persists. Removing the target
//     Secret's metadata.name leaves the deployment still asking for a secret
//     that no named object provides; removing pod labels leaves a Service
//     selector that still matches nothing. NoFaultFound is only "correct" under
//     the prompt's charitable absent-field convention; under a strict reading
//     the original fault class is. The two readings are reported side by side
//     and must never be pooled with the fault-deleting population.
type DecidingField struct {
	Kind  string
	Path  string
	Hides bool
}

// All returns the whole m1 catalog: every faulty scenario, its healthy twin,
// and the standalone healthy control.
func All() []Scenario {
	builders := []func(twin bool) Scenario{
		selectorLabelMismatch,
		statefulSetSelectorMismatch,
		daemonSetSelectorMismatch,
		replicaSetSelectorMismatch,
		secretWrongName,
		configMapRefWrongName,
		serviceAccountWrongName,
		imagePullSecretWrongName,
		pvcClaimWrongName,
		configMapVolumeWrongName,
		secretVolumeWrongName,
		storageClassWrongName,
		hpaTargetWrongName,
		vpaTargetWrongName,
		priorityClassWrongName,
		roleBindingRoleWrongName,
		clusterRoleBindingRoleWrongName,
		roleBindingSubjectWrongName,
		serviceSelectorMismatch,
		servicePortMismatch,
		envKeyWrongName,
		volumeMountWrongName,
		jobSecretWrongName,
		secretWrongNamespace,
		ingressBackendWrongName,
		ingressBackendWrongPort,
		serviceStatefulSetPortMismatch,
		pdbSelectorMismatch,
		networkPolicySelectorMismatch,
		secretRefCrowded,
		secretVolumeCrowded,
		servicePortCrowded,
		configMapRefCrowded,
	}

	var out []Scenario
	for _, build := range builders {
		out = append(out, build(false), build(true))
	}

	return append(out, healthyBundle(), healthyWebStack(), healthyRBAC())
}

// maybeTwin returns s unchanged, or converts it into its healthy twin: the
// same bundle with the single anomaly fixed (the caller flips the divergent
// value and the sick status), renamed <name>-twin, expected NoFaultFound, no
// deciding fields. Twins measure the per-scenario false-positive rate: whether
// the model actually discriminates the fault from the healthy shape, or just
// always suspects it on this bundle (lesson 1) — in which case the faulty
// baseline is bias, not diagnosis. The producer runs twins baseline-only:
// with no fault there is no saliency to measure. Twins reuse the faulty
// scenario's uid/resourceVersion literals; the two never share a prompt.
func maybeTwin(twin bool, s Scenario) Scenario {
	if !twin {
		return s
	}

	s.TwinOf = s.Name
	s.Name += "-twin"
	s.FaultClass = FaultNoFault
	s.DecidingFields = nil

	return s
}

// Scenarios returns the catalog filtered to one group.
func Scenarios(group string) []Scenario {
	var out []Scenario
	for _, s := range All() {
		if s.Group == group {
			out = append(out, s)
		}
	}

	return out
}

// selectorLabelMismatch is a single Deployment whose pod template labels do not
// match its own selector (app=web vs app=web-frontend). Diagnosing it requires
// reading both label fields, so neither alone is the deciding field.
//
// Note on all four single-workload selector scenarios (Deployment, StatefulSet,
// DaemonSet, ReplicaSet): a live API server rejects this manifest at admission
// ("selector does not match template labels"), so their failing status is an
// as-if — they model the pre-apply review case, kept in kubectl-get shape so
// every scenario's field population is comparable.
func selectorLabelMismatch(twin bool) Scenario {
	podApp, status := "web-frontend", StatusFailing
	if twin {
		podApp, status = "web", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:          "web",
		Namespace:     "production",
		App:           "web",
		Replicas:      3,
		SelectorApp:   "web",
		PodApp:        podApp,
		ContainerName: "web",
		Image:         "nginx:1.25",
		ContainerPort: 80,
		ServerMeta:    srv("18f0da56-db3c-43bf-a378-f3fb0f06c6a5", "825289"),
		Status:        status,
	})

	out := Scenario{
		Name:       "selector-label-mismatch",
		Group:      GroupSelector,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.selector.matchLabels.app"},
			{Kind: "Deployment", Path: "spec.template.metadata.labels.app"},
		},
		YAML: joinDocs(dep),
	}

	return maybeTwin(twin, out)
}

// statefulSetSelectorMismatch is a StatefulSet whose selector (app=database)
// does not match its own pod template labels (app=db) — the same SelectorMismatch
// root cause as the Deployment case, on a second workload Kind, within one
// document (so the 7B handles it, unlike the cross-document Service case).
//
// The bundle carries the headless governing Service the template's required
// spec.serviceName points at (2026-07-04: previously absent, so serviceName
// dangled — a second anomaly violating the one-anomaly rule, in the twin too).
// The anomaly lives in the SELECTOR, not the pod labels, so that Service stays
// fully healthy in both variants: its selector app=db matches the pods, whose
// labels are constant db.
func statefulSetSelectorMismatch(twin bool) Scenario {
	selectorApp, status := "database", StatusFailing
	if twin {
		selectorApp, status = "db", StatusHealthy
	}

	sts := NewStatefulSet(StatefulSetParams{
		Name:          "db",
		Namespace:     "production",
		App:           "db",
		Replicas:      3,
		SelectorApp:   selectorApp,
		PodApp:        "db",
		ContainerName: "db",
		Image:         "postgres:16.2",
		ContainerPort: 5432,
		ServerMeta:    srv("2fa8047b-869d-4724-a70d-71337826cfd5", "531795"),
		Status:        status,
	})

	svc := NewService(ServiceParams{
		Name: "db", Namespace: "production", App: "db",
		Headless: true, SelectorApp: "db", Port: 5432, TargetPort: 5432,
		ServerMeta: srv("83b7c9d1-52e6-4f0a-b1a4-9c27d3e8f615", "418362"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "statefulset-selector-mismatch",
		Group:      GroupSelector,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "StatefulSet", Path: "spec.selector.matchLabels.app"},
			// Unlike the Service-less single-doc siblings this side is
			// evidence-hiding: removing the pod labels leaves the anomalous
			// selector dangling AND breaks the healthy Service→pods match, so
			// NoFaultFound holds only under the charitable absent-field reading.
			{Kind: "StatefulSet", Path: "spec.template.metadata.labels.app", Hides: true},
		},
		YAML: joinDocs(sts, svc),
	})
}

// daemonSetSelectorMismatch is a DaemonSet whose pod template labels (app=log-agent)
// do not match its selector (app=agent) — SelectorMismatch on a third workload Kind,
// single-document, so the 7B handles it.
func daemonSetSelectorMismatch(twin bool) Scenario {
	podApp, status := "log-agent", StatusFailing
	if twin {
		podApp, status = "agent", StatusHealthy
	}

	ds := NewDaemonSet(DaemonSetParams{
		Name:          "agent",
		Namespace:     "production",
		App:           "agent",
		SelectorApp:   "agent",
		PodApp:        podApp,
		ContainerName: "agent",
		Image:         "fluent/fluent-bit:3.0.7",
		ContainerPort: 2020,
		ServerMeta:    srv("d34cc84d-ef05-46c7-a721-d50e28b1e8f8", "306140"),
		Status:        status,
	})

	return maybeTwin(twin, Scenario{
		Name:       "daemonset-selector-mismatch",
		Group:      GroupSelector,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "DaemonSet", Path: "spec.selector.matchLabels.app"},
			{Kind: "DaemonSet", Path: "spec.template.metadata.labels.app"},
		},
		YAML: joinDocs(ds),
	})
}

// pvcClaimWrongName is a Deployment mounting a volume backed by PVC "api-data",
// but the only PersistentVolumeClaim is named "apidata" — a dangling claim
// (Pending pod in a real cluster). Ref_NotFound.
func pvcClaimWrongName(twin bool) Scenario {
	// Divergence type: punctuation — the claim exists without the hyphen
	// ("apidata" vs the referenced "api-data").
	pvcName, status := "apidata", StatusFailing
	if twin {
		pvcName, status = "api-data", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:          "api",
		Namespace:     "production",
		App:           "api",
		Replicas:      2,
		SelectorApp:   "api",
		PodApp:        "api",
		ContainerName: "api",
		Image:         "ghcr.io/acme/api:2.3.1",
		ContainerPort: 8080,
		VolumeKind:    "pvc",
		VolumeRef:     "api-data",
		ServerMeta:    srv("6c0f7a1e-ae97-4bc9-a624-94f5e2bea039", "883794"),
		Status:        status,
	})

	// The mis-named claim itself is a healthy, Bound PVC — it is simply not the
	// one the Deployment asks for.
	pvc := NewPVC(PVCParams{
		Name: pvcName, Namespace: "production", App: "api", Storage: "10Gi",
		ServerMeta: srv("47c968e0-b76d-4f85-a08e-e58cbd43b5d8", "380426"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "pvc-claim-wrong-name",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.volumes[].persistentVolumeClaim.claimName"},
			{Kind: "PersistentVolumeClaim", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, pvc),
	})
}

// configMapVolumeWrongName is a Deployment mounting ConfigMap "api-files" as a
// volume, but the ConfigMap is named "api-config-files" — same Ref_NotFound, a different
// reference site (volume source, not envFrom) so configMap.name and configMapRef
// .name are distinct field-keys with their own cross-scenario profiles.
func configMapVolumeWrongName(twin bool) Scenario {
	// Divergence type: an extra middle segment — the ConfigMap exists as
	// "api-config-files" while the volume asks for "api-files".
	cmName, status := "api-config-files", StatusFailing
	if twin {
		cmName, status = "api-files", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:          "api",
		Namespace:     "production",
		App:           "api",
		Replicas:      2,
		SelectorApp:   "api",
		PodApp:        "api",
		ContainerName: "api",
		Image:         "ghcr.io/acme/api:2.3.1",
		ContainerPort: 8080,
		VolumeKind:    "configMap",
		VolumeRef:     "api-files",
		ServerMeta:    srv("684f1f14-f07d-4970-a119-b198eb64379c", "364255"),
		Status:        status,
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: cmName, Namespace: "production",
		Data:       map[string]string{"app.conf": "level=info"},
		ServerMeta: srv("c534b5a0-3d0c-4730-aff7-8756bcf71a2e", "391375"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "configmap-volume-wrong-name",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.volumes[].configMap.name"},
			{Kind: "ConfigMap", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, cm),
	})
}

// secretVolumeWrongName is a Deployment mounting Secret "api-certs" as a volume,
// but the Secret is named "api-tls" — Ref_NotFound at the secret volume source.
func secretVolumeWrongName(twin bool) Scenario {
	// Divergence type: semantic synonym — the Secret was created as "api-tls",
	// the volume asks for "api-certs"; same thing to a human, not to the API.
	secretName, status := "api-tls", StatusFailing
	if twin {
		secretName, status = "api-certs", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:          "api",
		Namespace:     "production",
		App:           "api",
		Replicas:      2,
		SelectorApp:   "api",
		PodApp:        "api",
		ContainerName: "api",
		Image:         "ghcr.io/acme/api:2.3.1",
		ContainerPort: 8080,
		VolumeKind:    "secret",
		VolumeRef:     "api-certs",
		ServerMeta:    srv("0283c6bf-adad-403a-a2ea-345f6ed2a76a", "313324"),
		Status:        status,
	})

	sec := NewSecret(SecretParams{
		Name: secretName, Namespace: "production",
		StringData: map[string]string{"tls.crt": "redacted-cert", "tls.key": "redacted-key"},
		ServerMeta: srv("ce0f4418-e6c8-4394-a7a3-24f4b0f42f21", "275846"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "secret-volume-wrong-name",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.volumes[].secret.secretName"},
			{Kind: "Secret", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, sec),
	})
}

// imagePullSecretWrongName is a Deployment whose pods reference image pull secret
// "registry-creds", but the only Secret in the bundle is named "registry-credentials" —
// a dangling reference (ImagePullBackOff in a real cluster). Ref_NotFound, a fourth
// reference kind on the cross-scenario profile.
func imagePullSecretWrongName(twin bool) Scenario {
	// Divergence type: abbreviation vs full word — the Secret was created as
	// "registry-credentials", the pod asks for "registry-creds".
	secretName, status := "registry-credentials", StatusFailing
	if twin {
		secretName, status = "registry-creds", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:            "api",
		Namespace:       "production",
		App:             "api",
		Replicas:        2,
		SelectorApp:     "api",
		PodApp:          "api",
		ContainerName:   "api",
		Image:           "ghcr.io/acme/api:2.3.1",
		ContainerPort:   8080,
		ImagePullSecret: "registry-creds",
		ServerMeta:      srv("241c3377-c6c1-4b8a-adc5-bd7b44c3804b", "993906"),
		Status:          status,
	})

	sec := NewSecret(SecretParams{
		Name:       secretName,
		Namespace:  "production",
		StringData: map[string]string{".dockerconfigjson": "redacted-docker-config"},
		ServerMeta: srv("dfd4b5d5-74fd-4d93-a55d-a947f62d9c70", "703368"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "imagepull-secret-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.imagePullSecrets[].name"},
			{Kind: "Secret", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, sec),
	})
}

// secretWrongName is a Deployment wired to a ConfigMap (correct — a healthy
// distractor) and a Secret (broken): the Deployment references secret
// "api-secret" but the Secret is actually named "api-secrets". The symmetric
// cm-wrong-name variant would instead break ConfigMapRef against the ConfigMap.
func secretWrongName(twin bool) Scenario {
	secretName, status := "api-secrets", StatusFailing
	if twin {
		secretName, status = "api-secret", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:          "api",
		Namespace:     "production",
		App:           "api",
		Replicas:      2,
		SelectorApp:   "api",
		PodApp:        "api",
		ContainerName: "api",
		Image:         "ghcr.io/acme/api:2.3.1",
		ContainerPort: 8080,
		ConfigMapRef:  "api-config",
		SecretRef:     "api-secret",
		ServerMeta:    srv("5e2ef0e0-0946-4787-a5cc-dc0a2531bc43", "152839"),
		Status:        status,
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-config", Namespace: "production",
		Data:       map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
		ServerMeta: srv("aa6a9647-4517-4529-a6d9-73e1c5d4da0a", "228326"),
	})

	sec := NewSecret(SecretParams{
		Name:       secretName,
		Namespace:  "production",
		StringData: map[string]string{"API_KEY": "redacted-api-key", "DB_PASSWORD": "redacted-password"},
		ServerMeta: srv("dc55e19e-1e43-4e98-aa4a-78d86ea73f6b", "228481"),
	})

	out := Scenario{
		Name:       "secret-ref-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.containers[].envFrom[].secretRef.name"},
			{Kind: "Secret", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, cm, sec),
	}

	return maybeTwin(twin, out)
}

// configMapRefWrongName mirrors secretWrongName with the fault on the ConfigMap
// side: the Deployment's envFrom configMapRef points at "api-config" but the
// ConfigMap is named "app-settings" (the Secret here is the healthy distractor).
// Same Ref_NotFound class, different deciding field — this is what gives
// configMapRef.name a cross-scenario profile (noise in secret-ref-wrong-name,
// deciding here).
func configMapRefWrongName(twin bool) Scenario {
	// Divergence type: a completely different name (the ConfigMap was created
	// under another naming convention) — no lexical overlap with the reference,
	// so a "two names differ by one char" heuristic cannot find it.
	cmName, status := "app-settings", StatusFailing
	if twin {
		cmName, status = "api-config", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:          "api",
		Namespace:     "production",
		App:           "api",
		Replicas:      2,
		SelectorApp:   "api",
		PodApp:        "api",
		ContainerName: "api",
		Image:         "ghcr.io/acme/api:2.3.1",
		ContainerPort: 8080,
		ConfigMapRef:  "api-config",
		SecretRef:     "api-secret",
		ServerMeta:    srv("da64943b-5bfc-40c4-aa5e-c5f7d6f776d7", "375965"),
		Status:        status,
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: cmName, Namespace: "production",
		Data:       map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
		ServerMeta: srv("264acd9c-8700-4e71-a64e-5dc16a3e6b37", "605340"),
	})

	sec := NewSecret(SecretParams{
		Name:       "api-secret",
		Namespace:  "production",
		StringData: map[string]string{"API_KEY": "redacted-api-key", "DB_PASSWORD": "redacted-password"},
		ServerMeta: srv("c2a1fecc-dc79-4e95-a2f3-de1acef757b8", "435786"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "configmap-ref-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.containers[].envFrom[].configMapRef.name"},
			{Kind: "ConfigMap", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, cm, sec),
	})
}

// serviceSelectorMismatch is a healthy Deployment plus a Service whose selector
// (app=storefront) matches none of the Deployment's pods (app=web), so the
// Service has no endpoints. The Deployment is internally consistent — the fault
// is purely the Service selector against the pod labels, so both are deciding.
// Same SelectorMismatch root cause as the Deployment case, on a different Kind.
func serviceSelectorMismatch(twin bool) Scenario {
	selectorApp := "storefront" // no pod carries app=storefront
	if twin {
		selectorApp = "web"
	}

	// Both statuses are healthy even in the faulty case: the Deployment's pods
	// run fine, and a Service carries no endpoint symptom in its own status —
	// the emptiness lives in Endpoints objects, which are not part of the bundle.
	dep := NewDeployment(DeploymentParams{
		Name:          "web",
		Namespace:     "production",
		App:           "web",
		Replicas:      3,
		SelectorApp:   "web",
		PodApp:        "web",
		ContainerName: "web",
		Image:         "nginx:1.25",
		ContainerPort: 8080,
		ServerMeta:    srv("cf28050f-91c9-430e-ae30-fbdc82e0f669", "131895"),
		Status:        StatusHealthy,
	})

	svc := NewService(ServiceParams{
		Name:        "web",
		Namespace:   "production",
		App:         "web",
		SelectorApp: selectorApp,
		// port == targetPort == containerPort: ports are fully healthy, so the only
		// anomaly is the selector. A 7B conflates port with targetPort, so an
		// unequal port would read as a spurious PortMismatch and mask the selector.
		Port:       8080,
		TargetPort: 8080,
		ClusterIP:  "10.96.144.201",
		ServerMeta: srv("bec0ae58-98c3-45b2-a889-b30bda34dcfb", "490059"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "service-selector-mismatch",
		Group:      GroupNetworking,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "Service", Path: "spec.selector.app"},
			// Cross-document: removing the pod labels leaves a selector that still
			// matches nothing — the emptiness persists, only the comparison is gone.
			{Kind: "Deployment", Path: "spec.template.metadata.labels.app", Hides: true},
		},
		YAML: joinDocs(svc, dep),
	})
}

// servicePortMismatch is a healthy Deployment plus a Service whose selector
// matches the pods (so it has endpoints) but whose targetPort (9090) does not
// match the container's containerPort (8080) — traffic reaches a port nothing
// listens on. Selector and labels are consistent; the fault is targetPort vs
// containerPort, so both are deciding.
func servicePortMismatch(twin bool) Scenario {
	targetPort := 9090 // pods listen on 8080
	if twin {
		targetPort = 8080
	}

	// Both statuses are healthy even in the faulty case: pods are ready and the
	// Service exists; the symptom (refused connections) only shows at traffic
	// time, not in status.
	dep := NewDeployment(DeploymentParams{
		Name:          "checkout",
		Namespace:     "production",
		App:           "checkout",
		Replicas:      2,
		SelectorApp:   "checkout",
		PodApp:        "checkout",
		ContainerName: "checkout",
		Image:         "ghcr.io/acme/checkout:1.4.0",
		ContainerPort: 8080,
		ServerMeta:    srv("9c99aea1-48a8-461e-aa79-348a1c0106d2", "783851"),
		Status:        StatusHealthy,
	})

	svc := NewService(ServiceParams{
		Name:        "checkout",
		Namespace:   "production",
		App:         "checkout",
		SelectorApp: "checkout",
		// port == containerPort (8080), so the ONLY anomalous value is targetPort:
		// removing it must restore a fully healthy manifest for the flip to hold.
		Port:       8080,
		TargetPort: targetPort,
		ClusterIP:  "10.96.72.34",
		ServerMeta: srv("ac90a999-0245-4a18-afbf-0a3faee73512", "539940"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "service-port-mismatch",
		Group:      GroupNetworking,
		FaultClass: FaultPortMismatch,
		DecidingFields: []DecidingField{
			// Removing targetPort genuinely repairs the bundle: it defaults to port
			// (8080), which equals the containerPort — so it is fault-deleting.
			{Kind: "Service", Path: "spec.ports[].targetPort"},
			// Removing containerPort only hides the pods' side of the comparison;
			// the Service still targets 9090 that nothing is known to listen on.
			{Kind: "Deployment", Path: "spec.template.spec.containers[].ports[].containerPort", Hides: true},
		},
		YAML: joinDocs(svc, dep),
	})
}

// serviceAccountWrongName is a Deployment whose pods run as serviceAccount
// "api-runner", but the only ServiceAccount in the bundle is named "api-runner-staging"
// — a dangling reference. Same Ref_NotFound class as the secret/configmap cases,
// a third reference kind, so serviceAccountName joins the cross-scenario profile
// (deciding here, noise wherever a serviceAccount is not the fault).
func serviceAccountWrongName(twin bool) Scenario {
	// Divergence type: environment suffix — the SA exists, but as its staging
	// variant (a copy-paste-between-environments mistake).
	saName, status := "api-runner-staging", StatusFailing
	if twin {
		saName, status = "api-runner", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:               "api",
		Namespace:          "production",
		App:                "api",
		Replicas:           2,
		SelectorApp:        "api",
		PodApp:             "api",
		ContainerName:      "api",
		Image:              "ghcr.io/acme/api:2.3.1",
		ContainerPort:      8080,
		ServiceAccountName: "api-runner",
		ServerMeta:         srv("38b0342f-5926-41c5-a94a-6a8a0d75b137", "683480"),
		Status:             status,
	})

	sa := NewServiceAccount(ServiceAccountParams{
		Name: saName, Namespace: "production", App: "api",
		ServerMeta: srv("18781033-649a-4489-a0ec-9ab31ac2f09d", "408953"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "serviceaccount-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.serviceAccountName"},
			{Kind: "ServiceAccount", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, sa),
	})
}

// replicaSetSelectorMismatch is a single ReplicaSet whose pod template labels
// (app=web-frontend) do not match its selector (app=web) — SelectorMismatch on a
// fourth workload Kind, single document.
func replicaSetSelectorMismatch(twin bool) Scenario {
	podApp, status := "web-frontend", StatusFailing
	if twin {
		podApp, status = "web", StatusHealthy
	}

	rs := NewReplicaSet(ReplicaSetParams{
		Name:          "web",
		Namespace:     "production",
		App:           "web",
		Replicas:      3,
		SelectorApp:   "web",
		PodApp:        podApp,
		ContainerName: "web",
		Image:         "nginx:1.25",
		ContainerPort: 8080,
		ServerMeta:    srv("0b7bbad4-d218-4413-a6f6-1fbdebd23bea", "615340"),
		Status:        status,
	})

	return maybeTwin(twin, Scenario{
		Name:       "replicaset-selector-mismatch",
		Group:      GroupSelector,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "ReplicaSet", Path: "spec.selector.matchLabels.app"},
			{Kind: "ReplicaSet", Path: "spec.template.metadata.labels.app"},
		},
		YAML: joinDocs(rs),
	})
}

// storageClassWrongName is a PVC requesting storageClass "fast-ssd", but the only
// StorageClass is named "fast-ssd-retain" — the claim stays Pending. Ref_NotFound.
func storageClassWrongName(twin bool) Scenario {
	// Divergence type: variant suffix — the class exists as "fast-ssd-retain";
	// the claim asks for plain "fast-ssd" and stays Pending.
	scName, status := "fast-ssd-retain", StatusFailing
	if twin {
		scName, status = "fast-ssd", StatusHealthy
	}

	pvc := NewPVC(PVCParams{
		Name: "api-data", Namespace: "production", App: "api",
		Storage: "10Gi", StorageClass: "fast-ssd",
		ServerMeta: srv("e3da9fde-3e4c-4573-ad94-eab4f9c2645f", "193082"),
		Status:     status,
	})

	sc := NewStorageClass(StorageClassParams{
		Name: scName, App: "api", Provisioner: "ebs.csi.aws.com",
		ServerMeta: srv("fd64cf11-c8ac-4a73-ae4f-b3a8d86e1caa", "666583"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "storageclass-wrong-name",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "PersistentVolumeClaim", Path: "spec.storageClassName"},
			{Kind: "StorageClass", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(pvc, sc),
	})
}

// hpaTargetWrongName is an HPA scaling scaleTargetRef "api", but the Deployment is
// named "api-server" — the HPA targets nothing. Ref_NotFound.
func hpaTargetWrongName(twin bool) Scenario {
	targetName, status := "api", StatusFailing
	if twin {
		targetName, status = "api-server", StatusHealthy
	}

	// The Deployment itself is healthy — only the HPA dangles, so only its
	// status is failing. Its condition text stays symptom-only: the real
	// FailedGetScale message names the missing target, which would plant the
	// answer in a non-deciding field.
	dep := NewDeployment(DeploymentParams{
		Name: "api-server", Namespace: "production", App: "api",
		Replicas: 2, SelectorApp: "api", PodApp: "api",
		ContainerName: "api", Image: "ghcr.io/acme/api:2.3.1", ContainerPort: 8080,
		ServerMeta: srv("7a1c1454-6c27-4260-ae76-457ee5549e01", "910222"),
		Status:     StatusHealthy,
	})

	hpa := NewHPA(HPAParams{
		Name: "api", Namespace: "production", App: "api",
		TargetKind: "Deployment", TargetName: targetName, MinReplicas: 2, MaxReplicas: 10,
		ServerMeta: srv("447a28f5-5360-4ab3-a82b-e351676c6eb2", "164779"),
		Status:     status,
	})

	return maybeTwin(twin, Scenario{
		Name:       "hpa-target-wrong-name",
		Group:      GroupScaling,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "HorizontalPodAutoscaler", Path: "spec.scaleTargetRef.name"},
			{Kind: "Deployment", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(hpa, dep),
	})
}

// vpaTargetWrongName is a VPA right-sizing targetRef "api", but the Deployment is
// named "api-server" — the VPA targets nothing. Ref_NotFound.
func vpaTargetWrongName(twin bool) Scenario {
	targetName, status := "api", StatusFailing
	if twin {
		targetName, status = "api-server", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name: "api-server", Namespace: "production", App: "api",
		Replicas: 2, SelectorApp: "api", PodApp: "api",
		ContainerName: "api", Image: "ghcr.io/acme/api:2.3.1", ContainerPort: 8080,
		ServerMeta: srv("a678d32f-eb25-442e-a0c2-9acdebf50b49", "803902"),
		Status:     StatusHealthy,
	})

	vpa := NewVPA(VPAParams{
		Name: "api", Namespace: "production", App: "api",
		TargetKind: "Deployment", TargetName: targetName,
		ServerMeta: srv("8fc184fa-3829-4b90-adc1-19acca6c9528", "312595"),
		Status:     status,
	})

	return maybeTwin(twin, Scenario{
		Name:       "vpa-target-wrong-name",
		Group:      GroupScaling,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "VerticalPodAutoscaler", Path: "spec.targetRef.name"},
			{Kind: "Deployment", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(vpa, dep),
	})
}

// priorityClassWrongName is a Deployment whose pods request priorityClass
// "high-priority", but the only PriorityClass is named "critical-priority" — the pods
// are rejected by admission. Ref_NotFound.
func priorityClassWrongName(twin bool) Scenario {
	// Divergence type: different word — the class exists as "critical-priority",
	// the pods request "high-priority".
	pcName, status := "critical-priority", StatusFailing
	if twin {
		pcName, status = "high-priority", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name: "api", Namespace: "production", App: "api",
		Replicas: 2, SelectorApp: "api", PodApp: "api",
		ContainerName: "api", Image: "ghcr.io/acme/api:2.3.1", ContainerPort: 8080,
		PriorityClassName: "high-priority",
		ServerMeta:        srv("0d5c514b-754f-4592-ae98-fec791ad64f3", "533125"),
		Status:            status,
	})

	pc := NewPriorityClass(PriorityClassParams{
		Name: pcName, App: "api", Value: 1000000, Description: "critical API pods",
		ServerMeta: srv("ab803b47-5580-4ffe-a95e-cc22c6ddeb92", "186587"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "priorityclass-wrong-name",
		Group:      GroupScaling,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.priorityClassName"},
			{Kind: "PriorityClass", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, pc),
	})
}

// roleBindingRoleWrongName is a RoleBinding granting role "pod-reader" to a
// ServiceAccount that exists, but the only Role is named "pod-viewer" — the grant
// dangles. Ref_NotFound; the subject reference is the healthy distractor.
func roleBindingRoleWrongName(twin bool) Scenario {
	// Divergence type: synonym — the Role exists as "pod-viewer", the binding
	// grants "pod-reader".
	roleName := "pod-viewer"
	if twin {
		roleName = "pod-reader"
	}

	// RBAC kinds and ServiceAccounts have no status subresource — a dangling
	// grant only surfaces at authorization time, so there is nothing to fail.
	sa := NewServiceAccount(ServiceAccountParams{
		Name: "api-sa", Namespace: "production", App: "api",
		ServerMeta: srv("6f6745bf-a31f-436d-ac1f-bc767edb29d8", "338563"),
	})
	role := NewRole(RoleParams{
		Name: roleName, Namespace: "production", App: "api",
		ServerMeta: srv("6d5f3e89-0a38-4c55-a448-81a702ab3f25", "615617"),
	})
	rb := NewRoleBinding(RoleBindingParams{
		Name: "api-read", Namespace: "production", App: "api",
		ServiceAccountName: "api-sa", RoleName: "pod-reader",
		ServerMeta: srv("91b527ad-3120-41f7-a7d6-6a80dad3b594", "349902"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "rolebinding-role-wrong-name",
		Group:      GroupRBAC,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "RoleBinding", Path: "roleRef.name"},
			{Kind: "Role", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(rb, role, sa),
	})
}

// clusterRoleBindingRoleWrongName is a ClusterRoleBinding granting clusterRole
// "node-reader" to a ServiceAccount that exists, but the only ClusterRole is named
// "node-readers" — kubectl auth can-i returns no. Ref_NotFound.
func clusterRoleBindingRoleWrongName(twin bool) Scenario {
	crName := "node-readers"
	if twin {
		crName = "node-reader"
	}

	sa := NewServiceAccount(ServiceAccountParams{
		Name: "api-sa", Namespace: "production", App: "api",
		ServerMeta: srv("54645a22-fb49-4fef-a63a-174389824a05", "469961"),
	})
	cr := NewClusterRole(ClusterRoleParams{
		Name: crName, App: "api",
		ServerMeta: srv("e6a0b733-6944-4ef7-a828-9277b629a14f", "531604"),
	})
	crb := NewClusterRoleBinding(ClusterRoleBindingParams{
		Name: "api-node-read", App: "api", Namespace: "production",
		ServiceAccountName: "api-sa", ClusterRoleName: "node-reader",
		ServerMeta: srv("5ecd7f8a-6eec-45f9-a3be-40609d315b06", "661120"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "clusterrolebinding-role-wrong-name",
		Group:      GroupRBAC,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "ClusterRoleBinding", Path: "roleRef.name"},
			{Kind: "ClusterRole", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(crb, cr, sa),
	})
}

// healthyBundle is a fully consistent Deployment+ConfigMap+Secret (refs resolve,
// selector matches template labels). The control: expected NoFaultFound. It
// measures the false-positive rate and confirms that in a healthy manifest no
// field carries fault signal. No deciding fields.
func healthyBundle() Scenario {
	dep := NewDeployment(DeploymentParams{
		Name:          "api",
		Namespace:     "production",
		App:           "api",
		Replicas:      2,
		SelectorApp:   "api",
		PodApp:        "api",
		ContainerName: "api",
		Image:         "ghcr.io/acme/api:2.3.1",
		ContainerPort: 8080,
		ConfigMapRef:  "api-config",
		SecretRef:     "api-secret",
		ServerMeta:    srv("0dba9035-6531-4d38-a875-84599bb99885", "415975"),
		Status:        StatusHealthy,
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-config", Namespace: "production",
		Data:       map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
		ServerMeta: srv("e756bd51-2da0-494e-adc0-013ed11c33ba", "398895"),
	})

	sec := NewSecret(SecretParams{
		Name:       "api-secret",
		Namespace:  "production",
		StringData: map[string]string{"API_KEY": "redacted-api-key", "DB_PASSWORD": "redacted-password"},
		ServerMeta: srv("4f843458-ca1e-4c30-a2a7-ece4b440d163", "588790"),
	})

	return Scenario{
		Name:       "healthy-bundle",
		Group:      GroupHealthy,
		FaultClass: FaultNoFault,
		YAML:       joinDocs(dep, cm, sec),
	}
}

// envKeyWrongName is a Deployment whose env valueFrom configMapKeyRef points at
// key "LOG_FORMAT", but the ConfigMap only has LOG_LEVEL and REGION — the key in
// valueFrom the Ref_NotFound class description promises is now actually tested.
// The ConfigMap name itself resolves; the key is the only anomaly. The env var
// NAME is a fixed distinct string (APP_LOGGING): with name == key (the old
// shape), removing the deciding key left the var name still echoing the missing
// key, so Table 2a's expected NoFaultFound was never honestly obtainable and
// the env[].name cell was a ground-truth echo, not an independent field.
func envKeyWrongName(twin bool) Scenario {
	envKey, status := "LOG_FORMAT", StatusFailing
	if twin {
		envKey, status = "LOG_LEVEL", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:          "api",
		Namespace:     "production",
		App:           "api",
		Replicas:      2,
		SelectorApp:   "api",
		PodApp:        "api",
		ContainerName: "api",
		Image:         "ghcr.io/acme/api:2.3.1",
		ContainerPort: 8080,
		EnvKey:        envKey,
		EnvName:       "APP_LOGGING",
		EnvConfigMap:  "api-config",
		ServerMeta:    srv("2d92b89e-beb7-4aaa-a0cb-c880c09705e8", "273161"),
		Status:        status,
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-config", Namespace: "production",
		Data:       map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
		ServerMeta: srv("5f916cd0-725e-4848-a748-a18b9de3f865", "956022"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "env-key-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.containers[].env[].valueFrom.configMapKeyRef.key"},
			// data is an atomic map (removed whole); with it gone the key set is
			// unknown, so removal only hides the evidence that LOG_FORMAT is absent.
			{Kind: "ConfigMap", Path: "data", Hides: true},
		},
		YAML: joinDocs(dep, cm),
	})
}

// volumeMountWrongName is a Deployment whose container mounts volume
// "cache-data", but the pod's only volume is named "data" — a dangling
// reference INSIDE one document, no supporting object needed. Divergence type:
// different compound word. Like the single-workload selector scenarios this is
// admission-rejected on a live server, so the failing status is an as-if.
func volumeMountWrongName(twin bool) Scenario {
	mountName, status := "cache-data", StatusFailing
	if twin {
		mountName, status = "data", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:          "api",
		Namespace:     "production",
		App:           "api",
		Replicas:      2,
		SelectorApp:   "api",
		PodApp:        "api",
		ContainerName: "api",
		Image:         "ghcr.io/acme/api:2.3.1",
		ContainerPort: 8080,
		VolumeKind:    "pvc",
		VolumeRef:     "api-cache",
		MountName:     mountName,
		ServerMeta:    srv("5dc74673-31ec-495b-a177-ce2315649347", "655755"),
		Status:        status,
	})

	// The claim resolves and is Bound — the volume side is fully healthy.
	pvc := NewPVC(PVCParams{
		Name: "api-cache", Namespace: "production", App: "api", Storage: "5Gi",
		ServerMeta: srv("70720f99-59e8-473b-a168-dda611c39913", "543351"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "volumemount-wrong-name",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.containers[].volumeMounts[].name"},
			{Kind: "Deployment", Path: "spec.template.spec.volumes[].name", Hides: true},
		},
		YAML: joinDocs(dep, pvc),
	})
}

// jobSecretWrongName is a one-shot Job whose envFrom references secret
// "db-credentials", but the Secret is named "db-secrets" — the same envFrom
// Ref_NotFound as the Deployment case on a third workload Kind, giving
// secretRef.name another cross-scenario appearance. Divergence type: different
// word.
func jobSecretWrongName(twin bool) Scenario {
	secretName, status := "db-secrets", StatusFailing
	if twin {
		secretName, status = "db-credentials", StatusHealthy
	}

	job := NewJob(JobParams{
		Name:          "db-migrate",
		Namespace:     "production",
		App:           "db-migrate",
		PodApp:        "db-migrate",
		ContainerName: "migrate",
		Image:         "ghcr.io/acme/migrate:1.7.0",
		SecretRef:     "db-credentials",
		ServerMeta:    srv("1d691714-f8e4-42b6-a239-4f8c8899dcac", "625032"),
		Status:        status,
	})

	sec := NewSecret(SecretParams{
		Name:       secretName,
		Namespace:  "production",
		StringData: map[string]string{"DB_URL": "redacted-url", "DB_PASSWORD": "redacted-password"},
		ServerMeta: srv("a4472299-7315-4113-a745-508741390046", "929471"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "job-secret-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Job", Path: "spec.template.spec.containers[].envFrom[].secretRef.name"},
			{Kind: "Secret", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(job, sec),
	})
}

// secretWrongNamespace is a Deployment in "production" referencing secret
// "api-secret" — which exists, with exactly that name, but in namespace
// "staging". The only anomaly is the namespace: tests whether the model reads
// namespaces at all instead of just matching names.
func secretWrongNamespace(twin bool) Scenario {
	ns, status := "staging", StatusFailing
	if twin {
		ns, status = "production", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:          "api",
		Namespace:     "production",
		App:           "api",
		Replicas:      2,
		SelectorApp:   "api",
		PodApp:        "api",
		ContainerName: "api",
		Image:         "ghcr.io/acme/api:2.3.1",
		ContainerPort: 8080,
		SecretRef:     "api-secret",
		ServerMeta:    srv("aa813e82-475b-4bb1-acf9-ac39d889e903", "854583"),
		Status:        status,
	})

	sec := NewSecret(SecretParams{
		Name:       "api-secret",
		Namespace:  ns,
		StringData: map[string]string{"API_KEY": "redacted-api-key"},
		ServerMeta: srv("fa87051e-ebfd-430c-a0d4-6a5284344749", "244207"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "secret-wrong-namespace",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.containers[].envFrom[].secretRef.name"},
			// Removing the namespace makes the Secret's location unknown — the
			// reference into production still dangles under a strict reading.
			{Kind: "Secret", Path: "metadata.namespace", Hides: true},
		},
		YAML: joinDocs(dep, sec),
	})
}

// ingressBackendWrongName is an Ingress routing shop.example.com to backend
// service "webapp", but the Service is named "web" — a dangling routing
// reference (503 from the ingress controller). The Service and its Deployment
// are fully healthy.
func ingressBackendWrongName(twin bool) Scenario {
	backend := "webapp"
	if twin {
		backend = "web"
	}

	ing := NewIngress(IngressParams{
		Name: "web", Namespace: "production", App: "web",
		Host: "shop.example.com", ServiceName: backend, ServicePort: 8080,
		ServerMeta: srv("02e00d8a-4990-4642-a898-290e3db4c186", "939296"),
		Status:     StatusHealthy, // the controller assigns an address either way
	})

	svc := NewService(ServiceParams{
		Name: "web", Namespace: "production", App: "web",
		SelectorApp: "web", Port: 8080, TargetPort: 8080,
		ClusterIP:  "10.96.201.77",
		ServerMeta: srv("6f747076-6128-4e48-a9d2-f6d74b5fd2ce", "407167"),
		Status:     StatusHealthy,
	})

	dep := NewDeployment(DeploymentParams{
		Name: "web", Namespace: "production", App: "web",
		Replicas: 3, SelectorApp: "web", PodApp: "web",
		ContainerName: "web", Image: "nginx:1.25", ContainerPort: 8080,
		ServerMeta: srv("f681f5b0-52e8-400c-a386-4123d40a1836", "677343"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "ingress-backend-wrong-name",
		Group:      GroupNetworking,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Ingress", Path: "spec.rules[].http.paths[].backend.service.name"},
			{Kind: "Service", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(ing, svc, dep),
	})
}

// ingressBackendWrongPort is an Ingress routing to the right Service but port
// 9090, which the Service does not expose (it serves 8080 only) — PortMismatch
// at the Ingress→Service hop. Everything else is consistent: the Ingress port
// is the only anomaly, so removing it must restore health for the flip.
func ingressBackendWrongPort(twin bool) Scenario {
	port := 9090
	if twin {
		port = 8080
	}

	ing := NewIngress(IngressParams{
		Name: "payments", Namespace: "production", App: "payments",
		Host: "pay.example.com", ServiceName: "payments", ServicePort: port,
		ServerMeta: srv("9d081258-9105-46a2-a673-ff495f5ef3d8", "212325"),
		Status:     StatusHealthy,
	})

	svc := NewService(ServiceParams{
		Name: "payments", Namespace: "production", App: "payments",
		SelectorApp: "payments", Port: 8080, TargetPort: 8080,
		ClusterIP:  "10.96.88.140",
		ServerMeta: srv("331c0c9c-066c-418a-a7b7-785107243fa1", "745691"),
		Status:     StatusHealthy,
	})

	dep := NewDeployment(DeploymentParams{
		Name: "payments", Namespace: "production", App: "payments",
		Replicas: 2, SelectorApp: "payments", PodApp: "payments",
		ContainerName: "payments", Image: "ghcr.io/acme/payments:3.1.2", ContainerPort: 8080,
		ServerMeta: srv("0ae5e556-3f65-4920-aded-747a33c1f802", "609399"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "ingress-backend-wrong-port",
		Group:      GroupNetworking,
		FaultClass: FaultPortMismatch,
		DecidingFields: []DecidingField{
			{Kind: "Ingress", Path: "spec.rules[].http.paths[].backend.service.port.number"},
			{Kind: "Service", Path: "spec.ports[].port", Hides: true},
		},
		YAML: joinDocs(ing, svc, dep),
	})
}

// serviceStatefulSetPortMismatch is a Service in front of a StatefulSet whose
// targetPort has a transposition typo: 5423 instead of the containerPort 5432.
// Second PortMismatch instance, on a different backing workload Kind and a
// different divergence type (digit transposition).
func serviceStatefulSetPortMismatch(twin bool) Scenario {
	targetPort := 5423
	if twin {
		targetPort = 5432
	}

	svc := NewService(ServiceParams{
		Name: "db", Namespace: "production", App: "db",
		SelectorApp: "db", Port: 5432, TargetPort: targetPort,
		ClusterIP:  "10.96.33.19",
		ServerMeta: srv("7958ebdc-b368-4c86-aa1c-d75383d74d33", "840253"),
		Status:     StatusHealthy,
	})

	sts := NewStatefulSet(StatefulSetParams{
		Name: "db", Namespace: "production", App: "db",
		Replicas: 3, SelectorApp: "db", PodApp: "db",
		ContainerName: "db", Image: "postgres:16.2", ContainerPort: 5432,
		ServerMeta: srv("1cdb7cd8-20b4-48c0-a7e2-6fec08d12bfe", "156743"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "service-statefulset-port-mismatch",
		Group:      GroupNetworking,
		FaultClass: FaultPortMismatch,
		DecidingFields: []DecidingField{
			{Kind: "Service", Path: "spec.ports[].targetPort"},
			{Kind: "StatefulSet", Path: "spec.template.spec.containers[].ports[].containerPort", Hides: true},
		},
		YAML: joinDocs(svc, sts),
	})
}

// The three *-crowded scenarios are profile densifiers (2026-07-04). The
// cross-scenario field profile (fieldprofile.gen.tex, the anti-circularity
// defense) needs every claimed field-key to appear as a NON-deciding healthy
// bystander in ≥2 scored scenarios — but most reference keys had 0–1 such
// appearances, all concentrated in scenarios the 7B cannot score. So each
// crowded scenario injects a fault from a pattern the 7B reliably diagnoses
// (envFrom ref, secret volume, targetPort — the gate-surviving patterns) and
// packs the bundle with fully-healthy witnesses of the under-profiled keys
// (serviceAccountName, claimName, priorityClassName, imagePullSecrets,
// roleRef/subjects, …). A witness needs no model competence to yield data:
// it only has to sit, resolving and healthy, in a scenario that passes the
// gate. Witnesses are distributed so none shares a Kind with the scenario's
// Hides locus (e.g. no second Secret where Secret metadata.name is deciding —
// ResolveLeaves resolves per Kind, so a second doc would become a bogus locus).

// secretRefCrowded injects the secret-ref-wrong-name fault (envFrom secretRef
// dangles) into a bundle crowded with healthy witnesses: configMapRef → CM,
// serviceAccountName → SA, and a PVC-backed volume. Gives configMapRef.name,
// serviceAccountName and claimName non-deciding appearances in a scenario the
// 7B scores ~0.9 on.
func secretRefCrowded(twin bool) Scenario {
	// Divergence type: swapped word order — the Secret exists as "api-cache",
	// the reference asks for "cache-api".
	secretName, status := "api-cache", StatusFailing
	if twin {
		secretName, status = "cache-api", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:               "api",
		Namespace:          "production",
		App:                "api",
		Replicas:           2,
		SelectorApp:        "api",
		PodApp:             "api",
		ContainerName:      "api",
		Image:              "ghcr.io/acme/api:2.3.1",
		ContainerPort:      8080,
		ConfigMapRef:       "api-config",
		SecretRef:          "cache-api",
		ServiceAccountName: "api-runner",
		VolumeKind:         "pvc",
		VolumeRef:          "api-data",
		ServerMeta:         srv("7c3f2e81-9a45-4d1b-8e67-2b9c4f0a5d13", "618442"),
		Status:             status,
	})

	sec := NewSecret(SecretParams{
		Name: secretName, Namespace: "production",
		StringData: map[string]string{"CACHE_URL": "redacted-cache-url"},
		ServerMeta: srv("f24a8c96-3e57-4b02-9d18-c67a1e84b5f0", "224917"),
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-config", Namespace: "production",
		Data:       map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
		ServerMeta: srv("9b615f38-d2c4-47a9-b3e5-081f7d62c9a4", "837156"),
	})

	sa := NewServiceAccount(ServiceAccountParams{
		Name: "api-runner", Namespace: "production", App: "api",
		ServerMeta: srv("4e8a17d5-6f92-4c38-a1b0-d95c3e2f8746", "149528"),
	})

	pvc := NewPVC(PVCParams{
		Name: "api-data", Namespace: "production", App: "api", Storage: "5Gi",
		ServerMeta: srv("a17e94c2-58b3-4f6d-92c8-3d40e6b1f759", "962371"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "secret-ref-crowded",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.containers[].envFrom[].secretRef.name"},
			{Kind: "Secret", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, sec, cm, sa, pvc),
	})
}

// secretVolumeCrowded injects the secret-volume-wrong-name fault (secret volume
// source dangles) into a bundle with healthy witnesses: priorityClassName → PC,
// envFrom configMapRef → CM, serviceAccountName → SA (its second witness
// appearance — the ≥2 rule). No second Secret: metadata.name of Kind Secret is
// this scenario's Hides locus.
func secretVolumeCrowded(twin bool) Scenario {
	// Divergence type: environment prefix — the Secret exists as "api-tls",
	// the volume asks for "prod-api-tls" (the suffix variant lives in
	// serviceaccount-wrong-name).
	secretName, status := "api-tls", StatusFailing
	if twin {
		secretName, status = "prod-api-tls", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:               "api",
		Namespace:          "production",
		App:                "api",
		Replicas:           2,
		SelectorApp:        "api",
		PodApp:             "api",
		ContainerName:      "api",
		Image:              "ghcr.io/acme/api:2.3.1",
		ContainerPort:      8080,
		ConfigMapRef:       "api-config",
		ServiceAccountName: "api-runner",
		PriorityClassName:  "api-critical",
		VolumeKind:         "secret",
		VolumeRef:          "prod-api-tls",
		ServerMeta:         srv("5d29b7f4-1c86-4e53-b9a2-7f01d8c64e35", "528709"),
		Status:             status,
	})

	sec := NewSecret(SecretParams{
		Name: secretName, Namespace: "production",
		StringData: map[string]string{"tls.crt": "redacted-cert", "tls.key": "redacted-key"},
		ServerMeta: srv("e83c51a9-47d0-4b16-8f2e-95a6b3d7c012", "316584"),
	})

	pc := NewPriorityClass(PriorityClassParams{
		Name: "api-critical", App: "api", Value: 1000000, Description: "critical API pods",
		ServerMeta: srv("2f74d8b1-9e35-4a60-8c17-b5e29a4f6d83", "741296"),
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-config", Namespace: "production",
		Data:       map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
		ServerMeta: srv("b98f26e4-53a1-4d7c-9e08-6a2c5f81d4b7", "405163"),
	})

	sa := NewServiceAccount(ServiceAccountParams{
		Name: "api-runner", Namespace: "production", App: "api",
		ServerMeta: srv("638d1a75-e4b9-4f28-a56c-90d7e3b8f142", "872645"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "secret-volume-crowded",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.volumes[].secret.secretName"},
			{Kind: "Secret", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, sec, pc, cm, sa),
	})
}

// servicePortCrowded injects the service-port-mismatch fault (targetPort vs
// containerPort) into a bundle with serviceAccountName → SA and
// imagePullSecrets → pull Secret witnesses. Deliberately SLIM (4 docs): the
// port comparison drowns in a bigger crowd — with the full RBAC chain aboard
// the 7B scored 0/2 (port 8000) and 1/4 (port 3000), so the RBAC witnesses
// moved to configmap-ref-crowded, whose ref pattern tolerates crowding. The
// Secret witness is safe here: the Hides locus lives in the Deployment
// (containerPort), not in Kind Secret.
func servicePortCrowded(twin bool) Scenario {
	// Divergence type: a different well-known port — the Service targets 3000
	// while the pods listen on 8080 (9090 and the digit transposition live in
	// the other two port scenarios). First tried 8000: at k=2 the 7B scored
	// 0/2 — visually near-identical to 8080, the comparison drowned in the
	// crowded bundle. 3000 restores the contrast without touching the prompt.
	targetPort := 3000
	if twin {
		targetPort = 8080
	}

	// All statuses healthy even in the faulty case: pods run, the Service
	// exists; refused connections only show at traffic time (mirrors
	// service-port-mismatch).
	svc := NewService(ServiceParams{
		Name: "payments", Namespace: "production", App: "payments",
		SelectorApp: "payments",
		// port == containerPort (8080): targetPort is the only anomalous value.
		Port: 8080, TargetPort: targetPort,
		ClusterIP:  "10.96.101.57",
		ServerMeta: srv("c45b92e7-8d13-4a6f-b704-1e58c9a2d6f3", "293840"),
		Status:     StatusHealthy,
	})

	dep := NewDeployment(DeploymentParams{
		Name:               "payments",
		Namespace:          "production",
		App:                "payments",
		Replicas:           2,
		SelectorApp:        "payments",
		PodApp:             "payments",
		ContainerName:      "payments",
		Image:              "ghcr.io/acme/payments:3.1.2",
		ContainerPort:      8080,
		ServiceAccountName: "payments-sa",
		ImagePullSecret:    "registry-credentials",
		ServerMeta:         srv("8a06e3d9-2b74-4c15-9f8e-d61a05b7c428", "657092"),
		Status:             StatusHealthy,
	})

	sa := NewServiceAccount(ServiceAccountParams{
		Name: "payments-sa", Namespace: "production", App: "payments",
		ServerMeta: srv("d5f183c6-a927-4e40-8b35-2c9e6f04a1d8", "384521"),
	})

	pull := NewSecret(SecretParams{
		Name: "registry-credentials", Namespace: "production",
		StringData: map[string]string{".dockerconfigjson": "redacted-docker-config"},
		ServerMeta: srv("39d7f5a1-6e82-4c04-b1f6-a48c27e95d30", "764218"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "service-port-crowded",
		Group:      GroupNetworking,
		FaultClass: FaultPortMismatch,
		DecidingFields: []DecidingField{
			{Kind: "Service", Path: "spec.ports[].targetPort"},
			{Kind: "Deployment", Path: "spec.template.spec.containers[].ports[].containerPort", Hides: true},
		},
		YAML: joinDocs(svc, dep, sa, pull),
	})
}

// configMapRefCrowded injects the configmap-ref-wrong-name fault (envFrom
// configMapRef dangles) into a bundle carrying the healthy RBAC chain — SA,
// Role, RoleBinding with both roleRef AND subjects resolving. The RBAC
// witnesses live HERE and not in service-port-crowded because the envFrom-ref
// pattern tolerates crowding (secret-ref-crowded smoked 2/2) while the port
// comparison drowned in it. Deciding Kinds are Deployment and ConfigMap, so
// SA/Role/RoleBinding are safe witness Kinds.
func configMapRefCrowded(twin bool) Scenario {
	// Divergence type: stale unversioned reference — the ConfigMap exists as
	// "team-config-v2", the reference still asks for "team-config".
	cmName, status := "team-config-v2", StatusFailing
	if twin {
		cmName, status = "team-config", StatusHealthy
	}

	dep := NewDeployment(DeploymentParams{
		Name:               "worker",
		Namespace:          "production",
		App:                "worker",
		Replicas:           2,
		SelectorApp:        "worker",
		PodApp:             "worker",
		ContainerName:      "worker",
		Image:              "ghcr.io/acme/worker:1.9.4",
		ContainerPort:      8080,
		ConfigMapRef:       "team-config",
		ServiceAccountName: "worker-sa",
		ServerMeta:         srv("e6a94d27-503b-4f18-9c6d-84b1f2e07a53", "836150"),
		Status:             status,
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: cmName, Namespace: "production",
		Data:       map[string]string{"QUEUE": "jobs", "WORKERS": "4"},
		ServerMeta: srv("0d52c8f1-7e94-4b36-a2d8-5f60b3a19e47", "294781"),
	})

	sa := NewServiceAccount(ServiceAccountParams{
		Name: "worker-sa", Namespace: "production", App: "worker",
		ServerMeta: srv("b2e07c54-186f-4da3-9e51-c7a4d8f36b90", "573629"),
	})

	role := NewRole(RoleParams{
		Name: "queue-reader", Namespace: "production", App: "worker",
		ServerMeta: srv("1b8e64f2-c059-4d83-a7e1-96b3d24c5f70", "508936"),
	})

	rb := NewRoleBinding(RoleBindingParams{
		Name: "queue-reader-binding", Namespace: "production", App: "worker",
		ServiceAccountName: "worker-sa", RoleName: "queue-reader",
		ServerMeta: srv("76c2a9e8-4f31-4b57-8d09-e3a51c68b294", "120473"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "configmap-ref-crowded",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.containers[].envFrom[].configMapRef.name"},
			{Kind: "ConfigMap", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(dep, cm, sa, role, rb),
	})
}

// pdbSelectorMismatch is a PodDisruptionBudget whose selector (app=webapp)
// matches none of the Deployment's pods (app=web) — the budget silently
// protects nothing, and voluntary disruptions can take down every replica.
// The Deployment itself is healthy.
func pdbSelectorMismatch(twin bool) Scenario {
	selector, status := "webapp", StatusFailing
	if twin {
		selector, status = "web", StatusHealthy
	}

	pdb := NewPodDisruptionBudget(PDBParams{
		Name: "web-pdb", Namespace: "production", App: "web",
		MinAvailable: 2, SelectorApp: selector, Pods: 3,
		ServerMeta: srv("a26c8548-8505-4ea5-ae81-4ba2cfba588f", "757331"),
		Status:     status,
	})

	dep := NewDeployment(DeploymentParams{
		Name: "web", Namespace: "production", App: "web",
		Replicas: 3, SelectorApp: "web", PodApp: "web",
		ContainerName: "web", Image: "nginx:1.25", ContainerPort: 8080,
		ServerMeta: srv("9f427d10-5f2b-44e4-a9f5-0da7bfcd1b14", "535677"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "pdb-selector-mismatch",
		Group:      GroupSelector,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "PodDisruptionBudget", Path: "spec.selector.matchLabels.app"},
			{Kind: "Deployment", Path: "spec.template.metadata.labels.app", Hides: true},
		},
		YAML: joinDocs(pdb, dep),
	})
}

// networkPolicySelectorMismatch is a NetworkPolicy whose podSelector
// (app=storefront) matches none of the pods (app=web) — the intended isolation
// applies to nothing. The from-selector targets the pods' own app, so the
// selector under podSelector is the only anomaly. NetworkPolicy has no status;
// the Deployment is healthy either way.
func networkPolicySelectorMismatch(twin bool) Scenario {
	selector := "storefront"
	if twin {
		selector = "web"
	}

	np := NewNetworkPolicy(NetworkPolicyParams{
		Name: "web-allow", Namespace: "production", App: "web",
		PodSelectorApp: selector, FromApp: "web", Port: 8080,
		ServerMeta: srv("d5a0ad14-c48b-4bbf-afaf-e70915f53da9", "955789"),
	})

	dep := NewDeployment(DeploymentParams{
		Name: "web", Namespace: "production", App: "web",
		Replicas: 3, SelectorApp: "web", PodApp: "web",
		ContainerName: "web", Image: "nginx:1.25", ContainerPort: 8080,
		ServerMeta: srv("58aff64f-2c90-44a2-ab14-dec4e76d4511", "396296"),
		Status:     StatusHealthy,
	})

	return maybeTwin(twin, Scenario{
		Name:       "networkpolicy-selector-mismatch",
		Group:      GroupNetworking,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "NetworkPolicy", Path: "spec.podSelector.matchLabels.app"},
			{Kind: "Deployment", Path: "spec.template.metadata.labels.app", Hides: true},
		},
		YAML: joinDocs(np, dep),
	})
}

// roleBindingSubjectWrongName is a RoleBinding whose subject ServiceAccount
// "api-sa" does not exist — the SA is named "api-account". The Role side is
// fully healthy, which gives roleRef.name a non-deciding appearance for its
// cross-scenario profile (deciding in rolebinding-role-wrong-name, noise here).
func roleBindingSubjectWrongName(twin bool) Scenario {
	saName := "api-account"
	if twin {
		saName = "api-sa"
	}

	sa := NewServiceAccount(ServiceAccountParams{
		Name: saName, Namespace: "production", App: "api",
		ServerMeta: srv("f829a1b1-065b-400b-aa8c-6b764dd4c4a7", "698259"),
	})
	role := NewRole(RoleParams{
		Name: "pod-reader", Namespace: "production", App: "api",
		ServerMeta: srv("d96c4ef8-664f-4493-a8c1-788682a980d0", "789978"),
	})
	rb := NewRoleBinding(RoleBindingParams{
		Name: "api-read", Namespace: "production", App: "api",
		ServiceAccountName: "api-sa", RoleName: "pod-reader",
		ServerMeta: srv("028ad1e4-3df7-4d9c-aa61-78ef8797a5cd", "301994"),
	})

	return maybeTwin(twin, Scenario{
		Name:       "rolebinding-subject-wrong-name",
		Group:      GroupRBAC,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "RoleBinding", Path: "subjects[].name"},
			{Kind: "ServiceAccount", Path: "metadata.name", Hides: true},
		},
		YAML: joinDocs(rb, role, sa),
	})
}

// healthyWebStack is a fully consistent Deployment+Service+HPA — a multi-Kind
// shape no faulty scenario uses, so its false-positive rate (and its ablations
// in Table 3b) probe a different surface than the twins do.
func healthyWebStack() Scenario {
	dep := NewDeployment(DeploymentParams{
		Name: "shop", Namespace: "production", App: "shop",
		Replicas: 3, SelectorApp: "shop", PodApp: "shop",
		ContainerName: "shop", Image: "ghcr.io/acme/shop:5.2.0", ContainerPort: 8080,
		ServerMeta: srv("745c73ac-78f1-490d-a1f0-bae2cd4aaf48", "153328"),
		Status:     StatusHealthy,
	})

	svc := NewService(ServiceParams{
		Name: "shop", Namespace: "production", App: "shop",
		SelectorApp: "shop", Port: 8080, TargetPort: 8080,
		ClusterIP:  "10.96.150.42",
		ServerMeta: srv("408f9330-34f1-46ed-a3d8-32368e708fcf", "210666"),
		Status:     StatusHealthy,
	})

	hpa := NewHPA(HPAParams{
		Name: "shop", Namespace: "production", App: "shop",
		TargetKind: "Deployment", TargetName: "shop", MinReplicas: 3, MaxReplicas: 12,
		ServerMeta: srv("7ade4448-b334-4529-ad46-43a46e5facdb", "290943"),
		Status:     StatusHealthy,
	})

	return Scenario{
		Name:       "healthy-web-stack",
		Group:      GroupHealthy,
		FaultClass: FaultNoFault,
		YAML:       joinDocs(dep, svc, hpa),
	}
}

// healthyRBAC is a fully consistent ServiceAccount+Role+RoleBinding — the RBAC
// shape with every reference resolving, probing the false-positive rate on a
// Kind family where the 7B never diagnoses the injected faults.
func healthyRBAC() Scenario {
	sa := NewServiceAccount(ServiceAccountParams{
		Name: "reporting-sa", Namespace: "production", App: "reporting",
		ServerMeta: srv("8b936310-ba4b-4226-a5cd-5aeecb36b4e5", "167029"),
	})
	role := NewRole(RoleParams{
		Name: "report-reader", Namespace: "production", App: "reporting",
		ServerMeta: srv("e1f1318b-ef26-408a-accd-37834a24ec08", "595617"),
	})
	rb := NewRoleBinding(RoleBindingParams{
		Name: "reporting-read", Namespace: "production", App: "reporting",
		ServiceAccountName: "reporting-sa", RoleName: "report-reader",
		ServerMeta: srv("60253c83-8ff3-4d4f-a55e-de858f76accf", "944830"),
	})

	return Scenario{
		Name:       "healthy-rbac",
		Group:      GroupHealthy,
		FaultClass: FaultNoFault,
		YAML:       joinDocs(rb, role, sa),
	}
}

// serverCreated is the fixed creationTimestamp every catalog object carries:
// generators must render byte-identical output on every call, so no clock is
// ever read.
const serverCreated = "2026-06-01T09:00:00Z"

// srv builds one object's ServerMeta: the fixed timestamp, generation 1, and a
// hand-picked literal uid + resourceVersion (deterministic by construction).
// Templates whose Kind has no generation field simply never render it.
func srv(uid, resourceVersion string) ServerMeta {
	return ServerMeta{Created: serverCreated, Generation: 1, ResourceVersion: resourceVersion, UID: uid}
}

// mustRender executes a parsed template against data and returns the trimmed
// YAML. It panics on error: the templates are static and the inputs are typed
// structs, so any failure here is a programming bug, not a runtime condition.
func mustRender(t *template.Template, data any) string {
	var sb strings.Builder
	if err := t.Execute(&sb, data); err != nil {
		panic(err)
	}

	return strings.TrimSpace(sb.String())
}

// joinDocs concatenates manifests into a single multi-document YAML stream.
func joinDocs(docs ...string) string {
	return strings.Join(docs, "\n---\n") + "\n"
}
