package main

import (
	"fmt"
	"math"
)

// alignChannels registers the red and blue channels of a colour tile onto green by its stars.
// Lateral colour (a dual-band filter focuses Hα and OIII slightly differently, a lens magnifies
// colours differently) shifts each channel by up to a pixel or two, growing from the centre, which
// gives every star a coloured fringe on one side. The fix is an affine map per channel (shift, scale,
// rotation) fitted to the per-channel star centroids, so the stars keep their real profile. It
// returns m itself, and no note, when there is nothing to correct.
func alignChannels(m *Img) (*Img, string) {
	if m.C < 3 {
		return m, ""
	}
	const ref = 1 // green
	stars := detectStars(m)
	type pos struct{ x, y float64 }
	var pts [][3]pos // per star: R, G, B centroids
	for _, s := range stars[:min(len(stars), 4000)] {
		var p [3]pos
		ok := true
		for c := 0; c < 3 && ok; c++ {
			p[c].x, p[c].y, ok = centroid(m, c, s.x, s.y)
		}
		if ok {
			pts = append(pts, p)
		}
	}
	if len(pts) < 30 {
		return m, ""
	}
	out := m
	var parts []string
	for _, c := range []int{0, 2} {
		var src, dst [][2]float64 // green → channel c
		for _, p := range pts {
			src, dst = append(src, [2]float64{p[ref].x, p[ref].y}), append(dst, [2]float64{p[c].x, p[c].y})
		}
		A, n, ok := fitAffine(src, dst, float64(m.W), float64(m.H))
		if !ok {
			continue
		}
		var worst float64 // largest shift at a tile corner
		for _, q := range [][2]float64{{0, 0}, {float64(m.W - 1), 0}, {0, float64(m.H - 1)}, {float64(m.W - 1), float64(m.H - 1)}} {
			x, y := A.apply(q[0], q[1])
			worst = math.Max(worst, math.Hypot(x-q[0], y-q[1]))
		}
		if worst < 0.3 {
			continue // already aligned: a fraction of a pixel shows no fringe, and fit noise is ~0.1 px
		}
		if out == m {
			out = newImg(m.W, m.H, m.C)
			out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
			copy(out.Pix, m.Pix)
		}
		resampleChannel(m, out, c, A)
		parts = append(parts, fmt.Sprintf("%s до %.1f px (%d звёзд)", []string{"R", "G", "B"}[c], worst, n))
	}
	if len(parts) == 0 {
		return m, ""
	}
	return out, fmt.Sprint(parts)
}

// centroid is the flux-weighted centre of the star near (x, y) in channel c, over the local sky.
// The aperture is re-centred on the estimate a few times: an aperture left where the star is in
// another channel would cut it off on one side and pull the centre back by a good part of the shift.
// ok is false near the edge, for saturated stars and for stars too faint to measure.
func centroid(m *Img, c int, x, y float64) (cx, cy float64, ok bool) {
	const r, ring = 6, 10
	ix, iy := int(math.Round(x)), int(math.Round(y))
	if ix < ring+3 || iy < ring+3 || ix >= m.W-ring-3 || iy >= m.H-ring-3 {
		return 0, 0, false
	}
	p := m.Pix[c*m.W*m.H:]
	var sky []float64
	for d := -ring; d <= ring; d++ { // window border
		sky = append(sky, float64(p[(iy-ring)*m.W+ix+d]), float64(p[(iy+ring)*m.W+ix+d]),
			float64(p[(iy+d)*m.W+ix-ring]), float64(p[(iy+d)*m.W+ix+ring]))
	}
	bg := median(sky)
	cx, cy = x, y
	for it := 0; it < 5; it++ {
		jx, jy := int(math.Round(cx)), int(math.Round(cy))
		if abs(jx-ix) > 3 || abs(jy-iy) > 3 {
			return 0, 0, false // wandered off to a neighbour
		}
		var sw, sx, sy, peak float64
		for dy := -r - 1; dy <= r+1; dy++ {
			for dx := -r - 1; dx <= r+1; dx++ {
				px, py := float64(jx+dx), float64(jy+dy)
				if (px-cx)*(px-cx)+(py-cy)*(py-cy) > r*r {
					continue
				}
				v := float64(p[(jy+dy)*m.W+jx+dx])
				peak = math.Max(peak, v)
				if w := v - bg; w > 0 {
					sw, sx, sy = sw+w, sx+w*px, sy+w*py
				}
			}
		}
		if peak >= fitHigh || peak-bg <= 0 || sw <= 0 {
			return 0, 0, false
		}
		cx, cy = sx/sw, sy/sw
	}
	return cx, cy, true
}

