package main

import (
	"math"
	"math/rand"
	"testing"
)

func TestFitLine(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	var x, y []float64
	for i := 0; i < 5000; i++ {
		v := rng.Float64() * 0.5
		w := 0.02 + 1.3*v + rng.NormFloat64()*0.002
		if i%10 == 0 {
			w += 0.3 * rng.Float64() // 10% outliers (stars on one side only)
		}
		x, y = append(x, v), append(y, w)
	}
	a, b := fitLine(x, y)
	if math.Abs(a-0.02) > 0.001 || math.Abs(b-1.3) > 0.005 {
		t.Fatalf("a=%.4f b=%.4f, want 0.02 1.3", a, b)
	}
}

func TestLinearFit(t *testing.T) {
	// B sees the same sky with gain 0.8 and offset +0.03; after LinearFit it must match A
	a := mkTile(tileW, tileH, identity, 0, field, 1, noBg)
	b := mkTile(tileW, tileH, bShift, 0, field, 0.8, func(x, y float64) float32 { return 0.03 })
	a.Name, b.Name, b.Col = "A", "B", 1
	tiles := []*Tile{a, b}
	W := []Warp{newWarp(identity, 0, a.Img), newWarp(bShift, 0, b.Img)}
	fit, notes := linearFit(tiles, W)
	t.Log(notes)
	// both must end in the reference tile's brightness (the unchanged one)
	gain, off := float32(1), float32(0)
	if fit[1] == b.Img {
		gain, off = 0.8, 0.03
	}
	var e float64
	for _, i := range []int{0, 1} {
		ref := mkTile(tileW, tileH, W[i].S, 0, field, gain, func(x, y float64) float32 { return off }).Img
		for k := range ref.Pix {
			e = math.Max(e, math.Abs(float64(fit[i].Pix[k]-ref.Pix[k])))
		}
	}
	if e > 0.002 {
		t.Fatalf("after LinearFit max error %.4f (%v)", e, notes)
	}
}
