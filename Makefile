.PHONY: run-selector run-references run-networking run-volumes run-scaling run-rbac run-healthy run-all smoke render clean-data

# Samples per variant. Default 10 for quick runs; a 0.2 saliency effect needs
# k>=30 to clear noise, so the paper run is: make run-all K=30.
K ?= 40

# The RCA model under test. Shards land in data/<model>/ and artifacts in
# paper/<model>/, so runs for different models never overwrite each other:
#   make run-all MODEL=qwen2.5:32b-instruct && make render MODEL=qwen2.5:32b-instruct
MODEL ?= qwen2.5:7b-instruct

# The cheap #12 gate check: baselines only, low k, no shards written. Run it
# after any generator/prompt change, before committing to a full run.
smoke:
	for g in selector references networking volumes scaling rbac healthy; do \
		go run ./cmd/heatmap -group $$g -baseline-only -k 3 -model $(MODEL); \
	done

run-selector:
	go run ./cmd/heatmap -group selector -k $(K) -model $(MODEL)

run-references:
	go run ./cmd/heatmap -group references -k $(K) -model $(MODEL)

run-networking:
	go run ./cmd/heatmap -group networking -k $(K) -model $(MODEL)

run-volumes:
	go run ./cmd/heatmap -group volumes -k $(K) -model $(MODEL)

run-scaling:
	go run ./cmd/heatmap -group scaling -k $(K) -model $(MODEL)

run-rbac:
	go run ./cmd/heatmap -group rbac -k $(K) -model $(MODEL)

run-healthy:
	go run ./cmd/heatmap -group healthy -k $(K) -model $(MODEL)

run-all: run-selector run-references run-networking run-volumes run-scaling run-rbac run-healthy

render:
	go run ./cmd/render -model $(MODEL)
