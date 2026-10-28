package main

import (
	"fmt"
	"math"
	"sort"
)

// LinearFit in the PixInsight sense: each tile is mapped by y = a + b·x per channel onto a reference,
// the line minimising the mean absolute deviation (robust to stars and to the odd misfit pixel), with
// pixels outside [fitLow, fitHigh] in either image rejected. PixInsight fits whole registered frames
// of one field; for a mosaic the pixel pairs come from the overlaps. The reference is the tile with the
// most overlap; the others follow in order of overlap with tiles already fitted, using all of them.

const (
	fitLow  = 0    // PixInsight "reject low": also drops black borders
	fitHigh = 0.92 // PixInsight "reject high": saturated stars
)

// fitLine returns a, b of y = a + b·x minimising Σ|y − a − b·x| (Numerical Recipes medfit).
func fitLine(x, y []float64) (a, b float64) {
	n := float64(len(x))
	var sx, sy, sxy, sxx float64
	for i := range x {
		sx, sy, sxy, sxx = sx+x[i], sy+y[i], sxy+x[i]*y[i], sxx+x[i]*x[i]
	}
	del := n*sxx - sx*sx
	if del == 0 {
		return 0, 1
	}
	aa, bb := (sxx*sy-sx*sxy)/del, (n*sxy-sx*sy)/del // least squares start
	var chisq float64
	for i := range x {
		d := y[i] - (aa + bb*x[i])
		chisq += d * d
	}
	sigb := math.Sqrt(chisq / del)
	tmp := make([]float64, len(x))
	rofunc := func(b float64) (float64, float64) { // returns Σ x·sgn(residual) and the median intercept
		for i := range x {
			tmp[i] = y[i] - b*x[i]
		}
		sort.Float64s(tmp)
		a := tmp[len(tmp)/2]
		var s float64
		for i := range x {
			d := y[i] - (b*x[i] + a)
			if math.Abs(d) > 1e-12 {
				s += x[i] * math.Copysign(1, d)
			}
		}
		return s, a
	}
	b1 := bb
	f1, a := rofunc(b1)
	if sigb == 0 {
		return a, b1
	}
	b2 := bb + math.Copysign(3*sigb, f1)
	f2, _ := rofunc(b2)
	for it := 0; f1*f2 > 0 && it < 60; it++ { // bracket the root
		b2, b1, f1 = b2+1.6*(b2-b1), b2, f2
		f2, _ = rofunc(b2)
	}
	sigb *= 0.01
	for it := 0; math.Abs(b2-b1) > sigb && it < 100; it++ { // bisect
		bm := 0.5 * (b1 + b2)
		if bm == b1 || bm == b2 {
			break
		}
		f, _ := rofunc(bm)
		if f*f1 >= 0 {
			f1, b1 = f, bm
		} else {
			b2 = bm
		}
	}
	bb = b1
	_, a = rofunc(bb)
	return a, bb
}

// overlapSamples pairs pixels of tile j with the same sky point in tile i (2% tile edges skipped),
// on a grid sized to give at most ~150k pairs.
func overlapSamples(mi, mj *Img, wi, wj Warp) (pj, pi [][2]float64) {
	step := max(1, int(math.Sqrt(float64(mj.W*mj.H)/150000)))
	gi, gj := 0.02*float64(max(mi.W, mi.H)), 0.02*float64(max(mj.W, mj.H))
	for y := 0; y < mj.H; y += step {
		for x := 0; x < mj.W; x += step {
			fx, fy := float64(x), float64(y)
			if fx < gj || fy < gj || fx > float64(mj.W-1)-gj || fy > float64(mj.H-1)-gj {
				continue
			}
			u, v := wi.inv(wj.apply(fx, fy))
			if u >= gi && v >= gi && u <= float64(mi.W-1)-gi && v <= float64(mi.H-1)-gi {
				pj, pi = append(pj, [2]float64{fx, fy}), append(pi, [2]float64{u, v})
			}
		}
	}
	return pj, pi
}

