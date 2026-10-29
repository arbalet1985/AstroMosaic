package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// sky is a smooth synthetic field: background + gaussian stars.
func sky(x, y float64, stars [][3]float64) float32 {
	v := 0.1 + 0.0001*x
	for _, s := range stars {
		dx, dy := x-s[0], y-s[1]
		if math.Abs(dx) < 12 && math.Abs(dy) < 12 {
			v += s[2] * math.Exp(-(dx*dx+dy*dy)/8)
		}
	}
	return float32(v)
}

func randStars(n int, w, h float64) [][3]float64 {
	rng := rand.New(rand.NewSource(1))
	var stars [][3]float64
	for i := 0; i < n; i++ {
		stars = append(stars, [3]float64{rng.Float64() * w, rng.Float64() * h, 0.3 + 0.5*rng.Float64()})
	}
	return stars
}

// mkTile renders a w×h tile whose pixel p sees sky at T(D(p)), D = radial k1, then gain·v + bg(x,y).
func mkTile(w, h int, T Aff, k1 float64, stars [][3]float64, gain float32, bg func(x, y float64) float32) *Tile {
	m := newImg(w, h, 1)
	D := newWarp(T, k1, m)
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			X, Y := D.apply(float64(x), float64(y))
			m.Pix[y*m.W+x] = gain*sky(X, Y, stars) + bg(float64(x), float64(y))
		}
	}
	return &Tile{Img: m}
}

func noBg(x, y float64) float32 { return 0 }

// maxErr is the largest distance between tile b's true and fitted positions in the mosaic.
func maxErr(w Warp, truth Warp, m *Img) float64 {
	var e float64
	for _, p := range [][2]float64{{0, 0}, {float64(m.W - 1), 0}, {0, float64(m.H - 1)}, {float64(m.W - 1), float64(m.H - 1)}} {
		x, y := w.apply(p[0], p[1])
		X, Y := truth.apply(p[0], p[1])
		e = math.Max(e, math.Hypot(x-X, y-Y))
	}
	return e
}

var (
	bShift = func() Aff { s, c := math.Sincos(2 * math.Pi / 180); return Aff{c, -s, 180, s, c, 10} }()
	field  = randStars(300, 420, 300)
	tileW  = 250
	tileH  = 280
)

// renderErr checks the mosaic against the sky (in tile A's frame) wherever a tile covers it.
func renderErr(t *testing.T, tiles []*Tile, W []Warp, out *Img, k1 float64) float64 {
	ref := newWarp(identity, k1, tiles[0].Img)
	var e float64
	for y := 5; y < out.H-5; y++ {
		for x := 5; x < out.W-5; x++ {
			cov := false
			for i, tl := range tiles {
				u, v := W[i].inv(float64(x), float64(y))
				cov = cov || (u > 3 && v > 3 && u < float64(tl.Img.W-4) && v < float64(tl.Img.H-4))
			}
			if !cov {
				continue
			}
			X, Y := ref.S.apply(float64(x), float64(y))
			e = math.Max(e, math.Abs(float64(out.at(0, x, y)-sky(X, Y, field))))
		}
	}
	return e
}

func TestStitch(t *testing.T) {
	// tile A: identity crop; tile B: rotated 2° and shifted, +0.05 background offset
	a, b := mkTile(tileW, tileH, identity, 0, field, 1, noBg), mkTile(tileW, tileH, bShift, 0, field, 1, func(x, y float64) float32 { return 0.05 })
	a.Name, b.Name, b.Col = "A", "B", 1
	var pts []PointPair
	for _, p := range [][2]float64{{20, 30}, {50, 250}, {10, 140}} {
		X, Y := bShift.apply(p[0], p[1])
		pts = append(pts, PointPair{A: [2]float64{X, Y}, B: p})
	}
	tiles := []*Tile{a, b}
	W, notes, _ := place(tiles, map[pairKey][]PointPair{{0, 1}: pts}, 0.15)
	if e := maxErr(W[1], newWarp(bShift, 0, b.Img), b.Img); e > 0.3 {
		t.Fatalf("placement error %.3f px (%v)", e, notes)
	}
	for _, ov := range []float64{0.15, 0} { // 0: butt joint, one tile per pixel
		out := render(tiles, W, seamCuts(tiles, W, ov))
		// bilinear sampling of sharp stars is lossy; check background-level agreement
		if e := renderErr(t, tiles, W, out, 0); e > 0.08 {
			t.Fatalf("overlap %.2f: render error %.3f", ov, e)
		}
	}
}

