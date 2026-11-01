package main

import (
	"image"
	"math"
	"math/rand"
	"os"
	"testing"
)

func ellipticalTestImage(vary bool) *Img {
	m := newImg(800, 800, 1)
	for i := range m.Pix {
		m.Pix[i] = .03
	}
	for y := 35; y < 770; y += 40 {
		for x := 35; x < 770; x += 40 {
			a, b, angle := 2.5, 1.7, .6
			if vary {
				a = 2.1 + .7*float64(x)/800
				b = 1.6 + .25*float64(y)/800
				angle = -.3 + .6*float64(x)/800
			}
			ca, sa := math.Cos(angle), math.Sin(angle)
			for dy := -13; dy <= 13; dy++ {
				for dx := -13; dx <= 13; dx++ {
					u, v := float64(dx)-.21, float64(dy)-.33
					X, Y := ca*u+sa*v, -sa*u+ca*v
					m.Pix[(y+dy)*m.W+x+dx] += float32(6 / (2 * math.Pi * a * b) * math.Exp(-.5*(X*X/(a*a)+Y*Y/(b*b))))
				}
			}
		}
	}
	return m
}

func TestEllipticalKernelAdjoint(t *testing.T) {
	k := makeEllipticalKernel(psfCovariance{2, .7, 1})
	const w, h = 31, 27
	rng := rand.New(rand.NewSource(11))
	x, y := make([]float32, w*h), make([]float32, w*h)
	for i := range x {
		x[i], y[i] = float32(rng.NormFloat64()), float32(rng.NormFloat64())
	}
	a, b := k.convolve(x, w, h), k.convolve(y, w, h)
	var left, right float64
	for i := range x {
		left += float64(a[i]) * float64(y[i])
		right += float64(x[i]) * float64(b[i])
	}
	if math.Abs(left-right) > 1e-5 {
		t.Fatalf("kernel adjoint mismatch %f vs %f", left, right)
	}
	var sum, xx, xy, yy float64
	for _, tap := range k.taps {
		sum += tap.weight
		xx += tap.weight * float64(tap.dx*tap.dx)
		xy += tap.weight * float64(tap.dx*tap.dy)
		yy += tap.weight * float64(tap.dy*tap.dy)
	}
	if math.Abs(sum-1) > 1e-12 || math.Abs(xx-2) > .02 || math.Abs(xy-.7) > .02 || math.Abs(yy-1) > .02 {
		t.Fatal("elliptical kernel lost covariance")
	}
}

func TestSpatialPSFInterpolationAcrossAngleWrap(t *testing.T) {
	g := &spatialPSF{w: 800, h: 800}
	for i := range g.nodes {
		angle := 89 * math.Pi / 180
		if i%4%2 == 1 {
			angle = -angle
		}
		c, s := math.Cos(angle), math.Sin(angle)
		g.nodes[i] = psfNode{cov: psfCovariance{6.25*c*c + 2.89*s*s, (6.25 - 2.89) * s * c, 6.25*s*s + 2.89*c*c}, n: 10, valid: true}
	}
	c, ok := g.covariance(200, 100)
	a, b, angle := c.axes()
	if !ok || math.Abs(a-2.5) > .002 || math.Abs(b-1.7) > .002 || math.Abs(math.Abs(angle)-math.Pi/2) > .002 {
		t.Fatal("interpolation across ±90 degrees rotated or circularised the PSF")
	}
}

