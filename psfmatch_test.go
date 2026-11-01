package main

import (
	"math"
	"os"
	"slices"
	"strings"
	"testing"
)

func psfTestTile(sigmas []float64) *Img {
	m := newImg(400, 400, len(sigmas))
	for i := range m.Pix {
		m.Pix[i] = .03
	}
	for c, sigma := range sigmas {
		for y := 45; y <= 355; y += 45 {
			for x := 45; x <= 355; x += 45 {
				for dy := -14; dy <= 14; dy++ {
					for dx := -14; dx <= 14; dx++ {
						u, v := float64(dx)-.23, float64(dy)-.37
						m.Pix[c*m.W*m.H+(y+dy)*m.W+x+dx] += float32(6 / (2 * math.Pi * sigma * sigma) * math.Exp(-.5*(u*u+v*v)/(sigma*sigma)))
					}
				}
			}
		}
	}
	return m
}

func TestPSFMatchingTilesAndChannels(t *testing.T) {
	a, b := psfTestTile([]float64{1.4, 1.7, 2}), psfTestTile([]float64{1.8, 2.2, 1.6})
	a.Hdr["TEST"] = "kept"
	a.BottomUp = true
	beforeA, beforeB := slices.Clone(a.Pix), slices.Clone(b.Pix)
	tiles := []*Tile{{Img: a, Name: "A"}, {Img: b, Name: "B"}}
	W := []Warp{newWarp(identity, 0, a), newWarp(identity, 0, b)}
	out, r := matchPSF(tiles, W)
	t.Log(r.note)
	if !r.applied || math.Abs(r.target-2.2*gaussianFWHM) > .03 {
		t.Fatalf("matching failed: %+v", r)
	}
	for i := range out {
		for c, p := range r.channels[i] {
			if math.Abs(p.after-r.target) > .04 {
				t.Fatalf("tile %d channel %d: %.4f want %.4f", i, c, p.after, r.target)
			}
			n := out[i].W * out[i].H
			var sourceSum, resultSum float64
			for j := 0; j < n; j++ {
				sourceSum += float64(tiles[i].Img.Pix[c*n+j])
				resultSum += float64(out[i].Pix[c*n+j])
			}
			if math.Abs(sourceSum-resultSum) > .003 {
				t.Fatalf("flux changed by %f", resultSum-sourceSum)
			}
		}
	}
	if !slices.Equal(beforeA, a.Pix) || !slices.Equal(beforeB, b.Pix) {
		t.Fatal("source image was modified")
	}
	if out[0].Hdr["TEST"] != "kept" || !out[0].BottomUp {
		t.Fatal("source metadata was lost")
	}
	// The broadest channel itself is copied without smoothing.
	n := b.W * b.H
	if !slices.Equal(out[1].Pix[n:2*n], b.Pix[n:2*n]) {
		t.Fatal("already broadest channel was blurred")
	}
}

func TestPSFMatchingUsesMosaicScale(t *testing.T) {
	a, b := psfTestTile([]float64{1.4}), psfTestTile([]float64{2})
	W := []Warp{newWarp(Aff{1.5, 0, 0, 0, 1.5, 0}, 0, a), newWarp(identity, 0, b)}
	out, r := matchPSF([]*Tile{{Img: a}, {Img: b}}, W)
	if !r.applied || out[0] != a || out[1] == b {
		t.Fatalf("pixel scales were ignored: %+v", r)
	}
	if math.Abs(r.target-1.5*1.4*gaussianFWHM) > .03 {
		t.Fatal("PSF target was computed in source pixels")
	}
	if math.Abs(r.channels[1][0].after-r.target) > .04 {
		t.Fatal("scaled PSFs did not match")
	}
}

func TestPSFMatchingSkipsEqualAndUnmeasurable(t *testing.T) {
	a := psfTestTile([]float64{2, 2, 2})
	out, r := matchPSF([]*Tile{{Img: a}}, []Warp{newWarp(identity, 0, a)})
	if r.applied || out[0] != a || !strings.Contains(r.note, "уже согласована") {
		t.Fatal("equal channels were smoothed")
	}
	b := newImg(100, 100, 3)
	out, r = matchPSF([]*Tile{{Img: a}, {Img: b}}, []Warp{newWarp(identity, 0, a), newWarp(identity, 0, b)})
	if r.applied || out[0] != a || out[1] != b || !strings.Contains(r.note, "не применено") {
		t.Fatal("partially measured set was smoothed")
	}
}

func TestGaussianPSFMaskAndLinearRange(t *testing.T) {
	const w, h = 80, 60
	src := make([]float32, w*h)
	for i := range src {
		src[i] = .2
		if i%w < 10 {
			src[i] = 0
		}
	}
	src[30*w+40] = 0
	src[20*w+50] = float32(math.NaN())
	out := gaussianPSFPlane(src, w, h, 1.7)
	for i, v := range out {
		if src[i] == 0 || math.IsNaN(float64(src[i])) {
			if v != 0 {
				t.Fatal("invalid border was filled")
			}
			continue
		}
		if math.Abs(float64(v)-.2) > 1e-6 {
			t.Fatal("masked boundary darkened a flat field")
		}
	}
	for i := range src {
		src[i] = -.1
	}
	out = gaussianPSFPlane(src, w, h, 1.7)
	for _, v := range out {
		if math.Abs(float64(v)+.1) > 1e-6 {
			t.Fatal("negative linear samples were clipped")
		}
	}
}

func TestPSFMatchingReal(t *testing.T) {
	if testing.Short() {
		t.Skip("real tile")
	}
	var tiles []*Tile
	var W []Warp
	for _, name := range []string{"1-1", "1-2", "2_1", "2_2"} {
		m, err := loadImage(`C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\` + name + `.xisf`)
		if os.IsNotExist(err) {
			t.Skip(err)
		}
		if err != nil {
			t.Fatal(err)
		}
		tiles = append(tiles, &Tile{Img: m, Name: name})
		W = append(W, newWarp(identity, 0, m))
	}
	out, r := matchPSF(tiles, W)
	for i := range tiles {
		t.Log(tiles[i].Name, r.tileNote(i))
	}
	if len(out) != len(tiles) || len(r.channels) != len(tiles) {
		t.Fatal("missing real-tile report")
	}
	for i := range tiles {
		if !r.applied && out[i] != tiles[i].Img {
			t.Fatal("failed matching changed the original")
		}
		if r.applied {
			for _, p := range r.channels[i] {
				if math.Abs(p.after-r.target) > math.Max(.15, .08*r.target) {
					t.Fatal("real PSF failed its post-check")
				}
			}
		}
	}
}
