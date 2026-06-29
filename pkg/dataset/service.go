package dataset

import (
	"text/template"

	_ "embed"
)

// ServiceParams are the values substituted into templates/service.yaml.
//
// SelectorApp is separate from the target pods' app label so a selector mismatch
// (the Service selects no pods → no endpoints) can be injected; TargetPort is
// separate from the pods' containerPort so a port mismatch can be injected. A
// healthy Service has SelectorApp == pod app and TargetPort == containerPort.
type ServiceParams struct {
	Name        string
	Namespace   string
	App         string // metadata.labels.app
	SelectorApp string // spec.selector.app
	Port        int    // spec.ports[].port
	TargetPort  int    // spec.ports[].targetPort
}

//go:embed templates/service.yaml
var serviceYAML string

var serviceTemplate = template.Must(template.New("service").Parse(serviceYAML))

// NewService renders a Service manifest from the given params.
func NewService(p ServiceParams) string {
	return mustRender(serviceTemplate, p)
}
