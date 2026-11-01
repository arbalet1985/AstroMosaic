package main

import (
	"math"
	"math/rand"
	"strings"
	"testing"
)

func lensMatches(t *testing.T, model lensModel, noise float64) ([]*Tile, []Aff, []match, []match, []match) {
	t.Helper()
	a, b := newImg(640, 480, 1), newImg(640, 480, 1)
	init := []Aff{identity, {1, 0, 160, 0, 1, 11}}
	tiles := []*Tile{{Img: a}, {Img: b}}
	W := solveWarpsModel(tiles, init, nil, model)
	W[1].S, W[1].Si = init[1], init[1].inv()
	rng := rand.New(rand.NewSource(27))
	var train, validation, all []match
	for i := 0; i < 500; i++ {
		x, y := 200+rng.Float64()*400, 30+rng.Float64()*400
		u, v := W[1].inv(W[0].apply(x, y))
		if u < 10 || v < 10 || u > 629 || v > 469 {
			continue
		}
		m := match{0, 1, [2]float64{x + noise*rng.NormFloat64(), y + noise*rng.NormFloat64()}, [2]float64{u, v}}
		all = append(all, m)
		if i%5 == 0 {
			validation = append(validation, m)
		} else {
			train = append(train, m)
		}
	}
	return tiles, init, train, validation, all
}

func TestExtendedLensValidation(t *testing.T) {
	tiles, init, train, validation, all := lensMatches(t, lensModel{.007, -.018, .004, -.003}, .01)
	trainW, kept, k := refine(tiles, init, train)
	finalW, allKept, _ := refine(tiles, init, all)
	if !canFitExtraLens(tiles, kept) {
		t.Fatal("well-covered field was rejected")
	}
	selected, error, note := validatedExtraLens(tiles, init, kept, validation, allKept, trainW, finalW, k)
	t.Log(note)
	if selected == nil || error > .1 || error > .5*rms(trainW, validation) {
		t.Fatalf("extended lens failed validation: %f, %s", error, note)
	}
	for _, p := range [][2]float64{{0, 0}, {639, 479}, {321.2, 123.4}} {
		x, y := selected[1].inv(selected[1].apply(p[0], p[1]))
		if math.Hypot(x-p[0], y-p[1]) > 1e-5 {
			t.Fatal("extended lens did not invert")
		}
	}
}

func TestExtendedLensRejectsNoiseAndPoorCoverage(t *testing.T) {
	tiles, init, train, validation, all := lensMatches(t, lensModel{.005, 0, 0, 0}, .1)
	trainW, kept, k := refine(tiles, init, train)
	finalW, allKept, _ := refine(tiles, init, all)
	selected, _, note := validatedExtraLens(tiles, init, kept, validation, allKept, trainW, finalW, k)
	t.Log(note)
	if selected != nil || !strings.Contains(note, "оставлена") {
		t.Fatal("noise was turned into distortion")
	}
	if canFitExtraLens(tiles, train[:30]) {
		t.Fatal("insufficient stars were accepted")
	}
	for i := range train {
		train[i].pA, train[i].pB = [2]float64{10, 10}, [2]float64{11, 10}
	}
	if canFitExtraLens(tiles, train) {
		t.Fatal("insufficient field coverage was accepted")
	}
	tiles, _, train, _, _ = lensMatches(t, lensModel{}, 0)
	for _, tile := range tiles {
		tile.Img.Hdr = map[string]string{"CRPIX1": "320", "CRPIX2": "240", "A_ORDER": "2", "B_ORDER": "2"}
	}
	if canFitExtraLens(tiles, train) {
		t.Fatal("additional lens model was fitted over SIP")
	}
}

func TestValidationKeepsStarTracksTogether(t *testing.T) {
	var ms []match
	for i := 0; i < 50; i++ {
		p, q, r := [2]float64{float64(i), 10}, [2]float64{float64(i) + 2, 12}, [2]float64{float64(i) + 5, 9}
		ms = append(ms, match{0, 1, p, q}, match{1, 2, q, r}, match{0, 2, p, r})
	}
	train, validation := splitMatchTracks(ms)
	if len(train) != 120 || len(validation) != 30 {
		t.Fatalf("unexpected split %d/%d", len(train), len(validation))
	}
	type point struct {
		i int
		p [2]float64
	}
	seen := map[point]bool{}
	for _, m := range train {
		seen[point{m.A, m.pA}], seen[point{m.B, m.pB}] = true, true
	}
	for _, m := range validation {
		if seen[point{m.A, m.pA}] || seen[point{m.B, m.pB}] {
			t.Fatal("same star entered training and validation")
		}
	}
}

func TestTANExtendedLensRoundTrip(t *testing.T) {
	a, b := wcsTile(80, 65), wcsTile(85, 67)
	for _, m := range []*Img{a, b} {
		m.Hdr["CD1_1"], m.Hdr["CD2_2"] = "-0.03", "0.03"
	}
	W := initialWarps([]*Tile{{Img: a}, {Img: b}}, .01)
	W[1].extra = [3]float64{-.02, .003, -.002}
	for _, p := range [][2]float64{{0, 0}, {999, 799}, {123.4, 567.8}, {900, 20}} {
		x, y := W[1].inv(W[1].apply(p[0], p[1]))
		if math.Hypot(x-p[0], y-p[1]) > 1e-5 {
			t.Fatal("TAN and extended lens failed to invert together")
		}
	}
}
