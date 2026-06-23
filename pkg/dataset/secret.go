package dataset

import (
	_ "embed"
	"text/template"
)

// SecretParams are the values substituted into templates/secret.yaml. The
// Secret type is fixed to Opaque. StringData is rendered in sorted key order,
// so output is deterministic.
type SecretParams struct {
	Name       string
	Namespace  string
	StringData map[string]string
}

//go:embed templates/secret.yaml
var secretYAML string

var secretTemplate = template.Must(template.New("secret").Parse(secretYAML))

// NewSecret renders a Secret manifest from the given params.
func NewSecret(p SecretParams) string {
	return mustRender(secretTemplate, p)
}
