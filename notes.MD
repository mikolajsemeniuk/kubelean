# Operacje kubectl — przestrzeń obserwacji i akcji agenta

> Notatka robocza do artykułu o reprezentacji/redukcji kontekstu k8s dla agentów AI.
> Kluczowy wniosek: większość outputu kubectl **nie jest YAML-em**, co podważa wąskie ramowanie „kompresji manifestów".

## Odczyt / obserwacja (to, co agent „widzi")

- **`get`** — listowanie z bogatą warstwą projekcji: `-o jsonpath`, `-o custom-columns`, `-o go-template`, `-o name`, `--field-selector`, `-l` (label selector), `-w` (watch/stream). To już jest wbudowana kompresja/projekcja po stronie API.
- **`describe`** — człekoczytelny zrzut + eventy powiązane z obiektem. Zupełnie inna reprezentacja niż manifest.
- **`logs`** — `--previous`, `-f`, `--since`, `--tail`, multi-container. Potrafią być gigantyczne i to one, a nie manifesty, rozsadzają okno kontekstu.
- **`events`** (`kubectl events` / `get events`) — strumień zdarzeń, często najważniejszy sygnał diagnostyczny.
- **`top`** — metryki node/pod (wymaga metrics-server).
- **`explain`** — dokumentacja schematu typu (agent może się uczyć struktury zamiast trzymać ją w kontekście).
- **`api-resources`, `api-versions`, `cluster-info`, `version`** — introspekcja klastra.

## Mutacja stanu (to, co agent „robi")

- **`apply`** (deklaratywnie, w tym `--server-side`), `create`, `replace`, `delete`, `edit`.
- **`patch`** — trzy warianty (strategic merge, JSON merge, JSON patch); najbardziej „chirurgiczna" mutacja i naturalnie zwięzła.
- **`set`** (image/resources/env/selector/serviceaccount), `scale`, `autoscale`, `annotate`, `label` — imperatywne skróty do typowych zmian.
- **`rollout`** — `status`, `history`, `undo`, `restart`, `pause`, `resume`.

## Debug / interakcja

- **`exec`, `attach`, `port-forward`, `cp`**.
- **`debug`** — ephemeral containers, debug node'a, kopia poda. Coraz ważniejsze dla agentów SRE.
- **`run`** — imperatywne odpalenie poda.

## Operacje na klastrze / node'ach

- **`cordon` / `uncordon`, `drain`, `taint`**.

## Auth / dostęp / bezpieczeństwo

- **`auth can-i`** — sprawdzenie RBAC (agent może odpytać, czy w ogóle *może* coś zrobić, zanim spróbuje).
- **`auth reconcile`, `certificate approve/deny`, `config`** (konteksty/kubeconfig).

## Bramki decyzyjne (bardzo istotne dla agenta)

- **`diff`** — pokazuje, co zmieni `apply`, bez aplikowania.
- **`--dry-run=client|server`** — walidacja bez skutków ubocznych.
- **`wait`** — blokowanie do spełnienia warunku (`--for=condition=...`), czyli feedback loop.
- **`kustomize` / `apply -k`, `proxy`**, oraz plugins (`krew`).
