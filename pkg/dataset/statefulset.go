package dataset

import (
	"text/template"

	_ "embed"
)

// StatefulSetParams are the values substituted into templates/statefulset.yaml.
// Like DeploymentParams, the label fields are separate so a selector/template
// mismatch can be injected: a healthy StatefulSet has App == SelectorApp == PodApp.
type StatefulSetParams struct {
	Name          string
	Namespace     string
	App           string
	Replicas      int
	SelectorApp   string
	PodApp        string
	ContainerName string
	Image         string
	ContainerPort int
	ServiceName   string // optional: spec.serviceName ("" defaults to Name, the pre-existing behavior); a divergent value is the governing-Service dangling reference
	ServerMeta           // optional: server-assigned metadata (kubectl get shape)
	Status        string // optional: StatusHealthy | StatusFailing ("" omits status)
}

//go:embed templates/statefulset.yaml
var statefulSetYAML string

var statefulSetTemplate = template.Must(template.New("statefulset").Parse(statefulSetYAML))

// NewStatefulSet renders a StatefulSet manifest from the given params.
func NewStatefulSet(p StatefulSetParams) string {
	return mustRender(statefulSetTemplate, p)
}