// linearFit returns brightness-matched copies of the tiles and a note per tile.
func linearFit(tiles []*Tile, W []Warp) ([]*Img, []string) {
	n := len(tiles)
	type pair struct{ pj, pi [][2]float64 } // pixels of j and of i at the same sky points
	ov := make([][]pair, n)                 // ov[j][i]
	weight := make([][]int, n)
	for j := range ov {
		ov[j] = make([]pair, n)
		weight[j] = make([]int, n)
	}
	parallel(n, func(lo, hi int) {
		for j := lo; j < hi; j++ {
			for i := 0; i < n; i++ {
				if i != j {
					pj, pi := overlapSamples(tiles[i].Img, tiles[j].Img, W[i], W[j])
					ov[j][i] = pair{pj, pi}
					weight[j][i] = len(pj)
				}
			}
		}
	})
	ref, best := 0, -1
	for j := range weight {
		s := 0
		for _, w := range weight[j] {
			s += w
		}
		if s > best {
			ref, best = j, s
		}
	}

	// fit on smoothed copies: y = a + b·x survives blurring, while interpolation of sharp stars and
	// residual misalignment (which bias the slope) mostly average out
	low := make([]*Img, n)
	for i, t := range tiles {
		low[i] = lowpass(t.Img)
	}
	type line struct{ a, b float64 }
	coef := make([][]line, n)
	notes := make([]string, n)
	coef[ref] = make([]line, tiles[ref].Img.C)
	for c := range coef[ref] {
		coef[ref][c] = line{0, 1}
	}
	notes[ref] = tiles[ref].Name + ": опорная"
	for done := 1; done < n; done++ {
		k, kw := -1, 0
		for j := range tiles {
			if coef[j] != nil {
				continue
			}
			s := 0
			for i := range tiles {
				if coef[i] != nil {
					s += weight[j][i]
				}
			}
			if k < 0 || s > kw {
				k, kw = j, s
			}
		}
		m := tiles[k].Img
		coef[k] = make([]line, m.C)
		var parts []string
		for c := 0; c < m.C; c++ {
			var xs, ys []float64
			for i := range tiles {
				if coef[i] == nil || i == k {
					continue
				}
				p, ci := ov[k][i], min(c, low[i].C-1)
				for s := range p.pj {
					x := float64(low[k].at(c, int(p.pj[s][0]), int(p.pj[s][1])))
					y := coef[i][ci].a + coef[i][ci].b*float64(bilinear(low[i], ci, p.pi[s][0], p.pi[s][1]))
					if x > fitLow && x < fitHigh && y > fitLow && y < fitHigh {
						xs, ys = append(xs, x), append(ys, y)
					}
				}
			}
			a, b := 0.0, 1.0
			if len(xs) >= 100 {
				if a, b = fitLine(xs, ys); b < 0.2 || b > 5 || math.IsNaN(a+b) {
					a, b = 0, 1 // implausible: leave the channel alone
				}
			}
			coef[k][c] = line{a, b}
			parts = append(parts, fmt.Sprintf("%s ×%.3f %+.5f", []string{"R", "G", "B"}[min(c, 2)], b, a))
			if m.C == 1 {
				parts[len(parts)-1] = parts[len(parts)-1][2:]
			}
		}
		if kw == 0 {
			parts = []string{"нет перекрытия, без изменений"}
		}
		notes[k] = fmt.Sprintf("%s: %v", tiles[k].Name, parts)
	}

	out := make([]*Img, n)
	for k, t := range tiles {
		if k == ref {
			out[k] = t.Img
			continue
		}
		m := t.Img
		fit := newImg(m.W, m.H, m.C)
		fit.Hdr, fit.BottomUp = m.Hdr, m.BottomUp
		for c := 0; c < m.C; c++ {
			l := coef[k][c]
			src, dst := m.Pix[c*m.W*m.H:(c+1)*m.W*m.H], fit.Pix[c*m.W*m.H:(c+1)*m.W*m.H]
			parallel(len(src), func(lo, hi int) {
				for i := lo; i < hi; i++ {
					dst[i] = float32(l.a + l.b*float64(src[i]))
				}
			})
		}
		out[k] = fit
	}
	return out, notes
}
