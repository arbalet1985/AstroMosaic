package main

import (
	"fmt"
	"math"
	"strings"
)

const gradientGrid = 20

type gradientSample struct {
	x, y            float64
	values          []float64
	valid, excluded bool
}

type gradientModel struct {
	w, h    int
	samples [gradientGrid * gradientGrid]gradientSample
	coeff   [][6]float64
	level   []float64
	note    string
}

func gradientBasis(x, y float64, w, h int) []float64 {
	u, v := 2*x/float64(w-1)-1, 2*y/float64(h-1)-1
	return []float64{1, u, v, u * u, u * v, v * v}
}

func gradientValue(p [6]float64, x, y float64, w, h int) float64 {
	u, v := 2*x/float64(w-1)-1, 2*y/float64(h-1)-1
	return p[0] + p[1]*u + p[2]*v + p[3]*u*u + p[4]*u*v + p[5]*v*v
}

// A bounded patch median avoids stars; whole cells can be excluded by the user.
// Extended diffuse emission cannot be identified uniquely from one image: the
// model must be reviewed and object cells excluded before explicit application.
func estimateGradient(m *Img, mask [gradientGrid * gradientGrid]bool) (gradientModel, error) {
	g := gradientModel{w: m.W, h: m.H, coeff: make([][6]float64, m.C), level: make([]float64, m.C)}
	if m.W < 100 || m.H < 100 || (m.C != 1 && m.C != 3) {
		return g, fmt.Errorf("нужно изображение ≥100×100, ч/б или RGB")
	}
	for iy := 0; iy < gradientGrid; iy++ {
		for ix := 0; ix < gradientGrid; ix++ {
			i := iy*gradientGrid + ix
			s := &g.samples[i]
			s.x, s.y = (float64(ix)+.5)*float64(m.W)/gradientGrid, (float64(iy)+.5)*float64(m.H)/gradientGrid
			s.excluded = mask[i]
			s.values = make([]float64, m.C)
			r := max(2, min(24, min(m.W, m.H)/(gradientGrid*3)))
			s.valid = true
			for c := 0; c < m.C; c++ {
				var vals []float64
				for y := int(s.y) - r; y <= int(s.y)+r; y++ {
					for x := int(s.x) - r; x <= int(s.x)+r; x++ {
						v := float64(m.at(c, x, y))
						if v != 0 && !math.IsNaN(v) && !math.IsInf(v, 0) {
							vals = append(vals, v)
						}
					}
				}
				if len(vals) < (2*r+1)*(2*r+1)*3/4 {
					s.valid = false
					break
				}
				s.values[c] = median(vals)
			}
		}
	}
	// Fit each channel independently, but share sample rejections across RGB.
	keep := make([]bool, len(g.samples))
	for i, s := range g.samples {
		keep[i] = s.valid && !s.excluded
	}
	fit := func(c, n int, use []bool, train bool) [6]float64 {
		ls := newLSQ(n)
		idx := make([]int, n)
		for j := range idx {
			idx[j] = j
		}
		for i, s := range g.samples {
			if !use[i] || (train && i%5 == 0) {
				continue
			}
			ls.add(idx, gradientBasis(s.x, s.y, m.W, m.H)[:n], s.values[c], 1)
		}
		var p [6]float64
		copy(p[:], ls.solve())
		return p
	}
	coverage := func() bool {
		var quadrants [4]int
		count := 0
		for i, s := range g.samples {
			if keep[i] {
				q := 0
				if s.x >= float64(m.W)/2 {
					q++
				}
				if s.y >= float64(m.H)/2 {
					q += 2
				}
				quadrants[q]++
				count++
			}
		}
		if count < 60 {
			return false
		}
		for _, n := range quadrants {
			if n < 10 {
				return false
			}
		}
		return true
	}
	if !coverage() {
		return g, fmt.Errorf("нужно ≥60 проб фона, ≥10 в каждом квадранте")
	}
	// Robust quadratic residual clipping: reject compact nebulae and galaxies.
	for it := 0; it < 5; it++ {
		next := append([]bool(nil), keep...)
		for c := 0; c < m.C; c++ {
			p := fit(c, 6, keep, true)
			var residual []float64
			for i, s := range g.samples {
				if keep[i] && i%5 != 0 {
					residual = append(residual, s.values[c]-gradientValue(p, s.x, s.y, m.W, m.H))
				}
			}
			center := median(residual)
			for i := range residual {
				residual[i] = math.Abs(residual[i] - center)
			}
			lim := math.Max(4*1.4826*median(residual), 1e-6)
			for i, s := range g.samples {
				if keep[i] && math.Abs(s.values[c]-gradientValue(p, s.x, s.y, m.W, m.H)-center) > lim {
					next[i] = false
				}
			}
		}
		keep = next
		if !coverage() {
			return g, fmt.Errorf("после защиты объектов недостаточно распределённых проб; исключите объекты вручную")
		}
	}
	var notes []string
	corrected := 0
	for c := 0; c < m.C; c++ {
		plane, curve := fit(c, 3, keep, true), fit(c, 6, keep, true)
		var pe, ce, raw []float64
		for i, s := range g.samples {
			if keep[i] && i%5 == 0 {
				pe = append(pe, math.Abs(s.values[c]-gradientValue(plane, s.x, s.y, m.W, m.H)))
				ce = append(ce, math.Abs(s.values[c]-gradientValue(curve, s.x, s.y, m.W, m.H)))
				raw = append(raw, s.values[c])
			}
		}
		if len(pe) < 10 {
			return g, fmt.Errorf("мало отложенных проб фона")
		}
		n := 3
		name := "плоскость"
		if median(ce) < .9*median(pe) && median(pe)-median(ce) > 1e-6 {
			n = 6
			name = "квадратичная"
		}
		baseline := median(raw)
		for i := range raw {
			raw[i] = math.Abs(raw[i] - baseline)
		}
		e := median(pe)
		if n == 6 {
			e = median(ce)
		}
		if e > .9*median(raw) || median(raw) < 1e-6 {
			g.coeff[c][0], g.level[c] = baseline, baseline
			notes = append(notes, channelName(c, m.C)+": значимый градиент не подтверждён, канал сохранён")
			continue
		}
		corrected++
		g.coeff[c] = fit(c, n, keep, false)
		var levels []float64
		for i, s := range g.samples {
			if keep[i] {
				levels = append(levels, gradientValue(g.coeff[c], s.x, s.y, m.W, m.H))
			}
		}
		g.level[c] = median(levels)
		notes = append(notes, fmt.Sprintf("%s: %s, ошибка проверки %.5f → %.5f", channelName(c, m.C), name, median(raw), e))
	}
	if corrected == 0 {
		return g, fmt.Errorf("значимый градиент на отложенных пробах не подтверждён")
	}
	count := 0
	for i := range g.samples {
		g.samples[i].excluded = !keep[i]
		if keep[i] {
			count++
		}
	}
	g.note = fmt.Sprintf("Фон: %d/400 проб. %s. Медианный уровень модели сохранён.", count, strings.Join(notes, "; "))
	return g, nil
}

func subtractGradient(m *Img, g gradientModel) *Img {
	out := newImg(m.W, m.H, m.C)
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	n := m.W * m.H
	parallel(m.H, func(lo, hi int) {
		for y := lo; y < hi; y++ {
			for x := 0; x < m.W; x++ {
				// A zero in any channel marks missing mosaic coverage, consistently for RGB.
				valid := true
				for c := 0; c < m.C; c++ {
					v := float64(m.Pix[c*n+y*m.W+x])
					if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
						valid = false
					}
				}
				for c := 0; c < m.C; c++ {
					i := c*n + y*m.W + x
					if valid {
						out.Pix[i] = m.Pix[i] - float32(gradientValue(g.coeff[c], float64(x), float64(y), m.W, m.H)-g.level[c])
					} else {
						v := m.Pix[i]
						if !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) {
							out.Pix[i] = v
						}
					}
				}
			}
		}
	})
	return out
}
