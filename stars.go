package main

import (
	"math"
	"sort"
)

type star struct{ x, y, f float64 }

// detectStars finds point sources (brightest first) with sub-pixel centroids on the channel mean.
func detectStars(m *Img) []star {
	n := m.W * m.H
	L := make([]float32, n)
	for c := 0; c < m.C; c++ {
		for i, v := range m.Pix[c*n : (c+1)*n] {
			L[i] += v / float32(m.C)
		}
	}
	step := max(1, n/100000)
	var s []float64
	for i := 0; i < n; i += step {
		s = append(s, float64(L[i]))
	}
	med := median(s)
	for i := range s {
		s[i] = math.Abs(s[i] - med)
	}
	sigma := math.Max(1.4826*median(s), 1e-7)
	thr := float32(med + 5*sigma)
	var out []star
	for y := 2; y < m.H-2; y++ {
		for x := 2; x < m.W-2; x++ {
			i := y*m.W + x
			v := L[i]
			if v <= thr {
				continue
			}
			peak, lit := true, 0
			for dy := -2; dy <= 2 && peak; dy++ {
				for dx := -2; dx <= 2; dx++ {
					j := i + dy*m.W + dx
					if j == i {
						continue
					}
					// strict on earlier pixels so a flat (saturated) top yields one peak
					if L[j] > v || (L[j] == v && j < i) {
						peak = false
						break
					}
					if abs(dx) <= 1 && abs(dy) <= 1 && L[j] > thr {
						lit++
					}
				}
			}
			if !peak || lit < 3 { // lit<3: hot pixel
				continue
			}
			var sw, sx, sy float64
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					w := float64(L[i+dy*m.W+dx]) - med
					if w > 0 {
						sw += w
						sx += w * float64(dx)
						sy += w * float64(dy)
					}
				}
			}
			out = append(out, star{float64(x) + sx/sw, float64(y) + sy/sw, sw})
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].f > out[b].f })
	return out[:min(len(out), 20000)]
}

// matchStars pairs stars of tile b with stars of tile a. t maps b's pixels into a's and may be off
// by up to radius px; the result is refined by translation voting and nearest-neighbour fits.
func matchStars(sa, sb []star, wa, ha, wb, hb int, t Aff, radius float64) []PointPair {
	// b's footprint in a, grown by radius
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range [][2]float64{{0, 0}, {float64(wb), 0}, {0, float64(hb)}, {float64(wb), float64(hb)}} {
		x, y := t.apply(p[0], p[1])
		x0, y0, x1, y1 = math.Min(x0, x), math.Min(y0, y), math.Max(x1, x), math.Max(y1, y)
	}
	const keep = 300
	var A, B [][2]float64
	for _, s := range sa {
		if len(A) < keep && s.x > x0-radius && s.x < x1+radius && s.y > y0-radius && s.y < y1+radius {
			A = append(A, [2]float64{s.x, s.y})
		}
	}
	for _, s := range sb {
		x, y := t.apply(s.x, s.y)
		if len(B) < keep && x > -radius && y > -radius && x < float64(wa)+radius && y < float64(ha)+radius {
			B = append(B, [2]float64{s.x, s.y})
		}
	}
	if len(A) < 4 || len(B) < 4 {
		return nil
	}

	// translation voting
	const bin = 4.0
	nb := int(2*radius/bin) + 1
	votes := make([]int32, nb*nb)
	idx := func(d float64) int { return int((d + radius) / bin) }
	for _, q := range B {
		x, y := t.apply(q[0], q[1])
		for _, p := range A {
			dx, dy := p[0]-x, p[1]-y
			if math.Abs(dx) < radius && math.Abs(dy) < radius {
				votes[idx(dy)*nb+idx(dx)]++
			}
		}
	}
	best := 0
	for i, v := range votes {
		if v > votes[best] {
			best = i
		}
	}
	if votes[best] < 4 {
		return nil
	}
	bx, by := best%nb, best/nb
	var mx, my, cnt float64
	for _, q := range B {
		x, y := t.apply(q[0], q[1])
		for _, p := range A {
			dx, dy := p[0]-x, p[1]-y
			if math.Abs(dx) < radius && math.Abs(dy) < radius && abs(idx(dx)-bx) <= 1 && abs(idx(dy)-by) <= 1 {
				mx, my, cnt = mx+dx, my+dy, cnt+1
			}
		}
	}
	t = Aff{1, 0, mx / cnt, 0, 1, my / cnt}.mul(t)

	// nearest-neighbour matching, refit, tighten
	tol := 4.0
	var pairs []PointPair
	for it := 0; it < 4; it++ {
		pairs = pairs[:0]
		used := make([]bool, len(A))
		var ss float64
		for _, q := range B {
			x, y := t.apply(q[0], q[1])
			bi, bd := -1, tol*tol
			for i, p := range A {
				if d := (p[0]-x)*(p[0]-x) + (p[1]-y)*(p[1]-y); !used[i] && d < bd {
					bi, bd = i, d
				}
			}
			if bi >= 0 {
				used[bi] = true
				pairs = append(pairs, PointPair{A: A[bi], B: q})
				ss += bd
			}
		}
		if len(pairs) < 4 {
			return nil
		}
		src := make([][2]float64, len(pairs))
		dst := make([][2]float64, len(pairs))
		for i, p := range pairs {
			src[i], dst[i] = p.B, p.A
		}
		t = fitSimilarity(src, dst)
		tol = math.Min(4, math.Max(1.5, 3*math.Sqrt(ss/float64(len(pairs)))))
	}
	return pairs
}
