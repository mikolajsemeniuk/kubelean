// Command heatmap is the m2 producer: for a group of scenarios it records raw
// per-trial RCA outcomes to a JSONL shard per scenario under -out. It removes
// each field (heatmap.Remove), asks the model k times (seeds 0..k-1), and writes
// one line per trial — no accuracy, no saliency, no LaTeX. Those are derived
// later by cmd/render from these shards, so a methodology change never re-runs
// the model. Each run overwrites its scenario shards.
//
// Trials within a baseline/variant are run concurrently through a channel-based
// worker pool (see runParallel): a fixed number of workers pull seeds off a
// jobs channel and push finished heatmap.Record values onto a results channel.
// A single goroutine drains results into the slice, so recs is never touched
// from more than one goroutine at a time — no mutex required. Order of recs is
// not guaranteed (and doesn't need to be: every record carries its own Seed,
// Variant, Doc and Field), but every seed 0..k-1 is represented exactly once.
//
//	go run ./cmd/heatmap -group secret-ref -parallel 8
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mikolajsemeniuk/kubelean/pkg/dataset"
	"github.com/mikolajsemeniuk/kubelean/pkg/heatmap"
	"github.com/mikolajsemeniuk/kubelean/pkg/providers"
	"github.com/schollz/progressbar/v3"
)

type diagnosis struct {
	FaultClass string `json:"fault_class"`
}

var (
	host, model, out, backend string
	group                     string
	k, numCtx, numPredict     int
	parallel                  int
	temp                      float64
	baselineOnly, force       bool
)

// buildSchema constrains the model to clean JSON whose fault_class is one of the
// catalog's classes (dataset.FaultClasses() — the single source of truth).
func buildSchema(classes []string) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"fault_class":     map[string]any{"type": "string", "enum": classes},
			"offending_field": map[string]any{"type": "string"},
		},
		"required": []string{"fault_class", "offending_field"},
	}
}

// buildPrompt frames the root-cause task. The class vocabulary (name + the check
// that defines it) is joined in from lines, not hardcoded, so it always matches
// the schema enum. The descriptions give the model the diagnostic procedure
// instead of a label cue. The "reduced" framing is load-bearing: variants have
// fields removed for the ablation, and we must stop the model from reading an
// absent field as a fault in itself.
func buildPrompt(lines []string) string {
	return `You are a senior Kubernetes SRE performing root-cause analysis.
You are given one or more manifests as returned by "kubectl get -o yaml".
The manifests may have been deliberately reduced — some fields removed to save context.
Treat every manifest as valid and well-formed: a missing field is not itself a fault.
Diagnose the root cause only from the information that is present.
At most one root-cause fault is present; the manifests may also be healthy.
Set fault_class to exactly one of these classes — check each in turn:
` + strings.Join(lines, "\n") + `
Set offending_field to the YAML path most responsible, or "none".`
}

type ChatClient interface {
	Chat(ctx context.Context, in providers.ChatInput) (providers.ChatOutput, error)
	Digest(ctx context.Context, model string) (string, error)
}