func TestGridOnly(t *testing.T) {
	// no points, no WCS: grid guess is ~30 px off, stars must fix it
	a, b := mkTile(tileW, tileH, identity, 0, field, 1, noBg), mkTile(tileW, tileH, bShift, 0, field, 1, noBg)
	a.Name, b.Name, b.Col = "A", "B", 1
	W, notes, _ := place([]*Tile{a, b}, nil, 0.15)
	t.Log(notes)
	if e := maxErr(W[1], newWarp(bShift, 0, b.Img), b.Img); e > 0.3 {
		t.Fatalf("placement error %.3f px (%v)", e, notes)
	}
}

func TestDistortion(t *testing.T) {
	const k1 = 0.02 // ≈3.7 px at the corners
	stars := randStars(700, 420, 300)
	a, b := mkTile(tileW, tileH, identity, k1, stars, 1, noBg), mkTile(tileW, tileH, bShift, k1, stars, 1, noBg)
	a.Name, b.Name, b.Col = "A", "B", 1
	W, notes, _ := place([]*Tile{a, b}, nil, 0.15)
	t.Log(notes)
	if math.Abs(W[0].K-k1) > 0.004 {
		t.Fatalf("k1 = %.4f, want %.4f (%v)", W[0].K, k1, notes)
	}
	if e := maxErr(W[1], newWarp(bShift, k1, b.Img), b.Img); e > 0.5 {
		t.Fatalf("placement error %.3f px (%v)", e, notes)
	}
}

func TestCrop(t *testing.T) {
	// edge finds, along the line p0 + s·dir, the s where coverage by tile i switches (bisection).
	covers := func(R []region, W []Warp, i int, X, Y float64) bool {
		u, v := W[i].inv(X, Y)
		return R[i].has(u, v, X, Y)
	}
	edge := func(R []region, W []Warp, i int, p0, dir [2]float64, lo, hi float64) float64 {
		in := covers(R, W, i, p0[0]+lo*dir[0], p0[1]+lo*dir[1])
		for k := 0; k < 60; k++ {
			m := (lo + hi) / 2
			if covers(R, W, i, p0[0]+m*dir[0], p0[1]+m*dir[1]) == in {
				lo = m
			} else {
				hi = m
			}
		}
		return lo
	}
	turned := func(deg, tx, ty float64) Aff {
		s, c := math.Sincos(deg * math.Pi / 180)
		return Aff{c, -s, tx, s, c, ty}
	}
	a, b := &Tile{Img: newImg(200, 100, 1)}, &Tile{Img: newImg(200, 100, 1), Col: 1}
	// b is 140 px to the right and turned 1.5°: the old rectangular crop left wedges here
	W := []Warp{newWarp(identity, 0, a.Img), newWarp(turned(1.5, 140, 0), 0, b.Img)}
	for _, ov := range []float64{0, 0.1} {
		R := seamCuts([]*Tile{a, b}, W, ov)
		for _, y := range []float64{2, 25, 50, 75, 97} { // along the whole seam
			ea := edge(R, W, 0, [2]float64{100, y}, [2]float64{1, 0}, 0, 99) // a ends
			eb := edge(R, W, 1, [2]float64{100, y}, [2]float64{1, 0}, 99, 0) // b starts
			if got, want := ea-eb, ov*200; math.Abs(got-want) > 0.01 {
				t.Fatalf("overlap %.2f at y=%.0f: tiles overlap by %.3f px, want %.1f", ov, y, got, want)
			}
		}
	}
	// vertical pair, grid says below but the sky says above (bottom-up FITS rows), turned −1°
	c := &Tile{Img: newImg(200, 100, 1), Row: 1}
	W[1] = newWarp(turned(-1, 0, -70), 0, c.Img)
	R := seamCuts([]*Tile{a, c}, W, 0.1)
	for _, x := range []float64{3, 100, 196} {
		ea := edge(R, W, 0, [2]float64{x, 0}, [2]float64{0, 1}, 60, 0) // a starts (going up)
		ec := edge(R, W, 1, [2]float64{x, 0}, [2]float64{0, 1}, 0, 60) // c ends
		if got := ec - ea; math.Abs(got-10) > 0.01 {
			t.Fatalf("vertical pair at x=%.0f: overlap %.3f px, want 10", x, got)
		}
	}
}

