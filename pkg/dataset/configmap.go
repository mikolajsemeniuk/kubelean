package dataset

import (
	"text/template"

	_ "embed"
)

// ConfigmapParams are the values substituted into templates/cm.yaml. Data is
// rendered in sorted key order, so output is deterministic.
type ConfigmapParams struct {
	Name      string
	Namespace string
	Data      map[string]string
}

//go:embed templates/cm.yaml
var cmYAML string

var configmapTemplate = template.Must(template.New("cm").Parse(cmYAML))

// NewConfigmap renders a ConfigMap manifest from the given params.
func NewConfigmap(p ConfigmapParams) string {
	return mustRender(configmapTemplate, p)
}
