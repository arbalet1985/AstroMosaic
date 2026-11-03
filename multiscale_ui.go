package main

import (
	"fmt"
	"image"
	"math"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

var noiseMethods = []string{"Starlet", "Siril NL-Bayes", "GraXpert"}

type noiseState struct {
	open, preview, apply, methodBtn, strengthBtn, residualBtn, benchmark widget.Clickable
	gainBtns                                                             [waveLevels]widget.Clickable
	source                                                               *Img
	model                                                                waveModel
	params, cached                                                       waveParams
	method, cachedMethod                                                 int
	region                                                               image.Rectangle
	hasPreview, showResidual                                             bool
	images                                                               [2][nPrev]paint.ImageOp
	residual                                                             paint.ImageOp
	panels                                                               [2]panel
	note, comparison                                                     string
	list                                                                 widget.List
}

func (s *noiseState) settings() waveParams {
	p := s.params
	if p == (waveParams{}) {
		p.Strength = .5
		for i := range p.Gain {
			p.Gain[i] = 1
		}
	}
	return p
}
func removedSignal(before, after *Img) *Img {
	out := newImg(before.W, before.H, before.C)
	n := before.W * before.H
	for c := 0; c < before.C; c++ {
		_, sigma, _ := restorationBackground(before.Pix[c*n:(c+1)*n], before.W)
		for i := 0; i < n; i++ {
			out.Pix[c*n+i] = float32(.5 + float64(before.Pix[c*n+i]-after.Pix[c*n+i])/(8*math.Max(sigma, 1e-9)))
		}
	}
	return out
}
func runNoise(m *Img, model waveModel, p waveParams, method int, region image.Rectangle, progress func(int, int)) (*Img, error) {
	if method == 0 {
		return filterWaveRegion(m, model, p, region, progress), nil
	}
	return externalDenoise(cropPixels(m, region), noiseMethods[method], p.Strength)
}
func (u *ui) handleNoise(gtx layout.Context) {
	s := &u.noise
	if s.open.Clicked(gtx) && !u.busy && u.result != nil {
		u.view = 9
		if s.source != u.result {
			u.previewNoise()
		}
	}
	if s.methodBtn.Clicked(gtx) && !u.busy {
		s.method = (s.method + 1) % len(noiseMethods)
	}
	if s.strengthBtn.Clicked(gtx) && !u.busy {
		p := s.settings()
		p.Strength += .25
		if p.Strength > 1 {
			p.Strength = 0
		}
		s.params = p
	}
	for i := range s.gainBtns {
		if s.gainBtns[i].Clicked(gtx) && !u.busy {
			p := s.settings()
			choices := []float64{1, 1.1, 1.25, 1.5, .75}
			for j, v := range choices {
				if p.Gain[i] == v {
					p.Gain[i] = choices[(j+1)%len(choices)]
					break
				}
			}
			s.params = p
		}
	}
	if s.residualBtn.Clicked(gtx) {
		s.showResidual = !s.showResidual
	}
	if s.preview.Clicked(gtx) && !u.busy && u.result != nil {
		u.previewNoise()
	}
	if s.benchmark.Clicked(gtx) && !u.busy && u.result != nil {
		u.benchmarkNoise()
	}
	if s.apply.Clicked(gtx) && !u.busy && s.hasPreview && s.source == u.result && s.cached == s.settings() && s.cachedMethod == s.method {
		source, model, p, method := s.source, s.model, s.cached, s.method
		u.busy = true
		u.status = "Шумоподавление/детали: обработка полного результата…"
		go func() {
			out, err := runNoise(source, model, p, method, image.Rect(0, 0, source.W, source.H), func(done, total int) {
				if done%12 == 0 {
					u.post(func() {
						u.status = fmt.Sprintf("Шумоподавление/детали: %d/%d блоков", done, total)
					})
				}
			})
			if err != nil {
				u.post(func() { u.busy = false; s.note = err.Error(); u.status = s.note })
				return
			}
			pv := previews(out, previewSide)
			u.post(func() {
				u.busy = false
				operation := noiseMethods[method] + fmt.Sprintf("; сила %.0f%%", 100*p.Strength)
				if method == 0 {
					operation += fmt.Sprintf("; детали %v", p.Gain)
				}
				recordProcessing(source, out, operation)
				u.pushHistory(source)
				u.setResult(out, pv)
				u.view = 2
				u.sel = nil
				u.angleEd.SetText("0")
				u.status = "Шумоподавление/детали применены: " + noiseMethods[method] + ". Проверьте «Отчёт качества»; доступна отмена."
			})
		}()
	}
}

func (u *ui) previewNoise() {
	s := &u.noise
	source, p, method := u.result, s.settings(), s.method
	var selection *frame
	if u.sel != nil {
		f := *u.sel
		selection = &f
	}
	model, valid := s.model, s.source == source && len(s.model.Sigma) == source.C
	u.busy = true
	s.hasPreview = false
	u.status = "Оцениваю шум по масштабам и рассчитываю пробу…"
	go func() {
		if !valid {
			model = estimateWaveModel(source)
		}
		region := restorationPreviewRect(source, nil, selection)
		before := cropPixels(source, region)
		after, err := runNoise(source, model, p, method, region, nil)
		if err != nil {
			u.post(func() { u.busy = false; s.source = source; s.model = model; s.note = err.Error(); u.status = s.note })
			return
		}
		pv := restorationPreviews(before, after)
		residual := preview(removedSignal(before, after), max(before.W, before.H), prevLinear)
		report := measureQuality(before, after)
		var lines []string
		for c := 0; c < source.C; c++ {
			lines = append(lines, fmt.Sprintf("%s шум %.5g → %.5g", channelName(c, source.C), report.Noise[0][c], report.Noise[1][c]))
		}
		note := noiseMethods[method] + ": " + strings.Join(lines, "; ") + fmt.Sprintf(". Контрольных звёзд: %d. Проверьте удалённый сигнал: заметные звёзды/волокна означают потерю сигнала.", len(report.Stars))
		u.post(func() {
			u.busy = false
			s.source, s.model, s.region = source, model, region
			s.cached, s.cachedMethod = p, method
			s.hasPreview = true
			s.note = note
			for side := range pv {
				for mode, im := range pv[side] {
					s.images[side][mode] = paint.NewImageOp(im)
				}
			}
			s.residual = paint.NewImageOp(residual)
			s.panels = [2]panel{{needFit: true}, {needFit: true}}
			u.status = note
		})
	}()
}

func (u *ui) benchmarkNoise() {
	s := &u.noise
	source := u.result
	p := s.settings()
	u.busy = true
	u.status = "Сравниваю Starlet, Siril и GraXpert на одинаковых участках…"
	go func() {
		model := estimateWaveModel(source)
		regions := noiseBenchmarkRegions(source)
		var rows []string
		for i, region := range regions {
			before := cropPixels(source, region)
			for method, name := range noiseMethods {
				started := time.Now()
				out, err := runNoise(source, model, p, method, region, nil)
				if err != nil {
					rows = append(rows, fmt.Sprintf("Участок %d [%d,%d] %s: НЕДОСТУПНО — %v", i+1, region.Min.X, region.Min.Y, name, err))
					continue
				}
				r := measureQuality(before, out)
				ratios := make([]string, source.C)
				for c := 0; c < source.C; c++ {
					ratios[c] = fmt.Sprintf("%s ×%.2f", channelName(c, source.C), r.Noise[1][c]/r.Noise[0][c])
				}
				extra := ""
				if len(r.Stars) > 0 {
					extra = fmt.Sprintf("; FWHM %s %.2f → %.2f px", channelName(0, source.C), median(r.values(0, 0, "width", false)), median(r.values(1, 0, "width", false)))
				}
				if math.Abs(r.Flux[0][0]) > 1e-12 {
					extra += fmt.Sprintf("; сумма %s %+.3f%% (включая фон)", channelName(0, source.C), 100*(r.Flux[1][0]-r.Flux[0][0])/math.Abs(r.Flux[0][0]))
				}
				rows = append(rows, fmt.Sprintf("Участок %d [%d,%d] %s: шум %s; контроль N=%d%s; %.1f с", i+1, region.Min.X, region.Min.Y, name, strings.Join(ratios, ", "), len(r.Stars), extra, time.Since(started).Seconds()))
				u.post(func() {
					u.status = fmt.Sprintf("Сравнение: участок %d/%d, %s готов", i+1, len(regions), name)
				})
			}
		}
		text := "Одинаковые участки и сила; Starlet использует заданные усиления, внешние методы — только шумоподавление. Число силы не калибровано между алгоритмами и не означает одинаковое сглаживание. Скорость включает запуск внешнего процесса. Участки выбраны по яркости фона и звезде, это не разметка объектов. Меньше шума не гарантирует сохранение деталей.\n" + strings.Join(rows, "\n")
		u.post(func() {
			u.busy = false
			s.comparison = text
			u.status = "Сравнение методов завершено; результаты на вкладке «Шум/детали»."
		})
	}()
}

func noiseBenchmarkRegions(m *Img) []image.Rectangle {
	type patch struct {
		x, y int
		v    float64
	}
	var patches []patch
	for gy := 0; gy < 5; gy++ {
		for gx := 0; gx < 5; gx++ {
			x, y := (2*gx+1)*m.W/10, (2*gy+1)*m.H/10
			var values []float64
			for dy := -24; dy <= 24; dy += 4 {
				for dx := -24; dx <= 24; dx += 4 {
					X, Y := reflectWave(x+dx, m.W), reflectWave(y+dy, m.H)
					v := float64(m.at(min(1, m.C-1), X, Y))
					if v != 0 && finitePoint(v, 0) {
						values = append(values, v)
					}
				}
			}
			patches = append(patches, patch{x, y, median(values)})
		}
	}
	for i := 0; i < len(patches); i++ {
		for j := i + 1; j < len(patches); j++ {
			if patches[j].v < patches[i].v {
				patches[i], patches[j] = patches[j], patches[i]
			}
		}
	}
	selected := []patch{patches[2], patches[12], patches[24]}
	stars := detectEmpiricalStars(m, true)
	if len(stars) > 0 {
		selected = append(selected, patch{int(stars[0].x), int(stars[0].y), 0})
	}
	var regions []image.Rectangle
	for _, p := range selected {
		w, h := min(256, m.W), min(256, m.H)
		x, y := max(0, min(m.W-w, p.x-w/2)), max(0, min(m.H-h, p.y-h/2))
		regions = append(regions, image.Rect(x, y, x+w, y+h))
	}
	return regions
}

func (u *ui) noiseView(gtx layout.Context) layout.Dimensions {
	s := &u.noise
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Body1(u.th, "Шумоподавление и детали — линейные данные").Layout),
			layout.Rigid(material.Body2(u.th, "Starlet: B3-spline, 4 масштаба; ослабление слабых коэффициентов, усиление только значимых деталей. Внешние Siril NL-Bayes / GraXpert должны быть установлены; модели не загружаются автоматически.\nНасыщение и пропуски защищены в Starlet. Усиления действуют только для Starlet. Удалённый сигнал: серый — ноль, чёрный/белый — ±4σ; цвет показывает различия каналов. Это отображение, не сохранённые данные.").Layout),

			layout.Rigid(material.Body2(u.th, s.note).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if s.comparison == "" {
					return layout.Dimensions{}
				}
				gtx.Constraints.Max.Y = gtx.Dp(150)
				s.list.Axis = layout.Vertical
				rows := strings.Split(s.comparison, "\n")
				return material.List(u.th, &s.list).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions { return material.Body2(u.th, rows[i]).Layout(gtx) })
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if !s.hasPreview || s.source != u.result {
					return layout.Center.Layout(gtx, material.Body2(u.th, "Рассчитайте пробу. После изменения параметров необходимо пересчитать перед применением.").Layout)
				}
				linkPanels(&s.panels)
				var children []layout.FlexChild
				for side := 0; side < 2; side++ {
					children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						img := s.images[side][u.si()]
						label := []string{"До", "После — общая растяжка"}[side]
						if side == 1 && s.showResidual {
							img = s.residual
							label = "Удалённый сигнал: До − После, ±4σ"
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(material.Body2(u.th, label).Layout), layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return u.zoomView(gtx, &s.panels[side], img, nil, nil) }))
					}))
				}
				return layout.Flex{}.Layout(gtx, children...)
			}),
		)
	})
}
