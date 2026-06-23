# kubelean — project context for Claude

> Onboarding doc so a fresh Claude instance knows what we're building without re-explaining.
> **The user communicates in Polish — respond in Polish.** Code, paper, and this doc are in English.

---

## TL;DR

**kubelean** is a research project (academic paper + open-source tool) on **saliency-budgeted
distillation of live Kubernetes resource output for LLM agents**.

When an LLM agent troubleshoots a cluster, it pulls *live* state via `kubectl get -o yaml` /
MCP / client-go. That output is bloated (`managedFields`, `status`, server-injected defaults,
verbose annotations). kubelean **prunes/condenses that output before it reaches the LLM** and
asks: *how much can you strip before diagnostic accuracy drops — and does pruning actually
**improve** Root-Cause-Analysis (RCA) accuracy by removing distracting noise?*

- **Repo / module:** `github.com/mikolajsemeniuk/kubelean` (Go 1.26)
- **Method name (for the paper):** "saliency-budgeted resource distillation"
- **Tool name / brand:** kubelean (a "lean output mode" that plugs into MCP servers)
- **Headline hypothesis (H1):** the accuracy-vs-content curve is **non-monotonic** — there is a
  sweet spot where less YAML = better RCA, not just cheaper.

---

## Who the user is

NLP researcher at **PW MiNI** (Warsaw University of Technology, Faculty of Mathematics and
Information Science), supervised by **Agnieszka Jastrzębska**. Codes in **Go**. Targets a strong
journal (Polish "140 punktów" tier). Values: cheap/laptop-measurable experiments (no AWS, no
burning GPT-4 tokens), surprising single-number "sweet-spot" findings, and **rigorous
verification methodology**.

## Lineage — where this came from

Prior project **llmbench** (`github.com/mikolajsemeniuk/llmbench`) introduced **LGS
(Lead-biased Grounding Score)** — a reference-free, embedding-only summary-quality metric
(Ollama + small local embedders, SummEval, held-out λ selection, Spearman correlations,
cluster bootstrap, paired comparisons, ablations). That paper went well. kubelean is the
**next** paper. It must keep LGS-level rigor but the topic is k8s + AI + LLM agents.

The user likes the genre of "Lost in the Middle" (positional bias) and "compress at 65% not
95%" (threshold/sweet-spot) papers.

---

## How k8s agents ACTUALLY work (the reality check that shaped this)

Verified against HolmesGPT, k8sgpt, kubectl-ai, KubeIntellect, Headlamp AI Assistant, Elastic.

Real agent loop: **alert/question → (RAG over runbooks/past-incidents) → tool-calling loop:
agent decides what to pull and fetches LIVE state (kubectl/MCP/Prometheus: `get -o yaml`,
`describe`, events, logs) → LLM reasons over pulled data → RCA/action.**

Key consequences:
- **Source of truth is the live cluster + observability stack, NOT a vector DB of YAML.** A
  manifest is one API call away, so embedding mutable manifests is pointless.
- Real RAG in k8s-AI is over **prose ops knowledge** (runbooks, postmortems, past RCAs),
  **tool selection (MCP)**, and **logs/events** — not manifests.
- The agent **does** ingest raw, noisy live resource output into its context window → that is
  the real, documented, unstudied pain kubelean attacks.

### Ideas we already REJECTED (do NOT re-propose)
- ❌ Embedding mutable k8s YAML into a RAG/vector DB for retrieval → solution looking for a
  problem (agents query live state; manifests change constantly).
- ❌ Pure knock-offs of "Lost in the Middle" / "compress at 65%" applied to prose/config →
  too derivative; reviewers will see them as reruns.
- ❌ Yet another MCP tool-retrieval benchmark → space is crowded (MCPToolBench++,
  LiveMCPBench, MCPVerse, RAG-MCP, ScaleMCP, Toolshed, Graph RAG-Tool Fusion).

### Parked alternative ideas (if we ever pivot)
- **N2** — reference-free grounding/faithfulness metric for agent RCA vs the live tool-outputs
  it actually pulled (LGS-style, hallucination detection for ops agents). Strongest alternative.
