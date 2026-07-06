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

Catalog right now: **33 faulty scenarios (22 Ref\_NotFound / 7 SelectorMismatch /
4 PortMismatch) + 33 healthy twins + 3 healthy controls = 69 entries, ~2000
variants; 7 run-groups, 31 Kinds** (21 Kinds appear in scenarios — incl. Job,
Ingress, NetworkPolicy, PDB since 2026-07). Class-balance rules that produced
these numbers: every class ≥3–5 scenarios; the scored set must not be >50% one
class (or a constant classifier inflates baselines); every field-key that m3 will
claim anything about needs ≥2 scenarios where it is non-deciding (the
cross-scenario profile — e.g. rolebinding-subject-wrong-name exists mainly to give
roleRef.name a non-deciding appearance). The four `*-crowded` scenarios
(2026-07-04) exist for that last rule at scale: each injects a fault from a
pattern the 7B reliably scores uncrowded (envFrom ref ×2 / secret volume /
targetPort) and packs the bundle with fully-healthy WITNESSES of
under-profiled keys (serviceAccountName, claimName, priorityClassName,
imagePullSecrets, roleRef/subjects…) — a witness needs no model competence, it
only has to sit, resolving, in a gate-surviving scenario. Witness placement
rule: never give a witness the same Kind as the scenario's Hides locus
(ResolveLeaves resolves per Kind — a second Secret doc where Secret
metadata.name decides becomes a bogus locus). Smoke-learned (k=2/4, honest):
**crowding itself costs the 7B accuracy across every pattern** — secret-ref
2/4 crowded vs 0.90 uncrowded, and the RBAC chain drowns both a port
comparison (0/2 at targetPort 8000, 1/4 after switching to the more contrasty
3000, 1/4 even slimmed to 4 docs) and a configmap ref (1/4). We deliberately
did NOT slim further to sneak under the gate (that would be scenario-design
overfitting); expect the crowded scenarios to gate out on 7B and score on the
multi-model run — and note the pair {uncrowded, crowded} of the same fault is
itself a controlled context-dilution measurement worth a paper paragraph.

### Next steps (queued — read before doing anything else)

