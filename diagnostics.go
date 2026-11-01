package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget/material"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const diagnosticSide = 4

type fieldBin struct {
	dx, dy, square    float64
	count             int
	fwhm, ellipticity []float64
}

type tileDiagnostic struct {
	name    string
	psfNote string
	bins    [diagnosticSide * diagnosticSide]fieldBin
	maps    [2]paint.ImageOp
}

type fieldDiagnostics struct {
	tiles                   []tileDiagnostic
	matchCount, validationN int
	rms, validationRMS      float64
	modelNote               string
}

func diagnosticBin(x, y float64, m *Img) int {
	col := min(diagnosticSide-1, max(0, int(x*diagnosticSide/float64(m.W))))
	row := min(diagnosticSide-1, max(0, int(y*diagnosticSide/float64(m.H))))
	return row*diagnosticSide + col
}

// starShape fits a Gaussian core to log(flux above local sky), rather than
// clipped positive moments which broaden faint stars with background noise.
// It is a diagnostic estimate, not a spatially varying deconvolution PSF.
func starShape(m *Img, s star) (fwhm, ellipticity float64, ok bool) {
	return starShapeChannel(m, s, min(1, m.C-1))
}

func starShapeChannel(m *Img, s star, c int) (fwhm, ellipticity float64, ok bool) {
	p, ok := fitStarPSF(m, s, c)
	return p.fwhm, p.ellipticity, ok
}

type starPSF struct {
	fwhm, ellipticity, x, y, sky float64
	covariance                   psfCovariance
}

func fitStarPSF(m *Img, s star, c int) (starPSF, bool) {
	x, y, ok := centroid(m, c, s.x, s.y)
	if !ok {
		return starPSF{}, false
	}
	ix, iy := int(math.Round(x)), int(math.Round(y))
	var ring []float64
	for d := -10; d <= 10; d++ {
		ring = append(ring, float64(m.at(c, ix+d, iy-10)), float64(m.at(c, ix+d, iy+10)), float64(m.at(c, ix-10, iy+d)), float64(m.at(c, ix+10, iy+d)))
	}
	bg := median(ring)
	for i := range ring {
		ring[i] = math.Abs(ring[i] - bg)
	}
	noise := 1.4826 * median(ring)
	peak := float64(m.at(c, ix, iy)) - bg
	if peak <= math.Max(10*noise, 1e-6) {
		return starPSF{}, false
	}
	type sample struct {
		basis []float64
		value float64
	}
	var samples []sample
	lsq := newLSQ(6)
	for dy := -6; dy <= 6; dy++ {
		for dx := -6; dx <= 6; dx++ {
			if dx*dx+dy*dy > 36 {
				continue
			}
			v := float64(m.at(c, ix+dx, iy+dy)) - bg
			if v < math.Max(4*noise, .08*peak) {
				continue
			}
			u, vpos := float64(ix+dx)-x, float64(iy+dy)-y
			basis := []float64{1, u, vpos, u * u, u * vpos, vpos * vpos}
			z := math.Log(v)
			lsq.add([]int{0, 1, 2, 3, 4, 5}, basis, z, 1)
			samples = append(samples, sample{basis, z})
		}
	}
	if len(samples) < 12 {
		return starPSF{}, false
	}
	p := lsq.solve()
	a, b, d := -2*p[3], -p[4], -2*p[5]
	delta := math.Hypot(a-d, 2*b)
	lo, hi := (a+d-delta)/2, (a+d+delta)/2
	if lo <= 0 || hi <= 0 {
		return starPSF{}, false
	}
	var error2 float64
	for _, s := range samples {
		v := -s.value
		for i, c := range s.basis {
			v += c * p[i]
		}
		error2 += v * v
	}
	major, minor := 2.354820045/math.Sqrt(lo), 2.354820045/math.Sqrt(hi)
	if math.Sqrt(error2/float64(len(samples))) > .25 || minor < 1 || major > 14 {
		return starPSF{}, false
	}
	det := a*d - b*b
	return starPSF{math.Sqrt(major * minor), 1 - minor/major, x, y, bg, psfCovariance{d / det, -b / det, a / det}}, true
}

