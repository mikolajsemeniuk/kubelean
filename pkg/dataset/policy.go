package dataset

import (
	"text/template"

	_ "embed"
)

// PriorityClassParams are the values substituted into templates/priorityclass.yaml.
// A PriorityClass is cluster-scoped and is the target of a pod's priorityClassName.
type PriorityClassParams struct {
	Name        string
	App         string
	Value       int
	Description string
}

//go:embed templates/priorityclass.yaml
var priorityClassYAML string

var priorityClassTemplate = template.Must(template.New("priorityclass").Parse(priorityClassYAML))

// NewPriorityClass renders a PriorityClass manifest from the given params.
func NewPriorityClass(p PriorityClassParams) string {
	return mustRender(priorityClassTemplate, p)
}

// PDBParams are the values substituted into templates/poddisruptionbudget.yaml.
// The fault site is the selector, which must match the pods it protects.
type PDBParams struct {
	Name         string
	Namespace    string
	App          string
	MinAvailable int
	SelectorApp  string // spec.selector.matchLabels.app
}

//go:embed templates/poddisruptionbudget.yaml
var pdbYAML string

var pdbTemplate = template.Must(template.New("poddisruptionbudget").Parse(pdbYAML))

// NewPodDisruptionBudget renders a PodDisruptionBudget manifest from the params.
func NewPodDisruptionBudget(p PDBParams) string {
	return mustRender(pdbTemplate, p)
}

// ResourceQuotaParams are the values substituted into templates/resourcequota.yaml.
type ResourceQuotaParams struct {
	Name      string
	Namespace string
	App       string
	CPU       string
	Memory    string
	Pods      int
}

//go:embed templates/resourcequota.yaml
var resourceQuotaYAML string

var resourceQuotaTemplate = template.Must(template.New("resourcequota").Parse(resourceQuotaYAML))

// NewResourceQuota renders a ResourceQuota manifest from the given params.
func NewResourceQuota(p ResourceQuotaParams) string {
	return mustRender(resourceQuotaTemplate, p)
}

// LimitRangeParams are the values substituted into templates/limitrange.yaml.
type LimitRangeParams struct {
	Name          string
	Namespace     string
	App           string
	DefaultCPU    string
	DefaultMemory string
	RequestCPU    string
	RequestMemory string
}

//go:embed templates/limitrange.yaml
var limitRangeYAML string

var limitRangeTemplate = template.Must(template.New("limitrange").Parse(limitRangeYAML))

// NewLimitRange renders a LimitRange manifest from the given params.
func NewLimitRange(p LimitRangeParams) string {
	return mustRender(limitRangeTemplate, p)
}
