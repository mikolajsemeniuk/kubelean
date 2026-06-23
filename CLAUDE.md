# CLAUDE.md

Guidance for any model/agent working in this repo. Read this before writing code.
This file is the spec. (`README.md` is an older, unrelated draft — ignore it.)

## What this project is

**kubelean** is a research POC that backs a scientific paper. The paper studies a
single question:

> When an LLM agent debugs a live Kubernetes cluster, how much of a resource's YAML
> actually carries *diagnostic* signal — and how much is noise we can drop before it
> reaches the model, to save context without losing root-cause accuracy?

Motivating scenario: an agent is asked *"why is the prod deployment not working?"*.
The real cause is e.g. the pod template `labels` not matching the Deployment
`selector`. For that fault, `labels`/`selector` carry almost all the signal; `status`
and similar fields carry ~none **for this case**. Returning the whole
`kubectl get -o yaml` wastes the model's context. We want an MCP server to return a
**narrowed YAML** that keeps the deciding fields and drops the noise.

The product of the project is one MCP-friendly function:

```
reduce(yaml string | yamls []string, level) -> reduced YAML
```

`level` selects how aggressively we prune. **Which fields each level keeps or drops is
NOT hand-picked — it is derived from measurement** (see milestones). This is the core
methodological rule below.

## Core methodological rule (read twice)

We must **never** justify cutting/keeping a field by intuition ("annotations are
useless", "image is obviously important"). A reviewer would (correctly) call that
cherry-picking. Every keep/drop decision must trace back to a **measured saliency
number** produced by the benchmark. The pipeline is strictly bottom-up:

```
generate faulty YAML  ->  measure per-field saliency  ->  derive levels from the data  ->  validate
   (m1)                      (m2, the heatmap)             (m3)                             (m4)
```

The levels are an *output* of the experiment, not an input.

## Key concepts (shared vocabulary)

- **field-key** — a canonical path identifying a field across resources, with array
  indices normalized, e.g. `spec.template.spec.containers[].image`. This is the unit
  that `reduce` drops, that the heatmap scores, and that levels (classes) group.
  Defining the exact normalization is the first task of m2 and everything depends on it.
- **field-remover kernel** — the deterministic primitive that, given a YAML and a set
  of field-keys, removes exactly those fields. Both `reduce` (drop a level's noise set)
  and `inspect` (drop one field for one trial) are thin wrappers over it. Build once,
  reuse everywhere.
- **saliency(field, scenario)** — how much removing a field changes the model's RCA
  correctness, e.g. `accuracy(full) − accuracy(full minus field)`, averaged over
  instances/repeats. High positive = the field carries signal (removing it hurts the
  diagnosis); ~0 = noise (safe to drop). The heatmap is the full `field × scenario`
  matrix of this metric.
- **level / class (L1, L2, L3, …)** — a group of field-keys with a saliency threshold.
  The count of levels and their thresholds are **discovered from the heatmap (m3)**,
  not decided up front.

## Milestones (do them in order, small steps — do NOT build ahead)

### m1 — deterministic faulty-resource generator
A reproducible, seeded generator that emits faulty resources: Deployment, StatefulSet,
Pod, ConfigMap, Secret, NetworkPolicy, … — a single resource or a bundle of related
ones (e.g. deploy + cm + secret). Each instance carries **ground truth**: the fault
class and the deciding field. This is the test data for everything downstream. To make
the later context-saving numbers meaningful, generated YAML should resemble real
cluster output (it has to contain prunable bloat, not just a clean minimal manifest).

### m2 — `inspect` benchmark + heatmap artifact
Function roughly:
```
inspect(yamls, model, field, scenario?) -> trial outcome(s)
```
For a given resource set, it removes `field` (via the field-remover kernel), asks the
`model` for the root cause, and records whether the diagnosis was correct. Run across
instances/fields/repeats this produces the **saliency artifact**: the data behind the
heatmap that shows which fields carry the most signal and which are noise.

**Two requirements on the artifact:**
1. Write **raw per-trial results** to disk first, then render the heatmap / paper table
   *from that file* — so tables regenerate without re-running the model.
2. The artifact must make "cut these fields" trivial — effectively a list of field-keys
   sorted by saliency.

### m3 — derive levels from the heatmap
A small cmd that reads the m2 artifact, **finds thresholds** in the saliency
distribution, and assigns field-keys to classes L1/L2/L3/… The number of classes and
the thresholds come from the data, not from us. Emits the level→field-keys config that
`reduce` consumes.

### m4 — validate classes, find the sweet spot
A cmd that takes the m3 classes and re-runs the same RCA test per level, reporting for
each level **how much context it saves** (token reduction) and **how much accuracy it
loses**. This locates the sweet spot and is the paper's headline result.

## Conventions & hard constraints

- **Language:** Go (`go.mod`, Go 1.26). Keep it minimal — no frameworks, no embellishment.
- **POC mindset:** smallest thing that answers the research question. Raise concerns
  rather than silently over-building.
- **Determinism:** generators are seed-reproducible. Record raw trial data so any paper
  output can be reproduced without re-querying the model.
- **Paper output:** every experiment cmd writes its results as `paper/<name>.gen.tex`
  (the paper is in LaTeX). These are `\input`-able fragments (a table/figure body), not
  standalone documents. Keep measurement logic separate from LaTeX formatting:
  compute → raw artifact → render `.gen.tex`.
- **`reduce` is config-driven:** until m3 produces a config, `reduce` only has a trivial
  level (raw / identity). The real levels are loaded from m3's output. This is what
  makes it easy to wire into an MCP server.
- **Small steps:** implement one milestone at a time, get sign-off, then proceed. Do not
  scaffold m2–m4 while doing m1.

## Current status

Scaffolding only. `go.mod` + empty `Makefile` / `cmd` / `pkg` / `paper`. Nothing
implemented. Next concrete step: **m1** (generator + the shared field-remover kernel).

## Known limitations / open questions (POC honesty)

- **Circularity risk.** If saliency just recovers the generator's own injected fields,
  the heatmap measures our fault design, not Kubernetes. Mitigate by measuring the full
  field × fault-class matrix and reporting the *aggregate marginal* signal (a field can
  matter for faults it isn't the "broken" one of). External validity is bounded by how
  representative the m1 fault catalog is — state this in the paper.
- **First-order only.** Single-field removal misses field *interactions* (redundant
  signals, sets that only matter together). Levels (m3) assume saliency is roughly
  additive. A full attribution (Shapley-style) is exponential — out of scope for the
  POC; name it as a limitation.
- **Field canonicalization is load-bearing.** Heatmap aggregation and `reduce` both need
  one canonical field-key scheme across resource kinds. Decide it in m2 before building
  artifacts.
- **Single fault per instance.** Real incidents can be multi-cause; the POC scopes to one
  injected fault per instance.
- **Undecided:** the model runtime and the RCA-scoring mechanism (how `inspect` runs the
  `model` and decides "correct diagnosis") are not yet specified — settle them at the
  start of m2, not now.
