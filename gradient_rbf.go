package main

import (
	"fmt"
	"math"
	"strings"
)

func (g gradientModel) surface(c int, x, y float64) float64 {
	if len(g.table) == 0 {
		return gradientValue(g.coeff[c], x, y, g.w, g.h)
	}
	u, v := math.Max(0, math.Min(64, x*64/float64(g.w-1))), math.Max(0, math.Min(64, y*64/float64(g.h-1)))
	ix, iy := min(63, int(u)), min(63, int(v))
	fx, fy := u-float64(ix), v-float64(iy)
	t := g.table[c]
	return (1-fy)*((1-fx)*t[iy*65+ix]+fx*t[iy*65+ix+1]) + fy*((1-fx)*t[(iy+1)*65+ix]+fx*t[(iy+1)*65+ix+1])
}

func rbfBasis(x, y float64, w, h int) []float64 {
	b := gradientBasis(x, y, w, h)
	u, v := x/float64(w-1), y/float64(h-1)
	for iy := 0; iy < 5; iy++ {
		for ix := 0; ix < 5; ix++ {
			dx, dy := u-float64(ix)/4, v-float64(iy)/4
			b = append(b, math.Exp(-(dx*dx+dy*dy)/(2*.23*.23)))
		}
	}
	return b
}

func rbfValue(p []float64, x, y float64, w, h int) float64 {
	b := rbfBasis(x, y, w, h)
	var value float64
	for i, v := range p {
		value += v * b[i]
	}
	return value
}

func estimateGradientMode(m *Img, mask [400]bool, rbf bool) (gradientModel, error) {
	g, baseErr := estimateGradient(m, mask)
	if !rbf {
		return g, baseErr
	}
	keep := make([]bool, 400)
	count := 0
	var quadrants [4]int
	for i, s := range g.samples {
		keep[i] = s.valid && !mask[i]
		if keep[i] {
			count++
			quadrants[(i/20/10)*2+(i%20/10)]++
		}
	}
	if count < 120 {
		return g, fmt.Errorf("RBF: нужно ≥120 проб фона")
	}
	for _, n := range quadrants {
		if n < 20 {
			return g, fmt.Errorf("RBF: нужно ≥20 проб в каждом квадранте")
		}
	}
	fit := func(c, n int, train bool) []float64 {
		ls := newLSQ(n)
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		used := 0
		for i, s := range g.samples {
			if !keep[i] || (train && i%5 == 0) {
				continue
			}
			ls.add(idx, rbfBasis(s.x, s.y, m.W, m.H)[:n], s.values[c], 1)
			used++
		}
		// Smooth Gaussian bases with a ridge penalty, never narrow interpolation bumps.
		for i := 6; i < n; i++ {
			ls.add([]int{i}, []float64{1}, 0, float64(used)*1e-5)
		}
		return ls.solve()
	}
	for it := 0; it < 5; it++ {
		next := append([]bool(nil), keep...)
		for c := 0; c < m.C; c++ {
			p := fit(c, 31, true)
			var res []float64
			for i, s := range g.samples {
				if keep[i] && i%5 != 0 {
					res = append(res, s.values[c]-rbfValue(p, s.x, s.y, m.W, m.H))
				}
			}
			center := median(res)
			for i := range res {
				res[i] = math.Abs(res[i] - center)
			}
			lim := math.Max(4*1.4826*median(res), 1e-6)
			for i, s := range g.samples {
				if keep[i] && math.Abs(s.values[c]-rbfValue(p, s.x, s.y, m.W, m.H)-center) > lim {
					next[i] = false
				}
			}
		}
		keep = next
	}
	count = 0
	quadrants = [4]int{}
	for i, k := range keep {
		if k {
			count++
			quadrants[(i/20/10)*2+(i%20/10)]++
		}
	}
	if count < 120 {
		return g, fmt.Errorf("RBF: после защиты объектов осталось <120 проб")
	}
	for _, n := range quadrants {
		if n < 20 {
			return g, fmt.Errorf("RBF: после защиты объектов недостаточно покрытия")
		}
	}
	table := make([][]float64, m.C)
	levels := make([]float64, m.C)
	anyRBF := false
	var notes []string
	for c := 0; c < m.C; c++ {
		q, p := fit(c, 6, true), fit(c, 31, true)
		var qe, pe []float64
		for i, s := range g.samples {
			if keep[i] && i%5 == 0 {
				qe = append(qe, math.Abs(s.values[c]-rbfValue(q, s.x, s.y, m.W, m.H)))
				pe = append(pe, math.Abs(s.values[c]-rbfValue(p, s.x, s.y, m.W, m.H)))
			}
		}
		if len(pe) < 15 {
			return g, fmt.Errorf("RBF: нужно ≥15 проверочных проб")
		}
		if median(pe) < .9*median(qe) && median(qe)-median(pe) > 1e-6 {
			p = fit(c, 31, false)
			anyRBF = true
			notes = append(notes, fmt.Sprintf("%s: RBF, проверка %.5f→%.5f", channelName(c, m.C), median(qe), median(pe)))
		} else {
			if baseErr != nil {
				var raw []float64
				for i, s := range g.samples {
					if keep[i] && i%5 == 0 {
						raw = append(raw, s.values[c])
					}
				}
				level := median(raw)
				for i := range raw {
					raw[i] = math.Abs(raw[i] - level)
				}
				if median(raw) >= 1e-6 && median(qe) < .9*median(raw) {
					p = fit(c, 6, false)
				} else {
					p = []float64{level, 0, 0, 0, 0, 0}
				}
			} else {
				p = append([]float64(nil), g.coeff[c][:]...)
			}
			notes = append(notes, channelName(c, m.C)+": сохранена плоскость/квадратичная")
		}
		table[c] = make([]float64, 65*65)
		for y := 0; y < 65; y++ {
			for x := 0; x < 65; x++ {
				table[c][y*65+x] = rbfValue(p, float64(x)*float64(m.W-1)/64, float64(y)*float64(m.H-1)/64, m.W, m.H)
			}
		}
		var values []float64
		for i, s := range g.samples {
			if keep[i] {
				values = append(values, rbfValue(p, s.x, s.y, m.W, m.H))
			}
		}
		levels[c] = median(values)
	}
	if !anyRBF {
		if baseErr == nil {
			g.note = "RBF не улучшила проверку. " + g.note
		}
		return g, baseErr
	}
	g.table, g.level = table, levels
	for i := range g.samples {
		g.samples[i].excluded = !keep[i]
	}
	g.note = fmt.Sprintf("Фон: %d/400 проб. %s. Медианный уровень модели сохранён.", count, strings.Join(notes, "; "))
	return g, nil
}