func TestSpatialPSFFitAndRestoration(t *testing.T) {
	m := ellipticalTestImage(true)
	model, err := estimateSpatialRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(model.spatial.summary())
	if model.spatial.nodes[0].cov.xx >= model.spatial.nodes[3].cov.xx {
		t.Fatal("field-dependent PSF was not measured")
	}
	p := restorationParams{5, .5}
	out := restoreImage(m, model, p, nil)
	note, err := assessRestorationModel(m, out, model, image.Point{})
	t.Log(note)
	if err != nil {
		t.Fatal(err)
	}
	var oldE, newE []float64
	for _, s := range model.stars {
		_, a, ok := starShapeChannel(m, s, 0)
		_, b, ok2 := starShapeChannel(out, s, 0)
		if ok && ok2 {
			oldE, newE = append(oldE, a), append(newE, b)
		}
	}
	t.Logf("ellipticity %.3f → %.3f", median(oldE), median(newE))
	if median(newE) > .95*median(oldE) {
		t.Fatal("directional blur did not reduce elongation")
	}
	region := image.Rect(270, 280, 470, 480)
	preview := restoreSpatialRegion(m, model, p, region, nil)
	for y := 0; y < preview.H; y++ {
		for x := 0; x < preview.W; x++ {
			if math.Abs(float64(preview.at(0, x, y)-out.at(0, x+region.Min.X, y+region.Min.Y))) > 1e-7 {
				t.Fatal("spatial preview differs from full result")
			}
		}
	}
}

func TestSpatialPSFSparseCoverage(t *testing.T) {
	m := ellipticalTestImage(false)
	model, err := estimateSpatialRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	model.spatial.nodes[5].valid = false
	if model.spatial.confidence(300, 300) != 0 {
		t.Fatal("unmeasured cell acquired a model")
	}
	out := restoreSpatialRegion(m, model, restorationParams{3, .35}, image.Rect(190, 210, 410, 390), nil)
	for y := 210; y < 390; y++ {
		for x := 200; x < 400; x++ {
			if out.at(0, x-190, y-210) != m.at(0, x, y) {
				t.Fatal("unmeasured cell was restored")
			}
		}
	}
	if model.spatial.confidence(199.9, 300) > .01 {
		t.Fatal("transition to unmeasured cell was not tapered")
	}
}

func TestSpatialRGBAndFlatField(t *testing.T) {
	base := ellipticalTestImage(false)
	m := newImg(base.W, base.H, 3)
	n := base.W * base.H
	for c, gain := range []float32{.8, 1, 1.2} {
		for i, v := range base.Pix {
			m.Pix[c*n+i] = gain * v
		}
	}
	m.Pix[300*m.W+300] = .99
	model, err := estimateSpatialRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	out := restoreImage(m, model, restorationParams{5, .35}, nil)
	for i := 0; i < n; i++ {
		if math.Abs(float64(m.Pix[i])/float64(m.Pix[n+i])-float64(out.Pix[i])/float64(out.Pix[n+i])) > 1e-6 {
			t.Fatal("overlap blending changed RGB ratios")
		}
	}
	for c := 0; c < 3; c++ {
		if m.Pix[c*n+300*m.W+305] != out.Pix[c*n+300*m.W+305] {
			t.Fatal("spatial operation changed protected saturation surroundings")
		}
	}
	flat := newImg(base.W, base.H, 1)
	for i := range flat.Pix {
		flat.Pix[i] = .03
	}
	mono, err := estimateSpatialRestoration(base)
	if err != nil {
		t.Fatal(err)
	}
	out = restoreImage(flat, mono, restorationParams{3, .35}, nil)
	for i, v := range out.Pix {
		if v != flat.Pix[i] {
			t.Fatal("patch blending produced a flat-field seam")
		}
	}
}

func TestSpatialPSFRealPreview(t *testing.T) {
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
	model, err := estimateSpatialRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(model.spatial.summary())
	region := restorationPreviewRect(m, model.stars, nil)
	out := restoreSpatialRegion(m, model, restorationParams{5, .35}, region, nil)
	note, err := assessRestorationModel(cropPixels(m, region), out, model, region.Min)
	t.Log(note)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("RESTORE_FULL_SPATIAL_REAL") != "" {
		full := restoreImage(m, model, restorationParams{5, .35}, nil)
		note, err := assessRestorationModel(m, full, model, image.Point{})
		t.Log("whole real tile:", note)
		if err != nil {
			t.Fatal(err)
		}
	}
}
