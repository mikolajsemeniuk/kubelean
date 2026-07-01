package dataset

import (
	"text/template"

	_ "embed"
)

// ServiceAccountParams are the values substituted into templates/serviceaccount.yaml.
type ServiceAccountParams struct {
	Name      string
	Namespace string
	App       string
}

//go:embed templates/serviceaccount.yaml
var serviceAccountYAML string

var serviceAccountTemplate = template.Must(template.New("serviceaccount").Parse(serviceAccountYAML))

// NewServiceAccount renders a ServiceAccount manifest from the given params.
func NewServiceAccount(p ServiceAccountParams) string {
	return mustRender(serviceAccountTemplate, p)
}
