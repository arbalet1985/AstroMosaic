package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func waveTestImage(w, h int) (*Img, *Img) {
	clean, noisy := newImg(w, h, 1), newImg(w, h, 1)
	rng := rand.New(rand.NewSource(42))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := float64(x-w/2), float64(y-h/2)
			v := .02 + .08*math.Exp(-(dx*dx+dy*dy)/18) + .006*math.Exp(-dy*dy/80)*(1+.3*math.Sin(float64(x)/14))
			clean.Pix[y*w+x] = float32(v)
			noisy.Pix[y*w+x] = float32(v + .001*rng.NormFloat64())
		}
	}
	return clean, noisy
}
func standardWaveParams() waveParams {
	return waveParams{Strength: .5, Gain: [waveLevels]float64{1, 1, 1, 1}}
}

func TestWaveDenoiseTruthAndNativePreview(t *testing.T) {
	clean, m := waveTestImage(620, 440)
	model := estimateWaveModel(m)
	p := standardWaveParams()
	full := filterWaveRegion(m, model, p, image.Rect(0, 0, m.W, m.H), nil)
	identity := p
	identity.Strength = 0
	same := filterWaveRegion(m, model, identity, image.Rect(0, 0, m.W, m.H), nil)
	for i, v := range m.Pix {
		if same.Pix[i] != v {
			t.Fatal("disabled processing changed pixels")
		}
	}
	r := image.Rect(205, 127, 540, 392)
	part := filterWaveRegion(m, model, p, r, nil)
	var old, new float64
	for i, v := range m.Pix {
		old += math.Pow(float64(v-clean.Pix[i]), 2)
		new += math.Pow(float64(full.Pix[i]-clean.Pix[i]), 2)
	}
	if new >= old*.65 {
		t.Fatalf("insufficient true MSE reduction: %g", new/old)
	}
	for y := 0; y < part.H; y++ {
		for x := 0; x < part.W; x++ {
			if math.Abs(float64(part.at(0, x, y)-full.at(0, x+r.Min.X, y+r.Min.Y))) > 1e-7 {
				t.Fatal("preview/full seam mismatch")
			}
		}
	}
	t.Logf("synthetic truth MSE ratio %.3f", new/old)
}

func TestWavePreservesInvalidSaturationAndConstant(t *testing.T) {
	m := newImg(120, 100, 1)
	for i := range m.Pix {
		m.Pix[i] = -.02
	}
	m.Pix[40*m.W+40] = 0
	m.Pix[50*m.W+50] = float32(math.NaN())
	m.Pix[60*m.W+60] = 1
	out := filterWaveRegion(m, estimateWaveModel(m), standardWaveParams(), image.Rect(0, 0, m.W, m.H), nil)
	if out.Pix[40*m.W+40] != 0 || !math.IsNaN(float64(out.Pix[50*m.W+50])) || out.Pix[60*m.W+60] != 1 {
		t.Fatal("protection failed")
	}
	if math.Abs(float64(out.Pix[0]+.02)) > 1e-7 {
		t.Fatal("constant/negative level changed")
	}
}

func TestWaveDetailsIncreaseKnownStructureWithoutBackgroundBoost(t *testing.T) {
	_, m := waveTestImage(384, 384)
	model := estimateWaveModel(m)
	p := standardWaveParams()
	base := filterWaveRegion(m, model, p, image.Rect(0, 0, m.W, m.H), nil)
	p.Gain = [waveLevels]float64{1, 1.25, 1.25, 1.1}
	detail := filterWaveRegion(m, model, p, image.Rect(0, 0, m.W, m.H), nil)
	var a, b, noiseA, noiseB float64
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			if y == m.H/2 && abs(x-m.W/2) > 40 {
				a += float64(base.at(0, x, y)) - .02
				b += float64(detail.at(0, x, y)) - .02
			}
			if abs(y-m.H/2) > 50 {
				noiseA += math.Pow(float64(base.at(0, x, y))-.02, 2)
				noiseB += math.Pow(float64(detail.at(0, x, y))-.02, 2)
			}
		}
	}
	if b <= a || noiseB > noiseA*1.1 {
		t.Fatalf("structure %g → %g, background power ratio %g", a, b, noiseB/noiseA)
	}
	t.Logf("known filament contrast ×%.3f; background error power ×%.3f", b/a, noiseB/noiseA)
}

