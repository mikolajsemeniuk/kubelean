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
	Name               string
	Namespace          string
	App                string // metadata.labels.app
	Replicas           int
	SelectorApp        string // spec.selector.matchLabels.app
	PodApp             string // spec.template.metadata.labels.app
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
	MountName          string // optional: volumeMounts[].name ("" defaults to "data", the volumes[].name)
	EnvKey             string // optional: env[].valueFrom.configMapKeyRef.key ("" omits the env block)
	EnvName            string // optional: env[].name ("" defaults to EnvKey)
	EnvConfigMap       string // optional: env[].valueFrom.configMapKeyRef.name
	ServerMeta                // optional: server-assigned metadata (kubectl get shape)
	Status             string // optional: StatusHealthy | StatusFailing ("" omits status)
}

//go:embed templates/deploy.yaml
var deployYAML string

var deploymentTemplate = template.Must(template.New("deploy").Parse(deployYAML))

// NewDeployment renders a Deployment manifest from the given params. MountName
// defaults to "data" (the volumes[].name) so existing callers render unchanged;
// a divergent MountName is the volumeMount→volume dangling-reference fault.
// EnvName defaults to EnvKey; a scenario injecting a wrong KEY must set a
// distinct EnvName, or the var name echoes the missing key after the deciding
// field is removed and the fault-deleting flip can never honestly read
// NoFaultFound.
func NewDeployment(p DeploymentParams) string {
	if p.MountName == "" {
		p.MountName = "data"
	}
	if p.EnvName == "" {
		p.EnvName = p.EnvKey
	}
	return mustRender(deploymentTemplate, p)
}
