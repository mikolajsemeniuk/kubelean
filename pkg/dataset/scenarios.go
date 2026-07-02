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
		replicaSetSelectorMismatch(),
		secretWrongName(),
		configMapRefWrongName(),
		serviceAccountWrongName(),
		imagePullSecretWrongName(),
		pvcClaimWrongName(),
		configMapVolumeWrongName(),
		secretVolumeWrongName(),
		storageClassWrongName(),
		hpaTargetWrongName(),
		vpaTargetWrongName(),
		priorityClassWrongName(),
		roleBindingRoleWrongName(),
		clusterRoleBindingRoleWrongName(),
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
//
// Note on all four single-workload selector scenarios (Deployment, StatefulSet,
// DaemonSet, ReplicaSet): a live API server rejects this manifest at admission
// ("selector does not match template labels"), so their failing status is an
// as-if — they model the pre-apply review case, kept in kubectl-get shape so
// every scenario's field population is comparable.
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
		ServerMeta:    srv("18f0da56-db3c-43bf-a378-f3fb0f06c6a5", "825289"),
		Status:        StatusFailing,
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
		ServerMeta:    srv("2fa8047b-869d-4724-a70d-71337826cfd5", "531795"),
		Status:        StatusFailing,
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
		ServerMeta:    srv("d34cc84d-ef05-46c7-a721-d50e28b1e8f8", "306140"),
		Status:        StatusFailing,
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
		ServerMeta:    srv("6c0f7a1e-ae97-4bc9-a624-94f5e2bea039", "883794"),
		Status:        StatusFailing,
	})

	// The mis-named claim itself is a healthy, Bound PVC — it is simply not the
	// one the Deployment asks for.
	pvc := NewPVC(PVCParams{
		Name: "api-datas", Namespace: "production", App: "api", Storage: "10Gi",
		ServerMeta: srv("47c968e0-b76d-4f85-a08e-e58cbd43b5d8", "380426"),
		Status:     StatusHealthy,
	})

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
		ServerMeta:    srv("684f1f14-f07d-4970-a119-b198eb64379c", "364255"),
		Status:        StatusFailing,
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-file", Namespace: "production",
		Data:       map[string]string{"app.conf": "level=info"},
		ServerMeta: srv("c534b5a0-3d0c-4730-aff7-8756bcf71a2e", "391375"),
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
		ServerMeta:    srv("0283c6bf-adad-403a-a2ea-345f6ed2a76a", "313324"),
		Status:        StatusFailing,
	})

	sec := NewSecret(SecretParams{
		Name: "api-cert", Namespace: "production",
		StringData: map[string]string{"tls.crt": "redacted-cert", "tls.key": "redacted-key"},
		ServerMeta: srv("ce0f4418-e6c8-4394-a7a3-24f4b0f42f21", "275846"),
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
		ServerMeta:      srv("241c3377-c6c1-4b8a-adc5-bd7b44c3804b", "993906"),
		Status:          StatusFailing,
	})

	sec := NewSecret(SecretParams{
		Name:       "registry-cred",
		Namespace:  "production",
		StringData: map[string]string{".dockerconfigjson": "redacted-docker-config"},
		ServerMeta: srv("dfd4b5d5-74fd-4d93-a55d-a947f62d9c70", "703368"),
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
		ServerMeta:    srv("5e2ef0e0-0946-4787-a5cc-dc0a2531bc43", "152839"),
		Status:        StatusFailing,
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-config", Namespace: "production",
		Data:       map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
		ServerMeta: srv("aa6a9647-4517-4529-a6d9-73e1c5d4da0a", "228326"),
	})

	sec := NewSecret(SecretParams{
		Name:       "api-secrets",
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
		ServerMeta:    srv("da64943b-5bfc-40c4-aa5e-c5f7d6f776d7", "375965"),
		Status:        StatusFailing,
	})

	cm := NewConfigmap(ConfigmapParams{
		Name: "api-configs", Namespace: "production",
		Data:       map[string]string{"LOG_LEVEL": "info", "REGION": "eu-west-1"},
		ServerMeta: srv("264acd9c-8700-4e71-a64e-5dc16a3e6b37", "605340"),
	})

	sec := NewSecret(SecretParams{
		Name:       "api-secret",
		Namespace:  "production",
		StringData: map[string]string{"API_KEY": "redacted-api-key", "DB_PASSWORD": "redacted-password"},
		ServerMeta: srv("c2a1fecc-dc79-4e95-a2f3-de1acef757b8", "435786"),
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
	// Both statuses are healthy: the Deployment's pods run fine, and a Service
	// carries no endpoint symptom in its own status — the emptiness lives in
	// Endpoints objects, which are not part of the bundle.
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
		SelectorApp: "storefront", // no pod carries app=storefront
		// port == targetPort == containerPort: ports are fully healthy, so the only
		// anomaly is the selector. A 7B conflates port with targetPort, so an
		// unequal port would read as a spurious PortMismatch and mask the selector.
		Port:       8080,
		TargetPort: 8080,
		ServerMeta: srv("bec0ae58-98c3-45b2-a889-b30bda34dcfb", "490059"),
		Status:     StatusHealthy,
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
	// Both statuses are healthy: pods are ready and the Service exists; the
	// symptom (refused connections) only shows at traffic time, not in status.
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
		TargetPort: 9090, // pods listen on 8080
		ServerMeta: srv("ac90a999-0245-4a18-afbf-0a3faee73512", "539940"),
		Status:     StatusHealthy,
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
		ServerMeta:         srv("38b0342f-5926-41c5-a94a-6a8a0d75b137", "683480"),
		Status:             StatusFailing,
	})

	sa := NewServiceAccount(ServiceAccountParams{
		Name: "api-runners", Namespace: "production", App: "api",
		ServerMeta: srv("18781033-649a-4489-a0ec-9ab31ac2f09d", "408953"),
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

// replicaSetSelectorMismatch is a single ReplicaSet whose pod template labels
// (app=web-frontend) do not match its selector (app=web) — SelectorMismatch on a
// fourth workload Kind, single document.
func replicaSetSelectorMismatch() Scenario {
	rs := NewReplicaSet(ReplicaSetParams{
		Name:          "web",
		Namespace:     "production",
		App:           "web",
		Replicas:      3,
		SelectorApp:   "web",
		PodApp:        "web-frontend",
		ContainerName: "web",
		Image:         "nginx:1.25",
		ContainerPort: 8080,
		ServerMeta:    srv("0b7bbad4-d218-4413-a6f6-1fbdebd23bea", "615340"),
		Status:        StatusFailing,
	})

	return Scenario{
		Name:       "replicaset-selector-mismatch",
		Group:      GroupSelector,
		FaultClass: FaultSelectorMismatch,
		DecidingFields: []DecidingField{
			{Kind: "ReplicaSet", Path: "spec.selector.matchLabels.app"},
			{Kind: "ReplicaSet", Path: "spec.template.metadata.labels.app"},
		},
		YAML: joinDocs(rs),
	}
}

// storageClassWrongName is a PVC requesting storageClass "fast-ssd", but the only
// StorageClass is named "fast-ssds" — the claim stays Pending. Ref_NotFound.
func storageClassWrongName() Scenario {
	pvc := NewPVC(PVCParams{
		Name: "api-data", Namespace: "production", App: "api",
		Storage: "10Gi", StorageClass: "fast-ssd",
		ServerMeta: srv("e3da9fde-3e4c-4573-ad94-eab4f9c2645f", "193082"),
		Status:     StatusFailing, // the claim stays Pending
	})

	sc := NewStorageClass(StorageClassParams{
		Name: "fast-ssds", App: "api", Provisioner: "ebs.csi.aws.com",
		ServerMeta: srv("fd64cf11-c8ac-4a73-ae4f-b3a8d86e1caa", "666583"),
	})

	return Scenario{
		Name:       "storageclass-wrong-name",
		Group:      GroupVolumes,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "PersistentVolumeClaim", Path: "spec.storageClassName"},
			{Kind: "StorageClass", Path: "metadata.name"},
		},
		YAML: joinDocs(pvc, sc),
	}
}

