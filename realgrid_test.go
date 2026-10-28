package main

import (
	"os"
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
