package dataset

import (
	"text/template"

	_ "embed"
)

// DaemonSetParams are the values substituted into templates/daemonset.yaml. As
// with Deployment/StatefulSet, the label fields are separate so a selector/template
// mismatch can be injected: a healthy DaemonSet has App == SelectorApp == PodApp.
type DaemonSetParams struct {
	Name          string
	Namespace     string
	App           string
	SelectorApp   string
	PodApp        string
	ContainerName string
	Image         string
	ContainerPort int
}

//go:embed templates/daemonset.yaml
var daemonSetYAML string

var daemonSetTemplate = template.Must(template.New("daemonset").Parse(daemonSetYAML))

// NewDaemonSet renders a DaemonSet manifest from the given params.
func NewDaemonSet(p DaemonSetParams) string {
	return mustRender(daemonSetTemplate, p)
}
