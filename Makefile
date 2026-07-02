.PHONY: run-selector run-references run-networking run-volumes run-scaling run-rbac run-healthy run-all smoke clean-data

# Samples per variant. Default 10 for quick runs; a 0.2 saliency effect needs
# k>=30 to clear noise, so the paper run is: make run-all K=30.
K ?= 10

# The cheap #12 gate check: baselines only, low k, no shards written. Run it
# after any generator/prompt change, before committing to a full run.
smoke:
	for g in selector references networking volumes scaling rbac healthy; do \
		go run ./cmd/heatmap -group $$g -baseline-only -k 3; \
	done

run-selector:
	go run ./cmd/heatmap -group selector -k $(K)

run-references:
	go run ./cmd/heatmap -group references -k $(K)

run-networking:
	go run ./cmd/heatmap -group networking -k $(K)

run-volumes:
	go run ./cmd/heatmap -group volumes -k $(K)

run-scaling:
	go run ./cmd/heatmap -group scaling -k $(K)

run-rbac:
	go run ./cmd/heatmap -group rbac -k $(K)

run-healthy:
	go run ./cmd/heatmap -group healthy -k $(K)

run-all: run-selector run-references run-networking run-volumes run-scaling run-rbac run-healthy

clean-data:
	rm -f data/*.jsonl
