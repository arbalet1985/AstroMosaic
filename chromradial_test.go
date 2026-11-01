package main

import (
	"math"
	"math/rand"
	"testing"
)

func TestChromRadialSelection(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	want := channelAlignment{Map: Aff{1.001, .0002, .2, -.0001, .999, -.3}, Radial: 1.1, CX: 500, CY: 400, Scale: 500}
	var src, dst [][2]float64
	for i := 0; i < 500; i++ {
		p := [2]float64{20 + rng.Float64()*960, 20 + rng.Float64()*760}
		x, y := want.apply(p[0], p[1])
		src = append(src, p)
		dst = append(dst, [2]float64{x + .015*rng.NormFloat64(), y + .015*rng.NormFloat64()})
	}
	a, n, ok := fitAffine(src, dst, 1000, 800)
	if !ok {
		t.Fatal("affine")
	}
	f := selectChromModel(src, dst, 1000, 800, channelAlignment{Map: a, Stars: n, Valid: true})
	if f.Radial == 0 || math.Abs(f.Radial-want.Radial) > .03 || f.ValidationAfter > f.ValidationBefore/3 {
		t.Fatalf("radial selection failed: %+v", f)
	}
	for _, p := range [][2]float64{{0, 0}, {999, 799}, {500, 400}} {
		x, y := f.apply(p[0], p[1])
		u, v := want.apply(p[0], p[1])
		if math.Hypot(x-u, y-v) > .04 {
			t.Fatal("wrong radial map")
		}
	}
	for i, p := range src {
		x, y := want.Map.apply(p[0], p[1])
		dst[i] = [2]float64{x + .015*rng.NormFloat64(), y + .015*rng.NormFloat64()}
	}
	a, n, _ = fitAffine(src, dst, 1000, 800)
	f = selectChromModel(src, dst, 1000, 800, channelAlignment{Map: a, Stars: n, Valid: true})
	if f.Radial != 0 {
		t.Fatal("noise selected nonlinear model")
	}
}

func TestChromRadialResampling(t *testing.T) {
	rgb := newImg(800, 800, 3)
	f := channelAlignment{Map: identity, Radial: .9, CX: 400, CY: 400, Scale: 400}
	// Sampling direction itself is tested on a ramp with an analytically known value.
	for y := 0; y < rgb.H; y++ {
		for x := 0; x < rgb.W; x++ {
			rgb.Pix[y*rgb.W+x] = float32(x) * .001
		}
	}
	out := newImg(rgb.W, rgb.H, 3)
	resampleChannelMapped(rgb, out, 0, f.apply)
	x, y := f.apply(100, 200)
	if math.Abs(float64(out.at(0, 100, 200))-x*.001) > 5e-5 || !finitePoint(y, 0) {
		t.Fatal("nonlinear resampling direction")
	}
}

func TestChromRadialImage(t *testing.T) {
	m := newImg(800, 800, 3)
	for i := range m.Pix {
		m.Pix[i] = .03
	}
	want := channelAlignment{Map: identity, Radial: .9, CX: 400, CY: 400, Scale: 400}
	for y := 35; y < 770; y += 40 {
		for x := 35; x < 770; x += 40 {
			for c := 0; c < 3; c++ {
				u, v := float64(x)+.21, float64(y)+.33
				if c == 0 {
					u, v = want.apply(u, v)
				}
				for dy := -9; dy <= 9; dy++ {
					for dx := -9; dx <= 9; dx++ {
						ix, iy := int(u)+dx, int(v)+dy
						r2 := (float64(ix)-u)*(float64(ix)-u) + (float64(iy)-v)*(float64(iy)-v)
						m.Pix[c*m.W*m.H+iy*m.W+ix] += .2 * float32(math.Exp(-r2/(2*1.7*1.7)))
					}
				}
			}
		}
	}
	report := measureChromatism(m)
	if math.Abs(report.Channels[0].Radial-.9) > .05 {
		t.Fatalf("radial model not measured: %+v", report.Channels[0])
	}
	out, _ := applyChannelAlignment(m, report)
	if chanOffset(out) > .06 {
		t.Fatal("radial fringes remain")
	}
}
