package dataset

import (
	"text/template"

	_ "embed"
)

// PodParams are the values substituted into templates/pod.yaml. A bare Pod carries
// the same pod-spec fault surface as a workload template, unmanaged by a controller.
type PodParams struct {
	Name          string
	Namespace     string
	App           string
	ContainerName string
	Image         string
	ContainerPort int
}

//go:embed templates/pod.yaml
var podYAML string

var podTemplate = template.Must(template.New("pod").Parse(podYAML))

// NewPod renders a bare Pod manifest from the given params.
func NewPod(p PodParams) string {
	return mustRender(podTemplate, p)
}
