# paper/

LaTeX source for the kubelean article. Every `*.gen.tex` is **auto-generated** by
a `cmd/` command — do not edit by hand; regenerate with `make <name>` (or
`make paper` for all). Each needs `\usepackage{booktabs}` in the preamble and is
meant to be pulled in with `\input{<name>.gen.tex}`.

| file | command | content |
|------|---------|---------|
| `tokens.gen.tex`   | `make tokens`   | mean prompt tokens per level L0→L1→L2→L3 |
| `accuracy.gen.tex` | `make accuracy` | RCA accuracy + tokens per profile, overall and by difficulty |
| `perclass.gen.tex` | `make perclass` | RCA accuracy per fault class across profiles |
| `noise.gen.tex`    | `make noise`    | structural (volume) and semantic (mislead) robustness sweeps |
| `oracle.gen.tex`   | `make oracle`   | L5 leave-one-field-out saliency + ground-truth recovery |
