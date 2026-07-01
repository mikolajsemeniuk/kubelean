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
}

// DecidingField is a ground-truth fault locus: the field whose value encodes the
// fault, qualified by the Kind of the document it lives in — so metadata.name in
// a Secret is not confused with metadata.name in a Deployment. Path is dotted
// with [] for array levels, e.g.
// spec.template.spec.containers[].envFrom[].secretRef.name. It is resolved to
// concrete pointers against a scenario's YAML by heatmap.ResolveLeaves.
type DecidingField struct {
	Kind string
	Path string
}

// All returns the whole m1 fault catalog, across groups.
func All() []Scenario {
	return []Scenario{
		selectorLabelMismatch(),
		statefulSetSelectorMismatch(),
		daemonSetSelectorMismatch(),
		secretWrongName(),
		configMapRefWrongName(),
		serviceAccountWrongName(),
		imagePullSecretWrongName(),
		pvcClaimWrongName(),
		configMapVolumeWrongName(),
		secretVolumeWrongName(),
		serviceSelectorMismatch(),
		servicePortMismatch(),
		healthyBundle(),
	}
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
func selectorLabelMismatch() Scenario {
	dep := NewDeployment(DeploymentParams{
		Name:          "web",
		Namespace:     "production",
		App:           "web",
		Replicas:      3,
		SelectorApp:   "web",
		PodApp:        "web-frontend",
		ContainerName: "web",
		Image:         "nginx:1.25",
		ContainerPort: 80,
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

	return out
}

// statefulSetSelectorMismatch is a single StatefulSet whose pod template labels
// (app=database) do not match its own selector (app=db) — the same SelectorMismatch
// root cause as the Deployment case, on a second workload Kind, within one
// document (so the 7B handles it, unlike the cross-document Service case).
func statefulSetSelectorMismatch() Scenario {
	sts := NewStatefulSet(StatefulSetParams{
		Name:          "db",
		Namespace:     "production",
		App:           "db",
		Replicas:      3,
		SelectorApp:   "db",
		PodApp:        "database",
		ContainerName: "db",
		Image:         "postgres:16.2",
		ContainerPort: 5432,
	})

	return Scenario{
		Name:       "statefulset-selector-mismatch",
		Group:      GroupSelector,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "StatefulSet", Path: "spec.selector.matchLabels.app"},
			{Kind: "StatefulSet", Path: "spec.template.metadata.labels.app"},
		},
		YAML: joinDocs(sts),
	}
}

// daemonSetSelectorMismatch is a DaemonSet whose pod template labels (app=log-agent)
// do not match its selector (app=agent) — SelectorMismatch on a third workload Kind,
// single-document, so the 7B handles it.
func daemonSetSelectorMismatch() Scenario {
	ds := NewDaemonSet(DaemonSetParams{
		Name:          "agent",
		Namespace:     "production",
		App:           "agent",
		SelectorApp:   "agent",
		PodApp:        "log-agent",
		ContainerName: "agent",
		Image:         "fluent/fluent-bit:3.0.7",
		ContainerPort: 2020,
	})

	return Scenario{
		Name:       "daemonset-selector-mismatch",
		Group:      GroupSelector,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "DaemonSet", Path: "spec.selector.matchLabels.app"},
			{Kind: "DaemonSet", Path: "spec.template.metadata.labels.app"},
		},
		YAML: joinDocs(ds),
	}
}

// pvcClaimWrongName is a Deployment mounting a volume backed by PVC "api-data",
// but the only PersistentVolumeClaim is named "api-datas" — a dangling claim
// (Pending pod in a real cluster). Ref_NotFound.
func pvcClaimWrongName() Scenario {
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
	})

	pvc := NewPVC(PVCParams{Name: "api-datas", Namespace: "production", App: "api", Storage: "10Gi"})

	return Scenario{
		Name:       "pvc-claim-wrong-name",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.volumes[].persistentVolumeClaim.claimName"},
			{Kind: "PersistentVolumeClaim", Path: "metadata.name"},
		},
		YAML: joinDocs(dep, pvc),
	}
}

