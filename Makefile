# kubelean — experiment runner.
#
# All experiment targets need a local Ollama (`ollama serve`) and the model.
# Unit tests (`make test`) need neither.

MODEL   ?= qwen2.5:7b-instruct
EMBED   ?= nomic-embed-text
N       ?= 3
K       ?= 3
TEMP    ?= 0.4
VOLUME  ?= 0
MISLEAD ?= 0

.PHONY: models bench matrix matrix-hard matrix-noise matrix-l4 oracle

## models: pull the local models the experiments default to
models:
	ollama pull $(MODEL)
	ollama pull $(EMBED)

## bench: token reduction L0 -> L1 -> L2 on one representative resource
bench:
	go run ./cmd/bench -model $(MODEL)

## matrix: full RCA benchmark, all classes, L0/L1/L2/L3 vs random-drop; writes paper/matrix.gen.tex
matrix:
	go run ./cmd/matrix -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP) -difficulty all

## matrix-hard: only the hard (multi-resource bundle) classes
matrix-hard:
	go run ./cmd/matrix -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP) -difficulty hard

## matrix-noise: full benchmark with structural + semantic noise added pre-distill
matrix-noise:
	go run ./cmd/matrix -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP) -volume $(VOLUME) -mislead $(MISLEAD)

## matrix-l4: full benchmark including the L4 goal-conditioned profile (needs EMBED)
matrix-l4:
	go run ./cmd/matrix -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP) -difficulty all -l4 -embed $(EMBED)

## oracle: L5 leave-one-field-out saliency map (expensive; small sample by default)
oracle:
	go run ./cmd/oracle -model $(MODEL) -n 1 -k 1 -temp $(TEMP)