func TestPhotometry(t *testing.T) {
	// B has gain 1.1 and an offset; background must be continuous across the seam
	a := mkTile(tileW, tileH, identity, 0, nil, 1, noBg)
	b := mkTile(tileW, tileH, bShift, 0, nil, 1.1, func(x, y float64) float32 { return 0.02 })
	a.Name, b.Name, b.Col = "A", "B", 1
	var pts []PointPair
	for _, p := range [][2]float64{{20, 30}, {50, 250}, {10, 140}} {
		X, Y := bShift.apply(p[0], p[1])
		pts = append(pts, PointPair{A: [2]float64{X, Y}, B: p})
	}
	tiles := []*Tile{a, b}
	W, _, _ := place(tiles, map[pairKey][]PointPair{{0, 1}: pts}, 0.15)
	out := render(tiles, W, seamCuts(tiles, W, 0.15))
	saved := field
	field = nil
	defer func() { field = saved }()
	if e := renderErr(t, tiles, W, out, 0); e > 0.005 {
		t.Fatalf("background error %.4f", e)
	}
}

func TestDebayer(t *testing.T) {
	// flat colour (R,G,B) = (0.8,0.5,0.2) sampled through RGGB, file stored bottom-up with odd height
	want := [3]float32{0.8, 0.5, 0.2}
	m := newImg(6, 5, 1)
	m.BottomUp = true
	m.Hdr["BAYERPAT"] = "RGGB"
	for y := 0; y < m.H; y++ {
		fy := m.H - 1 - y
		for x := 0; x < m.W; x++ {
			m.Pix[y*m.W+x] = want[map[byte]int{'R': 0, 'G': 1, 'B': 2}["RGGB"[(fy&1)*2+(x&1)]]]
		}
	}
	d := debayer(m, "RGGB")
	for c := 0; c < 3; c++ {
		for y := 0; y < d.H; y++ {
			for x := 0; x < d.W; x++ {
				if d.at(c, x, y) != want[c] {
					t.Fatalf("c%d (%d,%d) = %v, want %v", c, x, y, d.at(c, x, y), want[c])
				}
			}
		}
	}
}

func TestWCSRoundTrip(t *testing.T) {
	m := newImg(1000, 800, 1)
	m.BottomUp = true
	for k, v := range map[string]string{"CRVAL1": "83.8", "CRVAL2": "-5.4", "CRPIX1": "500", "CRPIX2": "400",
		"CD1_1": "-0.0003", "CD1_2": "0.00001", "CD2_1": "0.00001", "CD2_2": "0.0003"} {
		m.Hdr[k] = v
	}
	w := parseWCS(m)
	x, y := w.skyToPix(w.pixToSky(123.4, 567.8))
	if math.Abs(x-123.4) > 1e-6 || math.Abs(y-567.8) > 1e-6 {
		t.Fatalf("roundtrip %v %v", x, y)
	}
}

func TestFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := newImg(7, 5, 3)
	for i := range m.Pix {
		m.Pix[i] = float32(i) / float32(len(m.Pix))
	}
	for _, ext := range []string{".fits", ".tif", ".png"} {
		p := filepath.Join(dir, "x"+ext)
		if err := saveImage(p, m, true); err != nil {
			t.Fatal(err)
		}
		r, err := loadImage(p)
		if err != nil {
			t.Fatal(ext, err)
		}
		if r.W != 7 || r.H != 5 || r.C != 3 {
			t.Fatalf("%s geometry %dx%dx%d", ext, r.W, r.H, r.C)
		}
		for i := range m.Pix {
			if math.Abs(float64(r.Pix[i]-m.Pix[i])) > 1e-4 {
				t.Fatalf("%s pixel %d: %v vs %v", ext, i, r.Pix[i], m.Pix[i])
			}
		}
	}
	// minimal XISF: 2x1 mono UInt16 planar
	xmlh := `<xisf version="1.0"><Image geometry="2:1:1" sampleFormat="UInt16" location="attachment:%d:4"><FITSKeyword name="CRVAL1" value="10.5" comment=""/></Image></xisf>`
	h := fmt.Sprintf(xmlh, 512)
	var buf bytes.Buffer
	buf.WriteString("XISF0100")
	binary.Write(&buf, binary.LittleEndian, uint32(len(h)))
	buf.Write(make([]byte, 4))
	buf.WriteString(h)
	buf.Write(make([]byte, 512-buf.Len()))
	binary.Write(&buf, binary.LittleEndian, []uint16{0, 65535})
	p := filepath.Join(dir, "x.xisf")
	os.WriteFile(p, buf.Bytes(), 0o644)
	r, err := loadImage(p)
	if err != nil || r.Pix[1] != 1 || r.Hdr["CRVAL1"] != "10.5" {
		t.Fatalf("xisf: %v %v %v", err, r, r.Hdr)
	}
}