func measureFieldDiagnostics(tiles []*Tile, W []Warp, measured registrationMeasurements) fieldDiagnostics {
	d := fieldDiagnostics{tiles: make([]tileDiagnostic, len(tiles)), matchCount: len(measured.matches), rms: rms(W, measured.matches), validationN: measured.validationN, validationRMS: measured.validationRMS, modelNote: measured.modelNote}
	for i, t := range tiles {
		d.tiles[i].name = t.Name
	}
	for _, m := range measured.matches {
		for _, side := range []struct {
			i, other int
			p, q     [2]float64
		}{{m.A, m.B, m.pA, m.pB}, {m.B, m.A, m.pB, m.pA}} {
			x, y := W[side.i].inv(W[side.other].apply(side.q[0], side.q[1]))
			if x < 0 || y < 0 {
				continue
			}
			dx, dy := x-side.p[0], y-side.p[1]
			b := &d.tiles[side.i].bins[diagnosticBin(side.p[0], side.p[1], tiles[side.i].Img)]
			b.dx, b.dy, b.square, b.count = b.dx+dx, b.dy+dy, b.square+dx*dx+dy*dy, b.count+1
		}
	}
	parallel(len(tiles), func(lo, hi int) {
		for i := lo; i < hi; i++ {
			m := tiles[i].Img
			stars := detectStars(m)
			for _, s := range stars[:min(len(stars), 4000)] {
				f, e, ok := starShape(m, s)
				if !ok {
					continue
				}
				b := &d.tiles[i].bins[diagnosticBin(s.x, s.y, m)]
				b.fwhm, b.ellipticity = append(b.fwhm, f), append(b.ellipticity, e)
			}
		}
	})
	for i := range d.tiles {
		for c := 0; c < 2; c++ {
			d.tiles[i].maps[c] = paint.NewImageOp(diagnosticImage(d.tiles[i], c))
		}
	}
	return d
}

// A fixed grid is intentional: labels remain readable when a detector has a
// very wide aspect ratio. Cells correspond to equal fractions of the source.
func diagnosticImage(t tileDiagnostic, kind int) *image.RGBA {
	const cellSize = 160
	out := image.NewRGBA(image.Rect(0, 0, diagnosticSide*cellSize, diagnosticSide*cellSize))
	for i, b := range t.bins {
		x, y := (i%diagnosticSide)*cellSize, (i/diagnosticSide)*cellSize
		col := color.RGBA{45, 48, 53, 255}
		var labels []string
		if kind == 0 && b.count > 0 {
			rms := math.Sqrt(b.square / float64(b.count))
			col = chromColor(rms / 2)
			labels = []string{fmt.Sprintf("N=%d", b.count), fmt.Sprintf("RMS %.3f px", rms), fmt.Sprintf("dx %+.3f", b.dx/float64(b.count)), fmt.Sprintf("dy %+.3f", b.dy/float64(b.count))}
		} else if kind == 1 && len(b.fwhm) > 0 {
			f, e := median(b.fwhm), median(b.ellipticity)
			col = chromColor((f - 2) / 6)
			labels = []string{fmt.Sprintf("N=%d", len(b.fwhm)), fmt.Sprintf("FWHM %.2f px", f), fmt.Sprintf("e %.3f", e)}
		} else {
			labels = []string{"NO DATA"}
		}
		draw.Draw(out, image.Rect(x+1, y+1, x+cellSize-1, y+cellSize-1), image.NewUniform(col), image.Point{}, draw.Src)
		for j, label := range labels {
			// Outline keeps measurements readable at either end of the heat scale.
			for _, offset := range []image.Point{{1, 1}, {0, 0}} {
				ink := color.White
				if offset.X == 1 {
					ink = color.Black
				}
				drawer := font.Drawer{Dst: out, Src: image.NewUniform(ink), Face: basicfont.Face7x13, Dot: fixed.P(x+10+offset.X, y+24+j*22+offset.Y)}
				drawer.DrawString(label)
			}
		}
		if kind == 0 && b.count > 0 {
			dx, dy := b.dx/float64(b.count)*20, b.dy/float64(b.count)*20
			if length := math.Hypot(dx, dy); length > 24 {
				dx, dy = dx*24/length, dy*24/length
			}
			X, Y := float64(x+80)+dx, float64(y+128)+dy
			line := func(ax, ay, bx, by float64) {
				n := max(1, int(math.Ceil(math.Hypot(bx-ax, by-ay)*2)))
				for k := 0; k <= n; k++ {
					f := float64(k) / float64(n)
					out.SetRGBA(int(math.Round(ax+(bx-ax)*f)), int(math.Round(ay+(by-ay)*f)), color.RGBA{255, 255, 255, 255})
				}
			}
			line(float64(x+80), float64(y+128), X, Y)
			if length := math.Hypot(dx, dy); length > 2 {
				ux, uy := dx/length, dy/length
				line(X, Y, X-5*ux+3*uy, Y-5*uy-3*ux)
				line(X, Y, X-5*ux-3*uy, Y-5*uy+3*ux)
			}
		}
	}
	return out
}

