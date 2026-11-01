package main

import (
	"image"
	"math"
	"math/rand"
	"testing"
)

func comaTestImage() *Img {
	m := newImg(800, 800, 1)
	for i := range m.Pix {
		m.Pix[i] = .03
	}
	const sigma, w, dx, dy = 1.7, .22, 4.5, 1.5
	for y := 35; y < 770; y += 40 {
		for x := 35; x < 770; x += 40 {
			for yy := -13; yy <= 13; yy++ {
				for xx := -13; xx <= 13; xx++ {
					u, v := float64(xx)-.21, float64(yy)-.33
					a, b := math.Exp(-((u+w*dx)*(u+w*dx)+(v+w*dy)*(v+w*dy))/(2*sigma*sigma)), math.Exp(-((u-(1-w)*dx)*(u-(1-w)*dx)+(v-(1-w)*dy)*(v-(1-w)*dy))/(2*sigma*sigma))
					m.Pix[(y+yy)*m.W+x+xx] += float32(6 / (2 * math.Pi * sigma * sigma) * ((1-w)*a + w*b))
				}
			}
		}
	}
	return m
}

func TestComaAdjointAndDirection(t *testing.T) {
	p := make([]float64, comaSide*comaSide)
	p[comaRadius*comaSide+comaRadius-1] = .8
	p[comaRadius*comaSide+comaRadius+4] = .2
	k := kernelFromPSF(p)
	reverse := k.adjoint()
	const w, h = 37, 31
	x, y := make([]float32, w*h), make([]float32, w*h)
	rng := rand.New(rand.NewSource(8))
	for i := range x {
		x[i], y[i] = float32(rng.NormFloat64()), float32(rng.NormFloat64())
	}
	a, b := k.convolve(x, w, h), reverse.convolve(y, w, h)
	var left, right float64
	for i := range x {
		left += float64(a[i]) * float64(y[i])
		right += float64(x[i]) * float64(b[i])
	}
	if math.Abs(left-right) > 1e-5 {
		t.Fatal("asymmetric adjoint is incorrect")
	}
	point := make([]float32, w*h)
	point[15*w+18] = 1
	blur := k.convolve(point, w, h)
	if math.Abs(float64(blur[15*w+22])-.2) > 1e-6 || blur[15*w+14] != 0 {
		t.Fatal("coma kernel tail was reflected")
	}
}

func TestComaRestoration(t *testing.T) {
	m := comaTestImage()
	model, err := estimateComaRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(model.spatial.summary())
	region := image.Rect(200, 200, 600, 600)
	out := restoreSpatialRegion(m, model, restorationParams{5, .5}, region, nil)
	note, err := assessComaRestoration(cropPixels(m, region), out, model, region.Min)
	t.Log(note)
	if err != nil {
		t.Fatal(err)
	}
}

func TestComaRequiresAsymmetry(t *testing.T) {
	if _, err := estimateComaRestoration(ellipticalTestImage(false)); err == nil {
		t.Fatal("symmetric PSF was accepted as coma")
	}
}

func TestComaRGBPreviewMatchesFull(t *testing.T) {
	base := comaTestImage()
	m := newImg(base.W, base.H, 3)
	n := base.W * base.H
	for c, gain := range []float32{.8, 1, 1.2} {
		for i, v := range base.Pix {
			m.Pix[c*n+i] = gain * v
		}
	}
	model, err := estimateComaRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]float32(nil), m.Pix...)
	p := restorationParams{5, .35}
	out := restoreImage(m, model, p, nil)
	note, err := assessRestorationModel(m, out, model, image.Point{})
	t.Log(note)
	if err != nil {
		t.Fatal(err)
	}
	r := image.Rect(280, 260, 460, 440)
	pv := restoreSpatialRegion(m, model, p, r, nil)
	for i, v := range m.Pix {
		if v != before[i] {
			t.Fatal("modified source")
		}
	}
	for y := 0; y < pv.H; y++ {
		for x := 0; x < pv.W; x++ {
			for c := 0; c < 3; c++ {
				if math.Abs(float64(pv.at(c, x, y)-out.at(c, x+r.Min.X, y+r.Min.Y))) > 1e-7 {
					t.Fatal("coma preview differs from full operation")
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		for c := 0; c < 3; c++ {
			if math.Abs(float64(out.Pix[c*n+i])/float64(out.Pix[n+i])-float64(m.Pix[c*n+i])/float64(m.Pix[n+i])) > 1e-6 {
				t.Fatal("coma changed RGB ratio")
			}
		}
	}
}
