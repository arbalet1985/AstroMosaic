package main

import (
	"math"
	"math/rand"
	"testing"
)

func TestFourPixelGridSuppressionPreservesStarsAndFlux(t *testing.T) {
	const w, h = 256, 256
	rng := rand.New(rand.NewSource(484))
	src, clean := make([]float32, w*h), make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			star := .4 * math.Exp(-(math.Pow(float64(x)-150.3, 2)+math.Pow(float64(y)-110.2, 2))/5.12)
			clean[i] = float32(.1 + .02*float64(x)/w + star + .0002*rng.NormFloat64())
			grid := .001*math.Sin(math.Pi*float64(x)/2) + .0012*math.Cos(math.Pi*float64(y)/2) + .0007*math.Sin(math.Pi*float64(x+y)/2)
			src[i] = clean[i] + float32(grid)
		}
	}
	before := append([]float32(nil), src...)
	out, active := suppressFourPixelGrid(src, w, h)
	if active == 0 {
		t.Fatal("periodic grid was not detected")
	}
	var e0, e1, sum0, sum1 float64
	for i, v := range src {
		if v != before[i] {
			t.Fatal("source pixels mutated")
		}
		e0 += math.Pow(float64(v-clean[i]), 2)
		e1 += math.Pow(float64(out[i]-clean[i]), 2)
		sum0 += float64(v)
		sum1 += float64(out[i])
	}
	if e1 >= .2*e0 {
		t.Fatalf("grid not reduced enough: MSE ratio %.4f", e1/e0)
	}
	if math.Abs(sum1-sum0)/sum0 > 1e-4 {
		t.Fatal("grid correction changed mean flux")
	}
	peak := 110*w + 150
	if math.Abs(float64(out[peak]-clean[peak])) > .003 {
		t.Fatal("star peak altered")
	}
	t.Logf("MSE ratio %.4f; mean change %.7f; active windows %d", e1/e0, (sum1-sum0)/sum0, active)
}

func TestFourPixelGridSuppressionLeavesUnstructuredNoise(t *testing.T) {
	const w, h = 192, 160
	rng := rand.New(rand.NewSource(9871))
	src := make([]float32, w*h)
	for i := range src {
		src[i] = float32(.1 + .002*rng.NormFloat64())
	}
	out, _ := suppressFourPixelGrid(src, w, h)
	var diff, power float64
	for i, v := range src {
		diff += math.Pow(float64(out[i]-v), 2)
		power += math.Pow(float64(v)-.1, 2)
	}
	if diff > power*.003 {
		t.Fatal("correction removed unstructured noise")
	}
}