// configMapVolumeWrongName is a Deployment mounting ConfigMap "api-files" as a
// volume, but the ConfigMap is named "api-file" — same Ref_NotFound, a different
// reference site (volume source, not envFrom) so configMap.name and configMapRef
// .name are distinct field-keys with their own cross-scenario profiles.
func configMapVolumeWrongName() Scenario {
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
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-file", Namespace: "production",
		Data: map[string]string{"app.conf": "level=info"},
	})

	return Scenario{
		Name:       "configmap-volume-wrong-name",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.volumes[].configMap.name"},
			{Kind: "ConfigMap", Path: "metadata.name"},
		},
		YAML: joinDocs(dep, cm),
	}
}

// secretVolumeWrongName is a Deployment mounting Secret "api-certs" as a volume,
// but the Secret is named "api-cert" — Ref_NotFound at the secret volume source.
func secretVolumeWrongName() Scenario {
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
	})

	sec := NewSecret(SecretParams{
		Name: "api-cert", Namespace: "production",
		StringData: map[string]string{"tls.crt": "redacted-cert", "tls.key": "redacted-key"},
	})

	return Scenario{
		Name:       "secret-volume-wrong-name",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.volumes[].secret.secretName"},
			{Kind: "Secret", Path: "metadata.name"},
		},
		YAML: joinDocs(dep, sec),
	}
}

// imagePullSecretWrongName is a Deployment whose pods reference image pull secret
// "registry-creds", but the only Secret in the bundle is named "registry-cred" —
// a dangling reference (ImagePullBackOff in a real cluster). Ref_NotFound, a fourth
// reference kind on the cross-scenario profile.
func imagePullSecretWrongName() Scenario {
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
	})

	sec := NewSecret(SecretParams{
		Name:       "registry-cred",
		Namespace:  "production",
		StringData: map[string]string{".dockerconfigjson": "redacted-docker-config"},
	})

	return Scenario{
		Name:       "imagepull-secret-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.imagePullSecrets[].name"},
			{Kind: "Secret", Path: "metadata.name"},
		},
		YAML: joinDocs(dep, sec),
	}
}

// secretWrongName is a Deployment wired to a ConfigMap (correct — a healthy
// distractor) and a Secret (broken): the Deployment references secret
// "api-secret" but the Secret is actually named "api-secrets". The symmetric
// cm-wrong-name variant would instead break ConfigMapRef against the ConfigMap.
func secretWrongName() Scenario {
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
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-config", Namespace: "production",
		Data: map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
	})

	sec := NewSecret(SecretParams{
		Name:       "api-secrets",
		Namespace:  "production",
		StringData: map[string]string{"API_KEY": "redacted-api-key", "DB_PASSWORD": "redacted-password"},
	})

	out := Scenario{
		Name:       "secret-ref-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.containers[].envFrom[].secretRef.name"},
			{Kind: "Secret", Path: "metadata.name"},
		},
		YAML: joinDocs(dep, cm, sec),
	}

	return out
}

// configMapRefWrongName mirrors secretWrongName with the fault on the ConfigMap
// side: the Deployment's envFrom configMapRef points at "api-config" but the
// ConfigMap is named "api-configs" (the Secret here is the healthy distractor).
// Same Ref_NotFound class, different deciding field — this is what gives
// configMapRef.name a cross-scenario profile (noise in secret-ref-wrong-name,
// deciding here).
func configMapRefWrongName() Scenario {
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
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-configs", Namespace: "production",
		Data: map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
	})

	sec := NewSecret(SecretParams{
		Name:       "api-secret",
		Namespace:  "production",
		StringData: map[string]string{"API_KEY": "redacted-api-key", "DB_PASSWORD": "redacted-password"},
	})

	return Scenario{
		Name:       "configmap-ref-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.containers[].envFrom[].configMapRef.name"},
			{Kind: "ConfigMap", Path: "metadata.name"},
		},
		YAML: joinDocs(dep, cm, sec),
	}
}

