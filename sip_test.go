package main

import (
	"math"
	"testing"
)

func sipTestImage(bottomUp bool) *Img {
	m := newImg(500, 400, 1)
	m.BottomUp = bottomUp
	m.Hdr = map[string]string{
		"CTYPE1": "RA---TAN-SIP", "CTYPE2": "DEC--TAN-SIP",
		"CRVAL1": "359.9", "CRVAL2": "65", "CRPIX1": "231.25", "CRPIX2": "173.5",
		"CD1_1": "-0.001", "CD1_2": "0.0001", "CD2_1": "0.0001", "CD2_2": "0.001",
		"A_ORDER": "3", "B_ORDER": "3", "A_2_0": "2D-5", "A_1_1": "-1e-5", "A_0_3": "1e-8",
		"B_0_2": "-1.5e-5", "B_2_1": "-1e-8",
	}
	return m
}

func TestSIPCoordinates(t *testing.T) {
	for _, bottomUp := range []bool{false, true} {
		m := sipTestImage(bottomUp)
		w := parseWCS(m)
		if w == nil || w.sip == nil {
			t.Fatal("SIP WCS was not parsed")
		}
		linear := *w
		linear.sip = nil
		for _, p := range [][2]float64{{0, 0}, {499, 399}, {230.25, 172.5}, {123.4, 267.8}} {
			u, v := p[0]+1-231.25, p[1]+1-173.5
			sign := 1.0
			if bottomUp {
				v, sign = 400-p[1]-173.5, -1
			}
			a, b := 2e-5*u*u-1e-5*u*v+1e-8*v*v*v, -1.5e-5*v*v-1e-8*u*u*v
			ra, dec := w.pixToSky(p[0], p[1])
			R, D := linear.pixToSky(p[0]+a, p[1]+sign*b)
			if math.Abs(ra-R)+math.Abs(dec-D) > 1e-12 {
				t.Fatal("SIP differs from FITS offset convention")
			}
			x, y := w.skyToPix(ra, dec)
			if math.Hypot(x-p[0], y-p[1]) > 1e-5 {
				t.Fatalf("roundtrip %v: %f %f (bottomUp=%v)", p, x, y, bottomUp)
			}
			warp := newWarp(identity, .04, m)
			X, Y := warp.apply(p[0], p[1])
			if warp.K != 0 || math.Hypot(X-(p[0]+a), Y-(p[1]+sign*b)) > 1e-9 {
				t.Fatal("SIP was not used, or radial correction was applied twice")
			}
			x, y = warp.inv(X, Y)
			if math.Hypot(x-p[0], y-p[1]) > 1e-5 {
				t.Fatal("render warp did not invert SIP")
			}
		}
	}
}

func TestSIPMalformed(t *testing.T) {
	for _, change := range []func(*Img){
		func(m *Img) { delete(m.Hdr, "B_ORDER") },
		func(m *Img) { m.Hdr["A_ORDER"] = "10" },
		func(m *Img) { m.Hdr["A_ORDER"] = "2.5" },
		func(m *Img) { m.Hdr["A_2_0"] = "NaN" },
		func(m *Img) { m.Hdr["B_0_2"] = "+Inf" },
		func(m *Img) { m.Hdr["A_0_0"] = "1" },
		func(m *Img) { delete(m.Hdr, "CRPIX1") },
	} {
		m := sipTestImage(false)
		change(m)
		if _, err := parseSIP(m); err == nil {
			t.Fatal("malformed SIP silently accepted")
		}
		if parseWCS(m) != nil {
			t.Fatal("malformed SIP used as a linear WCS")
		}
	}
	// Coefficients may be present without -SIP in CTYPE; A/B still define SIP.
	m := sipTestImage(false)
	m.Hdr["CTYPE1"], m.Hdr["CTYPE2"] = "RA---TAN", "DEC--TAN"
	if w := parseWCS(m); w == nil || w.sip == nil {
		t.Fatal("SIP without CTYPE suffix was lost")
	}
	// Singular forward maps must fail closed instead of returning a false pixel.
	s := &sipDistortion{a: sipPolynomial{{2, 0, -1}}, sign: 1}
	if _, _, ok := s.inverse(1, 0); ok {
		t.Fatal("noninvertible SIP returned a pixel")
	}
}

func TestSIPGlobalFit(t *testing.T) {
	a, b := sipTestImage(false), sipTestImage(true)
	tiles := []*Tile{{Img: a}, {Img: b}}
	truth := initialWarps(tiles, 0)
	truth[1].S = Aff{1, 0, 180, 0, 1, 5}
	truth[1].Si = truth[1].S.inv()
	var matches []match
	for y := 30.; y < 370; y += 25 {
		for x := 220.; x < 460; x += 25 {
			X, Y := truth[0].apply(x, y)
			u, v := truth[1].inv(X, Y)
			matches = append(matches, match{0, 1, [2]float64{x, y}, [2]float64{u, v}})
		}
	}
	W, kept, k := refine(tiles, []Aff{identity, truth[1].S}, matches)
	if len(kept) < 60 || k != 0 || rms(W, kept) > .001 {
		t.Fatalf("SIP fit: n=%d k=%f rms=%f", len(kept), k, rms(W, kept))
	}
	if err := maxErr(W[1], truth[1], b); err > .01 {
		t.Fatalf("SIP fit deformed the field: %f", err)
	}
}

func TestSIPRender(t *testing.T) {
	m := sipTestImage(false)
	delete(m.Hdr, "A_1_1")
	delete(m.Hdr, "A_0_3")
	delete(m.Hdr, "B_2_1")
	w := newWarp(identity, 0, m)
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			X, Y := w.apply(float64(x), float64(y))
			m.Pix[y*m.W+x] = float32(.2 + .0002*X + .0001*Y)
		}
	}
	out := render([]*Tile{{Img: m}}, []Warp{w}, []region{full(m)})
	// For this monotone quadratic map, both lower bounds occur at (0,0).
	ox, oy := w.apply(0, 0)
	ox, oy = math.Floor(ox), math.Floor(oy)
	for _, p := range [][2]int{{50, 50}, {200, 180}, {400, 300}} {
		want := .2 + .0002*(float64(p[0])+ox) + .0001*(float64(p[1])+oy)
		if got := float64(out.at(0, p[0], p[1])); math.Abs(got-want) > 1e-6 {
			t.Fatalf("SIP was lost in rendering: got %.9f want %.9f", got, want)
		}
	}
	if _, ok := out.Hdr["A_ORDER"]; ok {
		t.Fatal("mosaic retained obsolete detector SIP")
	}
}
