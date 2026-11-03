package main

import (
	"image"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type colorHaloState struct {
	open, preview, apply, strengthBtn, overviewBtn widget.Clickable
	source, candidate                              *Img
	strength, cachedStrength                       float64
	overview                                       bool
	region                                         image.Rectangle
	images                                         [2][nPrev]paint.ImageOp
	panels                                         [2]panel
	note                                           string
}

func (s *colorHaloState) amount() float64 {
	if s.strength == 0 {
		return .75
	}
	return s.strength
}

func (u *ui) handleColorHalos(gtx layout.Context) {
	s := &u.colorHalo
	if s.open.Clicked(gtx) && !u.busy && u.result != nil && u.result.C == 3 {
		u.view = 7
		if s.source != u.result {
			u.previewColorHalos()
		}
	}
	if s.strengthBtn.Clicked(gtx) && !u.busy {
		s.strength = s.amount() + .25
		if s.strength > 1 {
			s.strength = .5
		}
	}
	if s.preview.Clicked(gtx) && !u.busy && u.result != nil {
		u.previewColorHalos()
	}
	if s.overviewBtn.Clicked(gtx) && !u.busy && s.candidate != nil {
		s.overview = !s.overview
		u.refreshHaloPreviews()
	}
	if s.apply.Clicked(gtx) && !u.busy && s.source == u.result && s.candidate != nil && s.cachedStrength == s.amount() {
		source, out, note := s.source, s.candidate, s.note
		u.busy = true
		go func() {
			pv := previews(out, previewSide)
			u.post(func() {
				u.busy = false
				recordProcessing(source, out, "Цветные ореолы")
				u.pushHistory(source)
				u.setResult(out, pv)
				u.sel = nil
				u.angleEd.SetText("0")
				u.view = 2
				u.status = "Цветные ореолы: " + note + " «Отменить» возвращает исходный результат."
			})
		}()
	}
}

func (u *ui) refreshHaloPreviews() {
	s := &u.colorHalo
	if s.source == nil || s.candidate == nil {
		return
	}
	tones := imageTones(s.source)
	region := s.region
	size := region.Size()
	if s.overview {
		region = image.Rect(0, 0, s.source.W, s.source.H)
		k := max(1, (max(s.source.W, s.source.H)+previewSide-1)/previewSide)
		size = image.Pt(max(1, s.source.W/k), max(1, s.source.H/k))
	}
	s.panels = [2]panel{{needFit: true}, {needFit: true}}
	for side, m := range []*Img{s.source, s.candidate} {
		for mode := 0; mode < nPrev; mode++ {
			s.images[side][mode] = paint.NewImageOp(renderDisplay(m, region, size, tones[mode], nil))
		}
		if s.overview {
			s.panels[side].detail = &detailView{source: m, tones: tones, mode: -1}
		}
	}

}

func (u *ui) previewColorHalos() {
	s := &u.colorHalo
	source, amount := u.result, s.amount()
	if source == nil || source.C != 3 {
		return
	}
	// Selection is copied before the worker starts; UI state stays on the UI thread.
	var selection *frame
	if u.sel != nil {
		copy := *u.sel
		selection = &copy
	}
	u.busy = true
	s.candidate = nil
	u.status = "Измеряю RGB-профили и проверяю коррекцию цветных ореолов…"
	go func() {
		out, report, err := correctColorHalos(source, amount)
		region := restorationPreviewRect(source, report.stars, selection)
		u.post(func() {
			u.busy = false
			s.source, s.candidate = source, out
			s.cachedStrength = amount
			s.region = region
			s.note = report.note
			if err != nil {
				s.note = err.Error()
				s.images = [2][nPrev]paint.ImageOp{}
			} else {
				u.refreshHaloPreviews()
			}
			u.status = s.note
		})
	}()
}

func (u *ui) colorHaloView(gtx layout.Context) layout.Dimensions {
	s := &u.colorHalo
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Body1(u.th, "Цветные ореолы звёзд — согласование радиальных RGB-профилей").Layout),
			layout.Rigid(material.Body2(u.th, "Для линейного RGB с совмещёнными каналами. Модель перераспределяет свет внутри звёзд, сохраняя поток R/G/B и остаток изображения. Ядро может стать немного шире. Насыщенные звёзды, заметные соседи и ненадёжные модели пропускаются.\nПроверьте участок до/после с общей растяжкой. Чтобы выбрать другой участок, выделите его на результате и нажмите «Пересчитать». После применения доступна отмена.").Layout),

			layout.Rigid(material.Body2(u.th, s.note).Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if s.candidate == nil {
					return layout.Center.Layout(gtx, material.Body1(u.th, "Рассчитайте предварительный результат").Layout)
				}
				linkPanels(&s.panels)
				var children []layout.FlexChild
				for side := 0; side < 2; side++ {
					children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(material.Body2(u.th, []string{"До", "После — общая растяжка с исходником"}[side]).Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return u.zoomView(gtx, &s.panels[side], s.images[side][u.si()], nil, nil)
							}),
						)
					}))
				}
				return layout.Flex{}.Layout(gtx, children...)
			}),
		)
	})
}
