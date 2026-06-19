package faults

import (
	"bytes"
	"fmt"
	"text/template"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/mikolajsemeniuk/kubelean/pkg/distill"
)

// basePodTemplate is a realistic, server-faithful healthy Pod with the usual
// `-o yaml` bloat (managedFields, default-injected fields, the projected
// service-account token volume/mount). Faults mutate spec/status on top of it,
// so every instance starts from the same realistic noise floor and distillation
// has something real to remove.
const basePodTemplate = `
apiVersion: v1
kind: Pod
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
  uid: {{.UID}}
  resourceVersion: "{{.RV}}"
  generation: 1
  creationTimestamp: "2026-06-1{{.D}}T0{{.D}}:12:33Z"
  labels:
    app: {{.App}}
    pod-template-hash: {{.Hash}}
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"v1","kind":"Pod","metadata":{"name":"{{.App}}","namespace":"{{.Namespace}}"},"spec":{"containers":[{"image":"{{.Image}}","name":"{{.App}}"}]}}
    prometheus.io/scrape: "true"
  ownerReferences:
  - apiVersion: apps/v1
    kind: ReplicaSet
    name: {{.App}}-{{.Hash}}
    uid: {{.OwnerUID}}
    controller: true
    blockOwnerDeletion: true
  managedFields:
  - manager: kube-controller-manager
    operation: Update
    apiVersion: v1
    time: "2026-06-1{{.D}}T0{{.D}}:12:33Z"
    fieldsType: FieldsV1
    fieldsV1:
      f:metadata:
        f:labels:
          f:app: {}
      f:spec:
        f:containers: {}
  - manager: kubelet
    operation: Update
    apiVersion: v1
    time: "2026-06-1{{.D}}T0{{.D}}:14:01Z"
    fieldsType: FieldsV1
    fieldsV1:
      f:status:
        f:conditions: {}
        f:containerStatuses: {}
    subresource: status
spec:
  restartPolicy: Always
  dnsPolicy: ClusterFirst
  schedulerName: default-scheduler
  terminationGracePeriodSeconds: 30
  serviceAccountName: {{.App}}
  serviceAccount: {{.App}}
  nodeName: ip-10-0-3-47.{{.Namespace}}.compute.internal
  securityContext: {}
  volumes:
  - name: kube-api-access-{{.TokenSuffix}}
    projected:
      defaultMode: 420
      sources:
      - serviceAccountToken:
          expirationSeconds: 3607
          path: token
      - configMap:
          name: kube-root-ca.crt
          items:
          - key: ca.crt
            path: ca.crt
  containers:
  - name: {{.App}}
    image: {{.Image}}
    imagePullPolicy: IfNotPresent
    terminationMessagePath: /dev/termination-log
    terminationMessagePolicy: File
    env:
    - name: LOG_LEVEL
      value: info
    resources:
      limits:
        memory: {{.MemLimit}}
      requests:
        memory: {{.MemLimit}}
    volumeMounts:
    - name: kube-api-access-{{.TokenSuffix}}
      mountPath: /var/run/secrets/kubernetes.io/serviceaccount
      readOnly: true
status:
  phase: Running
  hostIP: 10.0.3.47
  podIP: 10.0.3.182
  startTime: "2026-06-1{{.D}}T0{{.D}}:12:33Z"
  qosClass: Guaranteed
  conditions:
  - type: PodScheduled
    status: "True"
    lastProbeTime: null
    lastTransitionTime: "2026-06-1{{.D}}T0{{.D}}:12:33Z"
  containerStatuses:
  - name: {{.App}}
    image: {{.Image}}
    imageID: ""
    containerID: ""
    ready: true
    started: true
    restartCount: 0
    state:
      running:
        startedAt: "2026-06-1{{.D}}T0{{.D}}:12:34Z"
`

var basePodTmpl = template.Must(template.New("pod").Parse(basePodTemplate))

// basePod renders a healthy bloated Pod for p. Faults then overwrite status and
// the relevant spec fields.
func basePod(p Params) *unstructured.Unstructured {
	image := fmt.Sprintf("%s/%s:%s", p.Registry, p.App, p.Tag)
	data := map[string]string{
		"Name":        p.podName(),
		"Namespace":   p.Namespace,
		"App":         p.App,
		"Hash":        p.Hash,
		"Image":       image,
		"UID":         fmt.Sprintf("%08x-0000-4000-8000-%012x", p.rng.Int31(), p.rng.Int63()&0xffffffffffff),
		"OwnerUID":    fmt.Sprintf("%08x-0000-4000-8000-%012x", p.rng.Int31(), p.rng.Int63()&0xffffffffffff),
		"RV":          fmt.Sprintf("%d", 100000+p.rng.Intn(900000)),
		"D":           fmt.Sprintf("%d", 1+p.rng.Intn(8)),
		"TokenSuffix": randSuffix(p.rng, 5),
		"MemLimit":    fmt.Sprintf("%dMi", []int{64, 96, 128, 256}[p.rng.Intn(4)]),
	}
	var buf bytes.Buffer
	if err := basePodTmpl.Execute(&buf, data); err != nil {
		panic(fmt.Sprintf("base pod template: %v", err)) // template is a constant; failure is a programming error
	}

	obj, err := distill.FromYAML(buf.Bytes())
	if err != nil {
		panic(fmt.Sprintf("parse base pod: %v", err))
	}

	return obj
}

func setPhase(obj *unstructured.Unstructured, phase string) {
	_ = unstructured.SetNestedField(obj.Object, phase, "status", "phase")
}

func firstContainer(obj *unstructured.Unstructured, mutate func(c map[string]any)) {
	cs, _, _ := unstructured.NestedSlice(obj.Object, "spec", "containers")
	if len(cs) == 0 {
		return
	}

	cm, ok := cs[0].(map[string]any)
	if !ok {
		return
	}

	mutate(cm)
	cs[0] = cm
	_ = unstructured.SetNestedSlice(obj.Object, cs, "spec", "containers")
}

func setContainerStatus(obj *unstructured.Unstructured, cs map[string]any) {
	_ = unstructured.SetNestedSlice(obj.Object, []any{cs}, "status", "containerStatuses")
}

func setConditions(obj *unstructured.Unstructured, conds []any) {
	_ = unstructured.SetNestedSlice(obj.Object, conds, "status", "conditions")
}

func condition(kind, status, reason, message string) map[string]any {
	return map[string]any{
		"type":               kind,
		"status":             status,
		"reason":             reason,
		"message":            message,
		"lastProbeTime":      nil,
		"lastTransitionTime": "2026-06-15T09:14:01Z",
	}
}
