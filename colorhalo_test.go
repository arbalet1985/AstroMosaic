package main

import (
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"testing"
)

func haloTestImage() *Img {
	m := newImg(240, 240, 3)
	for c := 0; c < 3; c++ {
		for y := 0; y < m.H; y++ {
			for x := 0; x < m.W; x++ {
				v := .01 + float64(c)*.002 + float64(x)*1e-6 + float64(y)*2e-6
				for _, p := range [][2]float64{{60.2, 60.3}, {170.3, 60.2}, {60.3, 170.2}, {170.2, 170.3}} {
					r2 := (float64(x)-p[0])*(float64(x)-p[0]) + (float64(y)-p[1])*(float64(y)-p[1])
					s, w := 2.0, .006
					if c == 0 {
						s, w = 2.8, .001
					}
					v += (.15*math.Exp(-r2/(2*s*s)) + w*math.Exp(-r2/(2*5.6*5.6))) * float64(c+1) / 2
				}
				m.Pix[c*m.W*m.H+y*m.W+x] = float32(v)
			}
		}
	}
	return m
}

func TestColorHaloFluxBackgroundAndProfile(t *testing.T) {
	m := haloTestImage()
	before := append([]float32(nil), m.Pix...)
	out, r, err := correctColorHalos(m, .75)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.stars) != 4 || r.after >= .5*r.before {
		t.Fatalf("report: %+v", r)
	}
	for c := 0; c < 3; c++ {
		var sum, abs float64
		for i, v := range m.Pix[c*m.W*m.H : (c+1)*m.W*m.H] {
			d := float64(out.Pix[c*m.W*m.H+i] - v)
			sum += d
			abs += math.Abs(d)
			x, y := i%m.W, i/m.W
			if (x > 90 && x < 140) || (y > 90 && y < 140) {
				if d != 0 {
					t.Fatal("background modified outside stars")
				}
			}
		}
		if math.Abs(sum) > 1e-5 || abs < .01 {
			t.Fatalf("flux drift c%d: %g; change %g", c, sum, abs)
		}
	}
	for i, v := range m.Pix {
		if v != before[i] {
			t.Fatal("source mutated")
		}
	}
	t.Log(r.note)
}

func TestColorHaloRejectUnsafeStars(t *testing.T) {
	for _, kind := range []string{"saturated", "hole", "nan", "misaligned", "identical"} {
		m := haloTestImage()
		s := star{60.2, 60.3, 1}
		switch kind {
		case "saturated":
			m.Pix[60*m.W+60] = 1
		case "hole":
			m.Pix[80*m.W+60] = 0
		case "nan":
			m.Pix[80*m.W+60] = float32(math.NaN())
		case "misaligned":
			plane := append([]float32(nil), m.Pix[:m.W*m.H]...)
			for y := 0; y < m.H; y++ {
				for x := 2; x < m.W; x++ {
					m.Pix[y*m.W+x] = plane[y*m.W+x-2]
				}
			}
		case "identical":
			copy(m.Pix[m.W*m.H:], m.Pix[:m.W*m.H])
			copy(m.Pix[2*m.W*m.H:], m.Pix[:m.W*m.H])
		}
		_, _, _, _, ok := haloStarPatch(m, s, .75)
		if ok {
			t.Errorf("accepted %s", kind)
		}
	}
}

func TestRealColorHalos(t *testing.T) {
	path := `C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\mosaic333.fits`
	if _, err := os.Stat(path); err != nil {
		t.Skip("real image absent")
	}
	m, err := loadImage(path)
	if err != nil {
		t.Fatal(err)
	}
	out, r, err := correctColorHalos(m, .75)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(r.note)
	if len(r.stars) < 30 || r.after >= .8*r.before {
		t.Fatalf("insufficient correction: %+v", r)
	}
	var maxFluxDrift float64
	for _, s := range r.stars {
		for c := 0; c < 3; c++ {
			f, ok := fitHaloStar(m, c, s)
			if !ok {
				t.Fatal("accepted star no longer measurable")
			}
			var flux, delta float64
			for y := int(math.Round(s.y)) - 25; y <= int(math.Round(s.y))+25; y++ {
				for x := int(math.Round(s.x)) - 25; x <= int(math.Round(s.x))+25; x++ {
					if math.Hypot(float64(x)-f.x, float64(y)-f.y) >= haloRadius {
						continue
					}
					flux += float64(m.at(c, x, y)) - f.background(float64(x), float64(y))
					delta += float64(out.at(c, x, y)) - float64(m.at(c, x, y))
				}
			}
			maxFluxDrift = math.Max(maxFluxDrift, math.Abs(delta/flux))
		}
	}
	if maxFluxDrift > 1e-5 {
		t.Fatalf("aperture flux drift: %g", maxFluxDrift)
	}
	t.Logf("max relative aperture flux drift: %.3g", maxFluxDrift)
	if dir := os.Getenv("ASTROMOSAIC_HALO_OUTPUT"); dir != "" {
		if err := writeFITS(dir+"/mosaic333-halo-corrected.fits", out); err != nil {
			t.Fatal(err)
		}
		p := r.stars[0]
		box := image.Rect(int(p.x)-160, int(p.y)-160, int(p.x)+160, int(p.y)+160).Intersect(image.Rect(0, 0, m.W, m.H))
		t.Logf("preview region: %v", box)
		pv := restorationPreviews(cropPixels(m, box), cropPixels(out, box))
		combined := image.NewRGBA(image.Rect(0, 0, box.Dx()*2, box.Dy()))
		draw.Draw(combined, image.Rect(0, 0, box.Dx(), box.Dy()), pv[0][prevUnlinked], image.Point{}, draw.Src)
		draw.Draw(combined, image.Rect(box.Dx(), 0, box.Dx()*2, box.Dy()), pv[1][prevUnlinked], image.Point{}, draw.Src)
		f, err := os.Create(dir + "/halo-before-after.png")
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, combined); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
