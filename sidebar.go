package main

import (
	"fmt"
	"image/color"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type sidebarState struct {
	headers     [7]widget.Clickable
	open        [7]bool
	initialized bool
	lastView    int
	list        widget.List
}

func (u *ui) sidebar(gtx layout.Context) layout.Dimensions {
	if u.workspace.section == sectionPalettes {
		return u.paletteSidebar(gtx)
	}
	s := &u.sidebarState
	if !s.initialized {
		s.open[0] = u.result == nil
		s.initialized = true
		s.lastView = u.view
		if u.view >= 5 || u.view == 1 {
			s.open[6] = true
		}
	}
	if s.lastView != u.view {
		if u.view >= 5 || u.view == 1 {
			s.open[6] = true
			s.open[0] = false
			s.list.Position.First = 0
			s.list.Position.Offset = 0
		}
		s.lastView = u.view
	}
	for i := range s.headers {
		if s.headers[i].Clicked(gtx) {
			s.open[i] = !s.open[i]
		}
	}
	loaded := 0
	for k, t := range u.tiles {
		if k[0] < u.rows && k[1] < u.cols && t.img != nil {
			loaded++
		}
	}
	ready := !u.busy && u.result != nil
	viewImage := u.result != nil && (u.view == 2 || u.view == 5 || u.view == 6 || u.view == 7 || u.view == 9)
	button := func(c *widget.Clickable, text string, enabled bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			if !enabled {
				gtx = gtx.Disabled()
			}
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return material.Button(u.th, c, text).Layout(gtx)
		}
	}
	check := func(b *widget.Bool, text string, enabled bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			if !enabled {
				gtx = gtx.Disabled()
			}
			return checkBox(u.th, b, text).Layout(gtx)
		}
	}
	text := func(value string) layout.Widget { return material.Body2(u.th, value).Layout }
	slider := func(value *widget.Float) layout.Widget { return material.Slider(u.th, value).Layout }
	type section struct {
		name  string
		items []layout.Widget
	}
	action := "Склеить"
	if loaded == 1 {
		action = "Обработать"
	}
	mode := "PSF: ядра ▾"
	if u.psfField {
		mode = "PSF: поле/профили ▾"
	}
	groups := []section{
		{"Файлы и запуск", []layout.Widget{button(&u.openAll, "Открыть плитки…", !u.busy), button(&u.gridBtn, fmt.Sprintf("Сетка %d × %d ▾", u.rows, u.cols), !u.busy), button(&u.stitchBtn, action, !u.busy), button(&u.saveBtn, "Сохранить результат…", ready), button(&u.clearAll, "Очистить плитки", !u.busy)}},
		{"Просмотр", []layout.Widget{check(&u.stretch, "Авторастяжение", true), check(&u.link, "Связать каналы", true), check(&u.comparison.enabled, "До / После результата", u.comparison.ready && u.baseline != u.result), button(&u.comparison.fit, "Вписать", viewImage), button(&u.comparison.native, "100% — исходные пиксели", viewImage)}},
		{"Склейка: параметры", []layout.Widget{check(&u.chAlign, "Совместить каналы", !u.busy), check(&u.psfMatch, "Согласовать резкость", !u.busy), button(&u.psfModeBtn, mode, !u.busy), text("Меняет настройки следующего расчёта. PSF-согласование сглаживает более резкие плитки и каналы."), button(&u.flat.open, "Master flat…", !u.busy), button(&u.flat.clear, "Без flat", !u.busy && u.flat.image != nil)}},
		{"Диагностика", []layout.Widget{button(&u.mapBtn, "Карта хроматизма", !u.busy), button(&u.diagBtn, "Диагностика поля", true), button(&u.viewPts, "Ручные точки", true)}},
		{"Улучшения результата", []layout.Widget{button(&u.noise.open, "Шум / детали…", ready), button(&u.restoration.open, "Деконволюция…", ready), button(&u.colorHalo.open, "Цветные ореолы…", ready && u.result.C == 3), button(&u.gradient.open, "Градиент фона…", ready), button(&u.quality.open, "Отчёт качества…", ready)}},
		{"Кадрирование и геометрия", nil},
		{"Текущий инструмент", nil},
	}
	if loaded != 1 {
		groups[2].items = append(groups[2].items, check(&u.lfit, "LinearFit", !u.busy), check(&u.bgEq, "Фон по перекрытию", !u.busy), text(fmt.Sprintf("Перекрытие у шва: %.0f%%", u.overlapPct())), func(gtx layout.Context) layout.Dimensions {
			if u.busy {
				gtx = gtx.Disabled()
			}
			return slider(&u.overlapSl)(gtx)
		})
		groups[2].items = append(groups[2].items, check(&u.raggedEdge, "Рваный край", !u.busy && u.overlapPct() > 0))
		if u.overlapPct() == 0 {
			groups[2].items = append(groups[2].items, text("Рваный край доступен при перекрытии у шва больше 0%."))
		} else {
			groups[2].items = append(groups[2].items, text("Неровная граница смешивания внутри полосы перекрытия. Применяется при следующей склейке."))
		}
	}
	if u.flat.image != nil {
		groups[2].items = append(groups[2].items, text("Flat: "+u.flat.name))
	}
	if len(u.weak) > 0 {
		groups[3].items = append(groups[3].items, button(&u.weakBtn, fmt.Sprintf("Проверить пары (%d)", len(u.weak)), !u.busy))
	}
	geo := ready && u.view == 2
	groups[5].items = []layout.Widget{button(&u.ratioBtn, "Рамка: "+ratios[u.ratio].label, geo), text("Угол поворота, °"), func(gtx layout.Context) layout.Dimensions {
		if !geo {
			gtx = gtx.Disabled()
		}
		return material.Editor(u.th, &u.angleEd, "0").Layout(gtx)
	}, func(gtx layout.Context) layout.Dimensions {
		if !geo {
			gtx = gtx.Disabled()
		}
		return slider(&u.angleSl)(gtx)
	}, button(&u.angleDn, "Угол −0.1°", geo), button(&u.angleUp, "Угол +0.1°", geo), button(&u.interpBtn, "Интерполяция: "+interpNames[u.it], geo), check(&u.clamp, "Без ореолов", geo), button(&u.cropBtn, "Кадрировать", geo), button(&u.rotBtn, "Повернуть всё", geo), button(&u.rotL, "90° влево", geo), button(&u.rotR, "90° вправо", geo), button(&u.flipH, "Отразить ↔", geo), button(&u.flipV, "Отразить ↕", geo), button(&u.clrSel, "Снять рамку", geo), button(&u.undo, "Отменить обработку", ready && len(u.history) > 0)}
	switch u.view {
	case 1:
		groups[6].items = []layout.Widget{text("ЛКМ: точка слева, затем справа. ПКМ по метке: удалить. Колесо — масштаб, перетаскивание — сдвиг."), button(&u.clearPts, "Очистить точки пары", u.cur != nil)}
		if u.pairBtns == nil {
			u.pairBtns = map[cellPair]*widget.Clickable{}
		}
		for _, pair := range u.adjacent() {
			b := u.pairBtns[pair]
			if b == nil {
				b = new(widget.Clickable)
				u.pairBtns[pair] = b
			}
			label := fmt.Sprintf("[%d,%d] ↔ [%d,%d] (%d)", pair.A[0]+1, pair.A[1]+1, pair.B[0]+1, pair.B[1]+1, len(u.points[pair]))
			for _, weak := range u.weak {
				if weak == pair {
					label = "(!) " + label
					break
				}
			}
			groups[6].items = append(groups[6].items, func(gtx layout.Context) layout.Dimensions {
				style := material.Button(u.th, b, label)
				if u.cur != nil && *u.cur == pair {
					style.Background = color.NRGBA{R: 192, G: 96, B: 32, A: 255}
				}
				gtx.Constraints.Min.X = gtx.Constraints.Max.X
				return style.Layout(gtx)
			})
		}

	case 5:
		r := &u.restoration
		p := r.settings()
		model := "PSF: круглая"
		if r.spatial {
			model = "PSF: эллиптическая"
		}
		if r.coma {
			model = "PSF: кома"
		}
		if r.profile {
			model = "PSF: эмпирическая"
		}
		colorMode := "Цвет: общая яркость"
		if r.independent {
			colorMode = "Цвет: отдельные PSF"
		}
		groups[6].items = []layout.Widget{button(&r.mode, model+" ▾", ready), button(&r.iterations, fmt.Sprintf("Итерации: %d ▾", p.iterations), ready), button(&r.strength, fmt.Sprintf("Сила / доля PSF: %.0f%% ▾", p.fraction*100), ready), button(&r.colorBtn, colorMode+" ▾", ready && u.result.C == 3), check(&r.regularization, "Starlet-регуляризация", ready), button(&r.preview, "Пересчитать пробу", ready), button(&r.apply, "Применить ко всему результату", ready && r.approved && r.source == u.result && r.cachedParams == p && r.cachedSpatial == r.spatial && r.cachedComa == r.coma && r.cachedProfile == r.profile && r.cachedIndependent == r.independent), button(&r.residualBtn, "После / удалённый сигнал", r.hasPreview), button(&r.mapBtn, "Карта PSF / проба", r.modelValid && r.model.spatial != nil), button(&r.mapChannelBtn, "Канал карты PSF ▾", r.independent && r.modelValid)}
	case 6:
		r := &u.gradient
		mode := "Поверхность: квадратичная"
		if r.rbf {
			mode = "Поверхность: RBF"
		}
		groups[6].items = []layout.Widget{button(&r.modeBtn, mode+" ▾", ready), button(&r.preview, "Пересчитать фон", ready), button(&r.reset, "Сбросить исключения", ready), button(&r.modelBtn, "Модель / после", r.source == u.result), button(&r.apply, "Применить к результату", ready && r.valid && !r.dirty && r.source == u.result)}
	case 7:
		r := &u.colorHalo
		groups[6].items = []layout.Widget{button(&r.strengthBtn, fmt.Sprintf("Сила: %.0f%% ▾", 100*r.amount()), ready), button(&r.preview, "Пересчитать пробу", ready), button(&r.overviewBtn, "Обзор / участок", r.candidate != nil), button(&r.apply, "Применить к результату", ready && r.source == u.result && r.candidate != nil && r.cachedStrength == r.amount())}
	case 8:
		groups[6].items = []layout.Widget{button(&u.quality.refresh, "Пересчитать отчёт", ready)}
	case 9:
		r := &u.noise
		p := r.settings()
		groups[6].items = []layout.Widget{button(&r.methodBtn, noiseMethods[r.method]+" ▾", ready), button(&r.strengthBtn, fmt.Sprintf("Шум: %.0f%% ▾", p.Strength*100), ready)}
		for i := range r.gainBtns {
			groups[6].items = append(groups[6].items, button(&r.gainBtns[i], fmt.Sprintf("Детали %d–%d px: ×%.2f", 1<<i, 2<<i, p.Gain[i]), ready && r.method == 0))
		}
		groups[6].items = append(groups[6].items, button(&r.preview, "Пересчитать пробу", ready), button(&r.residualBtn, "После / удалённый сигнал", r.hasPreview), button(&r.apply, "Применить", ready && r.hasPreview && r.source == u.result && r.cached == p && r.cachedMethod == r.method), button(&r.benchmark, "Сравнить методы", ready))
	default:
		groups[6].items = []layout.Widget{text("Выберите улучшение результата — здесь появятся его параметры.")}
	}
	s.list.Axis = layout.Vertical
	order := []int{0, 1, 2, 3, 4, 5, 6}
	if u.view >= 5 || u.view == 1 {
		order = []int{6, 0, 1, 2, 3, 4, 5}
	}
	if u.workspace.section == sectionEnhance {
		groups[0].items = []layout.Widget{button(&u.workspace.openImage, "Открыть изображение…", !u.busy), button(&u.saveBtn, "Сохранить результат…", ready)}
		order = []int{0, 4, 1, 5, 6}
		if u.view >= 5 {
			order = []int{6, 4, 0, 1, 5}
		}
	}
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Body1(u.th, "AstroMosaic").Layout),
			layout.Rigid(button(&u.workspace.home, "← Разделы", !u.busy)),
			layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if u.workspace.section == sectionEnhance {
					return layout.Dimensions{}
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(button(&u.viewGrid, "Плитки", true)), layout.Rigid(button(&u.workspace.enhance, "Результат → Улучшения", ready)))
			}),
			layout.Rigid(button(&u.viewRes, "Результат", u.result != nil)),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !u.stitchPending {
					return layout.Dimensions{}
				}
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(text("Пересчёт заменит кадрирование, улучшения и историю. Сохраните результат, если он нужен.")), layout.Rigid(button(&u.stitchConfirm, "Пересчитать из исходников", !u.busy)), layout.Rigid(button(&u.stitchCancel, "Отмена пересчёта", !u.busy)))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.List(u.th, &s.list).Layout(gtx, len(order), func(gtx layout.Context, position int) layout.Dimensions {
					i := order[position]
					return layout.Inset{Top: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						prefix := "▸ "
						if s.open[i] {
							prefix = "▾ "
						}
						header := material.Button(u.th, &s.headers[i], prefix+groups[i].name)
						header.Background = color.NRGBA{R: 45, G: 49, B: 57, A: 255}
						children := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return header.Layout(gtx)
						})}
						if s.open[i] {
							for _, item := range groups[i].items {
								children = append(children, layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout), layout.Rigid(item))
							}
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
					})
				})
			}),
		)
	})
}
