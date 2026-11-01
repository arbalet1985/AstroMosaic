package main

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

func gradientTestImage() *Img {
	m := newImg(600, 500, 3)
	rng := rand.New(rand.NewSource(7))
	for c := 0; c < m.C; c++ {
		for y := 0; y < m.H; y++ {
			for x := 0; x < m.W; x++ {
				u, v := 2*float64(x)/float64(m.W-1)-1, 2*float64(y)/float64(m.H-1)-1
				bg := .06 + .012*u - .008*v + .018*u*u + .007*u*v - .009*v*v
				neb := .09 * math.Exp(-((u-.15)*(u-.15)+(v+.1)*(v+.1))/.025)
				m.Pix[c*m.W*m.H+y*m.W+x] = float32(float64(c+2)*.5*(bg+neb) + rng.NormFloat64()*.00015)
			}
		}
	}
	for y := 40; y < 470; y += 55 {
		for x := 40; x < 570; x += 55 {
			for dy := -6; dy <= 6; dy++ {
				for dx := -6; dx <= 6; dx++ {
					for c := 0; c < 3; c++ {
						m.Pix[c*m.W*m.H+(y+dy)*m.W+x+dx] += .15 * float32(math.Exp(-float64(dx*dx+dy*dy)/4))
					}
				}
			}
		}
	}
	return m
}

func TestGradientQuadraticAndObjects(t *testing.T) {
	m := gradientTestImage()
	var mask [400]bool
	for iy := 6; iy <= 11; iy++ {
		for ix := 9; ix <= 13; ix++ {
			mask[iy*20+ix] = true
		}
	}
	g, err := estimateGradient(m, mask)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(g.note)
	if !strings.Contains(g.note, "квадратичная") {
		t.Fatal("curvature was missed")
	}
	before := append([]float32(nil), m.Pix...)
	out := subtractGradient(m, g)
	for i, v := range before {
		if m.Pix[i] != v {
			t.Fatal("modified source")
		}
	}
	for c := 0; c < 3; c++ {
		for _, p := range [][2]int{{5, 5}, {590, 490}, {15, 470}, {570, 15}} {
			if math.Abs(float64(out.at(c, p[0], p[1]))-g.level[c]) > .001 {
				t.Fatalf("residual gradient at %v: %g", p, out.at(c, p[0], p[1]))
			}
		}
		x, y := 345, 225
		if math.Abs(float64(out.at(c, x, y))-g.level[c]-.09*float64(c+2)*.5) > .002 {
			t.Fatal("masked nebula was removed")
		}
	}
	// Sky subtraction changes the local background, not the star's local contrast.
	x, y := 40, 40
	old := float64(m.at(0, x, y) - m.at(0, x+10, y))
	new := float64(out.at(0, x, y) - out.at(0, x+10, y))
	if math.Abs(old-new) > .002 {
		t.Fatal("local stellar photometry changed")
	}
	m.Pix[0] = 0
	m.Pix[1] = float32(math.NaN())
	m.Pix[2] = -.04
	out = subtractGradient(m, g)
	if out.Pix[0] != 0 || out.Pix[1] != 0 || out.Pix[2] >= 0 {
		t.Fatal("missing/negative data mishandled")
	}
}

func TestGradientRejectsFlatOrSparse(t *testing.T) {
	m := newImg(400, 400, 1)
	for i := range m.Pix {
		m.Pix[i] = .05
	}
	if _, err := estimateGradient(m, [400]bool{}); err == nil {
		t.Fatal("flat image received gradient")
	}
	m = gradientTestImage()
	var mask [400]bool
	for i := range mask {
		mask[i] = i%20 < 15
	}
	if _, err := estimateGradient(m, mask); err == nil {
		t.Fatal("one-sided samples accepted")
	}
}

func TestGradientPlaneFallback(t *testing.T) {
	m := newImg(400, 400, 1)
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			m.Pix[y*m.W+x] = float32(.05 + .02*float64(x)/400 - .01*float64(y)/400)
		}
	}
	g, err := estimateGradient(m, [400]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if g.coeff[0][3] != 0 || g.coeff[0][4] != 0 || g.coeff[0][5] != 0 {
		t.Fatal("unnecessary curvature accepted")
	}
}