// TestNoDarkRing: a star that is much brighter in one tile (seeing, saturation) must not get a
// dark halo where the tiles overlap.
func TestNoDarkRing(t *testing.T) {
	shift := Aff{1, 0, 150, 0, 1, 0}
	starA := [][3]float64{{180, 100, 3}}
	starB := [][3]float64{{180, 100, 0.5}}
	a := mkTile(tileW, 200, identity, 0, starA, 1, noBg)
	b := mkTile(tileW, 200, shift, 0, starB, 1, noBg)
	rng := rand.New(rand.NewSource(2))
	for _, m := range []*Img{a.Img, b.Img} {
		for i := range m.Pix {
			m.Pix[i] += float32(0.005 * rng.NormFloat64())
		}
	}
	tiles := []*Tile{a, b}
	W := []Warp{newWarp(identity, 0, a.Img), newWarp(shift, 0, b.Img)}
	out := render(tiles, W, seamCuts(tiles, W, 0.4))
	// mean over a ring 8–18 px from the star vs the background just outside it
	var ring, bg, nr, nb float64
	for y := 60; y < 140; y++ {
		for x := 140; x < 220; x++ {
			d, v := math.Hypot(float64(x-180), float64(y-100)), float64(out.at(0, x, y))-0.0001*float64(x)
			switch {
			case d > 8 && d < 18:
				ring, nr = ring+v, nr+1
			case d > 26:
				bg, nb = bg+v, nb+1
			}
		}
	}
	if ring/nr < bg/nb-0.003 {
		t.Fatalf("dark ring: %.4f around the star vs background %.4f", ring/nr, bg/nb)
	}
}

// TestMatchBackground: tile B carries an extra offset and gradient; after matching, the backgrounds
// in the overlap agree.
func TestMatchBackground(t *testing.T) {
	shift := Aff{1, 0, 150, 0, 1, 20}
	a := mkTile(tileW, tileH, identity, 0, field, 1, noBg)
	b := mkTile(tileW, tileH, shift, 0, field, 1, func(x, y float64) float32 { return float32(0.03 + 0.0002*x - 0.0001*y) })
	rng := rand.New(rand.NewSource(3))
	for _, m := range []*Img{a.Img, b.Img} {
		for i := range m.Pix {
			m.Pix[i] += float32(0.003 * rng.NormFloat64())
		}
	}
	tiles := []*Tile{a, b}
	W := []Warp{newWarp(identity, 0, a.Img), newWarp(shift, 0, b.Img)}
	imgs, notes := matchBackground(tiles, W)
	t.Log(notes)
	diff := func(ma, mb *Img) float64 {
		var e float64
		for y := 40; y < 230; y += 5 {
			for x := 165; x < 240; x += 5 {
				u, v := W[1].inv(float64(x), float64(y))
				e = math.Max(e, math.Abs(float64(bilinear(mb, 0, u, v)-bilinear(ma, 0, float64(x), float64(y)))))
			}
		}
		return e
	}
	la, lb := background(a.Img), background(b.Img)
	ma, mb := background(imgs[0]), background(imgs[1])
	before, after := diff(la, lb), diff(ma, mb)
	t.Logf("background mismatch %.4f -> %.4f", before, after)
	if after > 0.003 {
		t.Fatalf("background still differs by %.4f (was %.4f)", after, before)
	}
}

// TestSeamNoise: two tiles of equal noise; the noise must stay even across the overlap. A wide
// detail crossfade averaged the two tiles into a quieter strip that a stretch shows as a light band.
func TestSeamNoise(t *testing.T) {
	shift := Aff{1, 0, 150, 0, 1, 0}
	a := mkTile(tileW, 200, identity, 0, nil, 1, noBg)
	b := mkTile(tileW, 200, shift, 0, nil, 1, noBg)
	rng := rand.New(rand.NewSource(4))
	for _, m := range []*Img{a.Img, b.Img} {
		for i := range m.Pix {
			m.Pix[i] += float32(0.01 * rng.NormFloat64())
		}
	}
	tiles := []*Tile{a, b}
	W := []Warp{newWarp(identity, 0, a.Img), newWarp(shift, 0, b.Img)}
	out := render(tiles, W, seamCuts(tiles, W, 0.4))
	lo, hi := math.Inf(1), 0.0
	for x := 20; x < out.W-28; x += 8 { // noise of 8-px columns, rows 20..180
		var s, s2, n float64
		for y := 20; y < 180; y++ {
			for xx := x; xx < x+8; xx++ {
				v := float64(out.at(0, xx, y))
				s, s2, n = s+v, s2+v*v, n+1
			}
		}
		sd := math.Sqrt(s2/n - (s/n)*(s/n))
		lo, hi = math.Min(lo, sd), math.Max(hi, sd)
	}
	if lo < 0.9*hi {
		t.Fatalf("noise dips to %.4f against %.4f across the seam", lo, hi)
	}
}
