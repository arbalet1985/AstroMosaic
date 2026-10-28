package main

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"strconv"
	"sync"
)

// Aff maps (x,y) -> (a*x + b*y + c, d*x + e*y + f).
type Aff [6]float64

var identity = Aff{1, 0, 0, 0, 1, 0}

func (t Aff) apply(x, y float64) (float64, float64) {
	return t[0]*x + t[1]*y + t[2], t[3]*x + t[4]*y + t[5]
}

// mul returns t∘u (apply u first).
func (t Aff) mul(u Aff) Aff {
	return Aff{
		t[0]*u[0] + t[1]*u[3], t[0]*u[1] + t[1]*u[4], t[0]*u[2] + t[1]*u[5] + t[2],
		t[3]*u[0] + t[4]*u[3], t[3]*u[1] + t[4]*u[4], t[3]*u[2] + t[4]*u[5] + t[5],
	}
}

func (t Aff) inv() Aff {
	det := t[0]*t[4] - t[1]*t[3]
	a, b, d, e := t[4]/det, -t[1]/det, -t[3]/det, t[0]/det
	return Aff{a, b, -(a*t[2] + b*t[5]), d, e, -(d*t[2] + e*t[5])}
}

// fitSimilarity finds rotation+scale+shift with dst ≈ T(src); one point = pure shift.
func fitSimilarity(src, dst [][2]float64) Aff {
	n := float64(len(src))
	var msx, msy, mdx, mdy float64
	for i := range src {
		msx += src[i][0] / n
		msy += src[i][1] / n
		mdx += dst[i][0] / n
		mdy += dst[i][1] / n
	}
	a, b := 1.0, 0.0
	if len(src) >= 2 {
		var num1, num2, den float64
		for i := range src {
			sx, sy := src[i][0]-msx, src[i][1]-msy
			dx, dy := dst[i][0]-mdx, dst[i][1]-mdy
			num1 += sx*dx + sy*dy
			num2 += sx*dy - sy*dx
			den += sx*sx + sy*sy
		}
		if den > 0 {
			a, b = num1/den, num2/den
		}
	}
	return Aff{a, -b, mdx - (a*msx - b*msy), b, a, mdy - (b*msx + a*msy)}
}

// ---------- WCS (TAN, linear part only; ponytail: SIP distortion ignored) ----------

type WCS struct {
	ra0, dec0, crpix1, crpix2 float64
	cd, icd                   [4]float64
	h                         int
	bottomUp                  bool
}

func parseWCS(m *Img) *WCS {
	f := func(k string) (float64, bool) {
		v, ok := m.Hdr[k]
		if !ok {
			return 0, false
		}
		x, err := strconv.ParseFloat(v, 64)
		return x, err == nil
	}
	ra, ok1 := f("CRVAL1")
	dec, ok2 := f("CRVAL2")
	if !ok1 || !ok2 {
		return nil
	}
	w := &WCS{ra0: ra * math.Pi / 180, dec0: dec * math.Pi / 180, h: m.H, bottomUp: m.BottomUp}
	w.crpix1, _ = f("CRPIX1")
	w.crpix2, _ = f("CRPIX2")
	if c11, ok := f("CD1_1"); ok {
		c12, _ := f("CD1_2")
		c21, _ := f("CD2_1")
		c22, _ := f("CD2_2")
		w.cd = [4]float64{c11, c12, c21, c22}
	} else {
		d1, ok1 := f("CDELT1")
		d2, ok2 := f("CDELT2")
		if !ok1 || !ok2 {
			return nil
		}
		if p11, ok := f("PC1_1"); ok {
			p12, _ := f("PC1_2")
			p21, _ := f("PC2_1")
			p22, _ := f("PC2_2")
			w.cd = [4]float64{d1 * p11, d1 * p12, d2 * p21, d2 * p22}
		} else {
			rot, _ := f("CROTA2")
			s, c := math.Sincos(rot * math.Pi / 180)
			w.cd = [4]float64{d1 * c, -d2 * s, d1 * s, d2 * c}
		}
	}
	det := w.cd[0]*w.cd[3] - w.cd[1]*w.cd[2]
	if det == 0 {
		return nil
	}
	w.icd = [4]float64{w.cd[3] / det, -w.cd[1] / det, -w.cd[2] / det, w.cd[0] / det}
	return w
}

func (w *WCS) pixToSky(x, y float64) (ra, dec float64) {
	p2 := y + 1
	if w.bottomUp {
		p2 = float64(w.h) - y
	}
	dx, dy := x+1-w.crpix1, p2-w.crpix2
	xi := (w.cd[0]*dx + w.cd[1]*dy) * math.Pi / 180
	eta := (w.cd[2]*dx + w.cd[3]*dy) * math.Pi / 180
	sd, cd := math.Sincos(w.dec0)
	den := cd - eta*sd
	ra = w.ra0 + math.Atan2(xi, den)
	dec = math.Atan2(sd+eta*cd, math.Hypot(xi, den))
	return
}

