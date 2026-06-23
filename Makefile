# kubelean — experiment runner.
#
# Each experiment is a small command that writes one paper/<name>.gen.tex table,
# so they can be run independently (and in parallel). All need a local Ollama
# (`ollama serve`) + the model; unit tests (`make test`) need neither.

MODEL   ?= qwen2.5:7b-instruct
EMBED   ?= nomic-embed-text
N       ?= 3
K       ?= 3
TEMP    ?= 0.4

.PHONY: models tokens accuracy perclass noise oracle paper all-l4

## models: pull the local models the experiments default to
models:
	ollama pull $(MODEL)
	ollama pull $(EMBED)

## tokens: token reduction L0->L1->L2->L3 (no LLM) -> paper/tokens.gen.tex
tokens:
	go run ./cmd/tokens -model $(MODEL) -n $(N)

## accuracy: main RCA benchmark, acc+tokens per profile / difficulty -> paper/accuracy.gen.tex
accuracy:
	go run ./cmd/accuracy -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP)

## perclass: per-fault-class accuracy across profiles -> paper/perclass.gen.tex
perclass:
	go run ./cmd/perclass -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP)

## noise: structural (volume) + semantic (mislead) robustness sweeps -> paper/noise.gen.tex
noise:
	go run ./cmd/noise -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP)

## oracle: L5 leave-one-field-out saliency (expensive; small sample) -> paper/oracle.gen.tex
oracle:
	go run ./cmd/oracle -model $(MODEL) -n 1 -k 1 -temp $(TEMP)

## paper: regenerate every .gen.tex (accuracy/perclass/oracle support -l4 via *-l4 vars)
paper: tokens accuracy perclass noise oracle

## all-l4: accuracy + perclass including the L4 goal-conditioned profile (needs EMBED)
all-l4:
	go run ./cmd/accuracy -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP) -l4 -embed $(EMBED)
	go run ./cmd/perclass -model $(MODEL) -n $(N) -k $(K) -temp $(TEMP) -l4 -embed $(EMBED)
