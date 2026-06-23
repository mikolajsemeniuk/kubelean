package dataset

import (
	"strings"
	"text/template"
)

// Scenario is one faulty instance of the m1 dataset: the rendered manifest(s)
// plus the ground truth used to score root-cause analysis in m2.
type Scenario struct {
	Name           string   // stable id
	FaultClass     string   // ground-truth label
	DecidingFields []string // canonical field-keys whose values encode the fault
	YAML           string   // rendered manifest(s); multi-document scenarios are --- joined
}

// joinDocs concatenates manifests into a single multi-document YAML stream.
func joinDocs(docs ...string) string {
	return strings.Join(docs, "\n---\n") + "\n"
}

// Scenarios returns the m1 fault catalog.
func Scenarios() []Scenario {
	return []Scenario{
		selectorLabelMismatch(),
		secretWrongName(),
	}
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
		FaultClass: "SelectorLabelMismatch",
		DecidingFields: []string{
			"spec.selector.matchLabels.app",
			"spec.template.metadata.labels.app",
		},
		YAML: joinDocs(dep),
	}

	return out
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
		FaultClass: "SecretRefNotFound",
		DecidingFields: []string{
			"spec.template.spec.containers[].envFrom[].secretRef.name",
			"metadata.name",
		},
		YAML: joinDocs(dep, cm, sec),
	}

	return out
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
