package main

import (
	"image"
	"math"
	"math/rand"
	"os"
	"slices"
	"testing"
)

func TestRestorationGaussian(t *testing.T) {
	m := psfTestTile([]float64{2.2, 2.1, 2.3})
	before := slices.Clone(m.Pix)
	model, err := estimateRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	out := restoreImage(m, model, restorationParams{iterations: 5, fraction: .5}, nil)
	note, err := assessRestoration(m, out, model.stars)
	t.Log(note)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, m.Pix) {
		t.Fatal("restoration modified its source")
	}
	n := m.W * m.H
	for i := 0; i < n; i++ {
		oldRatio := float64(m.Pix[i]) / float64(m.Pix[n+i])
		newRatio := float64(out.Pix[i]) / float64(out.Pix[n+i])
		if math.Abs(oldRatio-newRatio) > 1e-6 {
			t.Fatal("RGB ratios changed during luminance restoration")
		}
	}
	for _, v := range out.Pix {
		if !finitePoint(float64(v), 0) {
			t.Fatal("nonfinite restored sample")
		}
	}
}

func TestRestorationApproachesKnownLatentImage(t *testing.T) {
	m := psfTestTile([]float64{2.2})
	truth := psfTestTile([]float64{2.2 * math.Sqrt(1-.5*.5)})
	model, err := estimateRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	out := restoreImage(m, model, restorationParams{iterations: 5, fraction: .5}, nil)
	var beforeError, afterError float64
	for i, want := range truth.Pix {
		beforeError += math.Pow(float64(m.Pix[i]-want), 2)
		afterError += math.Pow(float64(out.Pix[i]-want), 2)
	}
	if afterError > .1*beforeError {
		t.Fatalf("restoration did not recover the known latent profile: MSE %.8f → %.8f", beforeError/float64(len(m.Pix)), afterError/float64(len(m.Pix)))
	}
}

func TestRestorationNoiseAndProtectedPixels(t *testing.T) {
	m := psfTestTile([]float64{2.2, 2.2, 2.2})
	rng := rand.New(rand.NewSource(9))
	for i := range m.Pix {
		m.Pix[i] += float32(.001 * rng.NormFloat64())
	}
	n := m.W * m.H
	for c := 0; c < m.C; c++ {
		m.Pix[c*n+30*m.W+30] = 0
		m.Pix[c*n+31*m.W+30] = -.001
	}
	m.Pix[200*m.W+200] = .99 // protected in all channels
	model, err := estimateRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	out := restoreImage(m, model, restorationParams{iterations: 5, fraction: .5}, nil)
	for c := 0; c < m.C; c++ {
		for _, p := range []int{0, 30*m.W + 30, 31*m.W + 30, 200*m.W + 200, 200*m.W + 205} {
			if out.Pix[c*n+p] != m.Pix[c*n+p] {
				t.Fatal("border, missing, negative or saturated-neighbour pixel changed")
			}
		}
	}
	note, err := assessRestoration(m, out, model.stars)
	t.Log(note)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestorationPreviewMatchesFull(t *testing.T) {
	for _, sigmas := range [][]float64{{2.2}, {2.2, 2.1, 2.3}} {
		m := psfTestTile(sigmas)
		model, err := estimateRestoration(m)
		if err != nil {
			t.Fatal(err)
		}
		p := restorationParams{iterations: 5, fraction: .5}
		full := restoreImage(m, model, p, nil)
		region := image.Rect(120, 120, 280, 280)
		halo := 2 * restorationRadius(model, p) * (p.iterations + 2)
		context := region.Inset(-halo).Intersect(image.Rect(0, 0, m.W, m.H))
		trial := restoreImage(cropPixels(m, context), model, p, nil)
		for y := region.Min.Y; y < region.Max.Y; y++ {
			for x := region.Min.X; x < region.Max.X; x++ {
				for c := 0; c < m.C; c++ {
					if math.Abs(float64(full.at(c, x, y)-trial.at(c, x-context.Min.X, y-context.Min.Y))) > 1e-7 {
						t.Fatal("preview context differs from full restoration")
					}
				}
			}
		}
	}
}

func TestRestorationRejectsMissingPSF(t *testing.T) {
	if _, err := estimateRestoration(newImg(100, 100, 3)); err == nil {
		t.Fatal("empty image produced a PSF")
	}
	m := psfTestTile([]float64{2.2})
	if _, err := assessRestoration(m, m, nil); err == nil {
		t.Fatal("unchanged image passed restoration verification")
	}
}

func TestRestorationRealPreview(t *testing.T) {
	if testing.Short() {
		t.Skip("real tile")
	}
	m, err := loadImage(`C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\1-1.xisf`)
	if os.IsNotExist(err) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	model, err := estimateRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	p := restorationParams{iterations: 5, fraction: .35}
	region := restorationPreviewRect(m, model.stars, nil)
	halo := 2 * restorationRadius(model, p) * (p.iterations + 2)
	context := region.Inset(-halo).Intersect(image.Rect(0, 0, m.W, m.H))
	before := cropPixels(m, region)
	out := restoreImage(cropPixels(m, context), model, p, nil)
	after := cropPixels(out, region.Sub(context.Min))
	note, err := assessRestoration(before, after, nil)
	t.Log(region, note)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("RESTORE_FULL_REAL") != "" {
		full := restoreImage(m, model, p, nil)
		note, err := assessRestoration(m, full, model.stars)
		t.Log("whole real tile:", note)
		if err != nil {
			t.Fatal(err)
		}
	}
}
