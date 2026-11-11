package main

import (
	"image"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func paletteTestInputs() (*Img, *Img) {
	a, b := newImg(192, 160, 3), newImg(192, 160, 3)
	rng := rand.New(rand.NewSource(73))
	n := a.W * a.H
	for y := 0; y < a.H; y++ {
		for x := 0; x < a.W; x++ {
			i := y*a.W + x
			o := .04 + .025*math.Sin(float64(x)/19)*math.Cos(float64(y)/17)
			a.Pix[i] = float32(.07 + .02*math.Cos(float64(x)/21))
			b.Pix[i] = float32(.05 + .015*math.Sin(float64(y)/13))
			a.Pix[n+i] = float32(.01 + o + .0005*rng.NormFloat64())
			a.Pix[2*n+i] = float32(.02 + .5*o + .002*rng.NormFloat64())
			b.Pix[n+i] = float32(.025 + .8*o + .0008*rng.NormFloat64())
			b.Pix[2*n+i] = float32(.02 + .4*o + .003*rng.NormFloat64())
		}
	}
	return a, b
}

func TestPaletteNoisyBlueGetsLessWeightAfterScaling(t *testing.T) {
	a, b := paletteTestInputs()
	p, err := preparePalette(a, b, false, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range p.O {
		if !c.Usable {
			t.Fatalf("channel %d wrongly rejected", i)
		}
	}
	if math.Abs(p.O[1].Gain-2) > .12 || math.Abs(p.O[2].Gain-1.25) > .1 || math.Abs(p.O[3].Gain-2.5) > .2 {
		t.Fatalf("wrong normalization %+v", p.O)
	}
	w := p.weights(paletteParams{Auto: true})
	if w[1] >= .04 || w[3] >= .02 || w[0] <= w[2] {
		t.Fatalf("noisy blue over-weighted: %v", w)
	}
	// Compare recovered OIII against the known clean signal on the HO G scale.
	m := p.compose(paletteParams{Oxygen: 1, Auto: true}, 0)
	var adaptive, equal float64
	n := a.W * a.H
	for y := 0; y < a.H; y++ {
		for x := 0; x < a.W; x++ {
			i := y*a.W + x
			truth := .05 + .025*math.Sin(float64(x)/19)*math.Cos(float64(y)/17) + p.O[0].Offset
			adaptive += math.Pow(float64(m.Pix[2*n+i])-truth, 2)
			var average float64
			for j, src := range [4]*Img{a, a, b, b} {
				average += (p.O[j].Offset + p.O[j].Gain*float64(src.at(1+j%2, x, y))) / 4
			}
			equal += math.Pow(average-truth, 2)
		}
	}
	if adaptive >= equal*.25 {
		t.Fatalf("weights did not reduce oxygen noise: weighted %g equal %g", adaptive, equal)
	}
	t.Logf("OIII weights HO G/B %.4f/%.4f SO G/B %.4f/%.4f; MSE ratio %.4f", w[0], w[1], w[2], w[3], adaptive/equal)
}

func TestPaletteSulfurYellowAndHOO(t *testing.T) {
	a, b := newImg(24, 20, 3), newImg(24, 20, 3)
	for i := range a.Pix {
		a.Pix[i] = .2
		b.Pix[i] = .1
	}
	p := &palettePrepared{HO: a, SO: b, Region: image.Rect(0, 0, 24, 20)}
	p.O[0] = paletteCalibration{Gain: 1, Noise: .01, Usable: true}
	s := paletteParams{Hydrogen: 1, Oxygen: 1, Sulfur: 0, Hue: 1, Auto: true}
	base := p.compose(s, 0)
	s.Sulfur = .7
	yellow := p.compose(s, 0)
	n := a.W * a.H
	for i := 0; i < n; i++ {
		if base.Pix[n+i] != base.Pix[2*n+i] {
			t.Fatal("S=0 is not HOO")
		}
		dr, dg, db := yellow.Pix[i]-base.Pix[i], yellow.Pix[n+i]-base.Pix[n+i], yellow.Pix[2*n+i]-base.Pix[2*n+i]
		if math.Abs(float64(dr-dg)) > 1e-7 || dr <= 0 || db != 0 {
			t.Fatal("sulfur is not yellow", dr, dg, db)
		}
	}
	s.Hue = 0
	orange := p.compose(s, 0)
	if orange.Pix[n] >= yellow.Pix[n] || orange.Pix[0] != yellow.Pix[0] {
		t.Fatal("sulfur hue did not turn orange")
	}
	// No clipping in linear float results.
	s.Hydrogen = 10
	if p.compose(s, 0).Pix[0] <= 1 {
		t.Fatal("linear values were clipped")
	}
}

func TestPaletteDifferentSizesAndBlankBorders(t *testing.T) {
	a, b := paletteTestInputs()
	b = cropPixels(b, image.Rect(16, 12, 176, 148))
	p, err := preparePalette(a, b, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Region != image.Rect(16, 12, 176, 148) || !strings.Contains(p.Note, "Обрезано") {
		t.Fatal("incorrect centered crop", p.Region, p.Note)
	}
	m := p.compose(paletteParams{Hydrogen: 1, Auto: true}, 0)
	if m.W != b.W || m.H != b.H {
		t.Fatal("wrong output dimensions")
	}
	p, err = preparePalette(a, b, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Region != image.Rect(0, 0, 160, 136) {
		t.Fatal("incorrect top-left crop", p.Region)
	}
	for y := 0; y < b.H; y++ {
		for c := 0; c < 3; c++ {
			b.Pix[c*b.W*b.H+y*b.W] = 0
		}
	}
	r := paletteCommonRegion(a, b)
	if r != image.Rect(1, 0, 160, 136) {
		t.Fatal("black registration border retained", r)
	}
}

func TestPaletteCropWCSAndManualWeights(t *testing.T) {
	a, b := paletteTestInputs()
	a.BottomUp = true
	a.Hdr["CRPIX1"] = "100"
	a.Hdr["CRPIX2"] = "90"
	a.Hdr["BAYERPAT"] = "RGGB"
	p := &palettePrepared{HO: a, SO: b, Region: image.Rect(10, 20, 180, 150)}
	for i := range p.O {
		p.O[i] = paletteCalibration{Gain: 1, Noise: .1, Usable: true}
	}
	s := paletteParams{Oxygen: 1, BlueHO: 0, BlueSO: 0, SourceSO: .25}
	w := p.weights(s)
	if w != [4]float64{.75, 0, .25, 0} {
		t.Fatal("manual weights", w)
	}
	m := p.compose(s, 0)
	if m.Hdr["CRPIX1"] != "90" || m.Hdr["CRPIX2"] != "80" || m.Hdr["BAYERPAT"] != "" || a.Hdr["CRPIX1"] != "100" {
		t.Fatal("incorrect crop metadata", m.Hdr)
	}
	p.O[3].Usable = false
	s.BlueSO = 1
	s.SourceSO = 1
	w = p.weights(s)
	if w[0] != 1 {
		t.Fatal("empty manual selection did not fall back to reference", w)
	}
}

func TestPaletteRejectsUnrelatedBlue(t *testing.T) {
	a, b := paletteTestInputs()
	rng := rand.New(rand.NewSource(4))
	n := a.W * a.H
	for i := 0; i < n; i++ {
		a.Pix[2*n+i] = float32(.05 + .01*rng.NormFloat64())
	}
	p, err := preparePalette(a, b, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.O[1].Usable || p.weights(paletteParams{Auto: true})[1] != 0 {
		t.Fatal("noise-only blue included")
	}
	if !strings.Contains(p.Note, "HO B") {
		t.Fatal("missing excluded-channel explanation")
	}
}

func TestPaletteAutoRegistration(t *testing.T) {
	a := newImg(430, 400, 3)
	b := newImg(410, 390, 3)
	rng := rand.New(rand.NewSource(29))
	for _, m := range []*Img{a, b} {
		for i := range m.Pix {
			m.Pix[i] = .02
		}
	}
	for i := 0; i < 100; i++ {
		x, y := 30+rng.Intn(340), 30+rng.Intn(320)
		for k, m := range []*Img{a, b} {
			for dy := -8; dy <= 8; dy++ {
				for dx := -8; dx <= 8; dx++ {
					px, py := x+dx-k*9, y+dy-k*6
					for c := 0; c < 3; c++ {
						m.Pix[c*m.W*m.H+py*m.W+px] += float32(.3 * math.Exp(-float64(dx*dx+dy*dy)/7))
					}
				}
			}
		}
	}
	mapping, _, err := paletteRegistration(a, b)
	if err != nil {
		t.Fatal(err)
	}
	x, y := mapping.apply(200, 200)
	if math.Abs(x-191) > .2 || math.Abs(y-194) > .2 {
		t.Fatal("registration direction/shift incorrect", mapping)
	}
	p, err := preparePalette(a, b, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Region.Dx() > b.W || p.Region.Dy() > b.H || p.Region.Min.X < 8 || p.Region.Min.Y < 5 {
		t.Fatal("auto footprint not cropped", p.Region)
	}
}

func TestPaletteRealIC443(t *testing.T) {
	// Explicit opt-in: the real 6014² RGB masters need substantial memory.
	if os.Getenv("ASTROMOSAIC_PALETTE_REAL") == "" {
		t.Skip("set ASTROMOSAIC_PALETTE_REAL=1 to test the IC 443 masters")
	}
	dir := `C:\Users\Admin\Desktop\ASTRO\asiair\IC 443`
	a, err := loadImage(filepath.Join(dir, "r_123_1_00001.fit"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadImage(filepath.Join(dir, "r_123_1_00002.fit"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := preparePalette(a, b, false, true)
	if err != nil {
		t.Fatal(err)
	}
	w := p.weights(paletteParams{Auto: true})
	t.Log(p.Note)
	for i, c := range p.O {
		t.Logf("OIII %d: %+v", i, c)
	}
	t.Logf("OIII weights: HO G/B %.4f/%.4f; SO G/B %.4f/%.4f", w[0], w[1], w[2], w[3])
	if !p.O[2].Usable {
		t.Fatal("SO oxygen did not contribute")
	}
	m := p.compose(paletteParams{Hydrogen: 1, Oxygen: 1, Sulfur: .5, Hue: 1, Auto: true}, 0)
	if m.W != p.Region.Dx() || m.H != p.Region.Dy() || m.C != 3 {
		t.Fatal("wrong full resolution output")
	}
	for _, v := range m.Pix {
		if !paletteFinite(float64(v)) {
			t.Fatal("non-finite output")
		}
	}
	previewPath := os.Getenv("ASTROMOSAIC_PALETTE_PREVIEW")
	if previewPath != "" {
		small := p.compose(paletteParams{Hydrogen: 1, Oxygen: 1, Sulfur: .5, Hue: 1, Auto: true}, 1200)
		if err := saveImage(previewPath, small, false); err != nil {
			t.Fatal(err)
		}
	}
}
