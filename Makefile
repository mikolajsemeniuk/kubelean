.PHONY: run-selector run-references run-networking run-volumes run-scaling run-rbac run-healthy run-all smoke render clean-data

# Samples per variant. Default 10 for quick runs; a 0.2 saliency effect needs
# k>=30 to clear noise, so the paper run is: make run-all K=30.
K ?= 40

# The RCA model under test. Shards land in data/<model>/ and artifacts in
# paper/<model>/, so runs for different models never overwrite each other:
#   make run-all MODEL=qwen3:14b && make render MODEL=qwen3:14b
MODEL ?= qwen2.5:7b-instruct

# Inference endpoint. One backend per model directory — mixing Ollama and vLLM
# shards for the same model mixes quantizations/weights and breaks digest
# comparability (this happened to gemma4: 33 Ollama + 36 vLLM shards).
HOST ?= http://192.168.100.121:12000
BACKEND ?= vllm

PRODUCE = go run ./cmd/heatmap -host $(HOST) -backend $(BACKEND) -model $(MODEL)

# The cheap #12 gate check: baselines only, low k, no shards written. Run it
# after any generator/prompt change, before committing to a full run.
smoke:
	for g in selector references networking volumes scaling rbac healthy; do \
		$(PRODUCE) -group $$g -baseline-only -k 3; \
	done

run-selector:
	$(PRODUCE) -group selector -k $(K)

run-references:
	$(PRODUCE) -group references -k $(K)

run-networking:
	$(PRODUCE) -group networking -k $(K)

run-volumes:
	$(PRODUCE) -group volumes -k $(K)

run-scaling:
	$(PRODUCE) -group scaling -k $(K)

run-rbac:
	$(PRODUCE) -group rbac -k $(K)

run-healthy:
	$(PRODUCE) -group healthy -k $(K)

run-all: run-selector run-references run-networking run-volumes run-scaling run-rbac run-healthy

render:
	go run ./cmd/render -model $(MODEL)