func TestRegularizedRestorationPreview(t *testing.T) {
	m := psfTestTile([]float64{2.2})
	model, err := estimateRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	p := restorationParams{iterations: 3, fraction: .35, regularize: true}
	out := restoreImage(m, model, p, nil)
	if note, err := assessRestorationModel(m, out, model, image.Point{}); err != nil {
		t.Fatalf("regularized candidate failed QA: %v; %s", err, note)
	}
	for _, v := range out.Pix {
		if !finitePoint(float64(v), 0) {
			t.Fatal("regularization created invalid values")
		}
	}
	region := image.Rect(120, 120, 280, 280)
	halo := 2 * restorationRadius(model, p) * (p.iterations + 2)
	context := region.Inset(-halo).Intersect(image.Rect(0, 0, m.W, m.H))
	trial := restoreImage(cropPixels(m, context), model, p, nil)
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			if math.Abs(float64(out.at(0, x, y)-trial.at(0, x-context.Min.X, y-context.Min.Y))) > 1e-7 {
				t.Fatal("regularized preview differs from full")
			}
		}
	}
	before, after := waveScalePower(m), waveScalePower(out)
	t.Logf("regularized scale power %v → %v", before, after)
}

func TestExternalDenoiseComparison(t *testing.T) {
	if os.Getenv("ASTROMOSAIC_EXTERNAL_BENCH") == "" {
		t.Skip("set ASTROMOSAIC_EXTERNAL_BENCH to run installed engines")
	}
	m, err := loadImage(`C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\mosaic333.fits`)
	if err != nil {
		t.Fatal(err)
	}
	model := estimateWaveModel(m)
	regions := noiseBenchmarkRegions(m)
	var rows []string
	dir := os.Getenv("ASTROMOSAIC_BENCH_OUTPUT")
	for i, r := range regions {
		if selected := os.Getenv("ASTROMOSAIC_BENCH_REGION"); selected != "" && selected != fmt.Sprint(i) {
			continue
		}
		before := cropPixels(m, r)
		for method, name := range noiseMethods {
			start := time.Now()
			out, err := runNoise(m, model, standardWaveParams(), method, r, nil)
			if err != nil {
				t.Errorf("region %d %s: %v", i, name, err)
				continue
			}
			report := measureQuality(before, out)
			row := fmt.Sprintf("region=%d x=%d y=%d method=%s seconds=%.2f noise=%v -> %v stars=%d", i, r.Min.X, r.Min.Y, name, time.Since(start).Seconds(), report.Noise[0], report.Noise[1], len(report.Stars))
			t.Log(row)
			rows = append(rows, row)
			if dir != "" {
				base := fmt.Sprintf("benchmark-%d-%d", i, method)
				if err := writeFITS(filepath.Join(dir, base+".fits"), out); err != nil {
					t.Fatal(err)
				}
				pv := restorationPreviews(before, out)
				combined := image.NewRGBA(image.Rect(0, 0, before.W*2, before.H))
				draw.Draw(combined, image.Rect(0, 0, before.W, before.H), pv[0][prevUnlinked], image.Point{}, draw.Src)
				draw.Draw(combined, image.Rect(before.W, 0, before.W*2, before.H), pv[1][prevUnlinked], image.Point{}, draw.Src)
				f, err := os.Create(filepath.Join(dir, base+".png"))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(f, combined)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "denoise-comparison.txt"), []byte(strings.Join(rows, "\n")), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWaveRealFullImage(t *testing.T) {
	if os.Getenv("ASTROMOSAIC_FULL_WAVE") == "" {
		t.Skip("set ASTROMOSAIC_FULL_WAVE for full-size processing")
	}
	m, err := loadImage(`C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\mosaic333.fits`)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	model := estimateWaveModel(m)
	out := filterWaveRegion(m, model, standardWaveParams(), image.Rect(0, 0, m.W, m.H), nil)
	r := measureQuality(m, out)
	for c := 0; c < m.C; c++ {
		if r.Noise[1][c] >= r.Noise[0][c] {
			t.Fatal("no measured denoising")
		}
		if math.Abs(r.Flux[1][c]/r.Flux[0][c]-1) > .02 {
			t.Fatal("whole-image signal drift >2%")
		}
	}
	t.Logf("full %dx%d: %s, noise %v → %v, N=%d, width R %.3f → %.3f", m.W, m.H, time.Since(start), r.Noise[0], r.Noise[1], len(r.Stars), median(r.values(0, 0, "width", false)), median(r.values(1, 0, "width", false)))
	if dir := os.Getenv("ASTROMOSAIC_BENCH_OUTPUT"); dir != "" {
		if err := writeFITS(filepath.Join(dir, "mosaic333-starlet.fits"), out); err != nil {
			t.Fatal(err)
		}
	}
}