func (w *WCS) skyToPix(ra, dec float64) (x, y float64) {
	sd0, cd0 := math.Sincos(w.dec0)
	sd, cd := math.Sincos(dec)
	sa, ca := math.Sincos(ra - w.ra0)
	cosc := sd0*sd + cd0*cd*ca
	xi := cd * sa / cosc * 180 / math.Pi
	eta := (cd0*sd - sd0*cd*ca) / cosc * 180 / math.Pi
	p1 := w.crpix1 + w.icd[0]*xi + w.icd[1]*eta
	p2 := w.crpix2 + w.icd[2]*xi + w.icd[3]*eta
	x = p1 - 1
	y = p2 - 1
	if w.bottomUp {
		y = float64(w.h) - p2
	}
	return
}

// ---------- placement ----------

// Tile is one grid cell's image.
type Tile struct {
	Img  *Img
	Row  int
	Col  int
	Name string
	wcs  *WCS
}

// PointPair links a pixel on tile A to the same star on tile B (full-res coords).
type PointPair struct{ A, B [2]float64 }

// pairKey identifies two tiles by index, a < b.
type pairKey struct{ A, B int }

// edge returns the transform mapping tile b's pixels into tile a's pixels, a cost and a method label.
func edge(ta, tb *Tile, pts []PointPair, overlap float64) (Aff, float64, string) {
	dr, dc := tb.Row-ta.Row, tb.Col-ta.Col
	dist := float64(abs(dr) + abs(dc))
	if len(pts) > 0 {
		src := make([][2]float64, len(pts))
		dst := make([][2]float64, len(pts))
		for i, p := range pts {
			src[i], dst[i] = p.B, p.A
		}
		return fitSimilarity(src, dst), dist, "точки"
	}
	if ta.wcs != nil && tb.wcs != nil {
		var src, dst [][2]float64
		for _, fy := range []float64{0, 0.5, 1} {
			for _, fx := range []float64{0, 0.5, 1} {
				x, y := fx*float64(tb.Img.W-1), fy*float64(tb.Img.H-1)
				ax, ay := ta.wcs.skyToPix(tb.wcs.pixToSky(x, y))
				src = append(src, [2]float64{x, y})
				dst = append(dst, [2]float64{ax, ay})
			}
		}
		t := fitSimilarity(src, dst)
		// the sky is the authority, not the grid labels; only reject a broken solution (scale far off)
		if s := math.Hypot(t[0], t[3]); s > 0.5 && s < 2 {
			return t, 10 + dist, "WCS"
		}
	}
	sx := float64(ta.Img.W) * (1 - overlap) * float64(dc)
	sy := float64(ta.Img.H) * (1 - overlap) * float64(dr)
	return Aff{1, 0, sx, 0, 1, sy}, 100 * dist, "сетка"
}

// Warp maps tile pixels to the mosaic: shared radial lens correction, then a per-tile similarity.
type Warp struct {
	S, Si      Aff     // similarity and its inverse
	K          float64 // radial k1, r normalised by the half-diagonal
	cx, cy, rn float64
}

func newWarp(S Aff, K float64, m *Img) Warp {
	return Warp{S: S, Si: S.inv(), K: K, cx: float64(m.W-1) / 2, cy: float64(m.H-1) / 2, rn: math.Hypot(float64(m.W), float64(m.H)) / 2}
}

func (w Warp) undist(x, y float64) (float64, float64) {
	dx, dy := x-w.cx, y-w.cy
	f := 1 + w.K*(dx*dx+dy*dy)/(w.rn*w.rn)
	return w.cx + dx*f, w.cy + dy*f
}

func (w Warp) apply(x, y float64) (float64, float64) { return w.S.apply(w.undist(x, y)) }

// inv maps mosaic coords back to tile pixels (fixed-point inversion of the radial term).
func (w Warp) inv(X, Y float64) (float64, float64) {
	qx, qy := w.Si.apply(X, Y)
	if w.K == 0 {
		return qx, qy
	}
	ex, ey := qx-w.cx, qy-w.cy
	dx, dy := ex, ey
	for i := 0; i < 4; i++ {
		f := 1 + w.K*(dx*dx+dy*dy)/(w.rn*w.rn)
		dx, dy = ex/f, ey/f
	}
	return w.cx + dx, w.cy + dy
}

// lsq accumulates weighted normal equations for a dense linear least-squares problem.
type lsq struct {
	n    int
	N, r []float64
}

func newLSQ(n int) *lsq { return &lsq{n, make([]float64, n*n), make([]float64, n)} }

// add appends the row w·(Σ c[k]·x[idx[k]] − rhs)².
func (s *lsq) add(idx []int, c []float64, rhs, w float64) {
	for i, a := range idx {
		s.r[a] += w * c[i] * rhs
		for j, b := range idx {
			s.N[a*s.n+b] += w * c[i] * c[j]
		}
	}
}

