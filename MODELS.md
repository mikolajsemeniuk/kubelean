# Models

## Paper runs — pinning & serving rules

vLLM exposes no weights hash (shard `model_digest` = served name), so record
per run: the HF revision (`huggingface-cli scan-cache` or vLLM startup log)
and the image digest (`docker inspect --format '{{index .RepoDigests 0}}'
vllm/vllm-openai:latest` — `latest` is a moving tag).

| served name (`MODEL=`) | HF repo | revision | dtype/quant as served |
|---|---|---|---|
| qwen2.5:7b-instruct | Qwen/Qwen2.5-7B-Instruct | _fill_ | bf16 (run 2026-07-20) |
| gemma4:12b | google/gemma-4-12B-it | _fill_ | run 2026-07-20: online fp8 W8A8 + fp8 KV + ngram spec (verified: 90160 trials, 0 unparsed) |
| ministral-3:8b | mistralai/Ministral-3-8B-Instruct-2512 | _fill_ | native fp8 checkpoint, bf16 KV |
| qwen3:14b | Qwen/Qwen3-14B-FP8 | _fill_ | official block-wise fp8, bf16 KV |
| phi4:14b | RedHatAI/phi-4-FP8-dynamic | _fill_ | calibrated fp8-dynamic (99.9% recovery), bf16 KV |

Rules (2026-07-21 research; rationale + sources in commit d900b51):

* **No ngram spec decode** on new runs — vllm#40875 corrupts structured
  output at `prompt_lookup_min: 2`, and spec decode changes per-seed sampling
  realizations (the seed-paired McNemar depends on them). Never change the
  serving config mid-run — one config per model directory.
* **KV cache bf16**, fp8 weights only via checkpoints (never online
  `--quantization fp8`; Ministral 2512 is already natively fp8).
* On fp8 models: `--attention-backend TRITON_ATTN` (FlashInfer fp8 accuracy
  bug on Blackwell) and `VLLM_MARLIN_USE_ATOMIC_ADD=1` (sm121 Marlin race =
  silently wrong results).
* Cudagraphs stay on; on hangs try `{"cudagraph_mode": "PIECEWISE"}` first.
  Keep host driver on 580.x (590.x deadlocks CUDAGraph on GB10).
* `--enable-prefix-caching`/`--enable-chunked-prefill` are V1 defaults — omit.

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
make run-all K=40 MODEL=qwen3:14b        # the paper run
make render MODEL=qwen3:14b
```
