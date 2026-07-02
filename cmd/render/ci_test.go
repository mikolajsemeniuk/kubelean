package main

import (
	"math"
	"testing"
)

const tol = 0.01

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.4f, want %.4f (±%.2f)", name, got, want, tol)
	}
}

func TestWilson(t *testing.T) {
	cases := []struct {
		x, n           int
		wantLo, wantHi float64
	}{
		{10, 10, 0.7225, 1.0000}, // perfect: pinned at 1 on top, floor 0.72
		{0, 10, 0.0000, 0.2775},  // zero: pinned at 0, ceiling 0.28
		{8, 10, 0.4902, 0.9433},
		{6, 10, 0.3133, 0.8318},
	}
	for _, c := range cases {
		lo, hi := wilson(c.x, c.n)
		approx(t, "wilson lo", lo, c.wantLo)
		approx(t, "wilson hi", hi, c.wantHi)
	}
}

func TestNewcombe(t *testing.T) {
	// 0.20 saliency at k=10: CI straddles 0 — indistinguishable from noise.
	diff, lo, hi := newcombe(10, 10, 8, 10)
	approx(t, "diff", diff, 0.20)
	approx(t, "lo", lo, -0.1123)
	approx(t, "hi", hi, 0.5098)
	if lo > 0 {
		t.Errorf("saliency 0.20 must NOT register as signal (lo=%.4f > 0)", lo)
	}

	// 1.00 saliency (deciding field removed): CI clears 0 by a mile — real signal.
	_, lo, _ = newcombe(10, 10, 0, 10)
	approx(t, "lo", lo, 0.6076)
	if lo <= 0 {
		t.Errorf("saliency 1.00 must register as signal (lo=%.4f <= 0)", lo)
	}
}

func TestMcNemar(t *testing.T) {
	cases := []struct {
		b, c int
		want float64
	}{
		{0, 0, 1.0},       // no discordant seeds — no evidence at all
		{3, 3, 1.0},       // perfectly split — capped at 1
		{5, 0, 0.0625},    // 2·(1/2)^5: five one-sided flips, not yet 0.05
		{8, 0, 0.0078},    // 2·(1/2)^8: significant at k=10 when 8 seeds flip
		{10, 0, 0.001953}, // full flip at k=10
		{9, 1, 0.021484},  // 2·(P(0)+P(1)) over n=10
	}
	for _, c := range cases {
		approx(t, "mcnemar", mcnemar(c.b, c.c), c.want)
		approx(t, "mcnemar sym", mcnemar(c.c, c.b), c.want) // symmetric in b,c
	}
}

func TestBHAdjust(t *testing.T) {
	// Classic BH: the three small p's share the q of the largest of them
	// (step-up monotonicity); the big one stays big.
	qs := bhAdjust([]float64{0.01, 0.02, 0.03, 0.5})
	want := []float64{0.04, 0.04, 0.04, 0.5}
	for i := range want {
		approx(t, "bh q", qs[i], want[i])
	}

	if got := bhAdjust(nil); len(got) != 0 {
		t.Errorf("bhAdjust(nil) = %v, want empty", got)
	}

	// A single test is left untouched.
	qs = bhAdjust([]float64{0.04})
	approx(t, "bh single", qs[0], 0.04)
}
