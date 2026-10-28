package main

import (
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// autoGrid puts images into grid cells: by sky position when all have WCS, else by an "R-C" / "R_C"
// suffix in the file or folder name (as ASIAIR names mosaic panels), else by stars shared in the
// overlaps, else row-major in the given order.
func autoGrid(imgs []*Img, paths []string) (cells []cell, rows, cols int, how string) {
	if c, ok := gridByWCS(imgs); ok {
		return finish(c, "по WCS")
	}
	if c, ok := gridByName(paths); ok {
		return finish(c, "по именам файлов")
	}
	if c, ok := gridByStars(imgs); ok {
		return finish(c, "по звёздам в перекрытиях")
	}
	n := len(imgs)
	cols = max(1, int(math.Ceil(math.Sqrt(float64(n)))))
	for i := range imgs {
		cells = append(cells, cell{i / cols, i % cols})
	}
	how = "по порядку выбора"
	if sameField(imgs) {
		how += "; внимание: есть кадры одного и того же поля — они наложатся по WCS, а не встанут рядом"
	}
	return finish(cells, how)
}

// sameField reports two plate-solved frames whose centres are less than a quarter tile apart.
func sameField(imgs []*Img) bool {
	for i := range imgs {
		wi := parseWCS(imgs[i])
		for j := i + 1; j < len(imgs) && wi != nil; j++ {
			if wj := parseWCS(imgs[j]); wj != nil {
				x, y := wi.skyToPix(wj.pixToSky(float64(imgs[j].W)/2, float64(imgs[j].H)/2))
				if math.Abs(x-float64(imgs[i].W)/2) < float64(imgs[i].W)/4 && math.Abs(y-float64(imgs[i].H)/2) < float64(imgs[i].H)/4 {
					return true
				}
			}
		}
	}
	return false
}

func finish(c []cell, how string) ([]cell, int, int, string) {
	rows, cols := 0, 0
	for _, k := range c {
		rows, cols = max(rows, k[0]+1), max(cols, k[1]+1)
	}
	return c, rows, cols, how
}

// gridByWCS projects tile centres onto the first tile's pixel grid (as they look on screen) and
// groups them into rows and columns; a new group starts after a gap of half a tile.
func gridByWCS(imgs []*Img) ([]cell, bool) {
	if len(imgs) == 0 {
		return nil, false
	}
	ref := parseWCS(imgs[0])
	if ref == nil {
		return nil, false
	}
	xs, ys := make([]float64, len(imgs)), make([]float64, len(imgs))
	for i, m := range imgs {
		w := parseWCS(m)
		if w == nil {
			return nil, false
		}
		xs[i], ys[i] = ref.skyToPix(w.pixToSky(float64(m.W)/2, float64(m.H)/2))
	}
	cx := groups(xs, float64(imgs[0].W)/2)
	cy := groups(ys, float64(imgs[0].H)/2)
	c := make([]cell, len(imgs))
	for i := range c {
		c[i] = cell{cy[i], cx[i]}
	}
	return c, unique(c)
}

var cellSuffix = regexp.MustCompile(`(\d+)\s*[-_xх]\s*(\d+)\s*$`)

// gridByName reads "R-C" from the file name, or from its folder when the file name has none.
func gridByName(paths []string) ([]cell, bool) {
	if len(paths) == 0 {
		return nil, false
	}
	c := make([]cell, len(paths))
	r0, c0 := math.MaxInt, math.MaxInt
	for i, p := range paths {
		m := cellSuffix.FindStringSubmatch(strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)))
		if m == nil {
			m = cellSuffix.FindStringSubmatch(filepath.Base(filepath.Dir(p)))
		}
		if m == nil {
			return nil, false
		}
		r, _ := strconv.Atoi(m[1])
		k, _ := strconv.Atoi(m[2])
		c[i] = cell{r, k}
		r0, c0 = min(r0, r), min(c0, k)
	}
	for i := range c {
		c[i] = cell{c[i][0] - r0, c[i][1] - c0}
	}
	return c, unique(c)
}