// fitAffine fits dst ≈ A·src by least squares, dropping pairs off by more than 3σ (blends,
// neighbours inside the window) and fitting again. It returns the map and the pairs kept.
func fitAffine(src, dst [][2]float64, w, h float64) (Aff, int, bool) {
	keep := make([]bool, len(src))
	for i := range keep {
		keep[i] = true
	}
	// coordinates centred and scaled to ±1 for a well-conditioned system
	cx, cy, s := w/2, h/2, math.Max(w, h)/2
	var A Aff
	n := 0
	for it := 0; it < 4; it++ {
		sx, sy := newLSQ(3), newLSQ(3)
		n = 0
		for i, p := range src {
			if keep[i] {
				u, v := (p[0]-cx)/s, (p[1]-cy)/s
				sx.add([]int{0, 1, 2}, []float64{1, u, v}, dst[i][0]-p[0], 1)
				sy.add([]int{0, 1, 2}, []float64{1, u, v}, dst[i][1]-p[1], 1)
				n++
			}
		}
		if n < 30 {
			return Aff{}, n, false
		}
		a, b := sx.solve(), sy.solve()
		// back to pixel coordinates: x' = x + a0 + a1·(x−cx)/s + a2·(y−cy)/s
		A = Aff{1 + a[1]/s, a[2] / s, a[0] - (a[1]*cx+a[2]*cy)/s, b[1] / s, 1 + b[2]/s, b[0] - (b[1]*cx+b[2]*cy)/s}
		res := make([]float64, 0, n)
		for i, p := range src {
			if keep[i] {
				x, y := A.apply(p[0], p[1])
				res = append(res, math.Hypot(x-dst[i][0], y-dst[i][1]))
			}
		}
		lim := math.Max(3*1.4826*median(res), 0.05)
		for i, p := range src {
			x, y := A.apply(p[0], p[1])
			keep[i] = math.Hypot(x-dst[i][0], y-dst[i][1]) <= lim
		}
	}
	return A, n, true
}

// resampleChannel writes channel c of out as channel c of m sampled at A(x, y), with Lanczos-4
// kept within the 2×2 nearest pixels so stars get no ringing halo.
func resampleChannel(m, out *Img, c int, A Aff) {
	const taps = 4
	n := m.W * m.H
	p, q := m.Pix[c*n:(c+1)*n], out.Pix[c*n:(c+1)*n]
	cx := func(x int) int { return min(max(x, 0), m.W-1) }
	cy := func(y int) int { return min(max(y, 0), m.H-1) }
	parallel(m.H, func(lo, hi int) {
		var wx, wy [2 * taps]float64
		for y := lo; y < hi; y++ {
			for x := 0; x < m.W; x++ {
				u, v := A.apply(float64(x), float64(y))
				fx, fy := int(math.Floor(u)), int(math.Floor(v))
				x0, y0 := fx-taps+1, fy-taps+1
				var sx, sy float64
				for k := range wx {
					wx[k], wy[k] = lanczos4W(u-float64(x0+k)), lanczos4W(v-float64(y0+k))
					sx, sy = sx+wx[k], sy+wy[k]
				}
				var acc float64
				for b := range wy {
					row := p[cy(y0+b)*m.W:]
					var r float64
					for a := range wx {
						r += wx[a] * float64(row[cx(x0+a)])
					}
					acc += wy[b] * r
				}
				acc /= sx * sy
				lo, hi := math.Inf(1), math.Inf(-1)
				for _, d := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
					t := float64(p[cy(fy+d[1])*m.W+cx(fx+d[0])])
					lo, hi = math.Min(lo, t), math.Max(hi, t)
				}
				q[y*m.W+x] = float32(math.Min(math.Max(acc, lo), hi))
			}
		}
	})
}
