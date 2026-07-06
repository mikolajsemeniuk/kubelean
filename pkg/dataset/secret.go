package dataset

import (
	_ "embed"
	"encoding/base64"
	"text/template"
)

// SecretParams are the values substituted into templates/secret.yaml. The
// Secret type is fixed to Opaque. StringData holds plaintext values for the
// caller's convenience; the manifest renders them base64-encoded under data,
// because that is what the API returns — `kubectl get` never shows stringData
// (a write-only input field). Rendered in sorted key order, so output is
// deterministic.
type SecretParams struct {
	Name       string
	Namespace  string
	Type       string // optional: defaults to Opaque; a .dockerconfigjson pull secret MUST be kubernetes.io/dockerconfigjson (kubelet rejects Opaque)
	StringData map[string]string
	ServerMeta // optional: server-assigned metadata (kubectl get shape)
}

//go:embed templates/secret.yaml
var secretYAML string

var secretTemplate = template.Must(template.New("secret").Parse(secretYAML))

// NewSecret renders a Secret manifest from the given params. Type defaults to
// Opaque so existing callers render byte-identical.
func NewSecret(p SecretParams) string {
	if p.Type == "" {
		p.Type = "Opaque"
	}
	data := make(map[string]string, len(p.StringData))
	for k, v := range p.StringData {
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}

	return mustRender(secretTemplate, struct {
		Name, Namespace, Type string
		Data                  map[string]string
		ServerMeta
	}{p.Name, p.Namespace, p.Type, data, p.ServerMeta})
}
