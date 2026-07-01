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
	Type        string // spec.type — "" defaults to ClusterIP; else NodePort/LoadBalancer
	Headless    bool   // spec.clusterIP: None (headless, e.g. for a StatefulSet)
	SelectorApp string // spec.selector.app
	Port        int    // spec.ports[].port
	TargetPort  int    // spec.ports[].targetPort
	NodePort    int    // spec.ports[].nodePort (0 omits it; only valid for NodePort)
}

//go:embed templates/service.yaml
var serviceYAML string

var serviceTemplate = template.Must(template.New("service").Parse(serviceYAML))

// NewService renders a Service manifest from the given params. Type defaults to
// ClusterIP so existing callers render unchanged.
func NewService(p ServiceParams) string {
	if p.Type == "" {
		p.Type = "ClusterIP"
	}
	return mustRender(serviceTemplate, p)
}