1. **The 7B paper run is DONE (2026-07-03, K=40) and rendered.** Headline results:
   only 7 of 29 faulty scenarios clear the (now dual) gate — 4 Ref_NotFound + all
   3 PortMismatch, zero SelectorMismatch — and after the negative-control floor
   only 5 saliency cells survive as signal (the two cross-sibling reference names
   on top). The old "PortMismatch parked" claim is DEAD WRONG at K=40: the three
   port scenarios are the strongest class (baselines 1.00, twin J 0.82–1.00).
   **2026-07-04 changes make the NEXT produce run a mandatory FULL re-run** (never
   mix with the 2026-07-03 shards): the Ref_NotFound description in faults.go was
   broadened (prompt change ⇒ every scenario's prompt changed), and two scenarios
   were regenerated — statefulset-selector-mismatch (dangling spec.serviceName
   fixed: governing headless Service added, anomaly moved into the selector) and
   env-key-wrong-name (env[].name decoupled from the key via EnvName so the var
   name no longer echoes the missing key). Both pairs' 2026-07-03 shards are
   stale-but-gated, so the current render stays honest; smoke before the re-run.
2. **Two renderer artifacts are still TODO** (pure cmd/render work, no model
   calls — can be built and tested against whatever shards exist).
   DONE 2026-07-04 (cmd/render/figures.go, unit-tested): `fieldprofile.gen.tex`
   (the per-field-key aggregate across scored scenarios — the m3 input and the
   anti-circularity defense), plus two paper figures: `heatmapfig.gen.tex` (the
   contrast map as TikZ — one dense grid per Kind, orange = signal, grey = no
   marginal signal, dashed = negative control, dark* = deciding; requires
   tikz) and `manifestfig.gen.tex` (the flagship scenario's YAML painted line
   by line by verdict; flagship = most signal cells, data-chosen; requires
   xcolor). Still TODO:
   - `power.gen.tex` — the auto-computed statistical-power paragraph: k, number of
     map cells m, FDR level, the minimal detectable effect at 80% power under the
     seed-paired McNemar + BH rule (lone-signal worst case: needs ≥⌈log2(2m/0.05)⌉
     one-sided discordant seeds), and the upper Newcombe bound on saliency for
     cells declared "noise". Keeps the paper's power claims always consistent with
     the data.
   - `categories.gen.tex` — the scalar / atomic-map / seq-elem stratification: the
     Category is recorded on every reduced trial and the ablate.go docs promise the
     three kinds are "separate populations", but no table shows them. Per category:
     cell count, saliency distribution, signal count, mean healthy-bundle FP.
3. After the 7B run renders clean: the **multi-model run** (`make run-all K=40
   MODEL=qwen2.5:32b-instruct` etc.) — the per-model data/paper isolation is already
   in place; it un-gates the scenarios 7B cannot diagnose (RBAC, HPA/VPA, storage,
   most of the new coverage) and is the paper's answer to "is this just the model?".

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
cmd/heatmap    PRODUCER: run model over baseline+ablations, write data/<model>/<scenario>.jsonl
cmd/render     RENDERER: read shards -> saliency + CIs + gate -> paper/<model>/*.gen.tex
cmd/viewer     throwaway browser heatmap on :8080 ( / and /confidence )
data/          raw per-trial JSONL shards, one DIRECTORY PER MODEL, one file per scenario
paper/         *.gen.tex fragments (LaTeX \input-able tables), one directory per model
```

All three cmds take `-model` (default qwen2.5:7b-instruct) and namespace their I/O by
it (`:`/`/` become `-`), so a 32b run never overwrites the 7b shards — that isolation
IS the future multi-model comparison. Make passes it via `MODEL=`.

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
- **the flip / deciding-field partition** — a deciding field (an injected locus) never
  enters the saliency map: scoring its removal as "missed the fault" mechanically
  yields saliency 1.00 — tautological. Deciding loci themselves split into two roles
  (`DecidingField.Hides`), which must never be pooled:
  - **fault-deleting** (`Hides: false`): removal deletes the fault itself — the
    dangling reference site, or one side of a same-document comparison. Expected
    answer is honestly `NoFaultFound`; Table 2a reports `Recognized`.
  - **evidence-hiding** (`Hides: true`): removal only hides the counter-evidence —
    the target object's `metadata.name`, the far side of a cross-document comparison.
    The reference still dangles, so there is no single correct answer: `NoFaultFound`
    is right under the prompt's charitable absent-field convention, the original
    fault under a strict reading. Table 2b reports both rates side by side. (The old
    pooled Table 2 showed `metadata.name` Recognized swinging 0.00–1.00 across
    identical constructions — that spread is the model flip-flopping between the two
    readings, not noise.)
- **healthy twin** — every faulty scenario has a `<name>-twin`: the identical bundle
  with the single anomaly fixed (and statuses healthy), expected `NoFaultFound`.
  Twins measure the per-scenario false-positive rate: a high faulty baseline with a
  low twin NoFault rate means the model always suspects that fault on that bundle
  shape — bias, not diagnosis (lesson 1). Built by the same constructor
  (`scenarioX(twin bool)` + `maybeTwin`); the producer runs twins **baseline-only**
  (no fault → no saliency to measure), so they cost k calls each. Rendered as
  `twins.gen.tex` — per pair: baseline accuracy (sensitivity), twin NoFault rate
  (specificity) and Youden's J = both − 1 with a Newcombe CI (bold = CI excludes 0;
  J≈0 = pure bias). Twins are excluded from the saliency map, the control table,
  and Table 3.
- **the gate (#12, dual since 2026-07-04)** — a *faulty* scenario is scored only when
  (a) baseline accuracy clears a threshold (`cmd/render -gate`, default 0.8) AND
  (b) the 95% Newcombe CI of Youden's J against its twin excludes 0. Baseline alone
  passes pure bias — job-secret-wrong-name scored 1.00 baseline with twin NoFault
  0.00 (J=0) and its "saliency" measured what shakes the reflex, not signal.
  Excluded scenarios and the reason (baseline / twin / both) land in Table 4.
- **negative-control floor (2026-07-04)** — server bookkeeping fields
  (creation/condition timestamps, uid, resourceVersion, generation) cannot encode a
  fault by construction, so their measured saliency estimates the scenario's
  removal-induced destabilization (lesson 8) — the blank sample of the assay. A cell
  is **signal** only if it clears McNemar+BH *and* exceeds the max control saliency
  of its scenario (the floor); BH-passing cells below the floor render as `destab`.
  Rationale: Table 3b cannot floor this (healthy bundles have no marginal diagnosis
  to knock off — their FP is ~0 while faulty-scenario destabilization is huge). At
  K=40 this cut the signal cells from 24 to 5.
- **level / class (L1, L2, …)** — a group of field-keys with a saliency threshold,
  discovered from the heatmap in m3. Not decided up front.
- **group** — a batch key for producing related scenarios together (`make run-<group>`):
  selector, references, networking, volumes, scaling, rbac, healthy. Purely
  organizational; the renderer reads all shards regardless of group.
- **fault class** — a root-cause label the model chooses from. Rule: **one class = one
  distinct root cause an SRE would name**, not one per component (a missing referenced
  object is `Ref_NotFound` whether it's a secret, configmap, pvc, or SA). Classes:
  `SelectorMismatch`, `Ref_NotFound`, `PortMismatch` (un-parked 2026-07-04: at K=40 it
  is the strongest class — see Current status), `NoFaultFound`.

## How to run

```
make smoke                # baselines only, k=3, no shards — the cheap #12 gate check
make run-<group>          # produce data/<model>/<scenario>.jsonl (slow: model calls)
make run-all              # all groups (K=30+ for the paper run; default K=10)
make render               # data/<model>/ -> paper/<model>/*.gen.tex (fast, pure)
make clean-data           # rm -rf data/*
go run ./cmd/viewer       # http://localhost:8080  (/ and /confidence)
```

Every target takes `MODEL=<ollama tag>` (default qwen2.5:7b-instruct) and `K=`.

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
- **Paper tables today:** `heatmap.gen.tex` (Table 1 saliency map / Table 2a
  fault-deleting control / Table 2b evidence-hiding control / Table 3 healthy
  false-positive rate / Table 3b per-field removal-induced hallucination on healthy
  bundles — near-zero, which is WHY the destabilization floor comes from
  negative-control fields instead / Table 4 gate-excluded scenarios with the
  reason (baseline / twin / both) and J / Table 6 localization: among
  correct-class baselines, did offending\_field point at a deciding locus, plus
  the top blamed path — measures path-REPORTING, not detection; read with 2a),
  `confidence.gen.tex` (the same with 95% CIs and a per-cell Verdict column:
  signal / destab / control / no), `fdr.gen.tex`
  (the decision table: every saliency cell ranked by McNemar p and BH q, with the
  raw discordant seed counts, the scenario's fragility floor, and the verdict —
  the audit trail for every bold cell), `twins.gen.tex` (discrimination: per faulty scenario its baseline accuracy,
  the twin's NoFaultFound rate, and Youden's J with CIs), `baseline.gen.tex` (the
  full unreduced manifests, for the paper to show what the agent sees).

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
   checklist — on cross-document selectors (Service↔pods) and uncommon references
   (HPA/VPA scaleTargetRef, RoleBinding roleRef, priorityClassName,
   storageClassName). Those scenarios are **correct but gated on 7B**, parked for
   the multi-model run. Do not "fix" them by over-fitting the prompt — that's the
   cherry-picking the whole method forbids. (This list originally also claimed port
   semantics (`targetPort`↔`containerPort`) fail — falsified at K=40: all three
   PortMismatch scenarios pass with J 0.82–1.00. Smoke-era conclusions don't
   automatically survive k=40; re-check before citing.)

4. **Scenario-design rule (breaks the experiment if violated): exactly ONE anomalous
   value.** Every sibling reference must resolve and every sibling value must be
   consistent; the injected fault is the *only* anomaly. Counter-example we hit: a
   Service with `port: 80` while `containerPort: 8080` — the model reads 80≠8080 as a
   port mismatch, so (a) the baseline passes for the wrong reason and (b) removing the
   real deciding field doesn't restore health, breaking the flip. Fix was
   `port==targetPort==containerPort` in the healthy state. When a scenario needs a
   supporting object (a Service the Ingress points to), that object must be fully healthy,
   which often means adding its backing workload too. Related design rule: the
   wrong-name scenarios deliberately use DIFFERENT divergence types (pluralization,
   unrelated name, env suffix, abbreviation, lost hyphen, extra segment, synonym) —
   if every fault were a one-char typo, the model could score by a "two names differ
   by one char" surface heuristic instead of resolving references, and the paper
   would be measuring typo detection. Confirmed empirically (smoke 2026-07, k=3):
   diversifying collapsed several previously-perfect 7B baselines (secret-volume and
   pvc-claim 3/3 → 0/3 — with a synonym name the model just says NoFaultFound), so
   the earlier accuracy WAS substantially typo matching. Never revert the diversity
   to win baselines back.

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

7. **Significance = paired McNemar + BH-FDR; CIs are the effect-size display.** At k=10
   every fraction has a ~±0.28 CI — 0.20 and 0.40 are statistically indistinguishable.
   Single proportions (control Recognized, FP) use **Wilson**; saliency shows its
   **Newcombe** 95% CI. But the bold/"Signal" rule is NOT the CI: baseline and every
   ablation share seeds 0..k-1, so each cell gets a **seed-paired exact McNemar
   p-value** (valid under H0 even if the shared seed doesn't couple the runs; coupling
   only adds power), then **Benjamini–Hochberg** across all cells of the map, signal =
   q ≤ 0.05. Why both parts matter: at ~150 simultaneous cells a raw per-cell 0.05
   admits ~7 false positives — on the k=10 data the old CI rule bolded exactly 3 cells,
   all destabilization artifacts (lesson 8), and all die under BH. Flip side: at k=10 a
   *lone* full flip (p=2⁻⁹) still cannot clear BH over 150 tests — real signal needs
   k≥30 (full flip p≈2·10⁻⁹) or several concordant cells. `make run-all K=30`.

8. **Removal-induced destabilization ≠ signal.** With a fragile model, removing an
   *unrelated* field can knock a correct diagnosis off, producing a positive saliency
   that is not diagnostic signal. Watch for borderline-significant non-deciding cells;
   note them as a first-order limitation. A stronger model reduces this. (The BH-FDR
   rule in lesson 7 is the systematic defense; it killed all three such cells at k=10.)

9. **Healthy (NoFault) scenarios are a control, not a saliency source.** They have no
   fault to lose; their "saliency" is just removal-induced hallucination. Excluded from
   the map; used for the false-positive rate (Table 3).

10. **K=40 buys power for artifacts too — and baseline-only gating passes bias.**
    The 2026-07-03 run: 24 cells cleared McNemar+BH, but they concentrated in the
    three marginal-baseline scenarios and sat mostly on bookkeeping fields
    (apiVersion, creationTimestamp, status timestamps/counters) with b≫c — a fragile
    correct diagnosis knocked off by ANY perturbation, exactly lesson 8 at scale.
    Table 3b cannot floor this (healthy FP ≈ 0: no marginal diagnosis to knock off),
    hence the negative-control floor (24 → 5 signal cells). Independently,
    job-secret-wrong-name walked through the old baseline-only gate with a 1.00
    baseline that its twin exposed as pure bias (J=0) — hence the dual gate. Also:
    correct class ≠ correct locus. In configmap-ref-wrong-name the model blames the
    HEALTHY sibling secretRef.name in ~60% of correct baselines (Table 6 top-blamed,
    localization 0.00) while Table 2a Recognized is 0.93 — it detects the dangling
    name but misreports the path. Table 6 measures path-reporting, not detection.

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
  injected fault per instance.
- **`validate.go` is structural only.** It checks required-field *presence*, not
  relational invariants. Since 2026-07 `requiredPaths` covers every Kind in the
  catalog (workloads incl. container image, Service, PVC, SC, HPA/VPA, RBAC, PC), so
  the `Valid` covariate is trustworthy for stratification — but still don't read it
  as "the manifest is semantically healthy".