// groups numbers values from the smallest, opening a new group after a gap larger than gap.
func groups(v []float64, gap float64) []int {
	idx := make([]int, len(v))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return v[idx[a]] < v[idx[b]] })
	out := make([]int, len(v))
	g := 0
	for k, i := range idx {
		if k > 0 && v[i]-v[idx[k-1]] > gap {
			g++
		}
		out[i] = g
	}
	return out
}

func unique(c []cell) bool {
	seen := map[cell]bool{}
	for _, k := range c {
		if seen[k] || k[0] >= 10 || k[1] >= 10 {
			return false
		}
		seen[k] = true
	}
	return true
}

// gridByStars lays tiles out from their content alone: every pair is searched for a common star
// pattern at any offset (tiles of one mount share orientation and scale, so a coarse translation vote
// finds the overlap and matchStars refines it), the best links form a tree from tile 0, and the tile
// centres, placed in tile 0's pixel grid, are grouped into rows and columns as gridByWCS does.
func gridByStars(imgs []*Img) ([]cell, bool) {
	n := len(imgs)
	if n < 2 {
		return nil, false
	}
	stars := make([][]star, n)
	parallel(n, func(lo, hi int) {
		for i := lo; i < hi; i++ {
			stars[i] = detectStars(imgs[i])
		}
	})
	type link struct {
		t   Aff // j's pixels into i's
		cnt int
	}
	L := make([][]link, n)
	for i := range L {
		L[i] = make([]link, n)
	}
	parallel(n*n, func(lo, hi int) {
		for k := lo; k < hi; k++ {
			i, j := k/n, k%n
			if i >= j {
				continue
			}
			if t, c, ok := findOverlap(stars[i], stars[j], imgs[i], imgs[j]); ok {
				L[i][j], L[j][i] = link{t, c}, link{t.inv(), c}
			}
		}
	})
	pos := make([]Aff, n) // tile k's pixels into tile 0's
	placed := make([]bool, n)
	pos[0], placed[0] = identity, true
	for done := 1; done < n; done++ {
		bu, bv := -1, -1
		for u := 0; u < n; u++ {
			for v := 0; v < n; v++ {
				if placed[u] && !placed[v] && L[u][v].cnt > 0 && (bu < 0 || L[u][v].cnt > L[bu][bv].cnt) {
					bu, bv = u, v
				}
			}
		}
		if bu < 0 {
			return nil, false // a tile shares no stars with the rest
		}
		pos[bv], placed[bv] = pos[bu].mul(L[bu][bv].t), true
	}
	xs, ys := make([]float64, n), make([]float64, n)
	for i, m := range imgs {
		xs[i], ys[i] = pos[i].apply(float64(m.W)/2, float64(m.H)/2)
	}
	cx := groups(xs, float64(imgs[0].W)/2)
	cy := groups(ys, float64(imgs[0].H)/2)
	c := make([]cell, n)
	for i := range c {
		c[i] = cell{cy[i], cx[i]}
	}
	return c, unique(c)
}

