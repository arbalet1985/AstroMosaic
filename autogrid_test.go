package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wcsTile is a 1000×800 tile whose centre looks at (ra, dec), 1"/px, rows stored bottom-up like FITS.
func wcsTile(ra, dec float64) *Img {
	m := newImg(1000, 800, 1)
	m.BottomUp = true
	for k, v := range map[string]string{"CRVAL1": fmt.Sprint(ra), "CRVAL2": fmt.Sprint(dec), "CRPIX1": "500.5", "CRPIX2": "400.5",
		"CD1_1": "-0.000277778", "CD1_2": "0", "CD2_1": "0", "CD2_2": "0.000277778"} {
		m.Hdr[k] = v
	}
	return m
}

func TestAutoGrid(t *testing.T) {
	// 2×3 mosaic, 15% overlap; east is left, north is up. Shuffled input order.
	step := func(px float64) float64 { return px * 0.85 / 3600 }
	type tc struct{ r, c int }
	want := []tc{{1, 2}, {0, 0}, {1, 0}, {0, 2}, {0, 1}, {1, 1}}
	var imgs []*Img
	for _, w := range want {
		imgs = append(imgs, wcsTile(80-step(1000*float64(w.c)), 20-step(800*float64(w.r))))
	}
	cells, rows, cols, how := autoGrid(imgs, nil)
	if rows != 2 || cols != 3 || how != "по WCS" {
		t.Fatalf("grid %d×%d %s", rows, cols, how)
	}
	for i, w := range want {
		if cells[i] != (cell{w.r, w.c}) {
			t.Fatalf("tile %d in %v, want %v (all %v)", i, cells[i], w, cells)
		}
	}

	// no WCS: ASIAIR-style folder names, file names win when they carry the cell
	flat := []*Img{newImg(4, 4, 1), newImg(4, 4, 1), newImg(4, 4, 1)}
	cells, rows, cols, how = autoGrid(flat, []string{`D:\a\NGC 6960_2-1\result.fit`, `D:\a\NGC 6960_1-1\result.fit`, `D:\a\x\panel_1_2.fits`})
	if how != "по именам файлов" || rows != 2 || cols != 2 || cells[0] != (cell{1, 0}) || cells[1] != (cell{0, 0}) || cells[2] != (cell{0, 1}) {
		t.Fatalf("by name: %v %d×%d %s", cells, rows, cols, how)
	}

	// nothing to go on: row-major in chosen order
	cells, rows, cols, how = autoGrid(flat, []string{"a.fit", "b.fit", "c.fit"})
	if how != "по порядку выбора" || rows != 2 || cols != 2 || cells[2] != (cell{1, 0}) {
		t.Fatalf("by order: %v %d×%d %s", cells, rows, cols, how)
	}
}

// Two frames of the same field (e.g. two filters), the second 20 px further east = left on screen,
// while the grid says it is to the right. WCS must win, and the overlap crop must leave both whole.
func TestWCSOverridesGrid(t *testing.T) {
	a := wcsTile(80, 20)
	b := wcsTile(80+20.0/3600/math.Cos(20*math.Pi/180), 20)
	cells, _, _, how := autoGrid([]*Img{a, b}, []string{"ho.fit", "so.fit"})
	if !strings.Contains(how, "одного и того же поля") {
		t.Fatalf("same field not reported: %v %s", cells, how)
	}
	tiles := []*Tile{{Img: a, Name: "A"}, {Img: b, Col: 1, Name: "B"}}
	W, notes, _ := place(tiles, nil, 0.15)
	x, y := W[1].apply(499.5, 399.5)
	if math.Abs(x-479.5) > 0.5 || math.Abs(y-399.5) > 0.5 {
		t.Fatalf("B centre at %.1f,%.1f, want 479.5,399.5 (%v)", x, y, notes)
	}
	for i, r := range seamCuts(tiles, W, 0.15) {
		if len(r.cuts) > 0 {
			t.Fatalf("same-field frame %d cropped to %v", i, r)
		}
	}
}

// TestAutoGridReal runs on the author's tiles when present.
func TestAutoGridReal(t *testing.T) {
	dir := `C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic`
	names := []string{"NGC 6960_1-1", "NGC 6960_1-2", "C 33", "NGC 6960_2-2"}
	var imgs []*Img
	var paths []string
	for _, n := range names {
		p := filepath.Join(dir, n, "result.fit")
		if _, err := os.Stat(p); err != nil || testing.Short() {
			t.Skip("real tiles not available")
		}
		m, err := loadImage(p)
		if err != nil {
			t.Fatal(err)
		}
		imgs, paths = append(imgs, m), append(paths, p)
	}
	cells, rows, cols, how := autoGrid(imgs, paths)
	t.Logf("%d×%d %s: %v", rows, cols, how, cells)
	if rows != 2 || cols != 2 || how != "по WCS" {
		t.Fatalf("grid %d×%d %s", rows, cols, how)
	}
	tiles := make([]*Tile, len(imgs))
	for i, m := range imgs {
		tiles[i] = &Tile{Img: m, Row: cells[i][0], Col: cells[i][1], Name: names[i]}
	}
	_, notes, weak := place(tiles, nil, 0.15)
	t.Log(notes)
	if len(weak) > 0 {
		t.Fatalf("weak pairs %v", weak)
	}
}

// TestGridByStars: a 2×2 mosaic given in shuffled order, tiles turned by up to 1.5°, no WCS or names.
func TestGridByStars(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	var stars [][3]float64
	for i := 0; i < 2500; i++ {
		stars = append(stars, [3]float64{rng.Float64() * 800, rng.Float64() * 720, 0.1 + rng.Float64()})
	}
	rot := func(deg, tx, ty float64) Aff {
		s, c := math.Sincos(deg * math.Pi / 180)
		return Aff{c, -s, tx, s, c, ty}
	}
	pos := map[cell]Aff{{0, 0}: rot(0, 10, 10), {0, 1}: rot(1, 340, 15), {1, 0}: rot(-0.5, 12, 320), {1, 1}: rot(1.5, 345, 318)}
	order := []cell{{1, 1}, {0, 0}, {1, 0}, {0, 1}}
	var imgs []*Img
	for _, k := range order {
		imgs = append(imgs, mkTile(400, 360, pos[k], 0, stars, 1, noBg).Img)
	}
	got, ok := gridByStars(imgs)
	if !ok {
		t.Fatal("no layout")
	}
	for i, k := range order {
		if got[i] != k {
			t.Fatalf("got %v, want %v", got, order)
		}
	}
}
