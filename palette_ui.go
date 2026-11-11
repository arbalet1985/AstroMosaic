package main

import (
	"fmt"
	"image"
	"log"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type paletteState struct {
	mode                            int
	chooser                         paletteChooser
	initialized                     bool
	open                            [2]widget.Clickable
	inputs                          [2]*Img
	names                           [2]string
	autoAlign, center, autoWeights  widget.Bool
	prepare, apply, advanced, reset widget.Clickable
	strength                        [3]widget.Float
	strengthEditors                 [3]widget.Editor
	strengthMinus, strengthPlus     [3]widget.Clickable
	strengthErrors                  [3]bool
	hue, blueHO, blueSO, sourceSO   widget.Float
	oxygenHue                       widget.Float
	showAdvanced                    bool
	protectSulfur, protectOxygen    widget.Bool
	protectForaxx                   widget.Bool
	suppressGrid                    widget.Bool
	gridNote                        string
	maskView                        int
	maskButton                      widget.Clickable
	maskImages                      [3]paint.ImageOp
	lockStretch                     widget.Bool
	tones                           [nPrev]displayTone
	prepared                        *palettePrepared
	images                          [nPrev]paint.ImageOp
	panel                           panel
	list                            widget.List
	previewBusy, dirty, hasPreview  bool
	revision                        uint64
	note                            string
}

func (p *paletteState) init() {
	if p.initialized {
		return
	}
	p.initialized = true
	p.center.Value, p.autoWeights.Value = true, true
	p.defaults()
}

func (p *paletteState) defaults() {
	for i, v := range []float64{1, 1, .5} {
		p.strengthEditors[i].SingleLine = true
		p.strengthEditors[i].Submit = true
		p.setStrength(i, v)
	}
	p.hue.Value = 1
	p.oxygenHue.Value = 0
	p.autoWeights.Value = true
	p.protectSulfur.Value, p.protectOxygen.Value = true, true
	p.protectForaxx.Value = true
	p.suppressGrid.Value = false
	p.lockStretch.Value = true
	if p.prepared != nil {
		b1, b2, so := p.prepared.autoFractions()
		p.blueHO.Value, p.blueSO.Value, p.sourceSO.Value = float32(b1), float32(b2), float32(so)
	}
}

func (p *paletteState) settings() paletteParams {
	return paletteParams{Mode: p.mode, Hydrogen: p.strengthValue(0), Oxygen: p.strengthValue(1), Sulfur: p.strengthValue(2), Hue: float64(p.hue.Value), OxygenHue: float64(p.oxygenHue.Value), Auto: p.autoWeights.Value, BlueHO: float64(p.blueHO.Value), BlueSO: float64(p.blueSO.Value), SourceSO: float64(p.sourceSO.Value), ProtectSulfur: p.protectSulfur.Value, ProtectOxygen: p.protectOxygen.Value, ProtectForaxx: p.protectForaxx.Value, SuppressGrid: p.suppressGrid.Value}
}

func (p *paletteState) strengthValue(i int) float64 {
	v := float64(p.strength[i].Value)
	return 3 * v * v
}

func (p *paletteState) setStrength(i int, value float64) {
	value = math.Max(0, math.Min(3, value))
	p.strength[i].Value = float32(math.Sqrt(value / 3))
	p.strengthEditors[i].SetText(strconv.FormatFloat(value, 'f', -1, 64))
	p.strengthErrors[i] = false
}

func paletteStrengthStep(i int) float64 {
	if i == 2 {
		return .001
	}
	return .01
}

func parsePaletteStrength(text string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(text), ",", "."), 64)
	return v, err == nil && paletteFinite(v) && v >= 0 && v <= 3
}

func (p *paletteState) validStrengths() bool {
	return !p.strengthErrors[0] && !p.strengthErrors[1] && !p.strengthErrors[2]
}

