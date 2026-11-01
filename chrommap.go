package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"path/filepath"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

type chromMapTile struct {
	name          string
	report        chromatism
	maps          [2]paint.ImageOp
	scale         float64
	residualMaps  [2]paint.ImageOp
	residualScale float64
}

func (u *ui) resetChromPanels() {
	u.chromPanels = [2]panel{{needFit: true}, {needFit: true}}
}

// Analysis uses the original tiles, independently of stitching and without resampling them.
func (u *ui) inspectChromatism() {
	tiles, cells, _, _ := u.gather()
	if len(tiles) == 0 {
		u.status = "Сначала откройте плитки"
		return
	}
	names := make([]string, len(tiles))
	for i, k := range cells {
		names[i] = tiles[i].Name + " " + filepath.Base(u.tiles[k].path)
	}
	u.busy, u.status = true, "Измеряю хроматизм по звёздам…"
	flat := u.flat.image
	go func() {
		maps := make([]chromMapTile, len(tiles))
		for i, t := range tiles {
			m := t.Img
			if flat != nil {
				calibrated, _, err := applyMasterFlat(m, flat)
				if err != nil {
					u.post(func() { u.busy = false; u.status = "Карта: " + err.Error() })
					return
				}
				m = calibrated
				names[i] += " [master flat]"
			}
			r := measureChromatism(m)
			scale := channelAlignThreshold
			for _, c := range r.Channels {
				if c.Valid {
					scale = math.Max(scale, c.MaxShift)
				}
			}
			rs := .3
			for _, f := range r.Channels {
				for _, p := range f.Residuals {
					rs = math.Max(rs, math.Hypot(p.dx, p.dy))
				}
			}
			maps[i] = chromMapTile{name: names[i], report: r, scale: scale, residualScale: rs}
			for c, fit := range r.Channels {
				if fit.Valid {
					maps[i].maps[c] = paint.NewImageOp(chromatismImage(r, c, scale))
					maps[i].residualMaps[c] = paint.NewImageOp(chromResidualImage(r, c, rs))
				}
			}
		}
		u.post(func() {
			u.chromMaps, u.chromIndex, u.view, u.busy = maps, 0, 3, false
			u.resetChromPanels()
			u.status = "Карта исходных плиток: модель той же коррекции, что используется при совмещении каналов"
		})
	}()
}

func chromColor(t float64) color.RGBA {
	t = math.Min(1, math.Max(0, t))
	// Dark blue → teal → yellow, identical magnitude scale for R and B.
	a, b, f := color.RGBA{18, 25, 58, 255}, color.RGBA{24, 160, 160, 255}, t*2
	if t > .5 {
		a, b, f = b, color.RGBA{255, 220, 65, 255}, (t-.5)*2
	}
	return color.RGBA{uint8(float64(a.R)*(1-f) + float64(b.R)*f), uint8(float64(a.G)*(1-f) + float64(b.G)*f), uint8(float64(a.B)*(1-f) + float64(b.B)*f), 255}
}

// Arrows show the displacement of the source channel towards green, i.e. minus
// the sampling offset. Positions and aspect ratio follow the normal tile preview.
func chromatismImage(r chromatism, channel int, scale float64) *image.RGBA {
	w := max(2, int(720*float64(r.W)/float64(max(r.W, r.H))))
	h := max(2, int(720*float64(r.H)/float64(max(r.W, r.H))))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	fit := r.Channels[channel]
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			px, py := float64(x)*float64(r.W-1)/float64(w-1), float64(y)*float64(r.H-1)/float64(h-1)
			u, v := fit.apply(px, py)
			out.SetRGBA(x, y, chromColor(math.Hypot(u-px, v-py)/scale))
		}
	}
	line := func(x, y, X, Y float64, col color.RGBA) {
		n := max(1, int(math.Ceil(math.Hypot(X-x, Y-y)*2)))
		for i := 0; i <= n; i++ {
			f := float64(i) / float64(n)
			out.SetRGBA(int(math.Round(x+(X-x)*f)), int(math.Round(y+(Y-y)*f)), col)
		}
	}
	for y := 30; y < h; y += 60 {
		for x := 30; x < w; x += 60 {
			px, py := float64(x)*float64(r.W-1)/float64(w-1), float64(y)*float64(r.H-1)/float64(h-1)
			u, v := fit.apply(px, py)
			dx, dy := (px-u)*24/scale, (py-v)*24/scale
			X, Y := float64(x)+dx, float64(y)+dy
			white := color.RGBA{255, 255, 255, 255}
			line(float64(x), float64(y), X, Y, white)
			if d := math.Hypot(dx, dy); d >= 2 {
				ux, uy := dx/d, dy/d
				line(X, Y, X-4*ux+3*uy, Y-4*uy-3*ux, white)
				line(X, Y, X-4*ux-3*uy, Y-4*uy+3*ux, white)
			}
		}
	}
	return out
}

