# kubelean — running findings log

Laptop-measurable results as the experiment is built up. All numbers are local
(Ollama), offline, on hand-authored single-Pod fixtures unless noted. These are
**pilot** signals (low statistical power) until the generator (pkg/faults) lands.

## Setup
- Fixtures: `testdata/pod_{crashloop,oomkilled,imagepull}.yaml` — 3 self-contained
  single-Pod faults (F01/F02/F03), each with a closed-set ground-truth label.
- Scoring: closed-set `root_cause_label` exact-match vs injected truth. Automatic,
  deterministic, no human, no LLM judge.
- Token counts: real model tokenizer (Ollama `prompt_eval_count`).

## M0 — token reduction (no LLM)
Single bloated CrashLoop Pod, model tokenizer:

| level | tokens | vs L0 |
|---|---|---|
| L0 raw | 1637 | 100% |
| L1 lossless | 1294 | 79% |
| L2 static | 972 | 59% |

L1 (lossless, by construction) already removes 21% of tokens; L2 removes 41%
while keeping every RCA-critical field.

## M1 — RCA accuracy, no added noise
3 fixtures, k=5, T=0.7. Both qwen2.5:7b-instruct and qwen3.6:35b-coding score
**100% at L0 and L2**. Ceiling effect: these labels appear verbatim in the
resource status (`CrashLoopBackOff`, `OOMKilled`, `ImagePullBackOff`), so RCA is
keyword extraction, not inference — pruning cannot help where there is no
confusion. Conclusion: H1 needs faults where the root cause must be *inferred*.

## M2 — noise sweeps (qwen2.5:7b-instruct, k=5, T=0.7)

Two independent noise knobs (pkg/faults.Inflate):
- **volume** = managedFields ballooning (fields L2 strips in full).
- **mislead** = stale ops annotations naming OTHER fault classes + a healthy sidecar.

### Volume axis — structural bloat is inert
| volume | L0 acc | L0 tok | L2 acc | L2 tok |
|---|---|---|---|---|
| 0 | 100% | 1304 | 100% | 694 |
| 4 | 100% | 4970 | 100% | 694 |
| 8 | 93%* | 8656 | 100% | 694 |
| 16 | 100% | 16078 | 100% | 694 |

L2 perfectly flat (managedFields fully stripped → distillation invariant). L0
**does not degrade** even at 16k tokens of managedFields. **managedFields is
inert noise**: stripping it is a free, RCA-safe token saving, but it was never
hurting accuracy. (*93% = 1/15 sampling noise.)

### Mislead axis — semantic distractors DO hurt; distillation protects
Before adding annotation-stripping to L2 (L2 kept annotations):

| mislead | L0 acc | L2 acc (keeps annotations) |
|---|---|---|
| 0 | 100% | 100% |
| 1 | 100% | 60% |
| 8 | 87% | 60% |

After adding annotation-stripping to L2:

| mislead | L0 acc | L0 tok | L2 acc | L2 tok |
|---|---|---|---|---|
| 0 | 93% | 1304 | **100%** | 685 |
| 1 | 93% | 1629 | **100%** | 888 |
| 2 | 80% | 1772 | **100%** | 918 |
| 4 | 100% | 2071 | **100%** | 983 |
| 8 | 80% | 2691 | **100%** | 1127 |

**First clean H1 signal (protection form):** raw input degrades under realistic
stale-annotation distractors (~80–89%), while distillation that strips them holds
**100%**. Distillation *improves* accuracy, not just token cost.

## M2 — full generator matrix (qwen2.5:7b-instruct, n=3, k=5, T=0.4)
10 fault classes × 3 instances × 5 repeats (15 trials/class), profiles
L0 / L2 / random-drop (equal-ish token budget = H2 control).

| profile | acc | mean tok |
|---|---|---|
| L0 raw | 69.3% | 1154 |
| **L2 static** | **74.0%** | **634** |
| random-drop | 54.7% | 556 |

By difficulty: easy L0=87% **L2=100%** rand=67%; hard L0=52% L2=48% rand=43%.

Per class (L0→L2→rand):

| class | diff | L0 | L2 | rand |
|---|---|--:|--:|--:|
| OOMKilled | easy | 33 | **100** | 47 |
| CrashLoopBackOff | easy | 100 | 100 | 53 |
| ImagePullBackOff_BadImage | easy | 100 | 100 | 67 |
| ImagePullBackOff_NoAuth | hard | 93 | 100 | 67 |
| CreateContainerConfigError | easy | 100 | 100 | 67 |
| Pending_InsufficientResources | easy | 100 | 100 | 100 |
| Scaling_ZeroReplicas | hard | 100 | 100 | 100 |
| ReadinessProbeFailure | hard | 67 | **40** | 40 |
| Service_SelectorMismatch | hard | 0 | 0 | 0 |
| NetworkPolicy_BlocksIngress | hard | 0 | 0 | 7 |

**Reads (this is the headline M2 result):**
1. **H1 supported in aggregate.** L2 **beats** L0 (74.0% vs 69.3%) at ~55% of the
   tokens. Pruning *improves* accuracy and cuts cost — not a trade-off.
2. **H2 strongly supported.** Random-drop at ~equal budget (54.7%, 556 tok) is far
   below structure-aware L2 (74.0%, 634 tok). The gain is *which* fields are kept.
3. **Heterogeneity is large (→ H5, the scientific hook).** OOMKilled +67pp (raw
   confuses OOM with the CrashLoopBackOff symptom; L2 exposes lastState=OOMKilled),
   ReadinessProbeFailure −27pp (L2 hurts), bundles flat at floor. This spread
   motivates goal-conditioned L4 (keep the right fields per fault class).
4. **The "hard" aggregate is misleading** — it is dragged down by the two bundle
   classes that sit at ~0% even at L0 (a 7b model-capacity floor on multi-resource
   RCA, not a distillation effect). Excluding bundles, distillation helps or holds
   on every hard class except ReadinessProbe.

Reproducible negatives / open items for M3:
- **ReadinessProbe L2 regression (67→40) is consistent across all runs.** The
  deciding evidence (running-but-not-ready container + readinessProbe + Ready=False)
  survives L2, so it is not a distill bug; likely the model confuses it with
  LivenessProbeFailure under leaner context → an L4 (goal-conditioned) case.
- **Bundles need a stronger model** to separate capacity from distillation.
- **Statistical tests** (McNemar, cluster bootstrap) and a budget/Pareto sweep.

## Conclusions shaping the design
1. **Not all bloat is equal.** Structural noise (managedFields) is inert →
   stripping = pure token savings, RCA-invariant. Semantic distractors
   (annotations) → degrade RCA → stripping = accuracy protection. This split is a
   richer thesis than "less YAML = better".
2. **Distill policy findings:** strip managedFields (free, safe) AND strip
   non-allowlisted annotations (removes distractors). Goal-conditioned L4 may
   reinstate specific annotation keys.
3. **Open gaps (need the generator):**
   - Statistical power: 3 fixtures → noisy L0 curve. Need ~25–40 instances/class.
   - Ceiling: easy faults cap L2 at 100% → we show *protection*, not yet the
     non-monotonic *improvement above a sub-100% clean baseline* (H1 headline).
     Requires harder faults: symptom ≠ root cause, label not literal in the YAML,
     ideally multi-resource bundles.
   - H2 control still owed: random-drop at equal token budget must NOT match
     structure-aware distillation.