// solve runs Gaussian elimination with partial pivoting; singular directions come out as 0.
func (s *lsq) solve() []float64 {
	n := s.n
	A := append([]float64(nil), s.N...)
	x := append([]float64(nil), s.r...)
	for k := 0; k < n; k++ {
		p := k
		for i := k + 1; i < n; i++ {
			if math.Abs(A[i*n+k]) > math.Abs(A[p*n+k]) {
				p = i
			}
		}
		if A[p*n+k] == 0 {
			continue
		}
		if p != k {
			for j := 0; j < n; j++ {
				A[k*n+j], A[p*n+j] = A[p*n+j], A[k*n+j]
			}
			x[k], x[p] = x[p], x[k]
		}
		for i := k + 1; i < n; i++ {
			f := A[i*n+k] / A[k*n+k]
			if f == 0 {
				continue
			}
			for j := k; j < n; j++ {
				A[i*n+j] -= f * A[k*n+j]
			}
			x[i] -= f * x[k]
		}
	}
	for k := n - 1; k >= 0; k-- {
		if A[k*n+k] == 0 {
			x[k] = 0
			continue
		}
		for j := k + 1; j < n; j++ {
			x[k] -= A[k*n+j] * x[j]
		}
		x[k] /= A[k*n+k]
	}
	return x
}

// match is one star seen on tiles A and B.
type match struct {
	A, B   int
	pA, pB [2]float64
}

// simRow adds sign·(x or y of tile k's similarity at q) to a row; tile 0 is fixed to identity.
func simRow(k int, q [2]float64, sign float64, y bool, idx []int, c []float64, rhs *float64) ([]int, []float64) {
	if k == 0 {
		if y {
			*rhs -= sign * q[1]
		} else {
			*rhs -= sign * q[0]
		}
		return idx, c
	}
	b := 4 * (k - 1) // params a, b, tx, ty: S = [a −b tx; b a ty]
	if y {
		return append(idx, b, b+1, b+3), append(c, sign*q[1], sign*q[0], sign)
	}
	return append(idx, b, b+1, b+2), append(c, sign*q[0], -sign*q[1], sign)
}

// solveWarps fits all similarities at once for a given K; init (weakly) holds tiles without stars.
func solveWarps(tiles []*Tile, init []Aff, ms []match, K float64) []Warp {
	n := len(tiles)
	W := make([]Warp, n)
	for i, t := range tiles {
		W[i] = newWarp(identity, K, t.Img)
	}
	if n == 1 {
		return W
	}
	s := newLSQ(4 * (n - 1))
	var idx []int
	var c []float64
	for _, m := range ms {
		qa, qb := [2]float64{}, [2]float64{}
		qa[0], qa[1] = W[m.A].undist(m.pA[0], m.pA[1])
		qb[0], qb[1] = W[m.B].undist(m.pB[0], m.pB[1])
		for _, y := range []bool{false, true} {
			var rhs float64
			idx, c = simRow(m.A, qa, 1, y, idx[:0], c[:0], &rhs)
			idx, c = simRow(m.B, qb, -1, y, idx, c, &rhs)
			s.add(idx, c, rhs, 1)
		}
	}
	for k := 1; k < n; k++ {
		m := tiles[k].Img
		for _, p := range [][2]float64{{0, 0}, {float64(m.W - 1), 0}, {0, float64(m.H - 1)}, {float64(m.W - 1), float64(m.H - 1)}} {
			X, Y := init[k].apply(p[0], p[1])
			q := [2]float64{}
			q[0], q[1] = W[k].undist(p[0], p[1])
			for _, y := range []bool{false, true} {
				rhs := X
				if y {
					rhs = Y
				}
				idx, c = simRow(k, q, 1, y, idx[:0], c[:0], &rhs)
				s.add(idx, c, rhs, 1e-4)
			}
		}
	}
	x := s.solve()
	for k := 1; k < n; k++ {
		b := 4 * (k - 1)
		W[k] = newWarp(Aff{x[b], -x[b+1], x[b+2], x[b+1], x[b], x[b+3]}, K, tiles[k].Img)
	}
	return W
}

func residual(W []Warp, m match) float64 {
	ax, ay := W[m.A].apply(m.pA[0], m.pA[1])
	bx, by := W[m.B].apply(m.pB[0], m.pB[1])
	return math.Hypot(ax-bx, ay-by)
}

func rms(W []Warp, ms []match) float64 {
	var s float64
	for _, m := range ms {
		r := residual(W, m)
		s += r * r
	}
	return math.Sqrt(s / math.Max(1, float64(len(ms))))
}

