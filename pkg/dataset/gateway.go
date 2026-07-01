package dataset

import (
	"text/template"

	_ "embed"
)

// GatewayParams are the values substituted into templates/gateway.yaml. The fault
// site is gatewayClassName (a reference to a GatewayClass that may not exist).
type GatewayParams struct {
	Name         string
	Namespace    string
	App          string
	GatewayClass string // spec.gatewayClassName
	Port         int
}

//go:embed templates/gateway.yaml
var gatewayYAML string

var gatewayTemplate = template.Must(template.New("gateway").Parse(gatewayYAML))

// NewGateway renders a Gateway (Gateway API) manifest from the given params.
func NewGateway(p GatewayParams) string {
	return mustRender(gatewayTemplate, p)
}

// HTTPRouteParams are the values substituted into templates/httproute.yaml. The
// fault sites are parentRefs (the Gateway it attaches to) and backendRefs (the
// Service it routes to) — either can dangle.
type HTTPRouteParams struct {
	Name        string
	Namespace   string
	App         string
	GatewayName string // spec.parentRefs[].name
	ServiceName string // spec.rules[].backendRefs[].name
	ServicePort int
}

//go:embed templates/httproute.yaml
var httpRouteYAML string

var httpRouteTemplate = template.Must(template.New("httproute").Parse(httpRouteYAML))

// NewHTTPRoute renders an HTTPRoute (Gateway API) manifest from the given params.
func NewHTTPRoute(p HTTPRouteParams) string {
	return mustRender(httpRouteTemplate, p)
}

// GatewayClassParams are the values substituted into templates/gatewayclass.yaml.
// A GatewayClass is cluster-scoped and is the target of a Gateway's
// gatewayClassName.
type GatewayClassParams struct {
	Name       string
	App        string
	Controller string
}

//go:embed templates/gatewayclass.yaml
var gatewayClassYAML string

var gatewayClassTemplate = template.Must(template.New("gatewayclass").Parse(gatewayClassYAML))

// NewGatewayClass renders a GatewayClass (Gateway API) manifest from the params.
func NewGatewayClass(p GatewayClassParams) string {
	return mustRender(gatewayClassTemplate, p)
}
