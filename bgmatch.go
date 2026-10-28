package main

import (
	"fmt"
	"math"
)

// matchBackground evens out the sky background across overlaps: every tile but the reference (the
// one with most overlap) gets an additive plane a + b·x + c·y per channel, fitted by least squares so
// that the starless backgrounds of overlapping tiles agree. Gain is left to LinearFit; this removes
// offsets and the gradient steps that a single offset cannot. Samples off by more than 3σ (nebula
// edges, satellite trails, misfit) are dropped and the fit repeated.
func matchBackground(tiles []*Tile, W []Warp) ([]*Img, []string) {
	n := len(tiles)
	bg := make([]*Img, n)
	parallel(n, func(lo, hi int) {
		for i := lo; i < hi; i++ {
			bg[i] = background(tiles[i].Img)
		}
	})
	type sample struct {
		i, j   int
		pi, pj [2]float64
	}
	var ss []sample
	cnt := make([]int, n)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			pj, pi := overlapSamples(bg[i], bg[j], W[i], W[j])
			step := max(1, len(pj)/3000)
			for s := 0; s < len(pj); s += step {
				ss = append(ss, sample{i, j, pi[s], pj[s]})
				cnt[i]++
				cnt[j]++
			}
		}
	}
	ref := 0
	for k := range cnt {
		if cnt[k] > cnt[ref] {
			ref = k
		}
	}
	// plane coordinates: tile pixel relative to the centre, in units of the longer side
	norm := func(m *Img, p [2]float64) (float64, float64) {
		s := float64(max(m.W, m.H))
		return (p[0] - float64(m.W-1)/2) / s, (p[1] - float64(m.H-1)/2) / s
	}
	col := func(k int) int { // unknowns of tile k: 3 per tile, the reference has none
		if k > ref {
			k--
		}
		return 3 * k
	}
	C := 1
	for _, t := range tiles {
		C = max(C, t.Img.C)
	}
	planes := make([][][3]float64, n) // planes[k][c]
	for k := range planes {
		planes[k] = make([][3]float64, C)
	}
	for c := 0; c < C; c++ {
		ch := func(k int) int { return min(c, bg[k].C-1) }
		type row struct {
			idx []int
			co  []float64
			rhs float64
		}
		var rows []row
		for _, s := range ss {
			vi, vj := float64(bilinear(bg[s.i], ch(s.i), s.pi[0], s.pi[1])), float64(bilinear(bg[s.j], ch(s.j), s.pj[0], s.pj[1]))
			if vi <= fitLow || vj <= fitLow || vi >= fitHigh || vj >= fitHigh {
				continue // black border or saturated
			}
			// (vi + d_i(pi)) − (vj + d_j(pj)) = 0
			var r row
			r.rhs = vj - vi
			for _, side := range []struct {
				k    int
				p    [2]float64
				sign float64
			}{{s.i, s.pi, 1}, {s.j, s.pj, -1}} {
				if side.k == ref {
					continue
				}
				x, y := norm(bg[side.k], side.p)
				b := col(side.k)
				r.idx = append(r.idx, b, b+1, b+2)
				r.co = append(r.co, side.sign, side.sign*x, side.sign*y)
			}
			if len(r.idx) > 0 {
				rows = append(rows, r)
			}
		}
		if len(rows) < 30 {
			continue
		}
		keep := make([]bool, len(rows))
		for i := range keep {
			keep[i] = true
		}
		var x []float64
		for it := 0; it < 3; it++ {
			s := newLSQ(3 * (n - 1))
			var used float64
			for i, r := range rows {
				if keep[i] {
					s.add(r.idx, r.co, r.rhs, 1)
					used++
				}
			}
			// slopes are pulled weakly to 0: a tile whose overlaps all lie along one edge
			// cannot tell its tilt across that edge
			for k := 0; k < n; k++ {
				if k != ref {
					b := col(k)
					s.add([]int{b + 1}, []float64{1}, 0, 1e-3*used/float64(n))
					s.add([]int{b + 2}, []float64{1}, 0, 1e-3*used/float64(n))
				}
			}
			x = s.solve()
			res := make([]float64, 0, len(rows))
			for i, r := range rows {
				if keep[i] {
					res = append(res, math.Abs(dot(r.idx, r.co, x)-r.rhs))
				}
			}
			lim := 3 * 1.4826 * median(res)
			for i, r := range rows {
				keep[i] = math.Abs(dot(r.idx, r.co, x)-r.rhs) <= lim
			}
		}
		for k := 0; k < n; k++ {
			if k != ref {
				b := col(k)
				planes[k][c] = [3]float64{x[b], x[b+1], x[b+2]}
			}
		}
	}

	out := make([]*Img, n)
	notes := []string{tiles[ref].Name + ": опорная"}
	for k, t := range tiles {
		if k == ref {
			out[k] = t.Img
			continue
		}
		m := t.Img
		fit := newImg(m.W, m.H, m.C)
		fit.Hdr, fit.BottomUp = m.Hdr, m.BottomUp
		var span float64 // largest correction at a tile corner, any channel
		for c := 0; c < m.C; c++ {
			p := planes[k][c]
			src, dst := m.Pix[c*m.W*m.H:(c+1)*m.W*m.H], fit.Pix[c*m.W*m.H:(c+1)*m.W*m.H]
			parallel(m.H, func(lo, hi int) {
				for y := lo; y < hi; y++ {
					for x := 0; x < m.W; x++ {
						u, v := norm(m, [2]float64{float64(x), float64(y)})
						dst[y*m.W+x] = src[y*m.W+x] + float32(p[0]+p[1]*u+p[2]*v)
					}
				}
			})
			for _, q := range [][2]float64{{0, 0}, {float64(m.W - 1), 0}, {0, float64(m.H - 1)}, {float64(m.W - 1), float64(m.H - 1)}} {
				u, v := norm(m, q)
				span = math.Max(span, math.Abs(p[0]+p[1]*u+p[2]*v))
			}
		}
		out[k] = fit
		notes = append(notes, fmt.Sprintf("%s: до %.5f", t.Name, span))
	}
	return out, notes
}

func dot(idx []int, c, x []float64) float64 {
	var s float64
	for i, k := range idx {
		s += c[i] * x[k]
	}
	return s
}