func main() {
	flag.StringVar(&host, "host", "http://localhost:12000", "inference host (vLLM default port; Ollama runs on http://localhost:11434)")
	flag.StringVar(&model, "model", "qwen2.5:7b-instruct", "model name")
	flag.StringVar(&out, "out", "data", "root output directory for JSONL shards (a per-model subdirectory is appended)")
	flag.StringVar(&group, "group", "", "scenario group to produce")
	flag.IntVar(&k, "k", 10, "samples per variant (seed = 0..k-1)")
	flag.StringVar(&backend, "backend", "vllm", "inference backend: ollama or vllm — one backend per model directory (mixing quantizations breaks digest comparability)")
	flag.Float64Var(&temp, "temp", 0.7, "sampling temperature (>0 so seeds give varied draws)")
	flag.IntVar(&numCtx, "num-ctx", 8192, "context window — avoids silent truncation of multi-doc prompts")
	flag.IntVar(&numPredict, "num-predict", 256, "max output tokens")
	flag.IntVar(&parallel, "parallel", 128, "concurrent in-flight trials per baseline/variant (match to OLLAMA_NUM_PARALLEL / vLLM capacity)")
	flag.BoolVar(&baselineOnly, "baseline-only", false, "run only the baselines and print accuracy — the cheap #12 smoke before a full run; writes no shard")
	flag.BoolVar(&force, "force", false, "redo scenarios whose shard file already exists (default: skip them — resume support so a crash mid-run only costs the time since the last completed scenario)")
	flag.Parse()

	scenarios := dataset.Scenarios(group)
	if len(scenarios) == 0 {
		log.Fatalf("no scenarios in group %q", group)
	}

	schema := buildSchema(dataset.FaultClasses())
	prompt := buildPrompt(dataset.FaultLines())

	ctx := context.Background()
	var client ChatClient
	switch backend {
	case "ollama":
		client = providers.NewOllama(host)
	case "vllm":
		client = providers.NewVLLM(host)
	default:
		log.Fatalf("unknown -backend %q (want ollama or vllm)", backend)
	}

	// Pin the exact weights for the paper: a tag can be re-pulled and change.
	digest, err := client.Digest(ctx, model)
	if err != nil {
		log.Printf("warning: could not read model digest: %v", err)
		digest = "unknown"
	}

	// Shards are namespaced per model (data/<model>/...) so a second model's
	// run never overwrites the first — the future multi-model comparison
	// depends on both sets surviving side by side.
	shardDir := filepath.Join(out, modelDir(model))
	if err := os.MkdirAll(shardDir, 0o755); err != nil {
		log.Fatal(err)
	}

	// Precompute targets once per scenario (heatmap.Keys is deterministic on
	// s.YAML) so we can both size the progress bar up front and reuse the
	// same slice in the main loop below instead of calling Keys twice.
	// perScenarioTotal is reused by the resume/-force skip logic below to
	// fast-forward the bar by the right amount when a shard is skipped.
	targetsByScenario := make([][]heatmap.Target, len(scenarios))
	perScenarioTotal := make([]int, len(scenarios))
	total := 0
	for i, s := range scenarios {
		perScenarioTotal[i] = k // baseline trials
		if baselineOnly {
			total += perScenarioTotal[i]
			continue
		}
		if s.TwinOf == "" {
			targets, err := heatmap.Keys(s.YAML)
			if err != nil {
				log.Fatalf("%s keys: %v", s.Name, err)
			}
			targetsByScenario[i] = targets
		}
		perScenarioTotal[i] += len(targetsByScenario[i]) * k
		total += perScenarioTotal[i]
	}

	bar := progressbar.NewOptions(total,
		progressbar.OptionSetDescription("running trials"),
		progressbar.OptionShowCount(),
		progressbar.OptionShowIts(),
		progressbar.OptionSetItsString("item"),
		progressbar.OptionSetElapsedTime(true),
		progressbar.OptionShowElapsedTimeOnFinish(),
		progressbar.OptionThrottle(200*time.Millisecond),
		// ETA is derived by the library from elapsed time / completed count,
		// so it settles in as soon as the first few trials land — no need
		// to compute it ourselves.
	)

	for i, s := range scenarios {
		start := time.Now()

		// Resume support: if this scenario's shard was already written by a
		// prior (crashed) run, skip it instead of redoing potentially tens of
		// minutes of work. -force disables this and always redoes everything.
		shardPath := filepath.Join(shardDir, s.Name+".jsonl")
		if !baselineOnly && !force {
			if _, statErr := os.Stat(shardPath); statErr == nil {
				fmt.Printf("%s [%s]: shard already exists, skipping (use -force to redo)\n", s.Name, s.Group)
				_ = bar.Add(perScenarioTotal[i])
				continue
			}
		}

		in := providers.ChatInput{
			Model:   model,
			Format:  schema,
			Options: providers.ChatOptions{Temperature: temp, NumCtx: numCtx, NumPredict: numPredict},
		}

		var recs []heatmap.Record

		bar.Describe(fmt.Sprintf("%s [baseline]", s.Name))

		// baseline: the full bundle, k trials, run concurrently.
		in.Prompt = prompt + "\n\nManifests:\n" + s.YAML
		baseRecs := runParallel(parallel, k, bar, func(seed int) heatmap.Record {
			r := trial(ctx, client, in, seed, s, digest)
			r.Variant = "baseline"
			r.Valid = true
			return r
		})

		baseCorrect := 0
		answers := map[string]int{}
		for _, r := range baseRecs {
			if r.Answer != nil && *r.Answer == s.FaultClass {
				baseCorrect++
			}
			if r.Answer != nil {
				answers[*r.Answer]++
			} else {
				answers["<unparseable>"]++
			}
		}
		recs = append(recs, baseRecs...)

		if baselineOnly {
			fmt.Printf("%s [%s]: baseline %d/%d correct, answers %v (baseline-only, no shard written)\n",
				s.Name, s.Group, baseCorrect, k, answers)
			continue
		}

		// A twin has no fault, hence no saliency to measure: baseline only. Its
		// k trials are the paired false-positive control for its faulty scenario.
		// targets was already computed in the precompute pass above (empty for
		// twins), so we just reuse it here instead of calling heatmap.Keys again.
		targets := targetsByScenario[i]

		fmt.Printf("%s [%s]: baseline %d/%d correct, ablating %d fields × k=%d (parallel=%d)…\n",
			s.Name, s.Group, baseCorrect, k, len(targets), k, parallel)

		invalid := 0
		for _, t := range targets {
			reduced, err := heatmap.Remove(s.YAML, t)
			if err != nil {
				log.Fatalf("%s remove %s: %v", s.Name, t.Pointer, err)
			}

			valid, _, err := heatmap.Valid(reduced)
			if err != nil {
				log.Fatalf("%s validate %s: %v", s.Name, t.Pointer, err)
			}
			if !valid {
				invalid++
			}

			in.Prompt = prompt + "\n\nManifests:\n" + reduced
			doc, field := t.Doc, t.Pointer

			bar.Describe(fmt.Sprintf("%s [%s]", s.Name, field))

			targetRecs := runParallel(parallel, k, bar, func(seed int) heatmap.Record {
				r := trial(ctx, client, in, seed, s, digest)
				r.Variant = "reduced"
				r.Doc = &doc
				r.Kind = t.Kind
				r.Field = &field
				r.Category = string(t.Category)
				r.Valid = valid
				return r
			})
			recs = append(recs, targetRecs...)
		}
		fmt.Printf("  %d/%d variants invalid (recorded, flagged in shard)\n", invalid, len(targets))

		path := shardPath
		f, err := os.Create(path)
		if err != nil {
			log.Fatalf("create %s: %v", path, err)
		}
		enc := json.NewEncoder(f)
		for _, r := range recs {
			if err := enc.Encode(r); err != nil {
				log.Fatalf("write %s: %v", path, err)
			}
		}
		if err := f.Close(); err != nil {
			log.Fatalf("close %s: %v", path, err)
		}

		d := time.Since(start)
		dur := fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
		fmt.Printf("wrote %s (%d trials) in %s\n", path, len(recs), dur)
	}

	_ = bar.Finish()
}

