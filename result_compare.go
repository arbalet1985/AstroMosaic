package main

import (
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type resultComparison struct {
	enabled         widget.Bool
	fit, native     widget.Clickable
	fraction        float32
	dragging, ready bool
	images          [2][nPrev]paint.ImageOp
	details         [2]*detailView
}

func (u *ui) fitActiveView(native bool) {
	p := &u.resPanel
	var size image.Point
	switch u.view {
	case 5:
		p = &u.restoration.panels[0]
		size = u.restoration.images[0][u.si()].Size()
	case 6:
		p = &u.gradient.panels[0]
		size = u.gradient.images[0][u.si()].Size()
	case 9:
		p = &u.noise.panels[0]
		size = u.noise.images[0][u.si()].Size()
	case 7:
		p = &u.colorHalo.panels[0]
		size = u.colorHalo.images[0][u.si()].Size()
	}
	if p.detail != nil {
		size = image.Pt(p.detail.source.W, p.detail.source.H)
	}
	if size.X > 0 {
		p.nativeSize = size
	}
	if native {
		p.oneToOne = true
	} else {
		p.needFit = true
	}
}

func (u *ui) pushHistory(source *Img) {
	u.history = append(u.history, source)
	u.baselineHistory = append(u.baselineHistory, u.baseline)
	if len(u.history) > 3 {
		u.history = u.history[1:]
		u.baselineHistory = u.baselineHistory[1:]
	}
}

func (u *ui) prepareResultComparison() {
	if u.baseline == nil {
		u.baseline = u.result
	}
	source, before := u.result, u.baseline
	u.comparison.ready = false
	u.comparison.fraction = .5
	u.comparison.enabled.Value = source != before
	u.comparison.images = [2][nPrev]paint.ImageOp{}
	u.comparison.details = [2]*detailView{}
	u.resPanel.detail = nil
	u.resPanel.nativeSize = image.Pt(source.W, source.H)
	work := func() {
		tones := imageTones(before)
		var images [2][nPrev]paint.ImageOp
		details := [2]*detailView{{source: before, tones: tones, mode: -1}, {source: source, tones: tones, mode: -1}}
		if before != source {
			for side, m := range []*Img{before, source} {
				k := max(1, (max(m.W, m.H)+previewSide-1)/previewSide)
				for mode := 0; mode < nPrev; mode++ {
					images[side][mode] = paint.NewImageOp(renderDisplay(m, image.Rect(0, 0, m.W, m.H), image.Pt(max(1, m.W/k), max(1, m.H/k)), tones[mode], nil))
				}
			}
		}
		finish := func() {
			if u.result != source || u.baseline != before {
				return
			}
			u.comparison.images, u.comparison.details = images, details
			u.comparison.ready = true
			u.resPanel.detail = details[1]
		}
		if u.tasks == nil {
			finish()
		} else {
			u.post(finish)
		}
	}
	if u.tasks == nil {
		work()
	} else {
		go work()
	}
}

func (u *ui) hasWipe() bool {
	return u.comparison.ready && u.comparison.enabled.Value && u.baseline != nil && u.baseline != u.result
}

func (u *ui) drawResultWipe(gtx layout.Context) {
	s := &u.comparison
	x := int(math.Round(float64(s.fraction * float32(gtx.Constraints.Max.X))))
	c := clip.Rect{Max: image.Pt(x, gtx.Constraints.Max.Y)}.Push(gtx.Ops)
	p := u.resPanel
	p.detail = s.details[0]
	u.paintPanelImage(gtx, &p, s.images[0][u.si()])
	c.Pop()
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	paint.FillShape(gtx.Ops, white, clip.Rect{Min: image.Pt(x-1, 0), Max: image.Pt(x+1, gtx.Constraints.Max.Y)}.Op())
	y := gtx.Constraints.Max.Y / 2
	paint.FillShape(gtx.Ops, color.NRGBA{R: 35, G: 38, B: 45, A: 230}, clip.Rect{Min: image.Pt(x-9, y-20), Max: image.Pt(x+9, y+20)}.Op())
	paint.FillShape(gtx.Ops, white, clip.Rect{Min: image.Pt(x-2, y-12), Max: image.Pt(x+2, y+12)}.Op())
	for side, label := range []string{"До улучшений", "Результат"} {
		xp := 8
		if side == 1 {
			xp = x + 8
		}
		if side == 0 && x < 110 || side == 1 && gtx.Constraints.Max.X-x < 100 {
			continue
		}
		o := op.Offset(image.Pt(xp, 8)).Push(gtx.Ops)
		g := gtx
		g.Constraints.Min = image.Point{}
		g.Constraints.Max.X = 140
		l := material.Body2(u.th, label)
		l.Color = white
		l.Layout(g)
		o.Pop()
	}
}

func (u *ui) wipePress(pos f32.Point, width int) bool {
	u.comparison.dragging = u.hasWipe() && math.Abs(float64(pos.X-u.comparison.fraction*float32(width))) <= 12
	return u.comparison.dragging
}
