package main

import (
	"image"
	"math"
	"testing"
)

func TestForaxxFinalMaskPreservesBaselineAndBlendsAllChannels(t *testing.T) {
	ho, so := newImg(24, 20, 3), newImg(24, 20, 3)
	n := ho.W * ho.H
	for c := 0; c < 3; c++ {
		for i := 0; i < n; i++ {
			ho.Pix[c*n+i] = float32(.06 + .02*float64(c) + .0001*float64(i))
			so.Pix[c*n+i] = float32(.13 + .03*float64(c) + .00015*float64(i))
		}
	}
	p := &palettePrepared{HO: ho, SO: so, Region: image.Rect(0, 0, 24, 20), HBackground: .01, SBackground: .02, ForaxxMask: make([]float32, n), DynamicTone: [2]paletteFactorTone{{0, .2}, {0, .2}}}
	for i := range p.O {
		p.O[i] = paletteCalibration{Offset: -.01, Gain: 1, Noise: .001, Usable: true}
	}
	for i := range p.ForaxxMask {
		p.ForaxxMask[i] = []float32{0, .25, 1}[i%3]
	}
	for _, mode := range []int{8, 9} {
		s := paletteParams{Mode: mode, Hydrogen: 1.3, Oxygen: 2.5, Sulfur: .7, SourceSO: .8, BlueHO: .3, BlueSO: .7, OxygenHue: .9}
		old := p.compose(s, 0)
		s.ProtectForaxx = true
		got := p.compose(s, 0)
		base := p.compose(paletteParams{Mode: 1, Hydrogen: s.Hydrogen, Oxygen: 1, BlueHO: s.BlueHO}, 0)
		for c := 0; c < 3; c++ {
			for i, m := range p.ForaxxMask {
				idx := c*n + i
				want := float64(base.Pix[idx]) + float64(m)*(float64(old.Pix[idx])-float64(base.Pix[idx]))
				if math.Abs(float64(got.Pix[idx])-want) > 5e-8 {
					t.Fatalf("incorrect outer blend mode %d channel %d mask %.2f: %g != %g", mode, c, m, got.Pix[idx], want)
				}
				if m == 0 && got.Pix[idx] != base.Pix[idx] {
					t.Fatal("Foraxx modified protected HOO")
				}
				if m == 1 && got.Pix[idx] != old.Pix[idx] {
					t.Fatal("Foraxx changed inside fully open mask")
				}
			}
		}
	}
	for mode := 0; mode < 8; mode++ {
		s := paletteParams{Mode: mode, Hydrogen: 1, Oxygen: 2, Sulfur: .5, SourceSO: .5}
		a := p.compose(s, 0)
		s.ProtectForaxx = true
		b := p.compose(s, 0)
		for i, v := range a.Pix {
			if b.Pix[i] != v {
				t.Fatal("final Foraxx mask altered a different palette", mode)
			}
		}
	}
}

func TestForaxxFinalMaskFeathersAndStaysWithinRange(t *testing.T) {
	a, b := paletteTestInputs()
	// Add a compact, clearly detected nebula. The broad sinusoidal field in
	// paletteTestInputs alone can correctly fail the emission threshold.
	for y := 0; y < a.H; y++ {
		for x := 0; x < a.W; x++ {
			e := .2 * math.Exp(-(math.Pow(float64(x)-96, 2)+math.Pow(float64(y)-80, 2))/288)
			for c, gain := range []float64{1, 1, .5} {
				a.Pix[c*a.W*a.H+y*a.W+x] += float32(e * gain)
			}
			for c, gain := range []float64{1, .8, .4} {
				b.Pix[c*b.W*b.H+y*b.W+x] += float32(e * gain)
			}
		}
	}
	p, err := preparePalette(a, b, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ForaxxMask) != p.Region.Dx()*p.Region.Dy() {
		t.Fatal("missing full-resolution mask")
	}
	var soft bool
	for _, v := range p.ForaxxMask {
		if !paletteFinite(float64(v)) || v < 0 || v > 1 {
			t.Fatal("invalid final mask")
		}
		soft = soft || (v > 0 && v < 1)
	}
	if !soft {
		t.Fatal("mask has no smooth transitions")
	}
	im := p.foraxxMaskImage()
	if im.W != p.Thumbnail.Region.Dx() || im.H != p.Thumbnail.Region.Dy() || im.C != 1 {
		t.Fatal("incorrect mask preview")
	}
}
