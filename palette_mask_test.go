package main

import (
	"image"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestPaletteMaskRejectsNoiseKeepsEmission(t *testing.T) {
	const w, h = 192, 160
	rng := rand.New(rand.NewSource(517))
	signal := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			emission := .04 * math.Exp(-math.Pow(float64(x)-96, 2)/512-math.Pow(float64(y)-80, 2)/512)
			signal[y*w+x] = float32(.02 + emission + .003*rng.NormFloat64())
		}
	}
	mask := paletteSignalMask(signal, w, h)
	var background, core float64
	var nb, nc int
	for y := 12; y < h-12; y++ {
		for x := 12; x < w-12; x++ {
			v := float64(mask[y*w+x])
			if v < 0 || v > 1 || !paletteFinite(v) {
				t.Fatal("invalid mask")
			}
			if x < 35 || x > w-35 {
				background += v
				nb++
			}
			if math.Hypot(float64(x-96), float64(y-80)) < 12 {
				core += v
				nc++
			}
		}
	}
	if background/float64(nb) > .02 || core/float64(nc) < .98 {
		t.Fatalf("background %.4f, emission %.4f", background/float64(nb), core/float64(nc))
	}
}

func TestPaletteOxygenGainAndSOActOnlyInMask(t *testing.T) {
	a, b := newImg(24, 20, 3), newImg(24, 20, 3)
	for i := range a.Pix {
		a.Pix[i] = .1
		b.Pix[i] = .3
	}
	p := &palettePrepared{HO: a, SO: b, Region: image.Rect(0, 0, 24, 20), OxygenMask: make([]float32, 480), SulfurMask: make([]float32, 480)}
	for i := range p.O {
		p.O[i] = paletteCalibration{Gain: 1, Noise: .01, Usable: true}
	}
	for i := 0; i < 480; i++ {
		if i%24 >= 12 {
			p.OxygenMask[i] = 1
			p.SulfurMask[i] = 1
		}
	}
	s := paletteParams{Hydrogen: 1, Oxygen: 3, Sulfur: 2, Hue: 1, SourceSO: 1, ProtectOxygen: true, ProtectSulfur: true}
	m := p.compose(s, 0)
	for i := 0; i < 480; i++ {
		wantB, wantR := .1, .1
		if i%24 >= 12 {
			wantB, wantR = .9, .7
		}
		if math.Abs(float64(m.Pix[960+i])-wantB) > 1e-6 || math.Abs(float64(m.Pix[i])-wantR) > 1e-6 {
			t.Fatalf("incorrect masked mixing at %d: R=%g B=%g", i, m.Pix[i], m.Pix[960+i])
		}
	}
	// Gain zero still preserves baseline HO outside the mask.
	s.Oxygen = 0
	m = p.compose(s, 0)
	if math.Abs(float64(m.Pix[960])-.1) > 1e-6 || m.Pix[960+12] != 0 {
		t.Fatal("masked oxygen gain changed the background")
	}
	s.ProtectOxygen = false
	s.Oxygen = 3
	m = p.compose(s, 0)
	if math.Abs(float64(m.Pix[960])-.9) > 1e-6 {
		t.Fatal("mask off did not restore global mixing")
	}
}

func TestPaletteMaskedPreviewMatchesFullResolutionAverage(t *testing.T) {
	a, b := paletteTestInputs()
	p, err := preparePalette(a, b, false, true)
	if err != nil {
		t.Fatal(err)
	}
	s := paletteParams{Hydrogen: 1.2, Oxygen: 2.5, Sulfur: .7, Hue: .4, Auto: true, ProtectSulfur: true, ProtectOxygen: true}
	full := p.compose(s, 0)
	smallP := p.thumbnail(32)
	small := smallP.compose(s, 0)
	stride := (max(full.W, full.H) + 31) / 32
	for y := 0; y < small.H; y++ {
		for x := 0; x < small.W; x++ {
			for c := 0; c < 3; c++ {
				var total, count float64
				for sy := y * stride; sy < min((y+1)*stride, full.H); sy++ {
					for sx := x * stride; sx < min((x+1)*stride, full.W); sx++ {
						total += float64(full.at(c, sx, sy))
						count++
					}
				}
				if math.Abs(float64(small.at(c, x, y))-total/count) > 3e-8 {
					t.Fatalf("masked preview differs from full result at %d,%d,%d", x, y, c)
				}
			}
		}
	}
}

func TestPaletteRealMaskedHOAndSO(t *testing.T) {
	if os.Getenv("ASTROMOSAIC_PALETTE_REAL") == "" {
		t.Skip("IC 443 optional real-data check")
	}
	dir := `C:\Users\Admin\Desktop\ASTRO\asiair\IC 443`
	a, err := loadImage(filepath.Join(dir, "HO.fit"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadImage(filepath.Join(dir, "SO.fit"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := preparePalette(a, b, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for j, mask := range [][]float32{p.SulfurMask, p.OxygenMask, p.ForaxxMask} {
		var total float64
		var zero, one int
		for _, v := range mask {
			if !paletteFinite(float64(v)) || v < 0 || v > 1 {
				t.Fatal("invalid real mask")
			}
			total += float64(v)
			if v == 0 {
				zero++
			}
			if v == 1 {
				one++
			}
		}
		t.Logf("mask %d mean %.3f; background %.1f%%; full signal %.1f%%", j, total/float64(len(mask)), 100*float64(zero)/float64(len(mask)), 100*float64(one)/float64(len(mask)))
		if zero == 0 || one == 0 {
			t.Fatal("mask did not separate background and signal")
		}
	}
	s := paletteParams{Hydrogen: 1, Oxygen: 1, Sulfur: .5, Hue: 1, Auto: true, ProtectSulfur: true, ProtectOxygen: true}
	m := p.compose(s, 0)
	for _, v := range m.Pix {
		if !paletteFinite(float64(v)) {
			t.Fatal("non-finite masked result")
		}
	}
	if dest := os.Getenv("ASTROMOSAIC_MASK_EXPORT"); dest != "" {
		if err := os.MkdirAll(dest, 0700); err != nil {
			t.Fatal(err)
		}
		if err := writeFITS(filepath.Join(dest, "mask_sulfur.fits"), p.maskImage(false)); err != nil {
			t.Fatal(err)
		}
		if err := writeFITS(filepath.Join(dest, "mask_oxygen.fits"), p.maskImage(true)); err != nil {
			t.Fatal(err)
		}
		if err := writeFITS(filepath.Join(dest, "masked.fits"), m); err != nil {
			t.Fatal(err)
		}
		s.ProtectSulfur = false
		s.ProtectOxygen = false
		if err := writeFITS(filepath.Join(dest, "unmasked.fits"), p.compose(s, 0)); err != nil {
			t.Fatal(err)
		}
	}
}
