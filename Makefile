.PHONY: run-selector run-references run-networking run-volumes run-scaling run-rbac run-healthy run-all smoke clean-data

# The cheap #12 gate check: baselines only, low k, no shards written. Run it
# after any generator/prompt change, before committing to a full run.
smoke:
	for g in selector references networking volumes scaling rbac healthy; do \
		go run ./cmd/heatmap -group $$g -baseline-only -k 3; \
	done

run-selector:
	go run ./cmd/heatmap -group selector

run-references:
	go run ./cmd/heatmap -group references

run-networking:
	go run ./cmd/heatmap -group networking

run-volumes:
	go run ./cmd/heatmap -group volumes

run-scaling:
	go run ./cmd/heatmap -group scaling

run-rbac:
	go run ./cmd/heatmap -group rbac

run-healthy:
	go run ./cmd/heatmap -group healthy

run-all: run-selector run-references run-networking run-volumes run-scaling run-rbac run-healthy

clean-data:
	rm -f data/*.jsonl
