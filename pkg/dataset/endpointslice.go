package dataset

import (
	"text/template"

	_ "embed"
)

// EndpointSliceParams are the values substituted into templates/endpointslice.yaml.
// The fault site is the kubernetes.io/service-name label (which Service the slice
// backs — a dangling or mismatched value detaches the endpoints) and the port.
type EndpointSliceParams struct {
	Name        string
	Namespace   string
	App         string
	ServiceName string // labels."kubernetes.io/service-name"
	Address     string
	Port        int
}

//go:embed templates/endpointslice.yaml
var endpointSliceYAML string

var endpointSliceTemplate = template.Must(template.New("endpointslice").Parse(endpointSliceYAML))

// NewEndpointSlice renders an EndpointSlice manifest from the given params.
func NewEndpointSlice(p EndpointSliceParams) string {
	return mustRender(endpointSliceTemplate, p)
}