// findOverlap looks for b's stars in a at any offset. Tiles of one mount share scale and nearly share
// orientation, so the vectors from a star to its nearest neighbours look the same in both tiles: two
// stars whose neighbour vectors agree twice are a candidate match. Candidates vote for the offset in
// coarse bins (a degree of rotation smears it across the overlap); the winning bin's candidates give
// a similarity, which matchStars refines. It returns b → a and the number of matched stars.
func findOverlap(sa, sb []star, a, b *Img) (Aff, int, bool) {
	const keep, k, reach, tol = 2000, 4, 120.0, 4.0
	sa, sb = sa[:min(len(sa), keep)], sb[:min(len(sb), keep)]
	if len(sa) < 20 || len(sb) < 20 {
		return Aff{}, 0, false
	}
	da, db := neighbourVecs(sa, k, reach), neighbourVecs(sb, k, reach)
	type key struct{ x, y int }
	hash := map[key][]int32{} // neighbour vector of a (in tol cells) → star of a
	for i, vs := range da {
		for _, v := range vs {
			kk := key{int(math.Floor(v[0] / tol)), int(math.Floor(v[1] / tol))}
			hash[kk] = append(hash[kk], int32(i))
		}
	}
	type cand struct{ p, q [2]float64 }
	var cs []cand
	hits := map[int32]int{}
	for j, vs := range db {
		clear(hits)
		for _, v := range vs {
			kx, ky := int(math.Floor(v[0]/tol)), int(math.Floor(v[1]/tol))
			seen := map[int32]bool{}
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					for _, i := range hash[key{kx + dx, ky + dy}] {
						if seen[i] {
							continue
						}
						for _, w := range da[i] {
							if math.Hypot(w[0]-v[0], w[1]-v[1]) < tol {
								seen[i] = true
								hits[i]++
								break
							}
						}
					}
				}
			}
		}
		for i, h := range hits {
			if h >= 2 {
				cs = append(cs, cand{[2]float64{sa[i].x, sa[i].y}, [2]float64{sb[j].x, sb[j].y}})
			}
		}
	}
	if len(cs) < 6 {
		return Aff{}, 0, false
	}
	bin := 0.03 * float64(max(a.W, a.H, b.W, b.H))
	votes := map[key][]int{}
	for c, x := range cs {
		kk := key{int(math.Floor((x.p[0] - x.q[0]) / bin)), int(math.Floor((x.p[1] - x.q[1]) / bin))}
		votes[kk] = append(votes[kk], c)
	}
	var bestK key
	bestV := 0
	for kk := range votes {
		n := 0
		for dy := 0; dy <= 1; dy++ {
			for dx := 0; dx <= 1; dx++ {
				n += len(votes[key{kk.x + dx, kk.y + dy}])
			}
		}
		if n > bestV {
			bestK, bestV = kk, n
		}
	}
	if bestV < 6 {
		return Aff{}, 0, false
	}
	var src, dst [][2]float64
	for dy := 0; dy <= 1; dy++ {
		for dx := 0; dx <= 1; dx++ {
			for _, c := range votes[key{bestK.x + dx, bestK.y + dy}] {
				src, dst = append(src, cs[c].q), append(dst, cs[c].p)
			}
		}
	}
	t := fitSimilarity(src, dst)
	for it := 0; it < 3; it++ { // drop stray candidates
		var s2, d2 [][2]float64
		for i := range src {
			x, y := t.apply(src[i][0], src[i][1])
			if math.Hypot(x-dst[i][0], y-dst[i][1]) < 10 {
				s2, d2 = append(s2, src[i]), append(d2, dst[i])
			}
		}
		if len(s2) < 4 {
			return Aff{}, 0, false
		}
		t = fitSimilarity(s2, d2)
	}
	if s := math.Hypot(t[0], t[3]); s < 0.95 || s > 1.05 {
		return Aff{}, 0, false
	}
	pairs := matchStars(sa, sb, a.W, a.H, b.W, b.H, t, 20)
	if len(pairs) < 10 {
		return Aff{}, 0, false
	}
	src, dst = make([][2]float64, len(pairs)), make([][2]float64, len(pairs))
	for i, q := range pairs {
		src[i], dst[i] = q.B, q.A
	}
	return fitSimilarity(src, dst), len(pairs), true
}

// neighbourVecs returns, for each star, the vectors to its k nearest neighbours within reach.
func neighbourVecs(s []star, k int, reach float64) [][][2]float64 {
	type key struct{ x, y int }
	grid := map[key][]int{}
	for i, p := range s {
		kk := key{int(p.x / reach), int(p.y / reach)}
		grid[kk] = append(grid[kk], i)
	}
	out := make([][][2]float64, len(s))
	for i, p := range s {
		var near [][3]float64 // dx, dy, d
		kx, ky := int(p.x/reach), int(p.y/reach)
		for dy := -1; dy <= 1; dy++ {
			for dx := -1; dx <= 1; dx++ {
				for _, j := range grid[key{kx + dx, ky + dy}] {
					if j == i {
						continue
					}
					vx, vy := s[j].x-p.x, s[j].y-p.y
					if d := math.Hypot(vx, vy); d < reach {
						near = append(near, [3]float64{vx, vy, d})
					}
				}
			}
		}
		sort.Slice(near, func(a, b int) bool { return near[a][2] < near[b][2] })
		for _, v := range near[:min(len(near), k)] {
			out[i] = append(out[i], [2]float64{v[0], v[1]})
		}
	}
	return out
}
