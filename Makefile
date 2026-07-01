.PHONY: run-selector run-references run-networking run-volumes run-healthy run-all clean-data

run-selector:
	go run ./cmd/heatmap -group selector

run-references:
	go run ./cmd/heatmap -group references

run-networking:
	go run ./cmd/heatmap -group networking

run-volumes:
	go run ./cmd/heatmap -group volumes

run-healthy:
	go run ./cmd/heatmap -group healthy

run-all: run-selector run-references run-networking run-volumes run-healthy

clean-data:
	rm -f data/*.jsonl
