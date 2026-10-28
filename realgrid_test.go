package main

import (
	"os"
	"strings"
	"testing"
)

// TestGridByStarsReal lays out the real C33 2×2 tiles (skipped when absent) without WCS and
// compares with the WCS layout.
func TestGridByStarsReal(t *testing.T) {
	dir := `C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\`
	var imgs []*Img
	for _, d := range []string{"NGC 6960_2-2", "C 33", "NGC 6960_1-1", "NGC 6960_1-2"} {
		m, err := loadImage(dir + d + `\result.fit`)
		if err != nil {
			if os.IsNotExist(err) {
				t.Skip(err)
			}
			t.Fatal(err)
		}
		imgs = append(imgs, m)
	}
	want, ok := gridByWCS(imgs)
	if !ok {
		t.Fatal("no WCS layout")
	}
	got, ok := gridByStars(imgs)
	t.Log("WCS:", want, "stars:", got)
	if !ok {
		t.Fatal("stars: no layout")
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("tile %d: stars %v, WCS %v", i, got[i], want[i])
		}
	}
}

// TestRandomNamesReal: the same 2×2 as xisf without WCS and with file names that do not match the
// sky (skipped when absent). Layout and links must come from the stars; a grid guess misplaced
// the tiles by ~200 px and left black discs round the stars in the overlaps.
func TestRandomNamesReal(t *testing.T) {
	dir := `C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\`
	var imgs []*Img
	var paths []string
	for _, d := range []string{"1-1", "1-2", "2_1", "2_2"} {
		m, err := loadImage(dir + d + `.xisf`)
		if err != nil {
			t.Skip(err)
		}
		imgs, paths = append(imgs, m), append(paths, dir+d+`.xisf`)
	}
	cells, _, _, how := autoGrid(imgs, paths)
	t.Log(how, cells)
	var tiles []*Tile
	for i, m := range imgs {
		tiles = append(tiles, &Tile{Img: m, Row: cells[i][0], Col: cells[i][1], Name: d(paths[i])})
	}
	W, notes, weak := place(tiles, nil, 0.15)
	t.Log(notes)
	var ms []match
	for a := range tiles {
		for b := a + 1; b < len(tiles); b++ {
			for _, p := range matchStars(detectStars(tiles[a].Img), detectStars(tiles[b].Img), tiles[a].Img.W, tiles[a].Img.H, tiles[b].Img.W, tiles[b].Img.H, W[a].Si.mul(W[b].S), 8) {
				ms = append(ms, match{a, b, p.A, p.B})
			}
		}
	}
	if e := rms(W, ms); len(ms) < 300 || e > 2 || len(weak) > 0 {
		t.Fatalf("matches %d, rms %.2f px, weak pairs %v", len(ms), e, weak)
	}
}

func d(p string) string { return p[strings.LastIndex(p, `\`)+1:] }
