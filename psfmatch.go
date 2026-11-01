package main

import (
	"fmt"
	"math"
	"strings"
)

const gaussianFWHM = 2.354820045

type psfChannel struct {
	n                                   int
	native, mosaic, scale, sigma, after float64
}

type psfMatchReport struct {
	target   float64
	applied  bool
	note     string
	channels [][]psfChannel
}

func channelName(c, C int) string {
	if C == 1 {
		return "L"
	}
	if c < 3 {
		return []string{"R", "G", "B"}[c]
	}
	return fmt.Sprintf("C%d", c+1)
}

func warpPixelScale(w Warp, x, y float64) float64 {
	ax, ay := w.apply(x-.5, y)
	bx, by := w.apply(x+.5, y)
	cx, cy := w.apply(x, y-.5)
	dx, dy := w.apply(x, y+.5)
	return math.Sqrt(math.Abs((bx-ax)*(dy-cy) - (by-ay)*(dx-cx)))
}

func estimatePSF(m *Img, w Warp, stars []star, c int) (psfChannel, string) {
	var native, mosaic, ellipticity []float64
	var cells [16][]float64
	for _, s := range stars {
		f, e, ok := starShapeChannel(m, s, c)
		if !ok {
			continue
		}
		scale := warpPixelScale(w, s.x, s.y)
		if !finitePoint(scale, f) || scale <= 0 {
			continue
		}
		native, mosaic, ellipticity = append(native, f), append(mosaic, f*scale), append(ellipticity, e)
		i := diagnosticBin(s.x, s.y, m)
		cells[i] = append(cells[i], f*scale)
	}
	p := psfChannel{n: len(native)}
	if p.n < 30 {
		return p, "нужно ≥30 подходящих звёзд"
	}
	p.native, p.mosaic = median(native), median(mosaic)
	p.scale = p.mosaic / p.native
	if median(ellipticity) > .25 {
		return p, "сильная вытянутость: нужна другая модель PSF"
	}
	count, lo, hi := 0, math.Inf(1), 0.0
	for _, values := range cells {
		if len(values) < 3 {
			continue
		}
		f := median(values)
		count, lo, hi = count+1, math.Min(lo, f), math.Max(hi, f)
	}
	if count < 4 {
		return p, "недостаточное покрытие поля звёздами"
	}
	if hi/lo > 1.35 {
		return p, "резкость сильно меняется по полю: нужна пространственная PSF"
	}
	return p, ""
}

// matchPSF only broadens Gaussian cores, to one median width in mosaic pixels.
// Original images remain immutable; any failed estimate or post-check cancels
// the whole operation so a partially matched set is never used for rendering.
func matchPSF(tiles []*Tile, W []Warp) ([]*Img, psfMatchReport) {
	out := make([]*Img, len(tiles))
	for i, t := range tiles {
		out[i] = t.Img
	}
	report := psfMatchReport{channels: make([][]psfChannel, len(tiles))}
	stars := make([][]star, len(tiles))
	for i, t := range tiles {
		s := detectStars(t.Img)
		stars[i] = s[:min(len(s), 4000)]
		report.channels[i] = make([]psfChannel, t.Img.C)
		for c := 0; c < t.Img.C; c++ {
			p, reason := estimatePSF(t.Img, W[i], stars[i], c)
			report.channels[i][c] = p
			if reason != "" {
				report.note = fmt.Sprintf("PSF не применено: %s %s — %s", t.Name, channelName(c, t.Img.C), reason)
				return out, report
			}
			report.target = math.Max(report.target, p.mosaic)
		}
	}
	changed := false
	for i := range tiles {
		for c := range report.channels[i] {
			p := &report.channels[i][c]
			targetNative := report.target / p.scale
			if targetNative > 14 {
				report.note = "PSF не применено: требуемая ширина выходит за диапазон оценки ядра"
				return out, report
			}
			p.sigma = math.Sqrt(math.Max(0, targetNative*targetNative-p.native*p.native)) / gaussianFWHM
			if report.target-p.mosaic < .1 || p.sigma < .35 {
				p.sigma = 0
			} else {
				changed = true
			}
		}
	}
	if !changed {
		report.note = fmt.Sprintf("PSF: ширина уже согласована (цель %.2f px мозаики)", report.target)
		return out, report
	}
	filtered := append([]*Img(nil), out...)
	for i, t := range tiles {
		for c, p := range report.channels[i] {
			if p.sigma == 0 {
				continue
			}
			if filtered[i] == t.Img {
				filtered[i] = newImg(t.Img.W, t.Img.H, t.Img.C)
				filtered[i].Hdr, filtered[i].BottomUp = t.Img.Hdr, t.Img.BottomUp
				copy(filtered[i].Pix, t.Img.Pix)
			}
			n := t.Img.W * t.Img.H
			blurred := gaussianPSFPlane(t.Img.Pix[c*n:(c+1)*n], t.Img.W, t.Img.H, p.sigma)
			copy(filtered[i].Pix[c*n:(c+1)*n], blurred)
		}
		for c := range report.channels[i] {
			post, reason := estimatePSF(filtered[i], W[i], stars[i], c)
			report.channels[i][c].after = post.mosaic
			if reason != "" || math.Abs(post.mosaic-report.target) > math.Max(.15, .08*report.target) {
				report.note = fmt.Sprintf("PSF не применено: проверка %s %s после пробы не подтвердила цель %.2f px", t.Name, channelName(c, t.Img.C), report.target)
				return out, report
			}
		}
	}
	beforeMin, afterMin, afterMax := math.Inf(1), math.Inf(1), 0.0
	for _, channels := range report.channels {
		for _, p := range channels {
			beforeMin = math.Min(beforeMin, p.mosaic)
			afterMin = math.Min(afterMin, p.after)
			afterMax = math.Max(afterMax, p.after)
		}
	}
	if afterMax-afterMin > math.Max(.15, .85*(report.target-beforeMin)) {
		report.note = fmt.Sprintf("PSF не применено: проба не уменьшила различия ширины (%.2f → %.2f px)", report.target-beforeMin, afterMax-afterMin)
		return out, report
	}
	report.applied = true
	report.note = fmt.Sprintf("PSF: ядра согласованы до %.2f px мозаики; более резкие каналы сглажены", report.target)
	return filtered, report
}

