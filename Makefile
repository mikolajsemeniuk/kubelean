# kubelean — experiment runner.
#
# All experiment targets need a local Ollama (`ollama serve`) and the model.
# Unit tests (`make test`) need neither.

MODEL   ?= qwen2.5:7b-instruct
N       ?= 3
K       ?= 3
TEMP    ?= 0.4
VOLUME  ?= 0
MISLEAD ?= 0

.PHONY: bench matrix matrix-hard matrix-noise

## bench: token reduction L0 -> L1 -> L2 on one representative resource
bench:
	go run ./cmd/bench -model $(MODEL)

## matrix: full RCA benchmark, all classes, L0 vs L2 vs random-drop
matrix:
	go run ./cmd/matrix -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP) -difficulty all

## matrix-hard: only the hard (multi-resource bundle) classes
matrix-hard:
	go run ./cmd/matrix -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP) -difficulty hard

## matrix-noise: full benchmark with structural + semantic noise added pre-distill
matrix-noise:
	go run ./cmd/matrix -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP) -volume $(VOLUME) -mislead $(MISLEAD)