// refine finds the global similarities and shared lens distortion that best overlay all matches.
func refine(tiles []*Tile, init []Aff, ms []match) ([]Warp, []match, float64) {
	W := solveWarps(tiles, init, ms, 0)
	reject := func() {
		thr := math.Max(1, 3*rms(W, ms))
		var keep []match
		for _, m := range ms {
			if residual(W, m) < thr {
				keep = append(keep, m)
			}
		}
		ms = keep
	}
	reject()
	W = solveWarps(tiles, init, ms, 0)
	K := 0.0
	if len(ms) >= 30 {
		f := func(k float64) float64 { return rms(solveWarps(tiles, init, ms, k), ms) }
		// golden-section search
		g := (math.Sqrt(5) - 1) / 2
		lo, hi := -0.05, 0.05
		x1, x2 := hi-g*(hi-lo), lo+g*(hi-lo)
		f1, f2 := f(x1), f(x2)
		for i := 0; i < 40; i++ {
			if f1 < f2 {
				hi, x2, f2 = x2, x1, f1
				x1 = hi - g*(hi-lo)
				f1 = f(x1)
			} else {
				lo, x1, f1 = x1, x2, f2
				x2 = lo + g*(hi-lo)
				f2 = f(x2)
			}
		}
		if k := (lo + hi) / 2; f(k) < 0.95*rms(W, ms) {
			K = k
		}
	}
	W = solveWarps(tiles, init, ms, K)
	reject()
	W = solveWarps(tiles, init, ms, K)
	return W, ms, K
}

// place returns tile->mosaic warps (reference = first tile), notes, and the sky neighbours that
// share too few stars to be aligned by them (they rest on WCS or the grid guess unless given points).
func place(tiles []*Tile, points map[pairKey][]PointPair, overlap float64) ([]Warp, []string, []pairKey) {
	n := len(tiles)
	stars := make([][]star, n)
	var wg sync.WaitGroup
	for i, t := range tiles {
		t.wcs = parseWCS(t.Img)
		wg.Add(1)
		go func() { defer wg.Done(); stars[i] = detectStars(t.Img) }()
	}
	type e struct {
		t    Aff
		cost float64
		how  string
	}
	E := make([][]e, n)
	for i := range E {
		E[i] = make([]e, n)
	}
	wg.Wait()
	parallel(n*n, func(lo, hi int) {
		for k := lo; k < hi; k++ {
			a, b := k/n, k%n
			if a >= b {
				continue
			}
			t, c, how := edge(tiles[a], tiles[b], points[pairKey{a, b}], overlap)
			// no points or WCS: look for the overlap by stars instead of trusting the grid guess
			if how == "сетка" && (n <= 25 || c <= 200) {
				if ts, cnt, ok := findOverlap(stars[a], stars[b], tiles[a].Img, tiles[b].Img); ok {
					t, c, how = ts, 50+c/100-float64(cnt)/1e4, "звёзды"
				}
			}
			E[a][b] = e{t, c, how}       // b -> a
			E[b][a] = e{t.inv(), c, how} // a -> b
		}
	})
	A := make([]Aff, n)
	placed := make([]bool, n)
	order := []int{0}
	A[0], placed[0] = identity, true
	type link struct{ u, v int }
	var links []link
	// Prim: always attach the unplaced tile with the cheapest link to a placed one; this is only
	// the starting guess for star matching and the global fit below.
	for len(order) < n {
		bu, bv, best := -1, -1, math.Inf(1)
		for _, u := range order {
			for v := 0; v < n; v++ {
				if !placed[v] && E[u][v].cost < best {
					bu, bv, best = u, v, E[u][v].cost
				}
			}
		}
		A[bv] = A[bu].mul(E[bu][bv].t)
		placed[bv] = true
		order = append(order, bv)
		links = append(links, link{bu, bv})
	}

	var ms []match
	count := map[pairKey]int{}
	for a := 0; a < n; a++ {
		for b := a + 1; b < n; b++ {
			ta, tb := tiles[a], tiles[b]
			radius := map[string]float64{"точки": 20, "WCS": 60, "звёзды": 20}[E[a][b].how]
			if radius == 0 {
				radius = 0.15 * float64(max(tb.Img.W, tb.Img.H))
			}
			if abs(ta.Row-tb.Row)+abs(ta.Col-tb.Col) != 1 {
				radius *= 2 // composed through other tiles
			}
			got := matchStars(stars[a], stars[b], ta.Img.W, ta.Img.H, tb.Img.W, tb.Img.H, A[a].inv().mul(A[b]), radius)
			if len(got) < 6 {
				got = append(got, points[pairKey{a, b}]...)
			}
			for _, p := range got {
				ms = append(ms, match{a, b, p.A, p.B})
			}
			count[pairKey{a, b}] = len(got)
		}
	}
	W, ms, K := refine(tiles, A, ms)
	var notes []string
	for _, l := range links {
		a, b := min(l.u, l.v), max(l.u, l.v)
		notes = append(notes, fmt.Sprintf("%s → %s: %s, звёзд %d", tiles[l.u].Name, tiles[l.v].Name, E[l.u][l.v].how, count[pairKey{a, b}]))
	}
	notes = append(notes, fmt.Sprintf("совпадений %d, ошибка %.2f px, дисторсия k=%.4f", len(ms), rms(W, ms), K))
	var weak []pairKey
	for k, c := range count {
		if ok, _, _ := skyNeighbour(W[k.A], W[k.B], tiles[k.A].Img, tiles[k.B].Img); ok && c < 6 && len(points[k]) == 0 {
			weak = append(weak, k)
		}
	}
	sort.Slice(weak, func(i, j int) bool { return weak[i].A < weak[j].A || weak[i].A == weak[j].A && weak[i].B < weak[j].B })
	return W, notes, weak
}

