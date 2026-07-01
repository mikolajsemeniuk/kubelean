package dataset

import (
	"text/template"

	_ "embed"
)

// IngressParams are the values substituted into templates/ingress.yaml. The fault
// site is the backend service name (a routing reference that can dangle).
type IngressParams struct {
	Name         string
	Namespace    string
	App          string
	IngressClass string
	Host         string
	ServiceName  string // spec.rules[].http.paths[].backend.service.name
	ServicePort  int
}

//go:embed templates/ingress.yaml
var ingressYAML string

var ingressTemplate = template.Must(template.New("ingress").Parse(ingressYAML))

// NewIngress renders an Ingress manifest from the given params.
func NewIngress(p IngressParams) string {
	return mustRender(ingressTemplate, p)
}

// IngressClassParams are the values substituted into templates/ingressclass.yaml.
// An IngressClass is cluster-scoped and is the target of an Ingress's
// ingressClassName.
type IngressClassParams struct {
	Name       string
	App        string
	Controller string
}

//go:embed templates/ingressclass.yaml
var ingressClassYAML string

var ingressClassTemplate = template.Must(template.New("ingressclass").Parse(ingressClassYAML))

// NewIngressClass renders an IngressClass manifest from the given params.
func NewIngressClass(p IngressClassParams) string {
	return mustRender(ingressClassTemplate, p)
}
