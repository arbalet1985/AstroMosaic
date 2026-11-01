package main

import (
	"fmt"
	"image"
	"image/color"
	"math"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type gradientState struct {
	open, preview, apply, reset, modelBtn widget.Clickable
	modeBtn                               widget.Clickable
	rbf                                   bool
	source                                *Img
	mask                                  [gradientGrid * gradientGrid]bool
	model                                 gradientModel
	valid, dirty, showModel               bool
	images                                [2][nPrev]paint.ImageOp
	modelImage                            paint.ImageOp
	panels                                [2]panel
	note                                  string
}

func gradientThumbnail(m *Img) *Img {
	scale := math.Min(1, 900/float64(max(m.W, m.H)))
	out := newImg(max(1, int(float64(m.W)*scale)), max(1, int(float64(m.H)*scale)), m.C)
	for c := 0; c < m.C; c++ {
		for y := 0; y < out.H; y++ {
			for x := 0; x < out.W; x++ {
				out.Pix[c*out.W*out.H+y*out.W+x] = m.at(c, int(math.Round(float64(x)*float64(m.W-1)/float64(max(1, out.W-1)))), int(math.Round(float64(y)*float64(m.H-1)/float64(max(1, out.H-1)))))
			}
		}
	}
	return out
}

func gradientPreviews(m *Img, g gradientModel, mask [400]bool) ([2][nPrev]*image.RGBA, *image.RGBA) {
	before := gradientThumbnail(m)
	after := newImg(before.W, before.H, before.C)
	lo, hi := 0.0, 0.0
	for c := 0; c < before.C; c++ {
		for y := 0; y < before.H; y++ {
			for x := 0; x < before.W; x++ {
				i := c*before.W*before.H + y*before.W + x
				v := g.surface(c, float64(x)*float64(m.W-1)/float64(max(1, before.W-1)), float64(y)*float64(m.H-1)/float64(max(1, before.H-1))) - g.level[c]
				lo, hi = math.Min(lo, v), math.Max(hi, v)
				after.Pix[i] = before.Pix[i]
				if before.Pix[i] != 0 {
					after.Pix[i] -= float32(v)
				}
			}
		}
	}
	pv := restorationPreviews(before, after)
	modelImg := image.NewRGBA(image.Rect(0, 0, before.W, before.H))
	span := math.Max(math.Abs(lo), math.Abs(hi))
	if span == 0 {
		span = 1
	}
	for y := 0; y < before.H; y++ {
		for x := 0; x < before.W; x++ {
			v := (g.surface(0, float64(x)*float64(m.W-1)/float64(max(1, before.W-1)), float64(y)*float64(m.H-1)/float64(max(1, before.H-1))) - g.level[0]) / span
			a := uint8(220 * math.Abs(v))
			col := color.RGBA{30, 30, 30, 255}
			if v > 0 {
				col.R += a
			} else {
				col.B += a
			}
			modelImg.SetRGBA(x, y, col)
		}
	}
	for i, s := range g.samples {
		col := color.RGBA{30, 230, 80, 255}
		if s.excluded || !s.valid {
			col = color.RGBA{255, 180, 25, 255}
		}
		if mask[i] {
			col = color.RGBA{255, 45, 70, 255}
		}
		x, y := int(s.x*float64(before.W)/float64(m.W)), int(s.y*float64(before.H)/float64(m.H))
		for d := -3; d <= 3; d++ {
			for _, q := range []image.Point{{x + d, y}, {x, y + d}} {
				if q.In(modelImg.Bounds()) {
					modelImg.SetRGBA(q.X, q.Y, col)
					for mode := range pv[0] {
						pv[0][mode].SetRGBA(q.X, q.Y, col)
					}
				}
			}
		}
	}
	return pv, modelImg
}

func (u *ui) handleGradient(gtx layout.Context) {
	s := &u.gradient
	if s.modeBtn.Clicked(gtx) && !u.busy {
		s.rbf = !s.rbf
		s.dirty = true
		s.valid = false
		s.note = "Модель изменена — пересчитайте фон"
	}
	if s.open.Clicked(gtx) && !u.busy && u.result != nil {
		u.view = 6
		if s.source != u.result {
			s.mask = [400]bool{}
			s.showModel = true
			u.previewGradient()
		}
	}
	if s.reset.Clicked(gtx) && !u.busy {
		s.mask = [400]bool{}
		s.dirty = true
		s.valid = false
		u.previewGradient()
	}
	if s.preview.Clicked(gtx) && !u.busy && u.result != nil {
		u.previewGradient()
	}
	if s.modelBtn.Clicked(gtx) {
		s.showModel = !s.showModel
		s.panels[1].needFit = true
	}
	if s.apply.Clicked(gtx) && !u.busy && s.valid && !s.dirty && s.source == u.result {
		source, model := u.result, s.model
		u.busy = true
		u.status = "Вычитаю модель фона…"
		go func() {
			out := subtractGradient(source, model)
			pv := previews(out, previewSide)
			u.post(func() {
				u.busy = false
				u.history = append(u.history, source)
				if len(u.history) > 3 {
					u.history = u.history[1:]
				}
				u.setResult(out, pv)
				u.sel = nil
				u.angleEd.SetText("0")
				u.view = 2
				u.status = "Градиент вычтен; «Отменить» возвращает исходный результат. " + model.note
			})
		}()
	}
}

func (u *ui) previewGradient() {
	s := &u.gradient
	source, mask := u.result, s.mask
	rbf := s.rbf
	u.busy = true
	s.valid = false
	u.status = "Оцениваю фон с защитой объектов…"
	go func() {
		g, err := estimateGradientMode(source, mask, rbf)
		pv, modelImg := gradientPreviews(source, g, mask)
		u.post(func() {
			u.busy = false
			s.source = source
			s.model = g
			s.valid = err == nil
			s.dirty = false
			s.note = g.note
			if err != nil {
				s.note = "Модель недоступна: " + err.Error() + ". Измените исключения и пересчитайте."
			}
			for side := range pv {
				for mode, img := range pv[side] {
					s.images[side][mode] = paint.NewImageOp(img)
				}
			}
			s.modelImage = paint.NewImageOp(modelImg)
			s.panels = [2]panel{{needFit: true}, {needFit: true}}
			u.status = s.note
		})
	}()
}

func (u *ui) gradientView(gtx layout.Context) layout.Dimensions {
	s := &u.gradient
	modeLabel := "Поверхность: квадратичная ▾"
	if s.rbf {
		modeLabel = "Поверхность: RBF ▾"
	}
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Body1(u.th, "Криволинейный градиент фона — аддитивная коррекция результата").Layout),
			layout.Rigid(material.Body2(u.th, "Исключите туманности и галактики: щелчок по ячейке слева, затем «Пересчитать». Зелёный — фон, жёлтый — автоматический отсев, красный — ваше исключение.\nПротяжённый объект может выглядеть как градиент: проверьте модель перед применением. Вычитается только изменение уровня, без обрезания отрицательных значений.").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.busy {
					gtx = gtx.Disabled()
				}
				return layout.Flex{}.Layout(gtx,
					layout.Rigid(material.Button(u.th, &s.preview, "Пересчитать").Layout),
					layout.Rigid(material.Button(u.th, &s.reset, "Сбросить исключения").Layout),
					layout.Rigid(material.Button(u.th, &s.modelBtn, "Модель / после").Layout),
					layout.Rigid(material.Button(u.th, &s.modeBtn, modeLabel).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !s.valid || s.dirty || s.source != u.result {
							gtx = gtx.Disabled()
						}
						return material.Button(u.th, &s.apply, "Применить к результату").Layout(gtx)
					}),
				)
			}),
			layout.Rigid(material.Body2(u.th, s.note).Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if s.source != u.result || s.images[0][u.si()].Size().X == 0 {
					return layout.Center.Layout(gtx, material.Body1(u.th, "Рассчитайте модель фона").Layout)
				}
				var children []layout.FlexChild
				for side := 0; side < 2; side++ {
					children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						label := []string{"До — щелчок исключает ячейку из оценки фона", "После — общая растяжка с исходником"}[side]
						img := s.images[side][u.si()]
						if side == 1 && s.showModel {
							img = s.modelImage
							label = "Модель канала L/R: красный вычитается, синий добавляется"
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(material.Body2(u.th, label).Layout), layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return u.zoomView(gtx, &s.panels[side], img, func(pt f32.Point, btn pointer.Buttons) {
								if side != 0 || u.busy || !btn.Contain(pointer.ButtonPrimary) {
									return
								}
								pn := &s.panels[0]
								p := pt.Sub(pn.pan).Mul(1 / pn.zoom)
								sz := img.Size()
								if p.X < 0 || p.Y < 0 || p.X >= float32(sz.X) || p.Y >= float32(sz.Y) {
									return
								}
								i := min(19, int(p.Y*20/float32(sz.Y)))*20 + min(19, int(p.X*20/float32(sz.X)))
								s.mask[i] = !s.mask[i]
								s.dirty = true
								s.valid = false
								s.note = fmt.Sprintf("Исключение [%d,%d] изменено — пересчитайте модель.", i/20+1, i%20+1)
								pv, modelImg := gradientPreviews(u.result, s.model, s.mask)
								for side := range pv {
									for mode, im := range pv[side] {
										s.images[side][mode] = paint.NewImageOp(im)
									}
								}
								s.modelImage = paint.NewImageOp(modelImg)
							}, nil)
						}))
					}))
				}
				return layout.Flex{}.Layout(gtx, children...)
			}),
		)
	})
}
