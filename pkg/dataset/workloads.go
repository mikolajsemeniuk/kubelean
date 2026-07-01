package dataset

import (
	"text/template"

	_ "embed"
)

// ReplicaSetParams are the values substituted into templates/replicaset.yaml. Like
// the other workloads, the label fields are separate so a selector/template
// mismatch can be injected: a healthy ReplicaSet has App == SelectorApp == PodApp.
type ReplicaSetParams struct {
	Name          string
	Namespace     string
	App           string
	Replicas      int
	SelectorApp   string
	PodApp        string
	ContainerName string
	Image         string
	ContainerPort int
}

//go:embed templates/replicaset.yaml
var replicaSetYAML string

var replicaSetTemplate = template.Must(template.New("replicaset").Parse(replicaSetYAML))

// NewReplicaSet renders a ReplicaSet manifest from the given params.
func NewReplicaSet(p ReplicaSetParams) string {
	return mustRender(replicaSetTemplate, p)
}

// JobParams are the values substituted into templates/job.yaml. A Job's fault
// surface is its pod template (image, refs); PodApp labels the generated pods.
type JobParams struct {
	Name          string
	Namespace     string
	App           string
	PodApp        string
	ContainerName string
	Image         string
}

//go:embed templates/job.yaml
var jobYAML string

var jobTemplate = template.Must(template.New("job").Parse(jobYAML))

// NewJob renders a Job manifest from the given params.
func NewJob(p JobParams) string {
	return mustRender(jobTemplate, p)
}

// CronJobParams are the values substituted into templates/cronjob.yaml. Beyond the
// nested pod template, Schedule is a cron-expression fault site.
type CronJobParams struct {
	Name          string
	Namespace     string
	App           string
	Schedule      string
	PodApp        string
	ContainerName string
	Image         string
}

//go:embed templates/cronjob.yaml
var cronJobYAML string

var cronJobTemplate = template.Must(template.New("cronjob").Parse(cronJobYAML))

// NewCronJob renders a CronJob manifest from the given params.
func NewCronJob(p CronJobParams) string {
	return mustRender(cronJobTemplate, p)
}
