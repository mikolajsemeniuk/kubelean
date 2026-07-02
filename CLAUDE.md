# CLAUDE.md

Guidance for any model/agent working in this repo. Read this fully before writing
code. This file is the spec **and** the accumulated field notes. (`README.md` is an
older, unrelated draft — ignore it.)

## What this project is

**kubelean** is a research POC (Go 1.26) backing a scientific paper that studies one
question:

> When an LLM agent debugs a live Kubernetes cluster, how much of a resource's YAML
> actually carries *diagnostic* signal — and how much is noise we can drop before it
> reaches the model, to save context without losing root-cause-analysis (RCA)
> accuracy?

Motivating scenario: an agent is asked *"why is the prod deployment not working?"*.
The real cause is e.g. the pod-template `labels` not matching the Deployment
`selector`. For that fault, `labels`/`selector` carry almost all the signal; `status`
and friends carry ~none **for this case**. Returning the whole `kubectl get -o yaml`
wastes context. The eventual product is one MCP-friendly function:

```
reduce(yaml | yamls, level) -> narrowed YAML   // NOT built yet — see status
```

`level` selects how aggressively to prune. **Which fields each level keeps or drops is
NOT hand-picked — it is derived from measurement.** That is the core rule below.

## Current status (2026-07, honest)

The **measurement pipeline is built and working**; the *product* (`reduce` + derived
levels) is not yet.

- **m1 (faulty-resource generator) — DONE.** 31 resource-Kind generators, a scenario
  catalog with ground-truth fault loci, seeded/deterministic. Since 2026-07 every
  catalog scenario renders in **realistic `kubectl get -o yaml` shape** (server
  metadata + per-Kind status — see "server shape" below). **All data/ shards produced
  before that are stale**: the YAML changed, so a full re-run is required before
  rendering anything for the paper.
- **m2 (inspect benchmark + heatmap + CI + gate) — DONE.** Producer runs the model,
  writes raw per-trial JSONL; renderer computes saliency with confidence intervals and
  emits the paper tables. Includes the flip (control partition), Wilson/Newcombe CIs,
  and the #12 baseline gate.
- **m3 (derive levels from the heatmap) — NOT STARTED.**
- **m4 (validate levels, find the sweet spot) — NOT STARTED.**
- **`reduce()` — NOT STARTED.** It will wrap the same field-remover kernel and load
  m3's level→field-keys config. The kernel (`heatmap.Remove`) already exists.

Catalog right now: **~20 scenarios, 4 fault classes, 7 run-groups, 31 Kinds.**

**Load-bearing reality — read this before adding scenarios:** the RCA model is a small
local model (qwen2.5:7b-instruct via Ollama). It reliably diagnoses only a *subset* of
faults; the rest score ~0 baseline and are honestly excluded by the gate (see
"Hard-won lessons"). A stronger model (the planned **multi-model comparison**) is the
critical path to turning our full component coverage into scored data. Adding more 7B
scenarios grows *coverage*, not *scored data*.

## Core methodological rule (read twice)

**Never** justify cutting/keeping a field by intuition ("annotations are useless",
"image is obviously important"). A reviewer would (correctly) call that cherry-picking.
Every keep/drop decision must trace back to a **measured saliency number**. The
pipeline is strictly bottom-up:

```
generate faulty YAML  ->  measure per-field saliency  ->  derive levels from data  ->  validate
   (m1)                      (m2, the heatmap)             (m3)                         (m4)
```

Levels are an *output* of the experiment, not an input.

## Architecture & data flow

```
pkg/dataset    generators (one file per Kind) + templates/ + scenarios.go + faults.go
pkg/heatmap    the field-remover kernel + resolver + validator + JSONL wire type
pkg/providers  ollama.go — the model client (structured output, digest pinning)
cmd/heatmap    PRODUCER: run model over baseline+ablations, write data/<scenario>.jsonl
cmd/render     RENDERER: read shards -> saliency + CIs + gate -> paper/*.gen.tex
cmd/viewer     throwaway browser heatmap on :8080 ( / and /confidence )
data/          raw per-trial JSONL shards, one file per scenario
paper/         *.gen.tex fragments (LaTeX \input-able tables)
```

Flow: **producer → raw JSONL → renderer → tables.** The producer is the only slow part
(model calls). The renderer is pure and fast: methodology changes never re-run the
model. This split is mandated — see "measurement contract".

