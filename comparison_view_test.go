package main

import (
	"image"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/widget/material"
)

func TestOverviewAveragesAndNativeRetainsPixels(t *testing.T) {
	m := newImg(6000, 6, 1)
	m.Pix[2*m.W+2001] = 1 // old nearest decimation omitted this pixel entirely
	p := preview(m, 2000, prevLinear)
	if p.RGBAAt(667, 0).R == 0 {
		t.Fatal("overview dropped subpixel star")
	}
	r := image.Rect(2000, 1, 2004, 5)
	native := renderDisplay(m, r, r.Size(), displayTone{}, nil)
	if native.RGBAAt(1, 1).R != 255 || native.RGBAAt(0, 1).R != 0 {
		t.Fatal("native pixels were resampled")
	}
}

func TestResultDividerDragDoesNotSelect(t *testing.T) {
	u := &ui{th: material.NewTheme()}
	before, after := newImg(64, 64, 1), newImg(64, 64, 1)
	for i := range before.Pix {
		before.Pix[i] = .1
		after.Pix[i] = .3
	}
	u.baseline = before
	u.setResult(after, previews(after, previewSide))
	var ops op.Ops
	var router input.Router
	frame := func() {
		ops.Reset()
		g := layout.Context{Ops: &ops, Constraints: layout.Exact(image.Pt(200, 150)), Source: router.Source()}
		u.resultView(g)
		router.Frame(&ops)
	}
	frame()
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(100, 75)}, pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(150, 75)}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(150, 75)})
	frame()
	if u.comparison.fraction != .75 || u.sel != nil {
		t.Fatalf("divider created selection or did not move: %g", u.comparison.fraction)
	}
}

func TestComparisonPreservesOriginalAcrossHistoryLimit(t *testing.T) {
	u := &ui{}
	base := newImg(16, 16, 1)
	for i := range base.Pix {
		base.Pix[i] = .01
	}
	u.setResult(base, previews(base, previewSide))
	for step := 0; step < 5; step++ {
		out := newImg(16, 16, 1)
		for i := range out.Pix {
			out.Pix[i] = .02 + float32(step)*.01
		}
		u.pushHistory(u.result)
		u.setResult(out, previews(out, previewSide))
		if u.baseline != base || u.comparison.details[0].source != base {
			t.Fatal("baseline replaced by previous correction")
		}
		if u.comparison.fraction != .5 || !u.hasWipe() {
			t.Fatal("wipe not enabled at centre")
		}
	}
	if len(u.history) != 3 || len(u.baselineHistory) != 3 {
		t.Fatal("undo retention changed")
	}
	for _, b := range u.baselineHistory {
		if b != base {
			t.Fatal("undo lost original baseline")
		}
	}
	if !u.wipePress(f32.Pt(100, 0), 200) || u.wipePress(f32.Pt(50, 0), 200) {
		t.Fatal("divider hit area incorrect")
	}
}

func TestComparePanesSyncBeforePainting(t *testing.T) {
	u := &ui{}
	p := [2]panel{{needFit: true}, {needFit: true}}
	linkPanels(&p)
	im := paint.NewImageOp(image.NewRGBA(image.Rect(0, 0, 200, 150)))
	var ops op.Ops
	var router input.Router
	frame := func() float32 {
		ops.Reset()
		g := layout.Context{Ops: &ops, Constraints: layout.Exact(image.Pt(200, 150)), Source: router.Source()}
		u.zoomView(g, &p[0], im, nil, nil)
		leftZoom := p[0].zoom
		leftPan := p[0].pan
		o := op.Offset(image.Pt(200, 0)).Push(&ops)
		u.zoomView(g, &p[1], im, nil, nil)
		o.Pop()
		if p[1].zoom != leftZoom || p[1].pan != leftPan {
			t.Fatal("pane update lagged a frame")
		}
		router.Frame(&ops)
		return leftZoom
	}
	frame()
	router.Queue(pointer.Event{Kind: pointer.Scroll, Source: pointer.Mouse, Position: f32.Pt(250, 75), Scroll: f32.Pt(0, -10)})
	if z := frame(); z <= 1 {
		t.Fatalf("right pane wheel did not update left: %g", z)
	}
	before := p[0].pan
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(250, 75)}, pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(270, 80)}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(270, 80)})
	frame()
	if p[0].pan.Sub(before) != f32.Pt(20, 5) {
		t.Fatalf("right pane drag: %v", p[0].pan.Sub(before))
	}
	// Releasing a plain click in a view without a callback must not panic.
	router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(250, 75)}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(250, 75)})
	frame()
}
