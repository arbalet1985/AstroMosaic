package main

import (
	"image"
	"math"
	"testing"
)

func profileTestImage(disk bool) *Img {
	m := newImg(900, 900, 1)
	for i := range m.Pix {
		m.Pix[i] = .03
	}
	for y := 40; y < 870; y += 45 {
		for x := 40; x < 870; x += 45 {
			var sum float64
			p := make([]float64, 39*39)
			for dy := -19; dy <= 19; dy++ {
				for dx := -19; dx <= 19; dx++ {
					r2 := (float64(dx)-.23)*(float64(dx)-.23) + (float64(dy)-.31)*(float64(dy)-.31)
					v := math.Pow(1+r2/(2.4*2.4), -2.4)
					if disk {
						v = 1 / (1 + math.Exp((math.Sqrt(r2)-3.3)*4))
					}
					p[(dy+19)*39+dx+19] = v
					sum += v
				}
			}
			for dy := -19; dy <= 19; dy++ {
				for dx := -19; dx <= 19; dx++ {
					m.Pix[(y+dy)*m.W+x+dx] += float32(5 * p[(dy+19)*39+dx+19] / sum)
				}
			}
		}
	}
	return m
}

func TestProfileRestoration(t *testing.T) {
	for _, disk := range []bool{false, true} {
		m := profileTestImage(disk)
		model, err := estimateProfileRestoration(m)
		if err != nil {
			t.Fatalf("disk=%v: %v", disk, err)
		}
		r := image.Rect(250, 250, 650, 650)
		out := restoreSpatialRegion(m, model, restorationParams{iterations: 5, fraction: .35}, r, nil)
		note, err := assessRestorationModel(cropPixels(m, r), out, model, r.Min)
		t.Logf("disk=%v %s", disk, note)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSeparateColorPSF(t *testing.T) {
	m := psfTestTile([]float64{2.3, 1.9, 2.1})
	m.Pix[200*m.W+200] = .99
	model, err := estimateRestorationMode(m, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	out := restoreImage(m, model, restorationParams{iterations: 5, fraction: .35}, nil)
	for c := 0; c < 3; c++ {
		if out.at(c, 204, 204) != m.at(c, 204, 204) {
			t.Fatal("separate PSFs changed shared saturation protection")
		}
	}
	r := image.Rect(90, 90, 190, 190)
	pv := restoreColorRegion(m, model, restorationParams{iterations: 5, fraction: .35}, r, nil)
	for c := 0; c < 3; c++ {
		for y := 0; y < pv.H; y++ {
			for x := 0; x < pv.W; x++ {
				if math.Abs(float64(pv.at(c, x, y)-out.at(c, x+r.Min.X, y+r.Min.Y))) > 1e-7 {
					t.Fatal("separate PSF preview differs from full result")
				}
			}
		}
	}
	if len(model.colorModels) != 3 || model.channels[0].fwhm < model.channels[1].fwhm {
		t.Fatal("separate channel PSFs not measured")
	}
	note, err := assessRestorationModel(m, out, model, image.Point{})
	t.Log(note)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range m.Pix {
		if !finitePoint(float64(out.Pix[i]), 0) || v < 0 && out.Pix[i] != v {
			t.Fatal("invalid channel restoration")
		}
	}
}
