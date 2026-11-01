package main

import "testing"

func TestSingleFrameKeepsNativePixels(t *testing.T) {
	m := psfTestTile([]float64{2.1, 2.2, 2.3})
	m.Hdr = map[string]string{"OBJECT": "single-frame", "A_ORDER": "2", "B_ORDER": "2", "A_2_0": "0.001"}
	before := append([]float32(nil), m.Pix...)
	out, d, _ := processSingleFrame(&Tile{Img: m, Name: "single"}, false, false)
	if out.W != m.W || out.H != m.H || len(d.tiles) != 1 || out.Hdr["OBJECT"] != "single-frame" {
		t.Fatal("single frame lost dimensions or metadata")
	}
	for i, v := range before {
		if out.Pix[i] != v {
			t.Fatal("native single-frame pixels resampled")
		}
	}
}
