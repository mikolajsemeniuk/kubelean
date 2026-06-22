# kubelean

**Saliency-budgeted distillation of live Kubernetes resource output for LLM agents.**

When an LLM agent troubleshoots a cluster it pulls *live* state via
`kubectl get -o yaml` / MCP / client-go. That output is bloated (`managedFields`,
`status`, server-injected defaults, verbose annotations). kubelean prunes and
condenses that output **before it reaches the LLM**, and asks the research
question:

> How much can you strip before diagnostic (root-cause-analysis) accuracy drops —
> and does pruning actually *improve* RCA accuracy by removing distracting noise?

The evaluation is **fully offline and laptop-measurable**: generated faulty
resources with injected ground truth are fed to a local LLM (via Ollama), and
RCA correctness is scored automatically against a closed set of labels — no
human and no LLM-judge.

## Hypotheses

- **H1** — pruning *improves* RCA accuracy up to a sweet spot (noise distracts),
  not just cheapens it.
- **H2** — the gain comes from *which* fields are kept (structure), not from
  cutting volume: structure-aware distillation beats equal-budget random-drop.
- **H3** — goal-conditioned distillation beats static buckets.
- **H5** — effects are heterogeneous across fault classes and robust across models.

See `docs/findings.md` for current pilot results and `docs/fault-taxonomy.md` for
the fault catalog and its grounding in prior benchmarks.

## How it works

```
   pkg/faults            pkg/distill              pkg/eval
  generate a       ->   prune the YAML     ->    ask a local LLM       ->   score
  faulty resource       (L0/L1/L2/random)        for the root cause          (exact-match
  (+ ground truth)                               (closed-set JSON)            vs ground truth)
```

- **`pkg/faults`** — deterministic generator of faulty resources. 10 fault
  classes (CrashLoop, OOMKilled, ImagePull, probes, scheduling, config, scaling,
  Service/NetworkPolicy bundles), each carrying a ground-truth label and the
  single deciding field. Instances are reproducible from a seed (nothing is
  written to disk). `Inflate` adds two independent noise axes: structural
  (`managedFields`) and semantic (stale distractor annotations).
- **`pkg/distill`** — the distillation transform. `L0` raw, `L1` lossless
  (server-managed noise), `L2` static buckets (≈ kubectl-neat, plus annotation
  stripping), `L3` corpus-entropy saliency (drop fields constant across the
  corpus), `L4` goal-conditioned embedding-grounding (keep fields whose embedding
  grounds the symptom), `L5` oracle leave-one-field-out saliency (the gold upper
  bound). `RandomDrop` is the H2 control: random cutting to the same token budget.
- **`pkg/eval`** — the closed-set label set and the Ollama RCA client that forces
  a structured `{root_cause_label, offending_field}` answer.

## Repository layout

```
pkg/distill/   distillation transform + token counter + H2 random-drop (tested)
pkg/faults/    fault-scenario generator + noise inflation (tested)
pkg/eval/      closed-set labels + structured-output RCA client
cmd/bench/     token reduction L0 -> L1 -> L2 on one resource
cmd/matrix/    full benchmark: RCA accuracy for L0/L1/L2/L3 (+L4) vs random-drop
cmd/oracle/    L5 leave-one-field-out oracle saliency map (expensive)
docs/          fault taxonomy + running findings log
paper/         LaTeX source; matrix.gen.tex / oracle.gen.tex auto-generated
```

## Prerequisites

- **Go 1.26+**
- **[Ollama](https://ollama.com)** running locally (`ollama serve`)
- At least one local model. The defaults use `qwen2.5:7b-instruct`:

```sh
make models        # pulls qwen2.5:7b-instruct
```

## Quickstart

```sh
make test          # unit tests (no Ollama needed)
make bench         # token reduction L0 -> L1 -> L2 (needs Ollama)
make matrix        # full RCA benchmark: L0 vs L2 vs random-drop (needs Ollama)
```

Tune the benchmark size and model:

```sh
make matrix MODEL=llama3.1:8b N=5 K=5         # 5 instances/class, 5 repeats
make matrix-hard                              # only the hard (bundle) classes
make matrix-noise VOLUME=8 MISLEAD=4          # add structural + semantic noise
make matrix-l4                                # include the L4 goal-conditioned profile
make oracle                                   # L5 leave-one-field-out saliency map
```

`make matrix-l4` and the L4 profile additionally need the embedding model
(`make models` pulls `nomic-embed-text`).

`N` = instances per fault class, `K` = repeats per instance per profile.

## Provenance & licensing

- Fault **taxonomy and labels** are derived from and cited to **Cloud-OpsBench**
  (arXiv 2603.00468); its dataset carries no license, so nothing is re-hosted.
- The Config / Scaling / Network fault classes are derived by hand from
  **OperAID** (`github.com/EricssonResearch/operaid`, MIT).
- Neither prior benchmark studies the *representation* of the resource output fed
  to the LLM — that orthogonal axis is kubelean's contribution.