- **N3** — "diagnostic sufficiency": minimal set of tool-outputs needed for correct RCA.
  (Natural second leg of kubelean → "context engineering for k8s agents: what to pull + how
  much to keep".)
- **N4** — reference-free "retrievability" metric for MCP **tool descriptions** (predict
  selection accuracy from the description, without running the agent). Pure LGS-move.

---

## The core idea (N1) — what kubelean is

Conceptual hook (the paper's "lead-biased grounding" equivalent):

> **Distillation = saliency-budgeted resource serialization.** Select the subset of a
> resource's fields that **maximizes diagnostic fidelity subject to a token budget B**
> (a knapsack: each field has a (saliency, token-cost) pair).

A static 3-bucket allow/deny list (Always-strip / Always-keep / Keep-but-summarize) is **NOT
the contribution** — it's just the L2 baseline (≈ kubectl-neat). The paper needs the full
mechanism spectrum + a principled saliency model.

### Mechanism spectrum (the ablation axis)
| Level | Mechanism | What it adds |
|---|---|---|
| **L0** | Raw `-o yaml` | baseline |
| **L1** | Lossless strip (server-managed fields + fields equal to schema default) | "free lunch", reduction with no information loss by construction |
| **L2** | Static role-based buckets (the 3 buckets) | ≈ kubectl-neat; prior-art baseline to beat |
| **L3** | **Corpus-entropy saliency** (low-entropy fields across a corpus = boilerplate; high-variance = signal) | revives the "template vs payload" insight, applied to live output |
| **L4** | **Goal-conditioned embedding-grounding** (keep fields whose embedding grounds the symptom: error/events/logs) | reuses LGS grounding machinery; query/fault-aware |
| **L5 (oracle)** | **Leave-one-field-out diagnostic saliency** (which field, removed, breaks RCA?) | gold saliency map = upper bound for cheap proxies |

### Operation classes (also ablated)
`drop` · `truncate` (top-k events/env/conditions) · `summarize` (long `status` → one line) ·
`canonicalize`.

### Differentiator vs kubectl-neat
kubectl-neat is **static and human-oriented**. kubelean tests **goal-conditioned** distillation
(CrashLoop needs `lastState.terminated` + probes + limits; ImagePull needs `image` +
`imagePullSecrets` + events; RBAC needs role/binding). The "static vs goal-conditioned" ablation
is a real finding neat has no answer to.

### Field-class notes (starting policy)
- **Always-strip (noise):** `managedFields`, `resourceVersion`, `uid`, `generation`,
  `creationTimestamp`, `selfLink`, `last-applied-configuration` annotation, default-injected
  fields (`terminationMessagePath`, `dnsPolicy: ClusterFirst`, …).
- **Always-keep (RCA-critical):** `image`, `env`, `resources/limits`, probes, volumes/mounts,
  `securityContext`, ownerRefs, `restartCount`, `lastState.terminated.reason/exitCode`,
  conditions `Ready/Available=false`.
- **Keep-but-summarize:** long `status`, events truncated to recent/abnormal.

---

## Hypotheses (the paper's theses, not just measurements)
- **H1 (headline):** pruning **improves** RCA accuracy (not only token cost) up to a sweet spot
  — accuracy-vs-content curve is non-monotonic (noise distracts attention).
- **H2 (mechanism + key control):** the gain comes from removing low-entropy boilerplate;
  **structure-aware saliency beats equal-token random-drop/truncation** (proves it's the
  *structure*, not the *cutting* — the "EmbedScorer-style" like-for-like control from LGS).
- **H3:** goal-conditioned (L4) beats static (L2), approaching the oracle (L5).
- **H4 (second leg, LGS-DNA):** a **reference-free "distillation fidelity" metric**
  (embedding-grounding) predicts RCA preservation **without running the LLM** → enables cheap
  tuning. Validated by correlating it with real RCA accuracy.
- **H5:** effects are robust across local models and fault classes.

---

## Methodology — must match LGS rigor (different stats, same level)

**The single most important enabler** (analog of LGS's free human labels / kubeconform oracle):

> Make RCA correctness **automatically + deterministically measurable**. Force the LLM to emit
> structured output: `root_cause_label` (from a **closed set = the injected fault classes**) +
> `offending_resource/field`. Correctness = exact-match against the injected ground truth.
> **No human, no GPT-4 judge.** This gives statistical power: hundreds of scenarios × profiles
> × models × repeats on a laptop.

### LGS → kubelean rigor mapping
- Held-out split (article-level) + λ on dev → **split by fault-family/scenario-template**;
  tune budget B, saliency thresholds, top-k **on dev**, report **on test**. Held-out selection
  is again a first-class methodological contribution.
- Spearman vs human ratings → **RCA accuracy (closed-set)** + **localization precision/recall**
  + (secondary) **execution-based**: does the proposed fix clear the fault on `kind`?
- Cluster bootstrap CI (article-level) → **cluster bootstrap** by fault-family (1000–5000
  resamples), 95% CI on the **accuracy difference** between profiles.
- Paired bootstrap for deltas → **paired design** (every profile on the same scenarios) +
  **McNemar's test** (correct test for paired binary outcomes) + paired cluster bootstrap;
  **Wilcoxon signed-rank** for token reductions.
- Embedder ablation → **model ablation**: 3–4 local LLMs (qwen2.5:7b, llama3.1:8b, +3B/+14B)
  → effect is model-robust, not a one-model artifact.
- G-Eval N=3 + report std → **K=5 repeats** per scenario×profile at T>0; show across-run std
  ≪ effect size.
- 13 baselines → Raw (L0), **kubectl-neat** (prior art), **random-drop / truncation at equal
  budget** (the H2 control), static-role (L2), "LLM-summarize-the-YAML" (expensive baseline),
  full method.
- λ-sweep → **budget sweep B** → Pareto **accuracy-vs-tokens** with CIs; locate the sweet spot.
- Add: **multiple-comparison correction** (Holm–Bonferroni / BH-FDR) — many profiles compared.

### Ablations (richness = LGS)
budget sweep (Pareto) · saliency-source (L2/L3/L4/L5) · operation class (drop/+truncate/
+summarize) · goal-conditioned vs static · model robustness · per-fault-class heterogeneity ·
context scale (single resource vs bundle; positional effect as a controlled covariate, not the
thesis) · **false-omission rate** (how often distillation removed the deciding field — answers
the trust/"shelfware" risk with a number).

### Stretch (top-tier)
oracle leave-one-out saliency map (L5) · **mixed-effects logistic regression**
`correct ~ profile + (1|scenario) + (1|model)` · proxy-metric fidelity + correlation study (H4)
· execution-based remediation on a subset.

### Threats to validity
single judge-model (→ multi-model) · synthetic faults vs real (→ validate a subset on public
postmortems) · closed-set vs open-ended RCA · `kind` ≠ prod scale · overfit to fault
distribution (→ held-out fault families).

---

## Dual artifact + adoption (so it's not shelfware)

**Science and tool are separable:** the **evaluation is fully offline** (feed distilled vs full
YAML to a local LLM, measure RCA on injected faults). The MCP server is the
**distribution/impact** channel, not a dependency of the paper.

**Why integration is easy:** an MCP server fully controls what a tool-call returns; distillation
is a pure post-processing transform on the already-fetched object, no protocol change, no extra
cluster call. Precedent: `Flux159/mcp-server-kubernetes` already post-processes output
(`MASK_SECRETS`, default on) — a "lean output mode" is the same hook. Ecosystem is **Go-native**
(`containers/kubernetes-mcp-server` uses client-go) = the user's home turf.

**Core artifact:** a pure Go function
`distill(obj *unstructured.Unstructured, profile Profile, goal Goal) *unstructured.Unstructured`
— stateless, deterministic, easily unit-tested, composes with MASK_SECRETS.

**Distribution vectors:** (1) own reference MCP server in Go (the paper's artifact); (2) PR a
"lean output mode" to existing servers (Flux159 / containers / rohitg00); (3) transparent MCP
proxy that rewrites tool results for any server.

**Anti-shelfware design rules:** (a) transparent — applied on the normal `get`/`describe` path,
not a new opt-in tool the agent must choose; (b) tiered profiles `full|lean|minimal`,
conservative by default, with an "expand to full resource X" escape-hatch (matches the agentic
loop); (c) ship as a reusable lib so multiple servers share one policy.

---

## Tech stack
- **Go** (k8s-native: `apimachinery/unstructured`, `sigs.k8s.io/yaml`).
- **Ollama** for local LLMs + embedders (qwen2.5:7b-instruct is the default; nomic-embed-text present for L4/H4).
- **Faults are generated in pure Go** (`pkg/faults`), in-RAM, deterministic from a seed — **not** `kind`. The science is fully offline. `kind`/docker are only needed for the stretch execution-based-remediation leg (currently absent locally).
- Reuse evaluation patterns from llmbench (`pkg/eval`: closed-set scoring; bootstrap/McNemar are TODO).

## Code layout (BUILT — read this before touching anything)

The pipeline is **generate → distill → evaluate**. See `README.md` + `Makefile` for how to run.

- `pkg/distill/` — the core transform. `Distill(obj, Profile{Level, Goal})`, pure & deterministic.
  Levels **implemented: L0 raw, L1 lossless (strip server-managed noise), L2 static buckets
  (defaults + SA plumbing + status boilerplate + annotations)**. `RandomDrop` = the **H2 control**
  (random cutting to a token budget). `OllamaCounter` (model-tokenizer token counting via
  `prompt_eval_count`). `ToYAML/FromYAML`. **L3 (corpus-entropy, `entropy.go`), L4
  (goal-conditioned embedding-grounding, `grounding.go`+`embed.go`), L5 (oracle
  leave-one-field-out, `oracle.go`) are now built.** Tested.
- `pkg/faults/` — the **data generator** (replaces `kind`). `Catalog()` = **10 fault classes**
  (`catalog.go`), each rendering a faulty resource + ground-truth label + deciding field.
  `basePod` = realistic bloated Pod template. `Inflate(obj, volume, mislead, seed)` = two noise
  axes (structural managedFields vs semantic distractor annotations). Instances are **in-RAM and
  reproducible from a seed (nothing written to disk)**. Tested.
- `pkg/eval/` — closed-set `Labels` + `RCAClient.Diagnose()` (Ollama, forces structured
  `{root_cause_label, offending_field}` JSON; returns the exact prompt token count so no separate
  tokenizer call is needed).
- `pkg/bench/` — **shared experiment harness** (so the `cmd/`s stay thin). `Renderer.Bundle`
  serializes a resource bundle under any profile (L0..L4/rand); `Run` does the two-pass sweep
  (Pass 1 serialize everything — keeps an L4 embedder loaded; Pass 2 the RCA calls); `Acc`
  accumulator; `Corpus`/`Inflate` helpers; `TexEscape`/`WriteFile` for the `.gen.tex` output.
- **`cmd/` = one small command per paper artifact** (like llmbench). Each writes
  `paper/<name>.gen.tex` (`-tex ""` to skip) and can run independently/in parallel:
  - `cmd/tokens/` — token reduction L0→L1→L2→L3 (no LLM, model tokenizer) → `tokens.gen.tex`.
  - `cmd/accuracy/` — main RCA benchmark, acc+tokens per profile + by difficulty (H1/H2/H3;
    `-l4` adds the goal-conditioned profile) → `accuracy.gen.tex`.
  - `cmd/perclass/` — per-fault-class accuracy across profiles (H5) → `perclass.gen.tex`.
  - `cmd/noise/` — structural (`-volumes`) + semantic (`-mislead`) robustness sweeps →
    `noise.gen.tex`.
  - `cmd/oracle/` — **L5 oracle** leave-one-field-out saliency per class + whether it recovers
    the injected `OffendingField`. Expensive (one RCA pass per field); tiny sample by default →
    `oracle.gen.tex`.
- `paper/` — LaTeX source; every `*.gen.tex` is auto-generated (regenerated each run; need
  `booktabs`). `paper/README.md` maps file→command. **`docs/` was removed** (findings live in
  the `.gen.tex` artifacts + git history; the taxonomy/provenance is the section below).
- **NOT built:** statistics (McNemar/cluster bootstrap/Holm), the MCP server
  (`cmd/kubelean`), the `paper/` prose itself.

## Fault taxonomy & provenance (was `docs/fault-taxonomy.md`)

The benchmark does **not** invent its own fault space — it anchors to two prior benchmarks
(defuses the "why roll your own benchmark?" objection):
- **Cloud-OpsBench** (arXiv 2603.00468, `github.com/LLM4Ops/Cloud-OpsBench`) — taxonomy + label
  backbone: 40 root-cause types in **8 categories** (Admission, Scheduling, Startup, Runtime,
  Service routing, Performance, Infrastructure, App-code) on k8s v1.31. Ground truth is
  `{fault_taxonomy, fault_object, root_cause}` ≈ our `{category, offending_field, root_cause_label}`.
  **License: none → cite & derive the taxonomy, do NOT re-host their files.** Their YAML is clean
  reconstructed spec (no bloat), so it is a *label* source, not the raw bloated `-o yaml` we prune.
- **OperAID** (`github.com/EricssonResearch/operaid`, **MIT**) — methodological sibling; source of
  the Config/Scaling/Network classes and of the closed-loop fault-injection→diagnosis→remediation
  →execution-verification pipeline cited for the (stretch) remediation leg.

**The key gap / our novelty:** neither benchmark keeps the bloated `kubectl get -o yaml`
(managedFields/status/defaults) — everyone discards it as noise. That discarded bloat is exactly
kubelean's raw material, so we source *labels/fault content* from prior art but **generate the
bloated output ourselves** (`pkg/faults`, server-faithful re-inflation). Each fault class carries
exactly one `OffendingField` path → clean localization metric + a well-defined L5 leave-one-out
target. `Catalog()` ships 10 classes (CrashLoop, OOMKilled, ImagePull×2, ReadinessProbe,
Pending_InsufficientResources, CreateContainerConfigError, Scaling_ZeroReplicas, and the
Service/NetworkPolicy **bundles**); `eval.Labels` is the wider closed set (14) the LLM must pick
from. The per-class `Provenance` field records which prior benchmark each class derives from.

---

## Status & next steps (live — updated mid-M3)

**Milestones:** M0 ✅ (distill + tokens) · M1 ✅ (first accuracy) · M2 ✅ (full generator matrix,
H1+H2) · **M3 in progress** — L3/L4/L5 mechanisms ✅ **built**; statistics still TODO · M4 (MCP
server / OSS-wedge) · M5 (paper).

**Headline results (latest run; regenerate the numbers via `make accuracy` → `paper/accuracy.gen.tex`,
no static log file anymore):** qwen2.5:7b, n=3, k=3, all 10 classes:
| profile | acc | tok |
|---|---|---|
| L0 | 68.9% | 1154 |
| **L1** | **81.1%** | 863 |
| L2 | 74.4% | 634 |
| L3 | 70.0% | 657 |
| rand | 45.6% | 527 |
- **L1 is now the headline:** lossless strip gives **+12pp AND −25% tokens with zero info loss** —
  cleaner than the old "L2 beats L0" story (no over-pruning caveat). Likely driven by removing the
  `last-applied-configuration` blob, not managedFields per se → **worth ablating** (managedFields-only
  vs annotation-only).
- **Non-monotonic curve = H1:** accuracy peaks at L1 then declines as you cut more (L2/L3) — the
  sweet-spot shape, not a trade-off.
- **H2 strongly supported:** random-drop (45.6%) ≪ every structured profile at equal/smaller budget.
- **L3 is goal-blind, and it shows:** `Scaling_ZeroReplicas` 100%→**0%** under L3 because Deployment
  is the *only* fault class of its Kind → `spec.replicas` is corpus-constant → entropy 0 → dropped
  (the deciding field!). Conversely L3 **fixes** `ReadinessProbe` (→100%) where L2 regressed (→44%).
  This blindness is the motivation for L4.
- **Bundles still ~0% at every level** = 7b model-capacity floor, not distillation (re-run on a
  stronger model to separate the two).

**Locked methodology decisions (do not re-litigate):**
- Eval is **offline**; data comes from the **pure-Go generator** (seeded, in-RAM), not `kind`.
- Taxonomy/labels **derived from Cloud-OpsBench** (no license → cite, don't re-host) + **OperAID**
  (MIT). See the taxonomy section above.
- Token counting uses the **model tokenizer** (`prompt_eval_count`), folded into `Diagnose`.
- L3 corpus is **cross-class per Kind** (NOT per-class: within one class the deciding field is
  constant → would be dropped). L4 anchor = symptom text from `status` + goal; `status` is never
  pruned. L5 base = L1; identity fields (apiVersion/kind, name/namespace) never removed.

**Open for M3 (next):**
- **Statistics** (the main gap): McNemar (paired binary), cluster bootstrap by fault-family,
  Holm/BH correction — wire as their own `cmd/` → `.gen.tex`.
- **Tune `-l4thresh`** (default 0.5 is a guess) on a dev split; report on test. Same for `-l3thresh`.
- **Ablate the L1 gain** (managedFields-only vs last-applied-only).
- Re-run **bundles on a stronger model**; Pareto/budget sweep.

**Gotchas for the next session:**
- The dev machine is **memory-constrained (swaps under load)** → keep runs modest (n≤5, k≤5);
  a too-large run looks "hung" but is just crawling. Ollama serializes per model — **don't run two
  models at once**; the experiment cmds print final tables only at the end (progress bars are on
  stderr). `cmd/accuracy`/`perclass` re-run the full sweep independently, so running both = 2× LLM
  cost (intentional: they're standalone artifact generators).
- **L4** needs the embed model loaded; `pkg/bench.Run` renders all profiles first (Pass 1) so Ollama
  doesn't thrash between the embed and RCA models. `make all-l4` runs accuracy+perclass with `-l4`.

## Key references
- k8s agents: HolmesGPT (CNCF), k8sgpt, kubectl-ai, KubeIntellect (arXiv 2509.02449),
  Headlamp AI Assistant (kubernetes.io).
- Output bloat: kubernetes/kubernetes#90933 (managedFields in `-o yaml`), kubectl-neat.
- MCP k8s servers: `containers/kubernetes-mcp-server` (Go, client-go), `Flux159/mcp-server-kubernetes`
  (MASK_SECRETS precedent), `rohitg00/kubectl-mcp-server`.
- Fault-injection + execution-based eval precedent: OperAID (k8s LLM remediation benchmark).
- Prior work by the user: LGS / llmbench (`github.com/mikolajsemeniuk/llmbench`).