// hpaTargetWrongName is an HPA scaling scaleTargetRef "api", but the Deployment is
// named "api-server" — the HPA targets nothing. Ref_NotFound.
func hpaTargetWrongName() Scenario {
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
		TargetKind: "Deployment", TargetName: "api", MinReplicas: 2, MaxReplicas: 10,
		ServerMeta: srv("447a28f5-5360-4ab3-a82b-e351676c6eb2", "164779"),
		Status:     StatusFailing,
	})

	return Scenario{
		Name:       "hpa-target-wrong-name",
		Group:      GroupScaling,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "HorizontalPodAutoscaler", Path: "spec.scaleTargetRef.name"},
			{Kind: "Deployment", Path: "metadata.name"},
		},
		YAML: joinDocs(hpa, dep),
	}
}

// vpaTargetWrongName is a VPA right-sizing targetRef "api", but the Deployment is
// named "api-server" — the VPA targets nothing. Ref_NotFound.
func vpaTargetWrongName() Scenario {
	dep := NewDeployment(DeploymentParams{
		Name: "api-server", Namespace: "production", App: "api",
		Replicas: 2, SelectorApp: "api", PodApp: "api",
		ContainerName: "api", Image: "ghcr.io/acme/api:2.3.1", ContainerPort: 8080,
		ServerMeta: srv("a678d32f-eb25-442e-a0c2-9acdebf50b49", "803902"),
		Status:     StatusHealthy,
	})

	vpa := NewVPA(VPAParams{
		Name: "api", Namespace: "production", App: "api",
		TargetKind: "Deployment", TargetName: "api",
		ServerMeta: srv("8fc184fa-3829-4b90-adc1-19acca6c9528", "312595"),
		Status:     StatusFailing,
	})

	return Scenario{
		Name:       "vpa-target-wrong-name",
		Group:      GroupScaling,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "VerticalPodAutoscaler", Path: "spec.targetRef.name"},
			{Kind: "Deployment", Path: "metadata.name"},
		},
		YAML: joinDocs(vpa, dep),
	}
}