func (u *ui) handlePalette(gtx layout.Context) {
	p := &u.palette
	if !p.initialized {
		return
	}
	u.handlePaletteChooser(gtx)
	for i := range p.open {
		if p.open[i].Clicked(gtx) && !u.busy {
			u.openPaletteInput(i)
		}
	}
	if p.advanced.Clicked(gtx) {
		p.showAdvanced = !p.showAdvanced
	}
	changed := false
	if p.suppressGrid.Update(gtx) {
		changed = true
	}
	if p.lockStretch.Update(gtx) {
		changed = true
	}
	if p.maskButton.Clicked(gtx) {
		count := 3
		if p.mode >= 8 {
			count = 4
		}
		p.maskView = (p.maskView + 1) % count
		p.panel.needFit = true
	}
	if p.protectSulfur.Update(gtx) {
		changed = true
	}
	if p.protectOxygen.Update(gtx) {
		changed = true
	}
	if p.protectForaxx.Update(gtx) {
		changed = true
	}
	if p.reset.Clicked(gtx) && !u.busy {
		p.defaults()
		changed = true
	}
	if p.autoAlign.Update(gtx) || p.center.Update(gtx) {
		p.prepared = nil
		p.chooser.stage = 0
		p.hasPreview = false
		p.revision++
		p.note = "Параметры совмещения изменены. Нажмите «Подготовить HO + SO»."
	}
	if p.autoWeights.Update(gtx) {
		changed = true
	}
	for i := range p.strength {
		if p.strength[i].Update(gtx) {
			p.setStrength(i, math.Round(p.strengthValue(i)*1000)/1000)
			changed = true
		}
		for _, button := range []struct {
			click *widget.Clickable
			sign  float64
		}{{&p.strengthMinus[i], -1}, {&p.strengthPlus[i], 1}} {
			if button.click.Clicked(gtx) && !u.busy {
				p.setStrength(i, math.Round((p.strengthValue(i)+button.sign*paletteStrengthStep(i))*1000)/1000)
				changed = true
			}
		}
		for {
			ev, ok := p.strengthEditors[i].Update(gtx)
			if !ok {
				break
			}
			switch ev.(type) {
			case widget.ChangeEvent, widget.SubmitEvent:
				v, valid := parsePaletteStrength(p.strengthEditors[i].Text())
				p.strengthErrors[i] = !valid
				if valid && !u.busy {
					p.strength[i].Value = float32(math.Sqrt(v / 3))
					if _, submitted := ev.(widget.SubmitEvent); submitted {
						p.setStrength(i, v)
					}
					changed = true
				}
			}
		}
	}
	for _, s := range []*widget.Float{&p.hue, &p.oxygenHue, &p.blueHO, &p.blueSO, &p.sourceSO} {
		if s.Update(gtx) {
			changed = true
		}
	}
	if changed {
		p.chooser.stage = 0
		p.dirty = true
		p.revision++
	}
	if p.prepare.Clicked(gtx) && !u.busy && p.inputs[0] != nil && p.inputs[1] != nil {
		u.preparePaletteInputs()
	}
	if p.apply.Clicked(gtx) && !u.busy && p.prepared != nil && p.validStrengths() {
		if p.suppressGrid.Value && len(p.prepared.GridHydrogen) == 0 {
			u.preparePaletteGrid()
		} else {
			u.applyPalette()
		}
	}
	if p.dirty && !p.previewBusy && p.prepared != nil && !u.busy && u.workspace.section == sectionPalettes {
		if p.suppressGrid.Value && len(p.prepared.GridHydrogen) == 0 {
			u.preparePaletteGrid()
		} else {
			u.previewPalette()
		}
	}
}

func (u *ui) openPaletteInput(index int) {
	u.busy = true
	go func() {
		f, err := u.expl.ChooseFile(inputExts...)
		if err != nil {
			u.post(func() { u.busy = false })
			return
		}
		path := f.(interface{ Name() string }).Name()
		f.Close()
		u.post(func() { u.status = "Загрузка " + filepath.Base(path) + "…" })
		m, err := loadImage(path)
		if err == nil && m.C != 3 {
			err = fmt.Errorf("выберите цветное RGB-изображение с камеры; получено каналов: %d", m.C)
		}
		u.post(func() {
			u.busy = false
			if err != nil {
				u.status = "Ошибка: " + err.Error()
				return
			}
			p := &u.palette
			p.inputs[index], p.names[index] = m, filepath.Base(path)
			p.prepared = nil
			p.gridNote = ""
			p.chooser.stage = 0
			p.images = [nPrev]paint.ImageOp{}
			p.hasPreview = false
			p.revision++
			p.note = "Загрузите оба файла и нажмите «Подготовить HO + SO»."
			if a, b := p.inputs[0], p.inputs[1]; a != nil && b != nil && (a.W != b.W || a.H != b.H) {
				p.note = fmt.Sprintf("Размеры отличаются: HO %d×%d, SO %d×%d. Результат будет обрезан до общей области.", a.W, a.H, b.W, b.H)
			}
			u.status = fmt.Sprintf("%s: %d×%d, RGB. %s", p.names[index], m.W, m.H, p.note)
		})
	}()
}