func (u *ui) diagnosticsView(gtx layout.Context) layout.Dimensions {
	d := &u.diagnostics
	if len(d.tiles) == 0 {
		return layout.Center.Layout(gtx, material.Body1(u.th, "Нажмите «Склеить»: диагностика строится по последней сшивке").Layout)
	}
	t := d.tiles[u.diagIndex]
	summary := fmt.Sprintf("Последняя сшивка: %d соответствий, RMS %.3f px (мозаика). ", d.matchCount, d.rms)
	if d.validationN > 0 {
		summary += fmt.Sprintf("Отдельная проверочная подгонка: %d отложенных, RMS %.3f px.", d.validationN, d.validationRMS)
	} else {
		summary += "Для проверки на отложенных нужно ≥60 соответствий."
	}
	return layout.UniformInset(unit.Dp(8)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if u.diagIndex == 0 {
							gtx = gtx.Disabled()
						}
						return material.Button(u.th, &u.diagPrev, "←").Layout(gtx)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if u.diagIndex+1 == len(d.tiles) {
							gtx = gtx.Disabled()
						}
						return material.Button(u.th, &u.diagNext, "→").Layout(gtx)
					}),
					layout.Flexed(1, material.Body1(u.th, fmt.Sprintf("  %d / %d  %s", u.diagIndex+1, len(d.tiles), t.name)).Layout),
				)
			}),
			layout.Rigid(material.Body2(u.th, summary).Layout),
			layout.Rigid(material.Body2(u.th, d.modelNote).Layout),
			layout.Rigid(material.Body2(u.th, t.psfNote).Layout),
			layout.Rigid(material.Body2(u.th, "Сетка 4×4 исходного поля, сверху вниз; NO DATA — нет измерений. Колесо: масштаб; перетаскивание: сдвиг.\nСлева: остатки принятых соответствий, пиксели плитки. Справа: ядра до PSF-согласования, G для RGB. e = 1 − малая/большая ось.").Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				children := make([]layout.FlexChild, 2)
				for c := range children {
					children[c] = layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(material.Body2(u.th, []string{"RMS: тёмный = 0, жёлтый ≥ 2 px; dx/dy — среднее смещение к другой плитке", "Медианы FWHM и e: тёмный ≤ 2, жёлтый ≥ 8 px; это оценка, не модель деконволюции"}[c]).Layout),
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return u.zoomView(gtx, &u.diagPanels[c], t.maps[c], func(_ f32.Point, _ pointer.Buttons) {}, nil)
							}),
						)
					})
				}
				return layout.Flex{}.Layout(gtx, children...)
			}),
		)
	})
}