// ---------- overlap crop ----------

// halfPlane keeps the mosaic points with nx·X + ny·Y ≤ c (n is a unit vector); alongX says the
// cut runs across the tile's x axis (a left/right seam), which decides how it shapes the blend weight.
type halfPlane struct {
	nx, ny, c float64
	alongX    bool
}

// region is the part of a tile used in the mosaic: the whole image less what the seams cut away.
type region struct {
	w, h  float64 // image size − 1
	cuts  []halfPlane
	exact bool // overlap 0: every mosaic pixel comes from exactly one tile
}

func full(m *Img) region { return region{w: float64(m.W - 1), h: float64(m.H - 1)} }

// has tests tile pixel (u,v), which sits at mosaic point (X,Y).
func (r region) has(u, v, X, Y float64) bool {
	if u < 0 || v < 0 || u > r.w || v > r.h {
		return false
	}
	for _, c := range r.cuts {
		if c.nx*X+c.ny*Y > c.c {
			return false
		}
	}
	return true
}

// weight rises smoothly from the region's border (image edge or seam cut) over half the tile size.
func (r region) weight(u, v, X, Y float64) float64 {
	dx, dy := math.Min(u, r.w-u), math.Min(v, r.h-v)
	for _, c := range r.cuts {
		if d := c.c - (c.nx*X + c.ny*Y); c.alongX {
			dx = math.Min(dx, d)
		} else {
			dy = math.Min(dy, d)
		}
	}
	s := func(d, half float64) float64 {
		t := math.Min(1, (d+1)/half)
		return t * t * (3 - 2*t)
	}
	return s(dx, r.w/2) * s(dy, r.h/2)
}

// skyNeighbour tells whether b sits beside a as placed (shifted by half a tile or more along one axis,
// by less than half across it), which axis, and whether b is left of / above a. Grid labels are not
// used: they may not match the sky, and frames of the same field are not neighbours.
func skyNeighbour(wa, wb Warp, ma, mb *Img) (ok, horiz, before bool) {
	u, v := wa.inv(wb.apply(float64(mb.W-1)/2, float64(mb.H-1)/2))
	u, v = u-float64(ma.W-1)/2, v-float64(ma.H-1)/2
	fu, fv := math.Abs(u)/float64(ma.W), math.Abs(v)/float64(ma.H)
	if fu > fv {
		return fu >= 0.5 && fu < 1 && fv < 0.5, true, u < 0
	}
	return fv >= 0.5 && fv < 1 && fu < 0.5, false, v < 0
}

