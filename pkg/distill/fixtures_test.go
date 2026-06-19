package distill

// crashloopFixture is a bloated CrashLoopBackOff Pod (server-faithful `-o yaml`
// output) used as the golden input for distillation rule tests. Inlined as a
// constant so the test suite has no filesystem dependency.
const crashloopFixture = `
apiVersion: v1
kind: Pod
metadata:
  name: payment-api-7d9f8c6b5-x2k4m
  namespace: prod
  uid: 3f8e1c2a-9b4d-4e6f-8a1b-2c3d4e5f6a7b
  resourceVersion: "884213"
  generation: 1
  creationTimestamp: "2026-06-15T09:12:33Z"
  labels:
    app: payment-api
    pod-template-hash: 7d9f8c6b5
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: |
      {"apiVersion":"v1","kind":"Pod","metadata":{"name":"payment-api","namespace":"prod"},"spec":{"containers":[{"image":"registry.internal/payment-api:1.4.2","name":"payment-api"}]}}
    kubernetes.io/psp: restricted
  ownerReferences:
  - apiVersion: apps/v1
    kind: ReplicaSet
    name: payment-api-7d9f8c6b5
    uid: 1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d
    controller: true
    blockOwnerDeletion: true
  managedFields:
  - manager: kube-controller-manager
    operation: Update
    apiVersion: v1
    time: "2026-06-15T09:12:33Z"
    fieldsType: FieldsV1
    fieldsV1:
      f:metadata:
        f:labels:
          f:app: {}
          f:pod-template-hash: {}
      f:spec:
        f:containers: {}
  - manager: kubelet
    operation: Update
    apiVersion: v1
    time: "2026-06-15T09:14:01Z"
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
  serviceAccountName: payment-api
  serviceAccount: payment-api
  nodeName: ip-10-0-3-47.eu-central-1.compute.internal
  securityContext: {}
  volumes:
  - name: config
    configMap:
      name: payment-api-config
      defaultMode: 420
  - name: kube-api-access-9xv7t
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
      - downwardAPI:
          items:
          - path: namespace
            fieldRef:
              apiVersion: v1
              fieldPath: metadata.namespace
  containers:
  - name: payment-api
    image: registry.internal/payment-api:1.4.2
    imagePullPolicy: IfNotPresent
    terminationMessagePath: /dev/termination-log
    terminationMessagePolicy: File
    env:
    - name: DATABASE_URL
      value: postgres://payments-db:5432/payments
    - name: LOG_LEVEL
      value: info
    resources:
      limits:
        memory: 128Mi
      requests:
        memory: 128Mi
    livenessProbe:
      httpGet:
        path: /healthz
        port: 8080
        scheme: HTTP
      initialDelaySeconds: 3
      periodSeconds: 10
      timeoutSeconds: 1
      successThreshold: 1
      failureThreshold: 3
    volumeMounts:
    - name: config
      mountPath: /etc/payment-api
    - name: kube-api-access-9xv7t
      mountPath: /var/run/secrets/kubernetes.io/serviceaccount
      readOnly: true
status:
  phase: Running
  hostIP: 10.0.3.47
  hostIPs:
  - ip: 10.0.3.47
  podIP: 10.0.3.182
  podIPs:
  - ip: 10.0.3.182
  startTime: "2026-06-15T09:12:33Z"
  qosClass: Guaranteed
  conditions:
  - type: Initialized
    status: "True"
    lastProbeTime: null
    lastTransitionTime: "2026-06-15T09:12:33Z"
  - type: Ready
    status: "False"
    reason: ContainersNotReady
    message: 'containers with unready status: [payment-api]'
    lastProbeTime: null
    lastTransitionTime: "2026-06-15T09:14:01Z"
  - type: ContainersReady
    status: "False"
    reason: ContainersNotReady
    message: 'containers with unready status: [payment-api]'
    lastProbeTime: null
    lastTransitionTime: "2026-06-15T09:14:01Z"
  - type: PodScheduled
    status: "True"
    lastProbeTime: null
    lastTransitionTime: "2026-06-15T09:12:33Z"
  containerStatuses:
  - name: payment-api
    image: registry.internal/payment-api:1.4.2
    imageID: registry.internal/payment-api@sha256:9c1f4d2e8a6b0c3f5d7e9a1b2c4d6e8f0a2b4c6d8e0f2a4b6c8d0e2f4a6b8c0d
    containerID: containerd://abcd1234ef567890
    ready: false
    started: false
    restartCount: 7
    state:
      waiting:
        reason: CrashLoopBackOff
        message: back-off 5m0s restarting failed container=payment-api pod=payment-api-7d9f8c6b5-x2k4m_prod
    lastState:
      terminated:
        reason: Error
        exitCode: 1
        startedAt: "2026-06-15T09:13:58Z"
        finishedAt: "2026-06-15T09:14:00Z"
        containerID: containerd://abcd1234ef567890
`
