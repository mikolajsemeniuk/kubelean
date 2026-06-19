# kubelean fault taxonomy

The kubelean RCA benchmark does **not** invent its own fault space. Its fault
classes are anchored to two prior canonical benchmarks, which defuses the
"why roll your own benchmark?" reviewer objection:

- **Cloud-OpsBench** (arXiv 2603.00468, repo github.com/LLM4Ops/Cloud-OpsBench) —
  taxonomy + label backbone. 452 cases (paper) / 754 (repo), **40 root-cause types
  in 8 categories** on Kubernetes v1.31. Per-case ground truth in `metadata.json`
  is `{fault_taxonomy, fault_object, root_cause}` — structurally identical to our
  `{category, offending_field, root_cause_label}`. **License: none** → we cite and
  derive the taxonomy, we do **not** re-host their files. Their YAML is *clean
  reconstructed spec* (no managedFields/status/default bloat), so it is a label
  source, **not** a source of the raw bloated `-o yaml` kubelean prunes.
- **OperAID** (github.com/EricssonResearch/operaid, **MIT**) — methodological
  sibling + reusable real seeds. Narrow (3 fault scenarios on Open5GS, 900
  experiments, 5 open LLMs) but ships downloadable fault manifests with
  ground truth, and introduces the closed-loop *fault-injection → diagnosis →
  remediation → execution-based verification* pipeline we cite for the (stretch)
  execution-based remediation leg.

Key gap (and our novelty): **neither benchmark keeps the bloated `kubectl get -o
yaml` server output** — everyone discards managedFields/status/defaults as noise.
That discarded bloat is exactly kubelean's raw material. So we source *labels and
fault content* from prior art, but must *generate the bloated output* ourselves
(server-faithful re-inflation, validated against a kind-captured subset).

## Cloud-OpsBench categories (verified backbone, 8 categories / 40 types)

| # | Cloud-OpsBench category | Resources touched |
|---|---|---|
| 1 | Admission control | Pod, webhooks, quotas |
| 2 | Scheduling | Pod, Node, PV |
| 3 | Startup | Pod, Container, ConfigMap/Secret |
| 4 | Runtime | Pod, Container (crash/OOM/probe) |
| 5 | Service routing | Service, Endpoints, Pod |
| 6 | Performance | Pod, resources, HPA |
| 7 | Infrastructure | Node, kubelet, runtime |
| 8 | Application code defect | container image / app source |

## kubelean fault classes → closed-set RCA labels

`root_cause_label` is the closed set the LLM must choose from (exact-match
scoring). `offending_field` is the single deciding field used for localization
precision/recall **and** as the L5 leave-one-out oracle target ("remove this
field → RCA breaks"). `goal` is the L4 goal-condition.

| ID | root_cause_label | Cloud-OpsBench cat. | offending_field (deciding) | scope | goal |
|----|------------------|:---:|---|:---:|------|
| F01 | `CrashLoopBackOff` | 4 Runtime | `status.containerStatuses[].lastState.terminated.{reason,exitCode}` | single | crashloop |
| F02 | `OOMKilled` | 4 Runtime | `lastState.terminated.reason=OOMKilled` + `spec…resources.limits.memory` | single | oom |
| F03 | `ImagePullBackOff_BadImage` | 3 Startup | `spec.containers[].image` (+ `status…waiting.reason`) | single | imagepull |
| F04 | `ImagePullBackOff_NoAuth` | 3 Startup | `spec.imagePullSecrets` (+ `image`, private registry) | single | imagepull |
| F05 | `LivenessProbeFailure` | 4 Runtime | `spec.containers[].livenessProbe` + `restartCount` | single | probe |
| F06 | `ReadinessProbeFailure` | 4 Runtime | `spec.containers[].readinessProbe` + `conditions[Ready]=False` | single | probe |
| F07 | `Pending_InsufficientResources` | 2 Scheduling | `spec…resources.requests` + event `FailedScheduling` | single | scheduling |
| F08 | `Pending_UnsatisfiableAffinity` | 2 Scheduling | `spec.{nodeSelector,affinity}` | single | scheduling |
| F09 | `PVC_Unbound` | 2 Scheduling | `PVC.spec.storageClassName` + `status.phase=Pending` | bundle | storage |
| F10 | `CreateContainerConfigError` | 3 Startup | `env.valueFrom`/`volumes` ref to missing ConfigMap/Secret | bundle | config |
| F11 | `Service_SelectorMismatch` | 5 Service routing | `Service.spec.selector` vs Pod labels (empty Endpoints) | bundle | connectivity |
| F12 | `RBAC_Forbidden` | 1 Admission control | missing `Role`/`RoleBinding` for ServiceAccount | bundle | rbac |

Notes:
- **single** scope = self-contained in one Pod object → usable offline with a
  hand-authored fixture, no cluster. **bundle** scope = needs ≥2 resources →
  feeds the "single resource vs bundle" context-scale ablation; deferred past M1.
- F01–F06 are the M1/M2 working set (self-contained, deterministic, one deciding
  field each). F07–F12 join at M3 for full coverage.
- Each `offending_field` is exactly one path → clean localization metric and a
  well-defined L5 leave-one-out oracle.

## Tooling constraint

Live fault injection needs docker + kind (currently absent locally). The science
is **offline**: hand-authored / Cloud-OpsBench-derived fixtures carry the
injected fault + ground-truth label, fed straight to a local LLM. kind/docker are
only required for the stretch execution-based remediation leg (OperAID-style).