// serviceSelectorMismatch is a healthy Deployment plus a Service whose selector
// (app=storefront) matches none of the Deployment's pods (app=web), so the
// Service has no endpoints. The Deployment is internally consistent — the fault
// is purely the Service selector against the pod labels, so both are deciding.
// Same SelectorMismatch root cause as the Deployment case, on a different Kind.
func serviceSelectorMismatch() Scenario {
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
	})

	svc := NewService(ServiceParams{
		Name:        "web",
		Namespace:   "production",
		App:         "web",
		SelectorApp: "storefront", // no pod carries app=storefront
		// port == targetPort == containerPort: ports are fully healthy, so the only
		// anomaly is the selector. A 7B conflates port with targetPort, so an
		// unequal port would read as a spurious PortMismatch and mask the selector.
		Port:       8080,
		TargetPort: 8080,
	})

	return Scenario{
		Name:       "service-selector-mismatch",
		Group:      GroupNetworking,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "Service", Path: "spec.selector.app"},
			{Kind: "Deployment", Path: "spec.template.metadata.labels.app"},
		},
		YAML: joinDocs(svc, dep),
	}
}

// servicePortMismatch is a healthy Deployment plus a Service whose selector
// matches the pods (so it has endpoints) but whose targetPort (9090) does not
// match the container's containerPort (8080) — traffic reaches a port nothing
// listens on. Selector and labels are consistent; the fault is targetPort vs
// containerPort, so both are deciding.
func servicePortMismatch() Scenario {
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
	})

	svc := NewService(ServiceParams{
		Name:        "checkout",
		Namespace:   "production",
		App:         "checkout",
		SelectorApp: "checkout",
		// port == containerPort (8080), so the ONLY anomalous value is targetPort:
		// removing it must restore a fully healthy manifest for the flip to hold.
		Port:       8080,
		TargetPort: 9090, // pods listen on 8080
	})

	return Scenario{
		Name:       "service-port-mismatch",
		Group:      GroupNetworking,
		FaultClass: FaultPortMismatch,
		DecidingFields: []DecidingField{
			{Kind: "Service", Path: "spec.ports[].targetPort"},
			{Kind: "Deployment", Path: "spec.template.spec.containers[].ports[].containerPort"},
		},
		YAML: joinDocs(svc, dep),
	}
}

// serviceAccountWrongName is a Deployment whose pods run as serviceAccount
// "api-runner", but the only ServiceAccount in the bundle is named "api-runners"
// — a dangling reference. Same Ref_NotFound class as the secret/configmap cases,
// a third reference kind, so serviceAccountName joins the cross-scenario profile
// (deciding here, noise wherever a serviceAccount is not the fault).
func serviceAccountWrongName() Scenario {
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
	})

	sa := NewServiceAccount(ServiceAccountParams{
		Name: "api-runners", Namespace: "production", App: "api",
	})

	return Scenario{
		Name:       "serviceaccount-wrong-name",
		Group:      GroupReferences,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.serviceAccountName"},
			{Kind: "ServiceAccount", Path: "metadata.name"},
		},
		YAML: joinDocs(dep, sa),
	}
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
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-config", Namespace: "production",
		Data: map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
	})

	sec := NewSecret(SecretParams{
		Name:       "api-secret",
		Namespace:  "production",
		StringData: map[string]string{"API_KEY": "redacted-api-key", "DB_PASSWORD": "redacted-password"},
	})

	return Scenario{
		Name:       "healthy-bundle",
		Group:      GroupHealthy,
		FaultClass: FaultNoFault,
		YAML:       joinDocs(dep, cm, sec),
	}
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
