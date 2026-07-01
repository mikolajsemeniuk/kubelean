package dataset

import (
	"text/template"

	_ "embed"
)

// RoleParams are the values substituted into templates/role.yaml. A Role holds no
// outgoing reference — it is the target a RoleBinding points at.
type RoleParams struct {
	Name      string
	Namespace string
	App       string
}

//go:embed templates/role.yaml
var roleYAML string

var roleTemplate = template.Must(template.New("role").Parse(roleYAML))

// NewRole renders a Role manifest from the given params.
func NewRole(p RoleParams) string {
	return mustRender(roleTemplate, p)
}

// RoleBindingParams are the values substituted into templates/rolebinding.yaml.
// The fault sites are roleRef.name (the Role granted) and subjects[].name (the
// ServiceAccount bound) — either can point at an object that is not present.
type RoleBindingParams struct {
	Name               string
	Namespace          string
	App                string
	ServiceAccountName string // subjects[].name
	RoleName           string // roleRef.name
}

//go:embed templates/rolebinding.yaml
var roleBindingYAML string

var roleBindingTemplate = template.Must(template.New("rolebinding").Parse(roleBindingYAML))

// NewRoleBinding renders a RoleBinding manifest from the given params.
func NewRoleBinding(p RoleBindingParams) string {
	return mustRender(roleBindingTemplate, p)
}

// ClusterRoleParams are the values substituted into templates/clusterrole.yaml. A
// ClusterRole is cluster-scoped and is the target of a ClusterRoleBinding's roleRef.
type ClusterRoleParams struct {
	Name string
	App  string
}

//go:embed templates/clusterrole.yaml
var clusterRoleYAML string

var clusterRoleTemplate = template.Must(template.New("clusterrole").Parse(clusterRoleYAML))

// NewClusterRole renders a ClusterRole manifest from the given params.
func NewClusterRole(p ClusterRoleParams) string {
	return mustRender(clusterRoleTemplate, p)
}

// ClusterRoleBindingParams are the values substituted into
// templates/clusterrolebinding.yaml. The fault sites are roleRef.name (the
// ClusterRole granted) and subjects[].name (the ServiceAccount bound) — either can
// dangle, and a missing binding is what makes `kubectl auth can-i` return no.
type ClusterRoleBindingParams struct {
	Name               string
	App                string
	Namespace          string // subjects[].namespace (the SA's namespace)
	ServiceAccountName string // subjects[].name
	ClusterRoleName    string // roleRef.name
}

//go:embed templates/clusterrolebinding.yaml
var clusterRoleBindingYAML string

var clusterRoleBindingTemplate = template.Must(template.New("clusterrolebinding").Parse(clusterRoleBindingYAML))

// NewClusterRoleBinding renders a ClusterRoleBinding manifest from the params.
func NewClusterRoleBinding(p ClusterRoleBindingParams) string {
	return mustRender(clusterRoleBindingTemplate, p)
}