// priorityClassWrongName is a Deployment whose pods request priorityClass
// "high-priority", but the only PriorityClass is named "high-priorities" — the pods
// are rejected by admission. Ref_NotFound.
func priorityClassWrongName() Scenario {
	dep := NewDeployment(DeploymentParams{
		Name: "api", Namespace: "production", App: "api",
		Replicas: 2, SelectorApp: "api", PodApp: "api",
		ContainerName: "api", Image: "ghcr.io/acme/api:2.3.1", ContainerPort: 8080,
		PriorityClassName: "high-priority",
		ServerMeta:        srv("0d5c514b-754f-4592-ae98-fec791ad64f3", "533125"),
		Status:            StatusFailing,
	})

	pc := NewPriorityClass(PriorityClassParams{
		Name: "high-priorities", App: "api", Value: 1000000, Description: "critical API pods",
		ServerMeta: srv("ab803b47-5580-4ffe-a95e-cc22c6ddeb92", "186587"),
	})

	return Scenario{
		Name:       "priorityclass-wrong-name",
		Group:      GroupScaling,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "Deployment", Path: "spec.template.spec.priorityClassName"},
			{Kind: "PriorityClass", Path: "metadata.name"},
		},
		YAML: joinDocs(dep, pc),
	}
}

// roleBindingRoleWrongName is a RoleBinding granting role "pod-reader" to a
// ServiceAccount that exists, but the only Role is named "pod-readers" — the grant
// dangles. Ref_NotFound; the subject reference is the healthy distractor.
func roleBindingRoleWrongName() Scenario {
	// RBAC kinds and ServiceAccounts have no status subresource — a dangling
	// grant only surfaces at authorization time, so there is nothing to fail.
	sa := NewServiceAccount(ServiceAccountParams{
		Name: "api-sa", Namespace: "production", App: "api",
		ServerMeta: srv("6f6745bf-a31f-436d-ac1f-bc767edb29d8", "338563"),
	})
	role := NewRole(RoleParams{
		Name: "pod-readers", Namespace: "production", App: "api",
		ServerMeta: srv("6d5f3e89-0a38-4c55-a448-81a702ab3f25", "615617"),
	})
	rb := NewRoleBinding(RoleBindingParams{
		Name: "api-read", Namespace: "production", App: "api",
		ServiceAccountName: "api-sa", RoleName: "pod-reader",
		ServerMeta: srv("91b527ad-3120-41f7-a7d6-6a80dad3b594", "349902"),
	})

	return Scenario{
		Name:       "rolebinding-role-wrong-name",
		Group:      GroupRBAC,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "RoleBinding", Path: "roleRef.name"},
			{Kind: "Role", Path: "metadata.name"},
		},
		YAML: joinDocs(rb, role, sa),
	}
}

// clusterRoleBindingRoleWrongName is a ClusterRoleBinding granting clusterRole
// "node-reader" to a ServiceAccount that exists, but the only ClusterRole is named
// "node-readers" — kubectl auth can-i returns no. Ref_NotFound.
func clusterRoleBindingRoleWrongName() Scenario {
	sa := NewServiceAccount(ServiceAccountParams{
		Name: "api-sa", Namespace: "production", App: "api",
		ServerMeta: srv("54645a22-fb49-4fef-a63a-174389824a05", "469961"),
	})
	cr := NewClusterRole(ClusterRoleParams{
		Name: "node-readers", App: "api",
		ServerMeta: srv("e6a0b733-6944-4ef7-a828-9277b629a14f", "531604"),
	})
	crb := NewClusterRoleBinding(ClusterRoleBindingParams{
		Name: "api-node-read", App: "api", Namespace: "production",
		ServiceAccountName: "api-sa", ClusterRoleName: "node-reader",
		ServerMeta: srv("5ecd7f8a-6eec-45f9-a3be-40609d315b06", "661120"),
	})

	return Scenario{
		Name:       "clusterrolebinding-role-wrong-name",
		Group:      GroupRBAC,
		FaultClass: FaultRefNotFound,
		DecidingFields: []DecidingField{
			{Kind: "ClusterRoleBinding", Path: "roleRef.name"},
			{Kind: "ClusterRole", Path: "metadata.name"},
		},
		YAML: joinDocs(crb, cr, sa),
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