func (r psfMatchReport) tileNote(i int) string {
	if !r.applied {
		return r.note
	}
	var parts []string
	for c, p := range r.channels[i] {
		parts = append(parts, fmt.Sprintf("%s %.2f → %.2f px (σ %.2f px исходника, N=%d)", channelName(c, len(r.channels[i])), p.mosaic, p.after, p.sigma, p.n))
	}
	return r.note + "\n" + strings.Join(parts, "; ")
}

func gaussianKernel(sigma float64) []float64 {
	radius := max(1, int(math.Ceil(4*sigma)))
	k := make([]float64, 2*radius+1)
	var sum float64
	for i := range k {
		x := float64(i - radius)
		k[i] = math.Exp(-.5 * x * x / (sigma * sigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	return k
}

// Half-sample reflection retains a flat field without darkening the outer edge.
func gaussianRaw(src []float32, w, h int, k []float64) []float32 {
	tmp, out := make([]float32, len(src)), make([]float32, len(src))
	indices := func(n int) []int {
		a := make([]int, n*len(k))
		r := len(k) / 2
		for x := 0; x < n; x++ {
			for j := range k {
				p := (x + j - r) % (2 * n)
				if p < 0 {
					p += 2 * n
				}
				if p >= n {
					p = 2*n - p - 1
				}
				a[x*len(k)+j] = p
			}
		}
		return a
	}
	X, Y := indices(w), indices(h)
	parallel(h, func(lo, hi int) {
		for y := lo; y < hi; y++ {
			for x := 0; x < w; x++ {
				var v float64
				for j, weight := range k {
					v += weight * float64(src[y*w+X[x*len(k)+j]])
				}
				tmp[y*w+x] = float32(v)
			}
		}
	})
	parallel(h, func(lo, hi int) {
		for y := lo; y < hi; y++ {
			for x := 0; x < w; x++ {
				var v float64
				for j, weight := range k {
					v += weight * float64(tmp[Y[y*len(k)+j]*w+x])
				}
				out[y*w+x] = float32(v)
			}
		}
	})
	return out
}

func gaussianPSFPlane(src []float32, w, h int, sigma float64) []float32 {
	k := gaussianKernel(sigma)
	masked := false
	for _, v := range src {
		if v == 0 || math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			masked = true
			break
		}
	}
	if !masked {
		return gaussianRaw(src, w, h, k)
	}
	values, weights := make([]float32, len(src)), make([]float32, len(src))
	for i, v := range src {
		if v != 0 && finitePoint(float64(v), 0) {
			values[i], weights[i] = v, 1
		}
	}
	values, weights = gaussianRaw(values, w, h, k), gaussianRaw(weights, w, h, k)
	for i, v := range src {
		if v == 0 || !finitePoint(float64(v), 0) || weights[i] < 1e-6 {
			values[i] = 0
		} else {
			values[i] /= weights[i]
		}
	}
	return values
}
