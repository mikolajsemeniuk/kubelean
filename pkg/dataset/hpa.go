package dataset

import (
	"text/template"

	_ "embed"
)

// HPAParams are the values substituted into templates/hpa.yaml. The fault site is
// scaleTargetRef.name (the workload to scale) — a reference that can dangle.
type HPAParams struct {
	Name        string
	Namespace   string
	App         string
	TargetKind  string // spec.scaleTargetRef.kind (Deployment, StatefulSet, ...)
	TargetName  string // spec.scaleTargetRef.name
	MinReplicas int
	MaxReplicas int
}

//go:embed templates/hpa.yaml
var hpaYAML string

var hpaTemplate = template.Must(template.New("hpa").Parse(hpaYAML))

// NewHPA renders a HorizontalPodAutoscaler manifest from the given params.
func NewHPA(p HPAParams) string {
	return mustRender(hpaTemplate, p)
}

// VPAParams are the values substituted into templates/vpa.yaml. Like HPA, the fault
// site is targetRef.name (the workload to right-size) — a reference that can dangle.
type VPAParams struct {
	Name       string
	Namespace  string
	App        string
	TargetKind string // spec.targetRef.kind
	TargetName string // spec.targetRef.name
}

//go:embed templates/vpa.yaml
var vpaYAML string

var vpaTemplate = template.Must(template.New("vpa").Parse(vpaYAML))

// NewVPA renders a VerticalPodAutoscaler manifest from the given params.
func NewVPA(p VPAParams) string {
	return mustRender(vpaTemplate, p)
}
