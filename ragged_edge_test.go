package main

import (
	"math"
	"reflect"
	"testing"
)

func TestRaggedEdgeGeometry(t *testing.T) {
	a, b := &Tile{Img: newImg(240, 180, 1)}, &Tile{Img: newImg(240, 180, 1)}
	s, c := math.Sincos(.02)
	for _, shift := range []Aff{{c, -s, 165, s, c, 0}, {c, -s, 0, s, c, 120}} {
		tiles := []*Tile{a, b}
		W := []Warp{newWarp(identity, 0, a.Img), newWarp(shift, 0, b.Img)}
		straight := seamCuts(tiles, W, .15)
		ragged := seamCutsWithRaggedEdge(tiles, W, .15, true)
		if !reflect.DeepEqual(ragged, seamCutsWithRaggedEdge(tiles, W, .15, true)) {
			t.Fatal("seam must be repeatable across runs")
		}
		ca, cb := ragged[0].cuts[0], ragged[1].cuts[0]
		width := ca.c + cb.c
		minOffset, maxOffset := math.Inf(1), math.Inf(-1)
		for i := -180; i <= 360; i++ {
			X, Y := float64(i), float64(i)*.37
			if math.Abs(ca.distance(X, Y)+cb.distance(X, Y)-width) > 1e-10 {
				t.Fatal("opposite boundaries no longer share a constant blend width")
			}
			offset := ca.distance(X, Y) - straight[0].cuts[0].distance(X, Y)
			if math.Abs(offset) > width*.2+1e-10 {
				t.Fatal("ragged edge escaped its displacement limit")
			}
			minOffset, maxOffset = math.Min(minOffset, offset), math.Max(maxOffset, offset)
		}
		if maxOffset-minOffset < 1 {
			t.Fatal("enabled edge stayed straight")
		}
		if !reflect.DeepEqual(seamCuts(tiles, W, 0), seamCutsWithRaggedEdge(tiles, W, 0, true)) {
			t.Fatal("zero overlap must retain the original butt joint")
		}
	}
}

func TestRaggedEdgeFourTileCoverage(t *testing.T) {
	var tiles []*Tile
	var W []Warp
	ox, oy := 0.0, 0.0
	for i, shift := range []Aff{{1, 0, 0, 0, 1, 0}, {1, -.02, 70, .02, 1, 0}, {1, .02, 0, -.02, 1, 70}, {1, 0, 70, 0, 1, 70}} {
		m := newImg(96, 96, 3)
		for c := 0; c < m.C; c++ {
			for p := 0; p < m.W*m.H; p++ {
				m.Pix[c*m.W*m.H+p] = float32(c+1) / 8
			}
		}
		tiles = append(tiles, &Tile{Img: m, Row: i / 2, Col: i % 2})
		W = append(W, newWarp(shift, 0, m))
		for _, p := range [][2]float64{{0, 0}, {95, 0}, {0, 95}, {95, 95}} {
			X, Y := shift.apply(p[0], p[1])
			ox, oy = math.Min(ox, X), math.Min(oy, Y)
		}
	}
	ox, oy = math.Floor(ox), math.Floor(oy)
	for _, overlap := range []float64{.01, .15, .5} {
		out := render(tiles, W, seamCutsWithRaggedEdge(tiles, W, overlap, true))
		for y := 0; y < out.H; y++ {
			for x := 0; x < out.W; x++ {
				covered := false
				for i, tile := range tiles {
					u, v := W[i].inv(float64(x)+ox, float64(y)+oy)
					covered = covered || inside(tile.Img, u, v)
				}
				if !covered {
					continue
				}
				for c := 0; c < out.C; c++ {
					v := float64(out.at(c, x, y))
					if math.IsNaN(v) || math.Abs(v-float64(c+1)/8) > 1e-6 {
						t.Fatalf("overlap %g: hole or brightness change at %d,%d channel %d: %g", overlap, x, y, c, v)
					}
				}
			}
		}
	}
}