func (u *ui) preparePaletteInputs() {
	p := &u.palette
	ho, so, auto, center := p.inputs[0], p.inputs[1], p.autoAlign.Value, p.center.Value
	grid := p.suppressGrid.Value
	u.busy = true
	p.revision++
	p.prepared = nil
	p.chooser.stage = 0
	p.hasPreview = false
	u.status = "Совмещение, общая область и оценка шума G/B…"
	go func() {
		prepared, err := preparePalette(ho, so, auto, center)
		gridNote := ""
		if err == nil && grid {
			prepared.GridHydrogen, prepared.Thumbnail.GridHydrogen, gridNote = paletteGridPlanes(prepared)
		}
		var masks [3]paint.ImageOp
		var tones [nPrev]displayTone
		if err == nil {
			masks[0] = paint.NewImageOp(preview(prepared.maskImage(false), 1200, prevLinear))
			masks[1] = paint.NewImageOp(preview(prepared.maskImage(true), 1200, prevLinear))
			masks[2] = paint.NewImageOp(preview(prepared.foraxxMaskImage(), 1200, prevLinear))
			tones = imageTones(prepared.compose(paletteParams{Hydrogen: 1, Oxygen: 1, Auto: true, ProtectOxygen: true}, 1200))
		}
		u.post(func() {
			u.busy = false
			if err != nil {
				p.note = "Ошибка: " + err.Error()
				u.status = p.note
				return
			}
			p.prepared = prepared
			p.gridNote = gridNote
			p.maskImages = masks
			p.tones = tones
			p.maskView = 0
			p.note = prepared.Note
			b1, b2, so := prepared.autoFractions()
			p.blueHO.Value, p.blueSO.Value, p.sourceSO.Value = float32(b1), float32(b2), float32(so)
			p.panel = panel{needFit: true}
			p.dirty = true
			u.stretch.Value, u.link.Value = true, true
			u.status = prepared.Note
		})
	}()
}

func (u *ui) previewPalette() {
	p := &u.palette
	prepared, params, revision := p.prepared, p.settings(), p.revision
	tones, locked := p.tones, p.lockStretch.Value
	p.previewBusy = true
	p.dirty = false
	go func() {
		m := prepared.compose(params, 1200)
		pv := previews(m, 1200)
		if locked {
			for i := range pv {
				pv[i] = renderDisplay(m, image.Rect(0, 0, m.W, m.H), image.Pt(m.W, m.H), tones[i], nil)
			}
		}
		u.post(func() {
			p.previewBusy = false
			if p.revision != revision || p.prepared != prepared {
				return
			}
			for i, im := range pv {
				p.images[i] = paint.NewImageOp(im)
			}
			p.hasPreview = true
		})
	}()
}

func (u *ui) applyPalette() {
	p := &u.palette
	prepared, params := p.prepared, p.settings()
	log.Printf("PALETTE params=%+v weights=%v inputs=%q", params, prepared.weights(params), p.names)
	u.busy = true
	u.status = "Рассчитываю палитру в полном разрешении…"
	go func() {
		m := prepared.compose(params, 0)
		pv := previews(m, previewSide)
		u.post(func() {
			u.busy = false
			// Keep the previous result available through the existing undo action.
			if u.result != nil {
				u.pushHistory(u.result)
			}
			u.baseline = nil
			u.sel = nil
			u.setResult(m, pv)
			u.diagnostics = fieldDiagnostics{}
			u.selectWorkspace(sectionEnhance)
			u.status = "Палитра готова. Доступны улучшения и сохранение. " + prepared.Note
		})
	}()
}

