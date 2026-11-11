package main

import (
	"image"
	"math"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

func TestPaletteFixedMappings(t *testing.T) {
	p := &palettePrepared{}
	want := [8][3]float64{{.7, .5, .2}, {.4, .2, .2}, {.3, .4, .2}, {.4, .3, .2}, {.4, .2, .3}, {.2, .4, .3}, {.2, .3, .4}, {.3, .2, .4}}
	for mode, v := range want {
		got := p.mapPalette(paletteParams{Mode: mode, Hue: 1}, .4, .2, .2, .3)
		for c := range got {
			if math.Abs(got[c]-v[c]) > 1e-12 {
				t.Fatalf("mode %d got %v want %v", mode, got, v)
			}
		}
	}
}

func TestForaxxUsesStretchedFactorsAndUnstretchedSignal(t *testing.T) {
	p := &palettePrepared{DynamicTone: [2]paletteFactorTone{{0, .25}, {0, .3}}}
	h, o, s := .08, .04, .03
	hv, ov := mtf(h, .25, 0, 1), mtf(o, .3, 0, 1)
	f := math.Pow(hv*ov, 1-hv*ov)
	fo := math.Pow(ov, 1-ov)
	for _, mode := range []int{8, 9} {
		got := p.mapPalette(paletteParams{Mode: mode}, h, o, o, s)
		r := h
		if mode == 9 {
			r = fo*s + (1-fo)*h
		}
		want := [3]float64{r, f*h + (1-f)*o, o}
		for c := range got {
			if math.Abs(got[c]-want[c]) > 1e-12 {
				t.Fatal("incorrect Foraxx", got, want)
			}
		}
	}
	for _, v := range []float64{-1, 0, 1e-30, .5, 1, 2} {
		p := palettePIP(v)
		if !paletteFinite(p) || p < 0 || p > 1 {
			t.Fatal("PIP boundary", v, p)
		}
	}
}

func TestTenLevelsAreDistinctAndPreserveSource(t *testing.T) {
	a, b := paletteTestInputs()
	before := append([]float32(nil), a.Pix...)
	p, err := preparePalette(a, b, false, false)
	if err != nil {
		t.Fatal(err)
	}
	base := paletteParams{Auto: true, ProtectSulfur: true, ProtectOxygen: true}
	for mode := 0; mode < paletteChoiceCount; mode++ {
		var previous paletteParams
		for level := 0; level < 10; level++ {
			s := p.preset(mode, level, base)
			if s.Mode != mode || s.Hydrogen < 0 || s.Hydrogen > 3 || s.Oxygen < 0 || s.Oxygen > 3 || s.Sulfur < 0 || s.Sulfur > 3 {
				t.Fatal("invalid preset", s)
			}
			if level > 0 && (s == previous || s.Hydrogen >= previous.Hydrogen || s.Oxygen < previous.Oxygen || s.Sulfur < previous.Sulfur) {
				t.Fatal("levels are duplicated or unordered", mode, level)
			}
			m := p.compose(s, 1200)
			for _, v := range m.Pix {
				if !paletteFinite(float64(v)) {
					t.Fatal("non-finite palette", mode, level)
				}
			}
			previous = s
		}
	}
	for i, v := range before {
		if a.Pix[i] != v {
			t.Fatal("gallery mutated source")
		}
	}
}

func TestPaletteChooserResponsiveLayout(t *testing.T) {
	u := &ui{th: material.NewTheme()}
	u.palette.chooser.stage = 1
	for i := range u.palette.chooser.images {
		u.palette.chooser.images[i] = paint.NewImageOp(image.NewRGBA(image.Rect(0, 0, 240, 180)))
		u.palette.chooser.params[i].Mode = i
	}
	for _, width := range []int{340, 680, 1000} {
		var ops op.Ops
		g := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(width, 640))}
		d := u.paletteChooserView(g)
		if d.Size.X != width || d.Size.Y != 640 {
			t.Fatal("gallery does not fit viewport", width, d.Size)
		}
	}
}
