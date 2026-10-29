package main

import (
	"image"
	"image/png"
	"os"
	"testing"

	"gioui.org/font/gofont"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// TestShot renders the toolbar to SHOT (skipped without it).
func TestShot(t *testing.T) {
	path := os.Getenv("SHOT")
	if path == "" {
		t.Skip("set SHOT")
	}
	sz := image.Pt(1600, 110)
	w, err := headless.NewWindow(sz.X, sz.Y)
	if err != nil {
		t.Skip(err)
	}
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th.TextSize = 14
	u := &ui{th: th, rows: 2, cols: 2, tiles: map[cell]*tileState{}, points: map[cellPair][]PointPair{}, pairBtns: map[cellPair]*widget.Clickable{}}
	u.link.Value, u.chAlign.Value, u.lfit.Value, u.bgEq.Value = true, true, true, true
	u.overlapSl.Value = 15.0 / maxOverlap
	if os.Getenv("SHOTRES") != "" {
		u.result = newImg(1, 1, 1)
	}
	if os.Getenv("SHOTGRID") != "" {
		u.grid.open, u.grid.hr, u.grid.hc = true, 3, 4
	}
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Constraints: layout.Exact(sz), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	u.layout(gtx)
	w.Frame(&ops)
	img := image.NewRGBA(image.Rectangle{Max: sz})
	w.Screenshot(img)
	f, _ := os.Create(path)
	defer f.Close()
	png.Encode(f, img)
}
