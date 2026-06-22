# paper/

LaTeX source for the kubelean article.

- `matrix.gen.tex` — **auto-generated** by `cmd/matrix` (do not edit by hand;
  regenerate with `make matrix`). Holds the per-profile and per-class RCA result
  tables, ready to `\input{}`. Requires `\usepackage{booktabs}` in the preamble.
- `oracle.gen.tex` — **auto-generated** by `cmd/oracle` (`make oracle`). The L5
  leave-one-field-out saliency table: top decisive field per fault class and
  whether it recovers the injected deciding field. Also needs `booktabs`.
