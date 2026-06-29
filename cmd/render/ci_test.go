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
