package main

import (
	"image"
	"testing"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

func TestResultDetailUsesSourceForWideIntermediateView(t *testing.T) {
	m := newImg(4500, 320, 1)
	m.Pix[30*m.W+2050] = 1
	d := &detailView{source: m, mode: -1}
	p := &panel{zoom: .32}
	var ops op.Ops
	g := layout.Context{Ops: &ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Exact(image.Pt(320, 180))}
	if !d.draw(g, p, image.Pt(1000, 71), prevLinear) {
		t.Fatal("wide ROI fell back to enlarged overview")
	}
	if d.region.Dx() != m.W || d.renderSize.X > 322 || d.renderSize.X >= d.region.Dx() {
		t.Fatal("detail texture not sampled for viewport", d.region, d.renderSize)
	}
	p.zoom = 4.5
	if !d.draw(g, p, image.Pt(1000, 71), prevLinear) {
		t.Fatal("native source view missing")
	}
	if d.renderSize != d.region.Size() {
		t.Fatal("100% pixels were downsampled")
	}
}