func (u *ui) paletteView(gtx layout.Context) layout.Dimensions {
	p := &u.palette
	if p.chooser.stage != 0 {
		return u.paletteChooserView(gtx)
	}
	if !p.hasPreview {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(650))
			message := "HOO + жёлтая сера\n\nОткройте два линейных цветных файла HO и SO. Кислород извлекается из G/B обоих файлов с учётом шума."
			if u.busy {
				message = "Подготовка HO + SO…"
			}
			return layout.UniformInset(unit.Dp(24)).Layout(gtx, material.Body1(u.th, message).Layout)
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			label := "Предпросмотр · " + paletteName(p.mode) + " · растяжение только для просмотра"
			if p.maskView > 0 {
				label = []string{"", "Маска серы · белое: добавлять SII · чёрное: сохранить HOO", "Маска кислорода · белое: смешивать и усиливать · чёрное: кислород HO", "Итоговая маска Foraxx · белое: Foraxx · чёрное: базовый HOO"}[p.maskView]
			}
			if p.previewBusy || p.dirty {
				label = "Обновляю предпросмотр…"
			}
			return layout.UniformInset(unit.Dp(8)).Layout(gtx, material.Body2(u.th, label).Layout)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			im := p.images[u.si()]
			if p.maskView > 0 {
				im = p.maskImages[p.maskView-1]
			}
			return u.zoomView(gtx, &p.panel, im, nil, nil)
		}))
}

