# Models

The paper ladder (one family-diverse capability axis, all served via vLLM):

| profile | served name (`MODEL=`) | params | role |
|---|---|---|---|
| qwen2.5 | qwen2.5:7b-instruct | 7B dense | lower anchor (the original RCA model) |
| ministral-3 | ministral-3:8b | 8B dense | small-model diversity (Mistral family) |
| gemma-4 | gemma4:12b | 12B dense | workhorse (32/33 scenarios scored at K=40) |
| qwen3-14b | qwen3:14b | 14B dense | codellama-13b replacement / upper anchor |

Dropped from the ladder (profiles kept for reproducibility):

* **codellama** (13B) — gated out on ALL 33 faulty scenarios at K=40; zero
  scored data. Its data/ directory is kept as honest coverage (Table 4).
* **minimax-m2.7**, **glm-4.5-air** — MoE with interleaved/hybrid thinking;
  minimax cannot cleanly disable thinking (`minimax_m2_append_think`), so
  outputs blow past `-num-predict` and trials crawl. Smoke first if tempted.
* **lfm2.5** (8B-A1B) — ~1B active params, expected to gate out like codellama.

## Weight pinning (the vLLM digest gap)

Ollama's `/api/tags` digest pins exact weights; vLLM's OpenAI API exposes no
weights hash, so `model_digest` in vLLM-produced shards is just the served
model name — it confirms the server, not the weights. **Before a paper run,
record here the exact HF revision and dtype/quantization per model** (e.g.
`huggingface-cli scan-cache` or the `revision` printed in vLLM startup logs).
This table is the paper's substitute for the digest contract:

| served name | HF repo | revision (commit) | dtype/quant |
|---|---|---|---|
| qwen2.5:7b-instruct | Qwen/Qwen2.5-7B-Instruct | _fill before run_ | bf16 |
| ministral-3:8b | mistralai/Ministral-3-8B-Instruct-2512 | _fill before run_ | bf16 |
| gemma4:12b | google/gemma-4-12B-it | _fill before run_ | bf16 |
| qwen3:14b | Qwen/Qwen3-14B | _fill before run_ | bf16 |

One backend per model directory: never mix Ollama and vLLM shards for the same
model — different quantizations are different weights (this happened to
gemma4: 33 Ollama + 36 vLLM shards in the 2026-07 run; the renderer's digest
warning catches it).

Note: `--served-model-name` must equal the `MODEL=` tag passed to make, or
`Digest()` fails and chat requests 404. The qwen3-14b profile already follows
this (`qwen3:14b`); older profiles used short names (`qwen2.5`, `gemma-4`) —
override with e.g. `--served-model-name qwen2.5:7b-instruct` when re-running.

## Start

```sh
export PROFILE=qwen3-14b
docker compose --profile $PROFILE up -d
docker compose --profile $PROFILE logs -f
docker compose --profile $PROFILE down
```

## Test

```sh
curl -s http://localhost:12000/v1/chat/completions -H "Content-Type: application/json" -d '{"model":"qwen3:14b","messages":[{"role":"user","content":"hi"}],"max_tokens":30}' | python3 -m json.tool
```

## Produce

```sh
make smoke MODEL=qwen3:14b                # k=3 baselines, no shards — always first
make run-all K=40 MODEL=qwen3:14b        # the paper run
make render MODEL=qwen3:14b
```
