package dataset

import (
	"text/template"

	_ "embed"
)

// DeploymentParams are the values substituted into templates/deploy.yaml.
//
// Label fields are intentionally separate so a selector/template mismatch can
// be injected: a healthy Deployment has App == SelectorApp == PodApp.
// ConfigMapRef and SecretRef are optional — left empty, the envFrom block is
// omitted entirely.
type DeploymentParams struct {
	Name          string
	Namespace     string
	App           string // metadata.labels.app
	Replicas      int
	SelectorApp   string // spec.selector.matchLabels.app
	PodApp        string // spec.template.metadata.labels.app
	ContainerName      string
	Image              string
	ContainerPort      int
	PriorityClassName  string // optional: pod priorityClassName ("" omits it)
	ServiceAccountName string // optional: pod serviceAccountName ("" omits it)
	ImagePullSecret    string // optional: imagePullSecrets[].name ("" omits it)
	ConfigMapRef       string // optional: envFrom configMapRef name ("" omits it)
	SecretRef          string // optional: envFrom secretRef name ("" omits it)
	VolumeKind         string // optional: volume source — "pvc" | "configMap" | "secret"
	VolumeRef          string // optional: the referenced name ("" omits the volume)
}

//go:embed templates/deploy.yaml
var deployYAML string

var deploymentTemplate = template.Must(template.New("deploy").Parse(deployYAML))

// NewDeployment renders a Deployment manifest from the given params.
func NewDeployment(p DeploymentParams) string {
	return mustRender(deploymentTemplate, p)
}
