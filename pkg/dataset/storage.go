package dataset

import (
	"text/template"

	_ "embed"
)

// StorageClassParams are the values substituted into templates/storageclass.yaml.
// A StorageClass is cluster-scoped and is the target of a PVC's storageClassName.
type StorageClassParams struct {
	Name        string
	App         string
	Provisioner string
}

//go:embed templates/storageclass.yaml
var storageClassYAML string

var storageClassTemplate = template.Must(template.New("storageclass").Parse(storageClassYAML))

// NewStorageClass renders a StorageClass manifest from the given params.
func NewStorageClass(p StorageClassParams) string {
	return mustRender(storageClassTemplate, p)
}

// PVParams are the values substituted into templates/persistentvolume.yaml. Its
// storageClassName ties it to a StorageClass; a PVC binds to it by class + size.
type PVParams struct {
	Name         string
	App          string
	Storage      string
	StorageClass string
	Path         string
}

//go:embed templates/persistentvolume.yaml
var pvYAML string

var pvTemplate = template.Must(template.New("persistentvolume").Parse(pvYAML))

// NewPV renders a PersistentVolume manifest from the given params.
func NewPV(p PVParams) string {
	return mustRender(pvTemplate, p)
}
