package main

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget/material"
)

const pickN = 10 // picker side; rows·cols ≤ 100 as elsewhere

// gridPicker chooses rows × cols the way Word inserts a table: hover over a square of cells,
// click to take it. The current grid is outlined.
type gridPicker struct {
	open   bool
	hr, hc int // hovered size, 0 when the pointer is outside
}

// Layout draws the picker; picked reports a click on size r × c.
func (g *gridPicker) Layout(gtx layout.Context, th *material.Theme, rows, cols int) (d layout.Dimensions, r, c int, picked bool) {
	cell, gap, pad := gtx.Dp(22), gtx.Dp(3), gtx.Dp(8)
	step := cell + gap
	field := image.Pt(pickN*step-gap, pickN*step-gap)
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: g, Kinds: pointer.Move | pointer.Enter | pointer.Leave | pointer.Press})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		hr, hc := min(max(int(e.Position.Y)/step+1, 1), pickN), min(max(int(e.Position.X)/step+1, 1), pickN)
		switch e.Kind {
		case pointer.Move, pointer.Enter:
			g.hr, g.hc = hr, hc
		case pointer.Leave:
			g.hr, g.hc = 0, 0
		case pointer.Press:
			r, c, picked = hr, hc, true
			g.open, g.hr, g.hc = false, 0, 0
		}
	}

	caption := fmt.Sprintf("Сейчас %d × %d. Наведите и щёлкните", rows, cols)
	if g.hr > 0 {
		caption = fmt.Sprintf("%d × %d  (строк × столбцов)", g.hr, g.hc)
	}
	m := op.Record(gtx.Ops)
	cgtx := gtx
	cgtx.Constraints = layout.Constraints{Max: image.Pt(field.X, gtx.Dp(40))}
	ld := material.Body2(th, caption).Layout(cgtx)
	label := m.Stop()

	size := image.Pt(field.X+2*pad, field.Y+ld.Size.Y+3*pad)
	box := image.Rectangle{Max: size}
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0x2a, G: 0x2d, B: 0x33, A: 0xff}, clip.UniformRRect(box, gtx.Dp(4)).Op(gtx.Ops))
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0x55, G: 0x5a, B: 0x64, A: 0xff}, clip.Stroke{Path: clip.UniformRRect(box, gtx.Dp(4)).Path(gtx.Ops), Width: 1}.Op())

	off := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
	for y := 0; y < pickN; y++ {
		for x := 0; x < pickN; x++ {
			rc := image.Rect(x*step, y*step, x*step+cell, y*step+cell)
			fill := color.NRGBA{R: 0x3a, G: 0x3e, B: 0x46, A: 0xff}
			if y < g.hr && x < g.hc {
				fill = color.NRGBA{R: 0x4a, G: 0x8c, B: 0xd8, A: 0xff}
			}
			paint.FillShape(gtx.Ops, fill, clip.Rect(rc).Op())
			if g.hr == 0 && y < rows && x < cols {
				paint.FillShape(gtx.Ops, color.NRGBA{R: 0x6c, G: 0xb4, B: 0xff, A: 0xff}, clip.Stroke{Path: clip.Rect(rc).Path(), Width: float32(gtx.Dp(2))}.Op())
			}
		}
	}
	area := clip.Rect{Max: field}.Push(gtx.Ops)
	pointer.CursorPointer.Add(gtx.Ops)
	event.Op(gtx.Ops, g)
	area.Pop()
	lo := op.Offset(image.Pt(0, field.Y+pad)).Push(gtx.Ops)
	label.Add(gtx.Ops)
	lo.Pop()
	off.Pop()
	return layout.Dimensions{Size: size}, r, c, picked
}