func (u *ui) paletteSidebar(gtx layout.Context) layout.Dimensions {
	p := &u.palette
	p.init()
	button := func(c *widget.Clickable, label string, enabled bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			if !enabled {
				gtx = gtx.Disabled()
			}
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return material.Button(u.th, c, label).Layout(gtx)
		}
	}
	text := func(label string) layout.Widget { return material.Body2(u.th, label).Layout }
	check := func(b *widget.Bool, label string, enabled bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			if !enabled {
				gtx = gtx.Disabled()
			}
			return checkBox(u.th, b, label).Layout(gtx)
		}
	}
	slider := func(s *widget.Float, enabled bool) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions {
			if !enabled {
				gtx = gtx.Disabled()
			}
			return material.Slider(u.th, s).Layout(gtx)
		}
	}
	ready := p.prepared != nil && !u.busy
	items := []layout.Widget{button(&u.workspace.home, "← Разделы", !u.busy), material.H6(u.th, "Палитры · "+paletteName(p.mode)).Layout, button(&p.open[0], "Открыть HO…", !u.busy)}
	for i := 0; i < 2; i++ {
		if i == 1 {
			items = append(items, button(&p.open[1], "Открыть SO…", !u.busy))
		}
		if p.inputs[i] != nil {
			items = append(items, text(fmt.Sprintf("%s · %d×%d", p.names[i], p.inputs[i].W, p.inputs[i].H)))
		}
	}
	items = append(items, check(&p.autoAlign, "Совместить по звёздам", !u.busy), text("Выключено — файлы уже совмещены."))
	if !p.autoAlign.Value {
		items = append(items, check(&p.center, "Разный размер: по центру", !u.busy), text("Снимите флажок для привязки к левому верхнему углу."))
	}
	items = append(items, button(&p.prepare, "Подготовить HO + SO", !u.busy && p.inputs[0] != nil && p.inputs[1] != nil))
	items = append(items, text("Палитра: "+paletteName(p.mode)), button(&p.chooser.open, "Выбрать из 10 палитр…", ready), button(&p.chooser.levels, "10 вариантов смешивания…", ready))
	if p.mode >= 8 {
		items = append(items, text("Foraxx: линейная адаптация динамического смешивания. Сам результат не растягивается."))
	}
	if p.note != "" {
		items = append(items, text(p.note))
	}
	items = append(items, check(&p.suppressGrid, "Ослабить сетку 4×4 в HO", ready), text("Для периодического узора в водороде. Коррекция отключаемая; проверьте звёзды и детали при 100%."))
	if p.suppressGrid.Value && p.gridNote != "" {
		items = append(items, text(p.gridNote))
	}
	params := p.settings()
	for i, item := range []struct {
		label string
		value float64
	}{{"Сила водорода", params.Hydrogen}, {"Сила кислорода", params.Oxygen}, {"Сила серы", params.Sulfur}} {
		items = append(items, text(fmt.Sprintf("%s: ×%.3f", item.label, item.value)), slider(&p.strength[i], ready), func(gtx layout.Context) layout.Dimensions {
			if !ready {
				gtx = gtx.Disabled()
			}
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					b := material.Button(u.th, &p.strengthMinus[i], fmt.Sprintf("−%g", paletteStrengthStep(i)))
					b.Inset = layout.UniformInset(unit.Dp(6))
					return b.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return material.Editor(u.th, &p.strengthEditors[i], "0–3").Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					b := material.Button(u.th, &p.strengthPlus[i], fmt.Sprintf("+%g", paletteStrengthStep(i)))
					b.Inset = layout.UniformInset(unit.Dp(6))
					return b.Layout(gtx)
				}))
		})
		if p.strengthErrors[i] {
			items = append(items, text("Введите число от 0 до 3; точка или запятая."))
		}
	}
	maskLabels := []string{"Просмотр: результат → маска серы", "Просмотр: маска серы → маска кислорода", "Просмотр: маска кислорода → результат"}
	if p.mode >= 8 {
		items = append(items, check(&p.protectForaxx, "Foraxx: итоговая маска защиты фона", ready), text("Чёрное — базовый HOO из HO; белое — готовый Foraxx; серое — плавное смешивание. Маска может исключать самое слабое излучение."))
		maskLabels = []string{"Просмотр: результат → маска серы", "Просмотр: маска серы → маска кислорода", "Просмотр: маска кислорода → итоговая Foraxx", "Просмотр: итоговая Foraxx → результат"}
	}
	items = append(items, text("Оттенок серы: оранжевый → жёлтый"), slider(&p.hue, ready && p.mode == 0),
		text(fmt.Sprintf("Оттенок кислорода: голубой → синий · %.0f%%", 100*p.oxygenHue.Value)), slider(&p.oxygenHue, ready && (p.mode == 0 || p.mode == 1 || p.mode == 4 || p.mode == 7 || p.mode >= 8)),
		check(&p.protectSulfur, "Сера: добавлять по маске", ready),
		check(&p.protectOxygen, "Кислород: смешивать и усиливать по маске", ready),
		text("Вне маски кислорода сохраняется OIII из HO без усиления. Самые слабые сигналы маска может исключать."),
		button(&p.maskButton, maskLabels[p.maskView], ready),
		check(&u.stretch, "Авторастяжение", true), check(&u.link, "Связать каналы", true), text("Для оценки цвета используйте связанные каналы. Предпросмотр усреднён; шум проверяйте в полном разрешении при одинаковом растяжении."))
	items = append(items, check(&p.lockStretch, "Фиксировать растяжение HOO", ready), text("Фиксация позволяет сравнивать фон при изменении смешивания без автоматической смены контраста."))
	if p.prepared != nil {
		w := p.prepared.weights(params)
		items = append(items, text(fmt.Sprintf("Вклад в OIII:\nHO G %.1f%% · B %.1f%%\nSO G %.1f%% · B %.1f%%", 100*w[0], 100*w[1], 100*w[2], 100*w[3])))
	}
	items = append(items, button(&p.advanced, "Дополнительные настройки ▾", true))
	if p.showAdvanced {
		items = append(items, check(&p.autoWeights, "Веса OIII по шуму", ready), text("Шумные каналы получают меньший вес после выравнивания уровня сигнала. Недостоверные каналы исключаются."))
		for _, item := range []struct {
			label string
			s     *widget.Float
		}{{"Доля B в кислороде HO", &p.blueHO}, {"Доля B в кислороде SO", &p.blueSO}, {"Доля кислорода из SO", &p.sourceSO}} {
			items = append(items, text(fmt.Sprintf("%s: %.0f%%", item.label, 100*item.s.Value)), slider(item.s, ready && !p.autoWeights.Value))
		}
	}
	items = append(items, button(&p.reset, "Сбросить смешивание", !u.busy), button(&p.apply, "Собрать → Улучшения", ready && p.validStrengths()), text("Полное разрешение. Сохранение FITS / XISF / TIFF / PNG / JPEG — в разделе «Улучшения»."))
	p.list.Axis = layout.Vertical
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.List(u.th, &p.list).Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
			return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, items[i])
		})
	})
}