// runParallel fans a[0..k) seeds out to `workers` goroutines over a jobs
// channel and fans the resulting heatmap.Records back in over a results
// channel. The only goroutine that ever appends to the returned slice is this
// one (draining results), so no mutex is needed — the channel itself is the
// synchronization. Every seed 0..k-1 is represented exactly once in the
// output; the order is not guaranteed to match seed order.
//
// bar.Add(1) is called exactly once per drained result, from this same
// draining goroutine — progressbar/v3 is internally safe for concurrent Add
// calls anyway, but we don't even need that guarantee here since only one
// goroutine ever touches it in this function.
func runParallel(workers, k int, bar *progressbar.ProgressBar, fn func(seed int) heatmap.Record) []heatmap.Record {
	if workers < 1 {
		workers = 1
	}
	if workers > k {
		workers = k
	}

	jobs := make(chan int, k)
	results := make(chan heatmap.Record, k)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seed := range jobs {
				results <- fn(seed)
			}
		}()
	}

	for i := 0; i < k; i++ {
		jobs <- i
	}
	close(jobs)

	go func() {
		wg.Wait()
		close(results)
	}()

	recs := make([]heatmap.Record, 0, k)
	for r := range results {
		recs = append(recs, r)
		_ = bar.Add(1)
	}
	return recs
}

// modelDir renders a model name as a directory component (":" and "/" are not
// filesystem-safe).
func modelDir(model string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(model)
}

// --- retry / backoff for transient network failures ---
//
// A multi-hour run across thousands of trials will, with near certainty, hit
// at least one transient network error: a stale pooled connection the server
// already closed (bare EOF), a brief connection reset, a one-off timeout —
// or, as observed in practice, the whole host briefly dropping off the LAN
// (dial fails with "host is down"/"connect: connection refused" instead of a
// mid-request EOF). Before this fix, ANY error from client.Chat — transient
// or permanent — went straight to log.Fatalf and killed the entire process.
//
// The two transient cases need very different retry budgets. A stale pooled
// connection is a client-side bookkeeping problem: the fix (dial a fresh
// connection) takes milliseconds, so a handful of quick retries is enough.
// A host that's actually gone from the network — thermal shutdown, driver
// reset, brief reboot — can plausibly take 10-60s to come back; retrying
// that with the same short budget as a stale connection just burns through
// all attempts in ~5s and gives up while the host is still down. isDialError
// distinguishes the two so each gets an appropriately sized budget.