### Key files
- `pkg/heatmap/ablate.go` — `Keys` (enumerate removable field-keys), `Remove` (delete a
  field-key from a YAML stream, collapsing empty parents), `NormalizeKey`, the walk +
  Category taxonomy.
- `pkg/heatmap/resolve.go` — `ResolveLeaves(yaml, kind, dottedPath)` → concrete
  `Locus{Doc,Pointer}` for a scenario's ground-truth deciding fields.
- `pkg/heatmap/validate.go` — `Valid(yaml)` structural required-field check (a covariate,
  not a gate).
- `pkg/heatmap/types.go` — `Record`, the JSONL per-trial wire type.
- `pkg/dataset/scenarios.go` — `All()`, `Scenarios(group)`, the `Scenario` +
  `DecidingField` types, group constants.
- `pkg/dataset/faults.go` — the fault-class catalog (name + description); single source
  for the schema enum and the prompt.
- `cmd/render/main.go` — saliency, `wilson`/`newcombe` (CIs), the gate; writes all
  `paper/*.gen.tex`. (`wilson`/`newcombe` are duplicated in cmd/viewer by design — small
  pure helpers, per-cmd like `frac`.)

## Key concepts (shared vocabulary)

- **field-key** — a canonical JSON-pointer with array indices normalized to `*`, e.g.
  `/spec/template/spec/containers/*/ports/*/containerPort`. This is the unit the heatmap
  scores, `reduce` will drop, and levels group. Normalization (`heatmap.NormalizeKey`)
  means one key spans every instance: removing it removes the field from *all* containers
  / all ports. `Keys` dedups to normalized keys; `Remove` expands a key back to every
  concrete match.
- **field-remover kernel** — `heatmap.Keys` + `heatmap.Remove`. Deterministic. Both the
  future `reduce` and the current per-trial ablation are thin wrappers. Build once, reuse.
- **server shape** (`pkg/dataset/server.go` + optional template blocks) — every catalog
  scenario renders as `kubectl get -o yaml` would return it: `ServerMeta`
  (creationTimestamp/generation/resourceVersion/uid, embedded in each Params; zero
  value omits everything, so non-opted scenarios render byte-identical) plus a
  per-Kind `Status` block (`StatusHealthy` / `StatusFailing` / `""`), set explicitly
  per scenario in scenarios.go. Secrets render base64 `data` (the API never returns
  `stringData`). This is what makes status/server fields measurable in the heatmap.
  Deliberately absent (leak-by-design otherwise): `managedFields` (kubectl ≥1.21
  hides them in get) and the `last-applied-configuration` annotation (embeds a JSON
  copy of the spec, so every removed field would survive inside it). Status text
  carries symptoms only, never root-cause detail — the real FailedGetScale message
  names the missing target, which would plant the answer in a non-deciding field.
  Extend via params/templates, never via a post-processing transform.
- **saliency(field, scenario)** = `baseline_accuracy − reduced_accuracy`, each a fraction
  over k trials. High positive = the field carries signal (removing it hurts diagnosis);
  ~0 = noise (safe to drop). The heatmap is the `field × scenario` matrix.
- **the flip / deciding-field partition** — a field whose removal *deletes the fault*
  (the injected locus) has an expected answer of `NoFaultFound`, not the fault. Scoring
  it as "missed the fault" mechanically yields saliency 1.00 — tautological. So deciding
  loci go to a **separate control table** (metric: `Recognized` = fraction that returned
  `NoFaultFound` once the fault is gone), never the saliency map.