// seamCuts trims sky neighbours so they overlap by the given fraction of the tile size. Both tiles
// are cut by lines parallel to one seam line in the mosaic: the bisector of the two facing tile edges,
// so the seam sits in the middle of the original overlap even where the tiles are turned against each
// other, and at 0 the tiles meet exactly along the whole seam.
func seamCuts(tiles []*Tile, W []Warp, overlap float64) []region {
	R := make([]region, len(tiles))
	for i, t := range tiles {
		R[i] = full(t.Img)
		R[i].exact = overlap == 0
	}
	axisX := func(w Warp, nx, ny float64) bool { // is the normal closer to the tile's x axis than to y?
		return math.Abs(nx*w.S[0]+ny*w.S[3]) > math.Abs(nx*w.S[1]+ny*w.S[4])
	}
	for a := range tiles {
		for b := a + 1; b < len(tiles); b++ {
			ma, mb := tiles[a].Img, tiles[b].Img
			ok, horiz, before := skyNeighbour(W[a], W[b], ma, mb)
			if !ok {
				// diagonal neighbours meet only where four tiles do: split by the bisector of their centres
				ax, ay := W[a].apply(float64(ma.W-1)/2, float64(ma.H-1)/2)
				bx, by := W[b].apply(float64(mb.W-1)/2, float64(mb.H-1)/2)
				u, v := W[a].inv(bx, by)
				fu, fv := math.Abs(u-float64(ma.W-1)/2)/float64(ma.W), math.Abs(v-float64(ma.H-1)/2)/float64(ma.H)
				if fu >= 0.5 && fu < 1 && fv >= 0.5 && fv < 1 {
					nx, ny := bx-ax, by-ay
					l := math.Hypot(nx, ny)
					nx, ny = nx/l, ny/l
					c0 := nx*(ax+bx)/2 + ny*(ay+by)/2
					d := overlap * math.Min(float64(ma.W), float64(ma.H))
					R[a].cuts = append(R[a].cuts, halfPlane{nx, ny, c0 + d/2, axisX(W[a], nx, ny)})
					R[b].cuts = append(R[b].cuts, halfPlane{-nx, -ny, -(c0 - d/2), axisX(W[b], nx, ny)})
				}
				continue
			}
			a, b := a, b // make b right of / below a
			if before {
				a, b, ma, mb = b, a, mb, ma
			}
			// facing edges: a's right (bottom) and b's left (top), as mosaic points
			wa, ha, wb, hb := float64(ma.W-1), float64(ma.H-1), float64(mb.W-1), float64(mb.H-1)
			var a1, a2, b1, b2 [2]float64
			d := overlap * float64(ma.W)
			if horiz {
				a1[0], a1[1] = W[a].apply(wa, 0)
				a2[0], a2[1] = W[a].apply(wa, ha)
				b1[0], b1[1] = W[b].apply(0, 0)
				b2[0], b2[1] = W[b].apply(0, hb)
			} else {
				a1[0], a1[1] = W[a].apply(0, ha)
				a2[0], a2[1] = W[a].apply(wa, ha)
				b1[0], b1[1] = W[b].apply(0, 0)
				b2[0], b2[1] = W[b].apply(wb, 0)
				d = overlap * float64(ma.H)
			}
			m1 := [2]float64{(a1[0] + b1[0]) / 2, (a1[1] + b1[1]) / 2}
			m2 := [2]float64{(a2[0] + b2[0]) / 2, (a2[1] + b2[1]) / 2}
			nx, ny := m2[1]-m1[1], m1[0]-m2[0] // perpendicular to the seam
			l := math.Hypot(nx, ny)
			nx, ny = nx/l, ny/l
			ax, ay := W[a].apply(wa/2, ha/2)
			bx, by := W[b].apply(wb/2, hb/2)
			if nx*(bx-ax)+ny*(by-ay) < 0 { // point n from a towards b
				nx, ny = -nx, -ny
			}
			c0 := nx*m1[0] + ny*m1[1]
			R[a].cuts = append(R[a].cuts, halfPlane{nx, ny, c0 + d/2, axisX(W[a], nx, ny)})
			R[b].cuts = append(R[b].cuts, halfPlane{-nx, -ny, -(c0 - d/2), axisX(W[b], nx, ny)})
		}
	}
	return R
}

// ---------- render ----------

func bilinear(m *Img, c int, u, v float64) float32 {
	x0, y0 := int(u), int(v)
	x1, y1 := min(x0+1, m.W-1), min(y0+1, m.H-1)
	fx, fy := float32(u-float64(x0)), float32(v-float64(y0))
	p := m.Pix[c*m.W*m.H:]
	top := p[y0*m.W+x0]*(1-fx) + p[y0*m.W+x1]*fx
	bot := p[y1*m.W+x0]*(1-fx) + p[y1*m.W+x1]*fx
	return top*(1-fy) + bot*fy
}

func inside(m *Img, u, v float64) bool {
	return u >= 0 && v >= 0 && u <= float64(m.W-1) && v <= float64(m.H-1)
}

// parallel splits [0,n) into chunks run on all CPUs.
func parallel(n int, f func(lo, hi int)) {
	k := runtime.NumCPU()
	var wg sync.WaitGroup
	for i := 0; i < k; i++ {
		lo, hi := n*i/k, n*(i+1)/k
		if lo < hi {
			wg.Add(1)
			go func() { defer wg.Done(); f(lo, hi) }()
		}
	}
	wg.Wait()
}

// lowpass approximates a gaussian blur (σ≈8.5 px) by three box passes per axis.
func lowpass(m *Img) *Img {
	const r = 8
	w, h := m.W, m.H
	out := newImg(w, h, m.C)
	tmp := make([]float32, w*h)
	k := float32(2*r + 1)
	cl := func(i, n int) int { return min(max(i, 0), n-1) }
	for c := 0; c < m.C; c++ {
		a, b := out.Pix[c*w*h:(c+1)*w*h], tmp
		copy(a, m.Pix[c*w*h:(c+1)*w*h])
		for pass := 0; pass < 3; pass++ {
			parallel(h, func(lo, hi int) {
				for y := lo; y < hi; y++ {
					src, dst := a[y*w:(y+1)*w], b[y*w:(y+1)*w]
					var s float32
					for i := -r; i <= r; i++ {
						s += src[cl(i, w)]
					}
					for x := 0; x < w; x++ {
						dst[x] = s / k
						s += src[cl(x+r+1, w)] - src[cl(x-r, w)]
					}
				}
			})
			parallel(w, func(lo, hi int) {
				acc := make([]float32, hi-lo)
				for i := -r; i <= r; i++ {
					row := b[cl(i, h)*w:]
					for x := lo; x < hi; x++ {
						acc[x-lo] += row[x]
					}
				}
				for y := 0; y < h; y++ {
					add, sub := b[cl(y+r+1, h)*w:], b[cl(y-r, h)*w:]
					for x := lo; x < hi; x++ {
						a[y*w+x] = acc[x-lo] / k
						acc[x-lo] += add[x] - sub[x]
					}
				}
			})
		}
	}
	return out
}