func (u *ui) chromatismView(gtx layout.Context) layout.Dimensions {
	if len(u.chromMaps) == 0 {
		return layout.Center.Layout(gtx, material.Body1(u.th, "Откройте плитки и нажмите «Карта хроматизма»").Layout)
	}
	t := u.chromMaps[u.chromIndex]
	scale := t.scale
	if u.chromResidual {
		scale = t.residualScale
	}
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if u.chromIndex == 0 {
							gtx = gtx.Disabled()
						}
						return material.Button(u.th, &u.mapPrev, "←").Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if u.chromIndex+1 == len(u.chromMaps) {
							gtx = gtx.Disabled()
						}
						return material.Button(u.th, &u.mapNext, "→").Layout(gtx)
					}),
					layout.Flexed(1, material.Body1(u.th, fmt.Sprintf("  %d / %d  %s  (%d×%d)", u.chromIndex+1, len(u.chromMaps), t.name, t.report.W, t.report.H)).Layout),
					layout.Rigid(material.Button(u.th, &u.chromResidualBtn, "Коррекция / остатки").Layout),
				)
			}),
			layout.Rigid(material.Body2(u.th, "Исходные плитки: аффинная модель и проверенная радиальная добавка R/B относительно G. Коррекция при сшивке: «Совместить каналы», максимум ≥0,30 px.\nКарта коррекции: направление исправления. Карта остатков: отложенные звёзды, вектор ошибки ×24/шкала, яркость — её модуль; чёрный — нет проб.").Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(material.Body2(u.th, "0 px  ").Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						bar := image.NewRGBA(image.Rect(0, 0, 256, 12))
						for x := 0; x < 256; x++ {
							for y := 0; y < 12; y++ {
								bar.SetRGBA(x, y, chromColor(float64(x)/255))
							}
						}
						return (widget.Image{Src: paint.NewImageOp(bar)}).Layout(gtx)
					}),
					layout.Rigid(material.Body2(u.th, fmt.Sprintf("  %.2f px; шкала общая для R и B этой плитки", scale)).Layout),
				)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				children := make([]layout.FlexChild, 2)
				for c := range children {
					children[c] = layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						fit := t.report.Channels[c]
						if !fit.Valid {
							return layout.Center.Layout(gtx, material.Body1(u.th, []string{"R → G", "B → G"}[c]+": нет надёжной модели.\nНужно ≥ 30 подходящих звёзд в RGB.\nДля ч/б хроматизм не измеряется.").Layout)
						}
						state := "ниже порога: не применяется"
						if fit.MaxShift >= channelAlignThreshold {
							state = "выше порога коррекции"
						}
						model := "аффинная"
						if fit.Radial != 0 {
							model = "аффинная + радиальная"
						}
						img := t.maps[c]
						if u.chromResidual {
							img = t.residualMaps[c]
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(material.Body2(u.th, fmt.Sprintf("%s: %s; максимум %.2f px; %d звёзд\nОшибка %.3f px; проверка %.3f→%.3f px (N=%d); %s", []string{"R → G", "B → G"}[c], model, fit.MaxShift, fit.Stars, fit.MedianResidual, fit.ValidationBefore, fit.ValidationAfter, len(fit.Residuals), state)).Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return u.zoomView(gtx, &u.chromPanels[c], img, func(_ f32.Point, _ pointer.Buttons) {}, nil)
							}),
						)
					})
				}
				return layout.Flex{}.Layout(gtx, children...)
			}),
		)
	})
}
