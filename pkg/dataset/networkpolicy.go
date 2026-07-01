package dataset

import (
	"text/template"

	_ "embed"
)

// NetworkPolicyParams are the values substituted into templates/networkpolicy.yaml.
// The fault sites are the label selectors: podSelector (which pods the policy
// governs) and the ingress-from podSelector (which pods may connect). A mismatch
// against the real pod labels silently blocks traffic — a semantic fault.
type NetworkPolicyParams struct {
	Name           string
	Namespace      string
	App            string
	PodSelectorApp string // spec.podSelector.matchLabels.app
	FromApp        string // spec.ingress[].from[].podSelector.matchLabels.app
	Port           int
}

//go:embed templates/networkpolicy.yaml
var networkPolicyYAML string

var networkPolicyTemplate = template.Must(template.New("networkpolicy").Parse(networkPolicyYAML))

// NewNetworkPolicy renders a NetworkPolicy manifest from the given params.
func NewNetworkPolicy(p NetworkPolicyParams) string {
	return mustRender(networkPolicyTemplate, p)
}