// background is a starless lowpass: pixels above the current estimate + 2σ are cut down to it and
// the result blurred again, until the stars have melted away. A plain lowpass keeps blurred stars, so
// detail = image − lowpass carries a dark ring around each star; where two tiles' stars differ
// (seeing, saturation) the averaged low band no longer fills that ring in and it shows as a black
// halo. Only the top is cut: lifting the dark sky around a star up to the blurred star as well would
// keep the star in the estimate and widen it with every pass.
func background(m *Img) *Img {
	low := lowpass(m)
	n := m.W * m.H
	cl := newImg(m.W, m.H, m.C)
	for it := 0; it < 5; it++ {
		for c := 0; c < m.C; c++ {
			src, bg, dst := m.Pix[c*n:(c+1)*n], low.Pix[c*n:(c+1)*n], cl.Pix[c*n:(c+1)*n]
			var d []float64
			for i := 0; i < n; i += max(1, n/100000) {
				d = append(d, math.Abs(float64(src[i]-bg[i])))
			}
			k := float32(2 * 1.4826 * median(d))
			for i, v := range src {
				dst[i] = min(v, bg[i]+k)
			}
		}
		low = lowpass(cl)
	}
	return low
}

// phot is a tile's photometric correction v' = g·v + b.
// ponytail: no per-tile gradient; overlaps are narrow strips that barely constrain a slope, and the
// wide low-band blend already hides smooth gradient differences.
type phot struct{ g, b float64 }

// fitPhotometry matches gain and offset of all tiles to tile 0 by least squares over
// medians of 32 px blocks in every pairwise overlap (medians ignore stars).
func fitPhotometry(tiles []*Tile, W []Warp, C int) [][]phot {
	n := len(tiles)
	ch := func(t *Tile, c int) int { return min(c, t.Img.C-1) }
	type row struct {
		i, j   int
		vi, vj []float64
	}
	var rows []row
	const B = 32
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			mi, mj := tiles[i].Img, tiles[j].Img
			gi, gj := 0.02*float64(max(mi.W, mi.H)), 0.02*float64(max(mj.W, mj.H)) // skip weak tile edges
			ok := func(m *Img, g, u, v float64) bool {
				return u >= g && v >= g && u <= float64(m.W-1)-g && v <= float64(m.H-1)-g
			}
			for by := 0; by+B <= mj.H; by += B {
				for bx := 0; bx+B <= mj.W; bx += B {
					X, Y := W[j].apply(float64(bx+B/2), float64(by+B/2))
					u, v := W[i].inv(X, Y)
					if u < -B || v < -B || u > float64(mi.W+B) || v > float64(mi.H+B) {
						continue
					}
					si, sj := make([][]float64, C), make([][]float64, C)
					for y := by; y < by+B; y += 4 {
						for x := bx; x < bx+B; x += 4 {
							u, v := W[i].inv(W[j].apply(float64(x), float64(y)))
							if !ok(mj, gj, float64(x), float64(y)) || !ok(mi, gi, u, v) {
								continue
							}
							for c := 0; c < C; c++ {
								si[c] = append(si[c], float64(bilinear(mi, ch(tiles[i], c), u, v)))
								sj[c] = append(sj[c], float64(mj.at(ch(tiles[j], c), x, y)))
							}
						}
					}
					if len(si[0]) < 32 {
						continue
					}
					r := row{i: i, j: j, vi: make([]float64, C), vj: make([]float64, C)}
					for c := 0; c < C; c++ {
						r.vi[c], r.vj[c] = median(si[c]), median(sj[c])
					}
					rows = append(rows, r)
				}
			}
		}
	}
	P := make([][]phot, n)
	for i := range P {
		P[i] = make([]phot, C)
		for c := range P[i] {
			P[i][c].g = 1
		}
	}
	if n == 1 {
		return P
	}
	for c := 0; c < C; c++ {
		s := newLSQ(2 * (n - 1))
		sv2 := make([]float64, n)
		cnt := make([]float64, n)
		for _, r := range rows {
			var idx []int
			var co []float64
			var rhs float64
			for _, side := range []struct {
				k    int
				v, s float64
			}{{r.i, r.vi[c], 1}, {r.j, r.vj[c], -1}} {
				if side.k == 0 {
					rhs -= side.s * side.v
					continue
				}
				b := 2 * (side.k - 1)
				idx = append(idx, b, b+1)
				co = append(co, side.s*side.v, side.s)
				sv2[side.k] += side.v * side.v
				cnt[side.k]++
			}
			s.add(idx, co, rhs, 1)
		}
		for k := 1; k < n; k++ {
			b := 2 * (k - 1)
			// gain is pulled hard to 1: it only moves when the overlaps hold real brightness range
			s.add([]int{b}, []float64{1}, 1, 0.1*sv2[k]+1e-12)
			s.add([]int{b + 1}, []float64{1}, 0, 1e-6*cnt[k]+1e-12)
		}
		x := s.solve()
		for k := 1; k < n; k++ {
			b := 2 * (k - 1)
			P[k][c] = phot{math.Min(2, math.Max(0.5, x[b])), x[b+1]}
		}
	}
	return P
}

