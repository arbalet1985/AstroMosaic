package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type qualityState struct {
	open, refresh widget.Clickable
	source        *Img
	data          qualityReport
	list          widget.List
}

func (u *ui) handleQuality(gtx layout.Context) {
	if u.quality.open.Clicked(gtx) && !u.busy && u.result != nil {
		u.view = 8
		if u.quality.source != u.result {
			u.calculateQuality()
		}
	}
	if u.quality.refresh.Clicked(gtx) && !u.busy && u.result != nil {
		u.calculateQuality()
	}
}
func (u *ui) calculateQuality() {
	before, after := u.baseline, u.result
	if before == nil {
		before = after
	}
	u.busy = true
	u.status = "Измеряю эффект улучшений на общих звёздах и исходных пикселях…"
	go func() {
		r := measureQuality(before, after)
		u.post(func() {
			u.busy = false
			if u.result != after {
				return
			}
			u.quality.source = after
			u.quality.data = r
			u.status = "Отчёт качества готов. Рекомендации — в конце отчёта."
		})
	}()
}

func (u *ui) qualityView(gtx layout.Context) layout.Dimensions {
	s := &u.quality
	s.list.Axis = layout.Vertical
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Body1(u.th, "Отчёт качества — исходный результат до всех улучшений → текущий").Layout),

			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if s.source != u.result {
					return material.Body2(u.th, "Отчёт появится после расчёта. Пробы без применения не учитываются.").Layout(gtx)
				}
				r := s.data
				return material.List(u.th, &s.list).Layout(gtx, 10+len(r.Recommendations), func(gtx layout.Context, i int) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						switch i {
						case 0:
							affected := len(r.values(0, 0, "width", true))
							text := fmt.Sprintf("Изменено %d из %d пикселей (%.3f%%). Нечисловых: %d.\nОбщих звёзд в контроле: %d, из них с изменениями в окрестности: %d. Выборка — до 400 ярких изолированных ненасыщенных звёзд, пригодных для измерения в обеих версиях.", r.Changed, r.W*r.H, 100*float64(r.Changed)/float64(r.W*r.H), r.Invalid, len(r.Stars), affected)
							if len(r.Operations) > 0 {
								text += "\nПрименено: " + strings.Join(r.Operations, " → ")
							}
							return material.Body2(u.th, text).Layout(gtx)
						case 1:
							var rows []string
							for c := 0; c < r.C; c++ {
								flux := "не определено"
								if math.Abs(r.Flux[0][c]) > 1e-12 {
									flux = fmt.Sprintf("%+.6f%%", 100*(r.Flux[1][c]-r.Flux[0][c])/math.Abs(r.Flux[0][c]))
								}
								row := fmt.Sprintf("%s: шум %.6g → %.6g; изменение суммы сигнала %s", channelName(c, r.C), r.Noise[0][c], r.Noise[1][c], flux)
								if len(r.Stars) > 0 {
									row += fmt.Sprintf("; FWHM %.2f → %.2f px; e %.3f → %.3f", median(r.values(0, c, "width", false)), median(r.values(1, c, "width", false)), median(r.values(0, c, "ellipse", false)), median(r.values(1, c, "ellipse", false)))
								}
								rows = append(rows, row)
							}
							if r.C == 3 && len(r.Stars) > 0 {
								rows = append(rows, fmt.Sprintf("RGB: смещение центров %.3f → %.3f px; расхождение профилей %.3f → %.3f (весь контроль)", median(r.values(0, 0, "shift", false)), median(r.values(1, 0, "shift", false)), median(r.values(0, 0, "color", false)), median(r.values(1, 0, "color", false))))
								if len(r.values(0, 0, "color", true)) > 0 {
									rows = append(rows, fmt.Sprintf("RGB только в изменённых окрестностях: %.3f → %.3f", median(r.values(0, 0, "color", true)), median(r.values(1, 0, "color", true))))
								}
							}
							return material.Body2(u.th, strings.Join(rows, "\n")).Layout(gtx)
						case 2, 3, 4, 5:
							titles := []string{"Карта изменённой площади: ярче — большая доля пикселей ячейки (0…100%)", "FWHM ядер, px: серый — До, бирюзовый — После; каналы слева направо", "Радиальные профили: средняя доля потока в кольце 1 px; пунктир До, линия После; R/G/B", "Расхождение RGB: X — До, Y — После; ниже диагонали — меньше; бирюзовые — изменённые окрестности"}
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(material.Body2(u.th, titles[i-2]).Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.Y, gtx.Constraints.Max.Y = gtx.Dp(220), gtx.Dp(220)
								return qualityChart(gtx, r, i-2, u.th)
							}))
						case 6:
							var rows []string
							for scale := 0; scale < waveLevels; scale++ {
								rows = append(rows, fmt.Sprintf("Масштаб %d–%d px: RMS коэффициентов %.6g → %.6g", 1<<scale, 2<<scale, r.ScalePower[0][scale], r.ScalePower[1][scale]))
							}
							rows = append(rows, "Мощность по масштабам измеряется в 16 одинаковых участках, включая сигнал и шум: её уменьшение само по себе не доказывает улучшение. Удалённый сигнал доступен в «Шум / детали» и деконволюции (серый ноль, ±4σ).")
							return material.Body2(u.th, strings.Join(rows, "\n")).Layout(gtx)
						case 7:
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(material.Body2(u.th, "RMS по масштабам (линейный сигнал): серый До, бирюзовый После; включает шум и структуру").Layout), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.Y, gtx.Constraints.Max.Y = gtx.Dp(220), gtx.Dp(220)
								return qualityChart(gtx, r, 4, u.th)
							}))
						case 8:
							return material.Body2(u.th, "FWHM — гауссова оценка ядра, не всей PSF. Профили измерены в радиусе 16 px; отражения большего размера не оцениваются. Шум — оценка по разностям соседних пикселей; мелкая структура тоже влияет. Сумма сигнала всего кадра включает фон и не заменяет фотометрию звёзд. Изменённая площадь не является процентом улучшения. Нулевой эффект и ухудшения показываются без общего «балла качества». Рекомендации используют ориентировочные пороги и требуют проверки пробы. Этапы совмещения и сшивки до исходного результата оцениваются отдельно в карте хроматизма и диагностике поля.").Layout(gtx)
						case 9:
							return material.Body1(u.th, "Предложения по дальнейшему улучшению").Layout(gtx)
						default:
							return material.Body2(u.th, "• "+r.Recommendations[i-10]).Layout(gtx)
						}
					})
				})
			}),
		)
	})
}

