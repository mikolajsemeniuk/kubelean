.PHONY: run-selector run-references run-healthy run-all

run-selector:
	go run ./cmd/heatmap -group selector

run-references:
	go run ./cmd/heatmap -group references

run-healthy:
	go run ./cmd/heatmap -group healthy

run-all: run-selector run-references run-healthy
