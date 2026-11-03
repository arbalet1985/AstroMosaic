package main

import (
	"image"
	"math"
	"strings"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget/material"
)

func TestPSFModeChangeKeepsEditedResult(t *testing.T) {
	original, cropped := newImg(64, 64, 3), newImg(32, 32, 3)
	u := &ui{result: cropped, baseline: original, history: []*Img{original}, view: 2, rows: 1, cols: 1, tiles: map[cell]*tileState{{0, 0}: {img: original}}}
	u.psfMatch.Value = true
	var ops op.Ops
	var router input.Router
	g := layout.Context{Ops: &ops, Constraints: layout.Exact(image.Pt(240, 45)), Source: router.Source()}
	material.Button(material.NewTheme(), &u.psfModeBtn, "PSF").Layout(g)
	router.Frame(&ops)
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(20, 20)}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(20, 20)})
	u.handle(g)
	if !u.psfField || u.busy || u.result != cropped || u.baseline != original || len(u.history) != 1 || !strings.Contains(u.status, "Настройки изменены") {
		t.Fatal("PSF mode rebuilt or discarded edits", u.status)
	}
	u.requestStitch()
	if !u.stitchPending || u.busy || u.result != cropped {
		t.Fatal("explicit rebuild bypassed protection")
	}
	u.stitchSettingsChanged()
	if u.stitchPending || len(u.history) != 1 {
		t.Fatal("changed settings retained stale confirmation or lost undo")
	}
}

func TestWideApertureSeparatesRedistributionFromFluxGain(t *testing.T) {
	before, after := newImg(160, 160, 1), newImg(160, 160, 1)
	for y := 0; y < 160; y++ {
		for x := 0; x < 160; x++ {
			dx, dy := float64(x)-80.2, float64(y)-80.3
			r2 := dx*dx + dy*dy
			bg := .01 + float64(x)*1e-6 + float64(y)*2e-6
			gaussian := func(s float64) float64 { return math.Exp(-r2/(2*s*s)) / (2 * math.Pi * s * s) }
			before.Pix[y*160+x] = float32(bg + .85*gaussian(3.2) + .15*gaussian(7))
			after.Pix[y*160+x] = float32(bg + .95*gaussian(2.5) + .05*gaussian(7))
		}
	}
	a, b := apertureFlux(before, 0, 80.2, 80.3), apertureFlux(after, 0, 80.2, 80.3)
	if math.Abs(b/a-1) > .01 {
		t.Fatalf("redistribution falsely changed flux: %g", b/a)
	}
	for i := range after.Pix {
		after.Pix[i] *= 1.2
	}
	gain := apertureFlux(after, 0, 80.2, 80.3) / a
	if gain < 1.18 || gain > 1.22 {
		t.Fatalf("true flux gain was missed: %g", gain)
	}
}

func TestRestorationStillRejectsRealFluxGain(t *testing.T) {
	before, after := psfTestTile([]float64{2.5}), psfTestTile([]float64{2})
	for i, v := range after.Pix {
		after.Pix[i] = .03 + 1.2*(v-.03)
	}
	note, err := assessRestoration(before, after, nil)
	if err == nil || !strings.Contains(err.Error(), "потока") {
		t.Fatalf("true photometric gain was accepted: %v; %s", err, note)
	}
}