- **the gate (#12)** — a *faulty* scenario whose baseline accuracy is below a threshold
  (`cmd/render -gate`, default 0.8) is excluded from the maps and reported in Table 4.
  Saliency is meaningless if the model cannot diagnose the full manifest to begin with.
- **level / class (L1, L2, …)** — a group of field-keys with a saliency threshold,
  discovered from the heatmap in m3. Not decided up front.
- **group** — a batch key for producing related scenarios together (`make run-<group>`):
  selector, references, networking, volumes, scaling, rbac, healthy. Purely
  organizational; the renderer reads all shards regardless of group.
- **fault class** — a root-cause label the model chooses from. Rule: **one class = one
  distinct root cause an SRE would name**, not one per component (a missing referenced
  object is `Ref_NotFound` whether it's a secret, configmap, pvc, or SA). Classes:
  `SelectorMismatch`, `Ref_NotFound`, `PortMismatch` (parked — see below), `NoFaultFound`.

## How to run

```
make run-<group>          # produce data/<scenario>.jsonl for a group (slow: model calls)
make run-all              # all groups
make clean-data           # rm data/*.jsonl
go run ./cmd/render       # data/ -> paper/*.gen.tex   (fast, pure; -gate to tune)
go run ./cmd/viewer       # http://localhost:8080  (/ and /confidence)
```

Producer flags (cmd/heatmap): `-group`, `-k` (samples, default 10), `-temp` (0.7),
`-num-ctx` (8192), `-model`, `-out` (data). Overwrites each scenario's shard.

## The measurement contract (do not violate)

- **k-sampling.** Each baseline AND each ablation runs k trials, seeds `0..k-1`,
  `temp=0.7` (>0 so seeds actually vary). Accuracy = fraction correct over k.
- **Determinism / reproducibility.** Model pinned by **digest** (via Ollama `/api/tags`);
  every shard records the digest. **One digest across all shards** or saliency isn't
  comparable (the renderer warns). Generators are seed-free but fully deterministic
  (static templates + typed structs).
- **Raw first.** Write per-trial `Record`s to JSONL, then render from that file. Tables
  must regenerate without touching the model. `Valid` is recorded per trial as a
  covariate (a removal that breaks a required field is still scored, just flagged).
- **Paper output.** Every rendered artifact is `paper/<name>.gen.tex` — an `\input`-able
  fragment, not a standalone doc. Compute → raw artifact → render `.gen.tex`; never mix
  measurement logic into LaTeX formatting.
- **Paper tables today:** `heatmap.gen.tex` (Table 1 saliency map / Table 2 control-
  Recognized / Table 3 healthy false-positive rate / Table 4 gated-out scenarios),
  `confidence.gen.tex` (the same with 95% CIs and a Signal column), `baseline.gen.tex`
  (the full unreduced manifests, for the paper to show what the agent sees).

## Hard-won lessons (the expensive part — do not relearn these)

1. **Baseline accuracy alone is misleading.** A small model can score a perfect baseline
   by *bias*, not diagnosis. Always report the control (Recognized) + the healthy
   false-positive rate. qwen-7b is trigger-happy on `Ref_NotFound` (≈40% FP on healthy
   manifests, low recognition when the fault is removed): the "10/10" is partly "always
   suspect a reference". Tables 2 + 3 exist to expose exactly this.

2. **The class label name is a strong cue for a 7B.** Specific names
   (`SecretRefNotFound`) leak the answer and inflate accuracy. Neutral names
   (`Ref_NotFound`) drop baselines to ~0 until each class carries a **procedural
   description** — the *check to perform*, MCP-endpoint style (name + description), NOT a
   hint at the answer. `faults.go` is the single source: names → schema enum,
   name+description → prompt. Descriptions are fragile: sharpening one class can break
   another (adding `PortMismatch` guidance broke `SelectorMismatch` detection).

3. **7B competence is PATTERN-SPECIFIC, not description-fixable.** It reliably diagnoses
   *common* references (envFrom→Secret/ConfigMap, serviceAccountName, imagePullSecrets,
   volume→pvc/secret) and *single-document* selector mismatches
   (Deployment/StatefulSet/DaemonSet/ReplicaSet). It fails — even given an explicit
   checklist — on cross-document selectors (Service↔pods), port semantics
   (`targetPort`↔`containerPort`), and uncommon references (HPA/VPA scaleTargetRef,
   RoleBinding roleRef, priorityClassName, storageClassName). Those scenarios are
   **correct but gated on 7B**, parked for the multi-model run. Do not "fix" them by
   over-fitting the prompt — that's the cherry-picking the whole method forbids.

4. **Scenario-design rule (breaks the experiment if violated): exactly ONE anomalous
   value.** Every sibling reference must resolve and every sibling value must be
   consistent; the injected fault is the *only* anomaly. Counter-example we hit: a
   Service with `port: 80` while `containerPort: 8080` — the model reads 80≠8080 as a
   port mismatch, so (a) the baseline passes for the wrong reason and (b) removing the
   real deciding field doesn't restore health, breaking the flip. Fix was
   `port==targetPort==containerPort` in the healthy state. When a scenario needs a
   supporting object (a Service the Ingress points to), that object must be fully healthy,
   which often means adding its backing workload too.

5. **Field canonicalization is load-bearing** (it was "the first task of m2"). Keys are
   normalized (`indices → *`) and deduped; `Remove` deletes *every* instance. Composite
   sequence elements (a whole container, a whole envFrom entry) are **not** emitted as
   ablation targets — removing a whole subtree is a trivial, information-free ablation and
   would smear. Only scalar leaves, atomic maps (labels/data/annotations — open-key
   user-defined maps, removed whole), and *scalar* sequence elements (an arg) are emitted.

6. **Empty-parent collapse is mandatory in `Remove`.** Removing `.../secretRef/name` must
   drop the now-empty `secretRef` and its now-empty envFrom entry, not leave
   `secretRef: {}`. A husk leaks the field's former presence and zeroes its saliency.
   Reverting the collapse silently corrupts every measurement. (See memory
   "empty-parent-pruning".)

7. **Confidence intervals, not raw fractions.** At k=10 every fraction has a ~±0.28 CI —
   0.20 and 0.40 are statistically indistinguishable. Single proportions (control
   Recognized, FP) use **Wilson**; saliency (a *difference* of two proportions) uses
   **Newcombe**. A field is "signal" only when its saliency CI **excludes 0**
   (`ciLow > 0`) — that is the bold rule, NOT `saliency > 0`. This keeps the map honestly
   dark and quantifies what k is needed (a 0.20 effect needs k≥30).

8. **Removal-induced destabilization ≠ signal.** With a fragile model, removing an
   *unrelated* field can knock a correct diagnosis off, producing a positive saliency
   that is not diagnostic signal. Watch for borderline-significant non-deciding cells;
   note them as a first-order limitation. A stronger model reduces this.

9. **Healthy (NoFault) scenarios are a control, not a saliency source.** They have no
   fault to lose; their "saliency" is just removal-induced hallucination. Excluded from
   the map; used for the false-positive rate (Table 3).

## Conventions & hard constraints

- **Language:** Go 1.26. Keep it minimal — no frameworks, no embellishment, no dead
  abstractions. Every new generator is one small `NewX` + a `templates/x.yaml`.
- **POC mindset:** smallest thing that answers the question. Raise concerns rather than
  silently over-building. Prefer parametric template extensions (one optional block →
  many scenarios) over one-off resources.
- **Small steps, get sign-off.** Implement/validate one thing, report, proceed. New
  scenarios: **smoke-validate the baseline (#12) at low k before committing** to a full
  run — a fault the model can't diagnose is gated, and it's cheaper to learn that at k=2.
- **Backward-compatible generators.** New optional fields must default to omitted so
  existing scenarios render byte-identical (else you invalidate their data). Same for the
  prompt: any change to `faults.go` text changes every scenario's prompt → forces a full
  re-run. Preserve exact wording unless a re-run is intended.

## Known limitations / open questions (POC honesty)

- **Model ceiling.** The RCA model (qwen-7b) is the binding constraint; a large share of
  faults is gated. The multi-model comparison is required so a reviewer can't attribute
  results to "just the model". Until then, scored data ≈ common-ref Ref_NotFound +
  single-doc SelectorMismatch.
- **Circularity risk.** If saliency merely recovers the generator's injected fields, the
  heatmap measures our fault design, not Kubernetes. Mitigate by the cross-scenario
  matrix: report a field's *aggregate marginal* signal (e.g. `configMapRef.name` is
  deciding in the configmap scenario, noise in the secret scenario). External validity is
  bounded by the fault catalog — state this in the paper.
- **First-order only.** Single-field removal misses interactions and includes
  destabilization noise (lesson 8). Levels (m3) assume roughly additive saliency; full
  Shapley attribution is exponential and out of scope.
- **Single fault per instance.** Real incidents can be multi-cause; the POC scopes to one
  injected fault per instance. `PortMismatch` is defined but parked (7B can't do it
  cleanly).
- **`validate.go` is structural only.** It checks required-field *presence*, not
  relational invariants, and has no rules for many Kinds (Service, RBAC, …) — so their
  ablations are never flagged invalid. `Valid` is a covariate, so this is tolerable, but
  don't read it as "the manifest is semantically healthy".
