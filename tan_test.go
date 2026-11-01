package main

import (
	"math"
	"testing"
)

func TestTANProjectiveAgainstSky(t *testing.T) {
	for _, centres := range [][4]float64{{359.8, 65, .2, 66}, {20, 89, 150, 88}, {80, 20, 85, 24}} {
		for _, flip := range []bool{false, true} {
			a, b := wcsTile(centres[0], centres[1]), wcsTile(centres[2], centres[3])
			a.BottomUp, b.BottomUp = flip, !flip
			a.Hdr["CD1_1"], a.Hdr["CD2_2"] = "-0.02", "0.02"
			b.Hdr["CD1_1"], b.Hdr["CD2_2"], b.Hdr["CD1_2"], b.Hdr["CD2_1"] = "-0.018", "0.021", "0.004", "0.003"
			b.Hdr["A_ORDER"], b.Hdr["B_ORDER"], b.Hdr["A_2_0"], b.Hdr["B_1_1"] = "2", "2", "2e-6", "-3e-6"
			ref, source := parseWCS(a), parseWCS(b)
			W := initialWarps([]*Tile{{Img: a}, {Img: b}}, 0)
			if W[0].plane == nil || W[1].plane == nil {
				t.Fatal("TAN projection not enabled")
			}
			for y := 0.; y <= 799; y += 79.9 {
				for x := 0.; x <= 999; x += 99.9 {
					wantX, wantY := ref.skyToPix(source.pixToSky(x, y))
					X, Y := W[1].apply(x, y)
					if math.Hypot(X-wantX, Y-wantY) > 1e-7 {
						t.Fatalf("TAN differs from sky transform by %f px", math.Hypot(X-wantX, Y-wantY))
					}
					u, v := W[1].inv(X, Y)
					if math.Hypot(u-x, v-y) > 1e-5 {
						t.Fatal("TAN+SIP roundtrip failed")
					}
				}
			}
		}
	}
}

func TestTANWideFieldFitAndMatching(t *testing.T) {
	a, b := wcsTile(80, 65), wcsTile(85, 67)
	for _, m := range []*Img{a, b} {
		m.Hdr["CD1_1"], m.Hdr["CD2_2"] = "-0.03", "0.03"
	}
	tiles := []*Tile{{Img: a}, {Img: b}}
	truth := initialWarps(tiles, 0)
	var matches []match
	var sa, sb []star
	var src, dst [][2]float64
	for y := 40.; y < 760; y += 50 {
		for x := 40.; x < 960; x += 50 {
			X, Y := truth[1].apply(x, y)
			u, v := truth[0].inv(X, Y)
			if u < 10 || v < 10 || u > 989 || v > 789 {
				continue
			}
			matches = append(matches, match{0, 1, [2]float64{u, v}, [2]float64{x, y}})
			sa, sb = append(sa, star{u, v, 1}), append(sb, star{x, y, 1})
			src, dst = append(src, [2]float64{x, y}), append(dst, [2]float64{u, v})
		}
	}
	if len(matches) < 100 {
		t.Fatal("test has too little overlap")
	}
	guess := fitSimilarity(src, dst)
	var oldError float64
	for i, p := range src {
		x, y := guess.apply(p[0], p[1])
		oldError += math.Pow(x-dst[i][0], 2) + math.Pow(y-dst[i][1], 2)
	}
	oldError = math.Sqrt(oldError / float64(len(src)))
	if oldError < 1 {
		t.Fatalf("field does not exercise projection error: %f", oldError)
	}
	W, kept, _ := refine(tiles, []Aff{identity, guess}, matches)
	if rms(W, kept) > .001 {
		t.Fatalf("TAN fit error %f", rms(W, kept))
	}
	got := matchStarsMapped(sa, sb, a.W, a.H, b.W, b.H, func(x, y float64) (float64, float64) { return truth[0].inv(truth[1].apply(x, y)) }, 8)
	if len(got) < len(matches)*9/10 {
		t.Fatalf("TAN matcher kept %d of %d", len(got), len(matches))
	}
	t.Logf("similarity-only RMS %.3f px; exact TAN RMS %.6f px; matches %d", oldError, rms(W, kept), len(got))
}

func TestTANUnsupportedAndHorizon(t *testing.T) {
	a, b := wcsTile(0, 0), wcsTile(180, 0)
	W := initialWarps([]*Tile{{Img: a}, {Img: b}}, 0)
	if W[1].plane != nil {
		t.Fatal("back of the tangent plane was projected")
	}
	b.Hdr["CTYPE1"], b.Hdr["CTYPE2"] = "RA---SIN", "DEC--SIN"
	if parseWCS(b) != nil {
		t.Fatal("SIN was interpreted as TAN")
	}
	plain := newImg(20, 20, 1)
	if W := initialWarps([]*Tile{{Img: plain}, {Img: a}}, 0); W[1].plane != nil {
		t.Fatal("TAN enabled without a reference WCS")
	}
}

func TestTANRender(t *testing.T) {
	a, b := wcsTile(80, 65), wcsTile(85, 67)
	for _, m := range []*Img{a, b} {
		m.Hdr["CD1_1"], m.Hdr["CD2_2"] = "-0.03", "0.03"
	}
	W := initialWarps([]*Tile{{Img: a}, {Img: b}}, 0)
	w := W[1]
	for y := 0; y < b.H; y++ {
		for x := 0; x < b.W; x++ {
			X, Y := w.apply(float64(x), float64(y))
			b.Pix[y*b.W+x] = float32(.3 + .00005*X + .00002*Y)
		}
	}
	out := render([]*Tile{{Img: b}}, []Warp{w}, []region{full(b)})
	ox, oy := math.Inf(1), math.Inf(1)
	for _, p := range [][2]float64{{0, 0}, {999, 0}, {0, 799}, {999, 799}} {
		X, Y := w.apply(p[0], p[1])
		ox, oy = math.Min(ox, X), math.Min(oy, Y)
	}
	ox, oy = math.Floor(ox), math.Floor(oy)
	for _, p := range [][2]float64{{100, 100}, {500, 400}, {900, 700}} {
		X, Y := w.apply(p[0], p[1])
		x, y := int(math.Round(X-ox)), int(math.Round(Y-oy))
		want := .3 + .00005*(float64(x)+ox) + .00002*(float64(y)+oy)
		if got := float64(out.at(0, x, y)); math.Abs(got-want) > 1e-6 {
			t.Fatalf("TAN not applied in rendering: %.8f want %.8f", got, want)
		}
	}
}
