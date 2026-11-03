package main

import (
	"fmt"
	"image"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type restorationState struct {
	open, preview, apply, iterations, strength, mode, mapBtn widget.Clickable
	colorBtn, mapChannelBtn                                  widget.Clickable
	profile, cachedProfile, independent, cachedIndependent   bool
	mapChannel                                               int
	spatial, cachedSpatial, showMap                          bool
	coma, cachedComa                                         bool
	psfMap                                                   paint.ImageOp
	mapPanel                                                 panel
	params, cachedParams                                     restorationParams
	source                                                   *Img
	model                                                    restorationModel
	modelValid, hasPreview, approved                         bool
	region                                                   image.Rectangle
	images                                                   [2][nPrev]paint.ImageOp
	panels                                                   [2]panel
	note                                                     string
}

func (s *restorationState) settings() restorationParams {
	p := s.params
	if p.iterations == 0 {
		p.iterations = 5
	}
	if p.fraction == 0 {
		p.fraction = .35
	}
	return p
}

func (u *ui) handleRestoration(gtx layout.Context) {
	s := &u.restoration
	if s.open.Clicked(gtx) && !u.busy {
		u.view = 5
		if u.result != nil && s.source != u.result {
			u.previewRestoration()
		}
	}
	if s.iterations.Clicked(gtx) && !u.busy {
		p := s.settings()
		choices := []int{3, 5, 8, 12}
		for i, v := range choices {
			if v == p.iterations {
				p.iterations = choices[(i+1)%len(choices)]
				break
			}
		}
		s.params = p
	}
	if s.strength.Clicked(gtx) && !u.busy {
		p := s.settings()
		choices := []float64{.35, .5, .65}
		for i, v := range choices {
			if v == p.fraction {
				p.fraction = choices[(i+1)%len(choices)]
				break
			}
		}
		s.params = p
	}
	if s.mode.Clicked(gtx) && !u.busy {
		if !s.spatial {
			s.spatial = true
			s.coma = false
		} else if !s.coma {
			s.coma = true
		} else if !s.profile {
			s.profile = true
		} else {
			s.profile = false
			s.spatial = false
			s.coma = false
		}
		s.modelValid = false
		s.approved = false
		s.showMap = false
	}
	if s.colorBtn.Clicked(gtx) && !u.busy && u.result != nil && u.result.C == 3 {
		s.independent = !s.independent
		s.modelValid = false
		s.approved = false
		s.showMap = false
	}
	if s.mapChannelBtn.Clicked(gtx) && s.modelValid && len(s.model.colorModels) > 0 {
		s.mapChannel = (s.mapChannel + 1) % 3
		if g := s.model.colorModels[s.mapChannel].spatial; g != nil {
			s.psfMap = paint.NewImageOp(spatialPSFImage(g))
			s.mapPanel.needFit = true
		}
	}
	if s.mapBtn.Clicked(gtx) && s.modelValid && s.model.spatial != nil {
		s.showMap = !s.showMap
		s.mapPanel.needFit = true
	}
	if s.preview.Clicked(gtx) && !u.busy && u.result != nil {
		u.previewRestoration()
	}
	if s.apply.Clicked(gtx) && !u.busy && s.approved && s.source == u.result && s.cachedParams == s.settings() && s.cachedSpatial == s.spatial && s.cachedComa == s.coma && s.cachedProfile == s.profile && s.cachedIndependent == s.independent {
		u.applyRestoration()
	}
}

// Fix both sides to the source stretch; navigation never changes the tone curve.
func restorationPreviews(before, after *Img) [2][nPrev]*image.RGBA {
	tones := imageTones(before)
	var out [2][nPrev]*image.RGBA
	for side, m := range []*Img{before, after} {
		for mode := 0; mode < nPrev; mode++ {
			out[side][mode] = renderDisplay(m, image.Rect(0, 0, m.W, m.H), image.Pt(m.W, m.H), tones[mode], nil)
		}
	}
	return out
}

func (u *ui) previewRestoration() {
	s := &u.restoration
	source, p := u.result, s.settings()
	spatial := s.spatial
	coma := s.coma
	profile, independent := s.profile, s.independent
	var selection *frame
	if u.sel != nil {
		f := *u.sel
		selection = &f
	}
	model, valid := s.model, s.modelValid && s.source == source
	u.busy, s.approved = true, false
	u.status = "Оцениваю PSF и рассчитываю пробу деконволюции…"
	go func() {
		var err error
		if !valid {
			model, err = estimateRestorationMode(source, spatial, coma, profile, independent)
		}
		if err != nil {
			u.post(func() {
				u.busy = false
				s.source = source
				s.hasPreview = false
				s.modelValid = false
				s.note = "Проба недоступна: " + err.Error()
				u.status = s.note
			})
			return
		}
		previewStars := model.stars
		if model.spatial != nil && selection == nil {
			previewStars = nil
			for _, star := range model.stars {
				if model.spatial.confidence(star.x, star.y) >= .8 {
					previewStars = append(previewStars, star)
				}
			}
		}
		region := restorationPreviewRect(source, previewStars, selection)
		halo := 2 * restorationRadius(model, p) * (p.iterations + 2)
		context := region.Inset(-halo).Intersect(image.Rect(0, 0, source.W, source.H))
		before := cropPixels(source, region)
		var after *Img
		if model.spatial != nil {
			after = restoreSpatialRegion(source, model, p, region, nil)
		} else {
			trial := restoreImage(cropPixels(source, context), model, p, nil)
			after = cropPixels(trial, region.Sub(context.Min))
		}
		note, check := assessRestorationModel(before, after, model, region.Min)
		approved := check == nil
		if check != nil {
			note = "Проба не прошла проверку: " + check.Error() + ". " + note
		} else {
			note = "Проба прошла проверку. " + note
		}
		pv := restorationPreviews(before, after)
		var psfMap *image.RGBA
		if model.spatial != nil {
			psfMap = spatialPSFImage(model.spatial)
		}
		u.post(func() {
			u.busy = false
			s.source, s.model, s.modelValid = source, model, true
			s.region, s.cachedParams, s.approved, s.hasPreview, s.note = region, p, approved, true, note
			s.cachedSpatial = spatial
			s.cachedComa = coma
			s.cachedProfile, s.cachedIndependent = profile, independent
			s.mapChannel = 0
			if psfMap != nil {
				s.psfMap = paint.NewImageOp(psfMap)
			}
			for side := range pv {
				for mode, img := range pv[side] {
					s.images[side][mode] = paint.NewImageOp(img)
				}
			}
			s.panels = [2]panel{{needFit: true}, {needFit: true}}
			u.status = note
		})
	}()
}

func (u *ui) applyRestoration() {
	s := &u.restoration
	source, model, p := u.result, s.model, s.cachedParams
	u.busy = true
	u.status = "Применяю деконволюцию ко всему результату…"
	go func() {
		out := restoreImage(source, model, p, func(c, it int) {
			u.post(func() {
				if c == -2 {
					u.status = fmt.Sprintf("Локальная деконволюция: участок %d…", it)
					return
				}
				channel := "яркости RGB"
				if c >= 0 {
					channel = channelName(c, source.C)
				}
				u.status = fmt.Sprintf("Деконволюция %s: итерация %d/%d…", channel, it, p.iterations)
			})
		})
		note, err := assessRestorationModel(source, out, model, image.Point{})
		if err != nil {
			u.post(func() {
				u.busy = false
				s.approved = false
				s.note = "Результат не изменён: " + err.Error() + ". " + note
				u.status = s.note
			})
			return
		}
		pv := previews(out, previewSide)
		u.post(func() {
			u.busy = false
			u.pushHistory(source)
			u.setResult(out, pv)
			u.sel = nil
			u.angleEd.SetText("0")
			u.view = 2
			s.approved = false
			u.status = "Деконволюция применена; «Отменить» возвращает исходный результат. " + note
		})
	}()
}

func (u *ui) restorationView(gtx layout.Context) layout.Dimensions {
	s := &u.restoration
	if u.result == nil {
		return layout.Center.Layout(gtx, material.Body1(u.th, "Сначала сшейте мозаику").Layout)
	}
	p := s.settings()
	modeLabel := "PSF: общая круглая ▾"
	strengthLabel := fmt.Sprintf("Доля PSF: %.0f%% ▾", p.fraction*100)
	if s.spatial {
		modeLabel = "PSF: локальная эллиптическая ▾"
		strengthLabel = fmt.Sprintf("Сила: %.0f%% ▾", p.fraction*100)
	}
	if s.coma {
		modeLabel = "PSF: асимметричная кома ▾"
	}
	if s.profile {
		modeLabel = "PSF: эмпирическая 33×33 ▾"
	}
	colorLabel := "Цвет: общая яркость ▾"
	if s.independent {
		colorLabel = "Цвет: отдельные PSF ▾"
	}
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Body1(u.th, "Деконволюция — отдельная операция над результатом").Layout),
			layout.Rigid(material.Body2(u.th, "Проба до 768×768 px: центр рамки результата либо область со звёздами. Слева — до, справа — после; растяжка общая.\nПо умолчанию RGB сохраняет отношения цветов. Отдельные PSF каналов исправляют разное размытие и могут менять цвет деталей.\nДля сохранения обработки нажмите «Применить ко всему результату».").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.busy {
					gtx = gtx.Disabled()
				}
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(material.Button(u.th, &s.iterations, fmt.Sprintf("Итерации: %d ▾", p.iterations)).Layout),
					layout.Rigid(material.Button(u.th, &s.strength, strengthLabel).Layout),
					layout.Rigid(material.Button(u.th, &s.preview, "Пересчитать пробу").Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !s.approved || s.source != u.result || p != s.cachedParams || s.spatial != s.cachedSpatial || s.coma != s.cachedComa || s.profile != s.cachedProfile || s.independent != s.cachedIndependent {
							gtx = gtx.Disabled()
						}
						return material.Button(u.th, &s.apply, "Применить ко всему результату").Layout(gtx)
					}),
					layout.Rigid(material.Button(u.th, &s.mode, modeLabel).Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if u.result.C != 3 {
							gtx = gtx.Disabled()
						}
						return material.Button(u.th, &s.colorBtn, colorLabel).Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !s.modelValid || s.source != u.result || s.model.spatial == nil {
							gtx = gtx.Disabled()
						}
						return material.Button(u.th, &s.mapBtn, "Карта PSF").Layout(gtx)
					}),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				note := s.note
				if s.source != u.result {
					note = "Результат изменился — рассчитайте новую пробу."
				} else if s.hasPreview && (p != s.cachedParams || s.spatial != s.cachedSpatial || s.coma != s.cachedComa || s.profile != s.cachedProfile || s.independent != s.cachedIndependent) {
					note = "Параметры изменены — пересчитайте пробу перед применением."
				}
				return material.Body2(u.th, note).Layout(gtx)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if s.showMap && s.modelValid && s.source == u.result && s.model.spatial != nil {
					legend := "Сетка 4×4 всего результата; RGB — общая яркость. N — число звёзд; оси — FWHM в px; угол по часовой стрелке от горизонтали.\nЭллипсы FWHM увеличены ×6. Цвет: от 2 (тёмный) до 10 (жёлтый) px. NO MODEL — нет надёжного измерения. Кнопка «Карта PSF» возвращает пробу."
					if s.model.spatial.coma {
						legend = "Сетка 4×4 всего результата; RGB — общая яркость. Профили 17×17 px показаны с увеличением и нелинейной яркостью.\nN — число изолированных звёзд; A — асимметрия. NO COMA — асимметрия не подтверждена. «Карта PSF» возвращает пробу."
					}
					if s.profile {
						legend = "Эмпирические профили 33×33 px: крылья/дефокус. N — число изолированных звёзд, A — асимметрия. NO MODEL — нет проверенной модели."
					}
					if s.independent {
						legend = "Отдельная PSF канала " + channelName(s.mapChannel, 3) + ". " + legend
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(material.Body2(u.th, legend).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !s.independent {
								return layout.Dimensions{}
							}
							return material.Button(u.th, &s.mapChannelBtn, "Канал PSF: "+channelName(s.mapChannel, 3)+" ▾").Layout(gtx)
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return u.zoomView(gtx, &s.mapPanel, s.psfMap, func(_ f32.Point, _ pointer.Buttons) {}, nil)
						}),
					)
				}
				if !s.hasPreview || s.source != u.result {
					return layout.Center.Layout(gtx, material.Body1(u.th, "Проба появится после расчёта").Layout)
				}
				linkPanels(&s.panels)
				children := make([]layout.FlexChild, 2)
				for side := range children {
					children[side] = layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(material.Body2(u.th, fmt.Sprintf("%s — участок (%d,%d), %d×%d", []string{"До", "После"}[side], s.region.Min.X, s.region.Min.Y, s.region.Dx(), s.region.Dy())).Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return u.zoomView(gtx, &s.panels[side], s.images[side][u.si()], func(_ f32.Point, _ pointer.Buttons) {}, nil)
							}),
						)
					})
				}
				return layout.Flex{}.Layout(gtx, children...)
			}),
		)
	})
}
