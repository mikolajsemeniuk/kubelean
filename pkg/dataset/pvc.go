package dataset

import (
	"text/template"

	_ "embed"
)

// PVCParams are the values substituted into templates/pvc.yaml.
type PVCParams struct {
	Name         string
	Namespace    string
	App          string
	Storage      string
	StorageClass string // optional: spec.storageClassName ("" omits it)
}

//go:embed templates/pvc.yaml
var pvcYAML string

var pvcTemplate = template.Must(template.New("pvc").Parse(pvcYAML))

// NewPVC renders a PersistentVolumeClaim manifest from the given params.
func NewPVC(p PVCParams) string {
	return mustRender(pvcTemplate, p)
}
