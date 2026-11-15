package vecmath

import (
	"math"
	"math/rand/v2"
	"testing"
)

func naiveDot(a, b []float32) float64 {
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

func naiveL2Sq(a, b []float32) float64 {
	var s float64
	for i := range a {
		d := float64(a[i]) - float64(b[i])
		s += d * d
	}
	return s
}

func randVec(r *rand.Rand, n int) []float32 {
	v := make([]float32, n)
	for i := range v {
		v[i] = r.Float32()*2 - 1
	}
	return v
}

func approxEqual(a float32, b float64) bool {
	return math.Abs(float64(a)-b) <= 1e-4*math.Max(1, math.Abs(b))
}

func TestDotAndL2MatchNaive(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	// Lengths cover the unrolled body, the tail, and both together.
	for _, n := range []int{0, 1, 3, 4, 5, 7, 8, 384, 1536, 1537} {
		a, b := randVec(r, n), randVec(r, n)
		if got, want := Dot(a, b), naiveDot(a, b); !approxEqual(got, want) {
			t.Errorf("Dot n=%d: got %v want %v", n, got, want)
		}
		if got, want := L2Sq(a, b), naiveL2Sq(a, b); !approxEqual(got, want) {
			t.Errorf("L2Sq n=%d: got %v want %v", n, got, want)
		}
	}
}

func TestNormalize(t *testing.T) {
	v := []float32{3, 4}
	if !Normalize(v) {
		t.Fatal("Normalize returned false")
	}
	if !approxEqual(v[0], 0.6) || !approxEqual(v[1], 0.8) || !approxEqual(Norm(v), 1) {
		t.Fatalf("got %v", v)
	}

	zero := []float32{0, 0, 0}
	if Normalize(zero) {
		t.Fatal("zero vector must not normalize")
	}
}

func TestIsFinite(t *testing.T) {
	if !IsFinite([]float32{1, -2, 0}) {
		t.Error("finite vector reported non-finite")
	}
	if IsFinite([]float32{1, float32(math.NaN())}) || IsFinite([]float32{float32(math.Inf(1))}) {
		t.Error("non-finite vector reported finite")
	}
}

func BenchmarkDot1536(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))
	x, y := randVec(r, 1536), randVec(r, 1536)
	b.SetBytes(1536 * 4 * 2)
	for b.Loop() {
		Dot(x, y)
	}
}