const (
	// connMaxAttempts/connRetryMaxWait: in-flight connection problems
	// (EOF, ECONNRESET, EPIPE, timeouts) — fast to recover from.
	connMaxAttempts   = 6
	connRetryBaseWait = 500 * time.Millisecond
	connRetryMaxWait  = 30 * time.Second

	// dialMaxAttempts/dialRetryMaxWait: the TCP dial itself failed (host
	// unreachable/down/refused) — give the host real time to come back.
	dialMaxAttempts   = 14
	dialRetryBaseWait = 1 * time.Second
	dialRetryMaxWait  = 20 * time.Second
	// Total worst-case wall clock across all dial attempts at these settings
	// is on the order of a few minutes, which is the point: we'd rather a
	// single trial block for a few minutes than kill a multi-hour run over
	// what turned out to be a 20-second network blip.
)

// isDialError reports whether err is a failure to establish the TCP
// connection at all (net.OpError with Op == "dial"), as opposed to a failure
// that happened after a connection existed (EOF, reset, timeout mid-request).
// This is the "host is down" / "connection refused" / "no route to host"
// case — categorically different from a stale pooled connection.
func isDialError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op == "dial"
	}
	return false
}

// isRetryable reports whether err looks like a transient network failure
// worth retrying at all, as opposed to a permanent/logical failure (bad
// request, malformed response) that will just fail the same way again.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if isDialError(err) {
		return true
	}
	// The failure that originally killed the networking run: server
	// (uvicorn — vLLM's default keep-alive is 5s and isn't configurable via
	// any `vllm serve` flag) closed a pooled connection out from under us;
	// Go's transport only discovers this as a bare EOF when it tries to
	// reuse it.
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return false
}

// retryBudget picks the (maxAttempts, baseWait, maxWait) budget appropriate
// to the failure just observed: dial errors get the long budget, everything
// else gets the short one.
func retryBudget(err error) (maxAttempts int, baseWait, maxWait time.Duration) {
	if isDialError(err) {
		return dialMaxAttempts, dialRetryBaseWait, dialRetryMaxWait
	}
	return connMaxAttempts, connRetryBaseWait, connRetryMaxWait
}

// retryDelay returns an exponential backoff with full jitter, capped at
// maxWait, so many goroutines that all just hit the same transient failure
// (e.g. the whole host dropping off the LAN) don't all retry in lockstep and
// hammer it the instant it comes back.
func retryDelay(attempt int, baseWait, maxWait time.Duration) time.Duration {
	backoff := baseWait * time.Duration(1<<uint(attempt))
	if backoff > maxWait {
		backoff = maxWait
	}
	return time.Duration(rand.Int63n(int64(backoff)))
}

// trial runs one model call at the given seed and returns the raw record.
// Transient network errors are retried with exponential backoff (see
// isRetryable/retryDelay above); anything else — or exhausting maxAttempts —
// still fails the whole run via log.Fatalf, same as before this change.
// Answer is nil when the response does not parse (a stopgap recorded for
// item #8).
// in is received by value, so mutating in.Options.Seed here only touches this
// call's own copy — safe to call concurrently from multiple goroutines sharing
// the same outer `in`.
func trial(ctx context.Context, client ChatClient, in providers.ChatInput, seed int, s dataset.Scenario, digest string) heatmap.Record {
	in.Options.Seed = int64(seed)

	var res providers.ChatOutput
	var err error
	for attempt := 0; ; attempt++ {
		res, err = client.Chat(ctx, in)
		if err == nil {
			break
		}
		if !isRetryable(err) {
			log.Fatalf("%s chat (seed %d): %v", s.Name, seed, err)
		}

		maxAttempts, baseWait, maxWait := retryBudget(err)
		if attempt >= maxAttempts-1 {
			log.Fatalf("%s chat (seed %d): giving up after %d attempts: %v", s.Name, seed, attempt+1, err)
		}

		wait := retryDelay(attempt, baseWait, maxWait)
		kind := "conn"
		if isDialError(err) {
			kind = "dial"
		}
		log.Printf("warning: %s chat (seed %d) attempt %d/%d [%s] failed (%v), retrying in %s",
			s.Name, seed, attempt+1, maxAttempts, kind, err, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			log.Fatalf("%s chat (seed %d): context done while waiting to retry: %v", s.Name, seed, ctx.Err())
		}
	}

	r := heatmap.Record{
		Scenario:    s.Name,
		Group:       s.Group,
		FaultClass:  s.FaultClass,
		Seed:        int64(seed),
		K:           k,
		Model:       model,
		ModelDigest: digest,
		Temp:        temp,
		NumCtx:      numCtx,
		Raw:         res.Response,
	}

	var d diagnosis
	if err := json.Unmarshal([]byte(res.Response), &d); err == nil {
		answer := d.FaultClass
		r.Answer = &answer
	}

	return r
}
