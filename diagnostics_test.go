package main

import (
	"math"
	"os"
	"testing"
)

func TestFieldDiagnosticsReal(t *testing.T) {
	m, err := loadImage(`C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\1-1.xisf`)
	if os.IsNotExist(err) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	stars := detectStars(m)
	var fwhm, ellipticity []float64
	for _, s := range stars[:min(len(stars), 4000)] {
		if f, e, ok := starShape(m, s); ok {
			fwhm = append(fwhm, f)
			ellipticity = append(ellipticity, e)
		}
	}
	if len(fwhm) < 30 {
		t.Fatalf("only %d measurable stars", len(fwhm))
	}
	t.Logf("Gaussian core: %d stars, median FWHM %.3f px, ellipticity %.3f", len(fwhm), median(fwhm), median(ellipticity))
}

func TestStarShapeGaussian(t *testing.T) {
	for _, sigma := range [][2]float64{{1.5, 1.5}, {2.5, 1.5}} {
		m := newImg(81, 81, 1)
		const angle = .6
		for y := 0; y < m.H; y++ {
			for x := 0; x < m.W; x++ {
				dx, dy := float64(x)-40.2, float64(y)-39.7
				u, v := math.Cos(angle)*dx+math.Sin(angle)*dy, -math.Sin(angle)*dx+math.Cos(angle)*dy
				m.Pix[y*m.W+x] = float32(.05 + .6*math.Exp(-.5*(u*u/(sigma[0]*sigma[0])+v*v/(sigma[1]*sigma[1]))))
			}
		}
		f, e, ok := starShape(m, star{40.2, 39.7, 1})
		wantF, wantE := 2.354820045*math.Sqrt(sigma[0]*sigma[1]), 1-sigma[1]/sigma[0]
		if !ok || math.Abs(f-wantF) > .02 || math.Abs(e-wantE) > .01 {
			t.Fatalf("PSF (%v): got %f %f %v, want %f %f", sigma, f, e, ok, wantF, wantE)
		}
		m.Pix[40*m.W+40] = 1
		if _, _, ok := starShape(m, star{40.2, 39.7, 1}); ok {
			t.Fatal("saturated star was measured")
		}
	}
}

func TestDiagnosticResidualsAndEmptyBins(t *testing.T) {
	a, b := newImg(100, 100, 1), newImg(100, 100, 1)
	W := []Warp{newWarp(identity, 0, a), newWarp(identity, 0, b)}
	m := registrationMeasurements{matches: []match{{0, 1, [2]float64{12, 12}, [2]float64{13, 14}}}}
	d := measureFieldDiagnostics([]*Tile{{Img: a}, {Img: b}}, W, m)
	if math.Abs(d.rms-math.Sqrt(5)) > 1e-9 || d.tiles[0].bins[0].count != 1 || d.tiles[1].bins[0].dx != -1 || d.tiles[1].bins[0].dy != -2 {
		t.Fatal("residual magnitude or direction is wrong")
	}
	if len(d.tiles[0].bins[1].fwhm) != 0 || d.tiles[0].bins[1].count != 0 {
		t.Fatal("empty area acquired measurements")
	}
	if d.validationN != 0 {
		t.Fatal("missing validation was reported as zero error")
	}
}