func qualityChart(gtx layout.Context, r qualityReport, kind int, th *material.Theme) layout.Dimensions {
	size := gtx.Constraints.Max
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{R: 22, G: 25, B: 30, A: 255})
	w, h := float32(size.X-64), float32(size.Y-40)
	line := func(x0, y0, x1, y1 float32, col color.NRGBA) {
		var p clip.Path
		p.Begin(gtx.Ops)
		p.MoveTo(f32.Pt(48+x0, 8+h-y0))
		p.LineTo(f32.Pt(48+x1, 8+h-y1))
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: p.End(), Width: 2}.Op())
	}
	label := func(x, y float32, text string) {
		offset := op.Offset(image.Pt(int(x), int(y))).Push(gtx.Ops)
		g := gtx
		g.Constraints.Min = image.Point{}
		g.Constraints.Max = image.Pt(120, 24)
		l := material.Caption(th, text)
		l.Color = color.NRGBA{R: 205, G: 210, B: 220, A: 255}
		l.Layout(g)
		offset.Pop()
	}
	gray := color.NRGBA{R: 135, G: 145, B: 155, A: 255}
	teal := color.NRGBA{R: 30, G: 195, B: 170, A: 255}
	if kind == 0 {
		for iy := 0; iy < 40; iy++ {
			for ix := 0; ix < 40; ix++ {
				count := (((iy+1)*r.H+39)/40 - (iy*r.H+39)/40) * (((ix+1)*r.W+39)/40 - (ix*r.W+39)/40)
				v := 0.
				if count > 0 {
					v = math.Min(1, float64(r.Area[iy*40+ix])/float64(count))
				}
				col := color.NRGBA{R: uint8(20 + 235*math.Sqrt(v)), G: uint8(15 + 180*v), B: uint8(25 + 30*v), A: 255}
				paint.FillShape(gtx.Ops, col, clip.Rect{Min: image.Pt(ix*size.X/40, iy*size.Y/40), Max: image.Pt((ix+1)*size.X/40, (iy+1)*size.Y/40)}.Op())
			}
		}
	} else if len(r.Stars) > 0 || kind == 4 {
		switch kind {
		case 1, 4:
			channels := r.C
			value := func(t, c int) float64 { return median(r.values(t, c, "width", false)) }
			name := func(c int) string { return channelName(c, r.C) }
			if kind == 4 {
				channels = waveLevels
				value = func(t, c int) float64 { return r.ScalePower[t][c] }
				name = func(c int) string { return fmt.Sprintf("%d–%d px", 1<<c, 2<<c) }
			}
			top := 1e-9
			for c := 0; c < channels; c++ {
				for t := 0; t < 2; t++ {
					top = math.Max(top, value(t, c)*1.15)
				}
			}
			label(2, 8, fmt.Sprintf("%.2g", top))
			label(2, 8+h/2, fmt.Sprintf("%.2g", top/2))
			label(2, 8+h-12, "0")
			for c := 0; c < channels; c++ {
				label(48+(float32(c)+.4)*w/float32(channels), 8+h, name(c))
				for t, col := range []color.NRGBA{gray, teal} {
					x := float32(c)*w/float32(channels) + float32(t)*w/float32(channels)/3 + w/float32(channels)/8
					val := value(t, c)
					paint.FillShape(gtx.Ops, col, clip.Rect{Min: image.Pt(int(48+x), int(8+h-h*float32(val/top))), Max: image.Pt(int(48+x+w/float32(channels)/3), int(8+h))}.Op())
				}
			}
		case 2:
			label(48, 8+h, "0 px")
			label(48+w/2, 8+h, "8 px")
			label(48+w-30, 8+h, "16 px")
			label(2, 8, "1")
			label(2, 8+h*.4, "1e-2")
			label(2, 8+h*.8, "1e-4")
			colors := []color.NRGBA{{R: 245, G: 80, B: 80, A: 255}, {R: 70, G: 210, B: 110, A: 255}, {R: 80, G: 140, B: 255, A: 255}}
			for t := 0; t < 2; t++ {
				for c := 0; c < r.C; c++ {
					for j := 0; j < 15; j++ {
						if t == 0 && j%2 == 1 {
							continue
						}
						a, b := r.Profiles[t][c][j], r.Profiles[t][c][j+1]
						y := func(v float64) float32 { return float32((math.Log10(math.Max(1e-5, v))+5)/5) * h }
						line(float32(j)*w/15, y(a), float32(j+1)*w/15, y(b), colors[c])
					}
				}
			}
		case 3:
			if r.C == 3 {
				top := .01
				for _, s := range r.Stars {
					top = math.Max(top, math.Max(s.Color[0], s.Color[1])*1.05)
				}
				label(48, 8+h, "0")
				label(48+w-40, 8+h, fmt.Sprintf("%.2f", top))
				label(2, 8, fmt.Sprintf("%.2f", top))
				line(0, 0, w, h, gray)
				for _, s := range r.Stars {
					col := gray
					if s.Changed {
						col = teal
					}
					x, y := 48+w*float32(s.Color[0]/top), 8+h-h*float32(s.Color[1]/top)
					paint.FillShape(gtx.Ops, col, clip.Ellipse{Min: image.Pt(int(x)-3, int(y)-3), Max: image.Pt(int(x)+3, int(y)+3)}.Op(gtx.Ops))
				}
			}
		}
	}
	return layout.Dimensions{Size: size}
}
