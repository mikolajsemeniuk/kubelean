# kubelean m2 — produce raw JSONL shards per group. One step per group; run-all
# lists them. Render is separate: go run ./cmd/render
#
#   make run-secret-ref
#   make run-all

.PHONY: run-selector-mismatch run-secret-ref run-all

run-selector-mismatch:
	go run ./cmd/heatmap -group selector-mismatch

run-secret-ref:
	go run ./cmd/heatmap -group secret-ref

run-all: run-selector-mismatch run-secret-ref