// render blends all tiles in two bands: the blurred background is feathered across the whole
// overlap (no brightness step), while detail and noise switch over a narrow zone at the seam
// (no doubled stars, no smoother band where two noisy tiles would be averaged).
// Pixels cropped away are still used where no other tile covers the mosaic. With exact regions
// (overlap 0) each pixel is taken whole from one tile: the one it lies deepest in, which also
// settles the corner where four seam lines do not meet in a single point.
func render(tiles []*Tile, W []Warp, crop []region) *Img {
	n := len(tiles)
	type box struct{ x0, y0, x1, y1 float64 }
	bb := make([]box, n)
	all := box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	C := 1
	low := make([]*Img, n)
	for i, t := range tiles {
		C = max(C, t.Img.C)
		low[i] = background(t.Img)
		b := box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		w, h := float64(t.Img.W-1), float64(t.Img.H-1)
		for k := 0.0; k <= 16; k++ {
			f := k / 16
			for _, p := range [][2]float64{{f * w, 0}, {f * w, h}, {0, f * h}, {w, f * h}} {
				x, y := W[i].apply(p[0], p[1])
				b = box{math.Min(b.x0, x), math.Min(b.y0, y), math.Max(b.x1, x), math.Max(b.y1, y)}
			}
		}
		bb[i] = b
		all = box{math.Min(all.x0, b.x0), math.Min(all.y0, b.y0), math.Max(all.x1, b.x1), math.Max(all.y1, b.y1)}
	}
	ch := func(t *Tile, c int) int { return min(c, t.Img.C-1) }
	P := fitPhotometry(tiles, W, C)

	ox, oy := math.Floor(all.x0), math.Floor(all.y0)
	Wd, H := int(math.Ceil(all.x1)-ox)+1, int(math.Ceil(all.y1)-oy)+1
	out := newImg(Wd, H, C)
	parallel(H, func(lo, hi int) {
		sl, sh := make([]float64, C), make([]float64, C)
		for y := lo; y < hi; y++ {
			Y := float64(y) + oy
			for x := 0; x < Wd; x++ {
				X := float64(x) + ox
				for pass := 0; pass < 2; pass++ { // pass 1: cropped-away pixels fill holes
					var wl, wh float64
					clear(sl)
					clear(sh)
					best, bestW := -1, -1.0
					var bu, bv float64
					for i, t := range tiles {
						if X < bb[i].x0 || X > bb[i].x1 || Y < bb[i].y0 || Y > bb[i].y1 {
							continue
						}
						u, v := W[i].inv(X, Y)
						r := crop[i]
						if pass == 1 {
							r = full(t.Img)
						}
						if !r.has(u, v, X, Y) {
							continue
						}
						w := r.weight(u, v, X, Y)
						if crop[i].exact {
							if w > bestW {
								best, bestW, bu, bv = i, w, u, v
							}
							continue
						}
						w2 := w * w
						w4 := w2 * w2
						w8 := w4 * w4
						for c := 0; c < C; c++ {
							p := P[i][c]
							l := float64(bilinear(low[i], ch(t, c), u, v))
							hf := float64(bilinear(t.Img, ch(t, c), u, v)) - l
							sl[c] += w * (p.g*l + p.b)
							sh[c] += w8 * p.g * hf
						}
						wl += w
						wh += w8
					}
					if best >= 0 {
						t := tiles[best]
						for c := 0; c < C; c++ {
							p := P[best][c]
							out.Pix[c*Wd*H+y*Wd+x] = float32(p.g*float64(bilinear(t.Img, ch(t, c), bu, bv)) + p.b)
						}
						break
					}
					if wl > 0 {
						for c := 0; c < C; c++ {
							v := sl[c] / wl
							if wh > 0 {
								v += sh[c] / wh
							}
							out.Pix[c*Wd*H+y*Wd+x] = float32(v)
						}
						break
					}
				}
			}
		}
	})
	return out
}
