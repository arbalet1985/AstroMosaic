package main

import (
	"fmt"
	"image"
	"math"
)

// Positive kernel fitting broadens measured profiles to a common round target
// in mosaic coordinates. No inverse filter or negative ringing lobes are used.
const matchKernelRadius = 8

func warpJacobian(w Warp, x, y float64) [4]float64 {
	a, b := w.apply(x-.5, y)
	c, d := w.apply(x+.5, y)
	e, f := w.apply(x, y-.5)
	g, h := w.apply(x, y+.5)
	return [4]float64{c - a, g - e, d - b, h - f}
}

func transformCovariance(c psfCovariance, j [4]float64) psfCovariance {
	return psfCovariance{j[0]*j[0]*c.xx + 2*j[0]*j[1]*c.xy + j[1]*j[1]*c.yy, j[0]*j[2]*c.xx + (j[0]*j[3]+j[1]*j[2])*c.xy + j[1]*j[3]*c.yy, j[2]*j[2]*c.xx + 2*j[2]*j[3]*c.xy + j[3]*j[3]*c.yy}
}

func nativeTargetCovariance(w Warp, x, y, sigma float64) (psfCovariance, bool) {
	j := warpJacobian(w, x, y)
	d := j[0]*j[3] - j[1]*j[2]
	if math.Abs(d) < 1e-8 || !finitePoint(d, 0) {
		return psfCovariance{}, false
	}
	inv := [4]float64{j[3] / d, -j[1] / d, -j[2] / d, j[0] / d}
	return transformCovariance(psfCovariance{sigma * sigma, 0, sigma * sigma}, inv), true
}

func covarianceProfile(c psfCovariance, r int) []float64 {
	d := c.xx*c.yy - c.xy*c.xy
	side := 2*r + 1
	p := make([]float64, side*side)
	if d <= 1e-8 {
		p[r*side+r] = 1
		return p
	}
	var sum float64
	for i := range p {
		x, y := float64(i%side-r), float64(i/side-r)
		p[i] = math.Exp(-.5 * (c.yy*x*x - 2*c.xy*x*y + c.xx*y*y) / d)
		sum += p[i]
	}
	for i := range p {
		p[i] /= sum
	}
	return p
}

// Full finite-support convolution of two centred point spread functions.
func convolveProfiles(p, k []float64) []float64 {
	s, t := int(math.Sqrt(float64(len(p)))), int(math.Sqrt(float64(len(k))))
	outSide := s + t - 1
	out := make([]float64, outSide*outSide)
	for j, b := range k {
		if b == 0 {
			continue
		}
		x, y := j%t, j/t
		for i, a := range p {
			out[(y+i/s)*outSide+x+i%s] += a * b
		}
	}
	return out
}

func fitPositiveMatchingKernel(p []float64, target psfCovariance) ([]float64, float64, bool) {
	_, _, source := psfMoments(p)
	diff := psfCovariance{target.xx - source.xx, target.xy - source.xy, target.yy - source.yy}
	_, minor, _ := diff.axes()
	if minor < .1 {
		return nil, 0, false
	}
	k := covarianceProfile(diff, matchKernelRadius)
	s, t := int(math.Sqrt(float64(len(p)))), 2*matchKernelRadius+1
	outSide := s + t - 1
	want := covarianceProfile(target, outSide/2)
	var error float64
	for it := 0; it < 35; it++ {
		pred := convolveProfiles(p, k)
		error = psfDistance(pred, want)
		if error < .04 {
			break
		}
		ratio := make([]float64, len(want))
		for i, v := range want {
			ratio[i] = v / math.Max(1e-12, pred[i])
		}
		var sum float64
		for j := range k {
			x, y := j%t, j/t
			var back float64
			for i, a := range p {
				back += a * ratio[(y+i/s)*outSide+x+i%s]
			}
			k[j] *= math.Min(2, math.Max(.5, back))
			sum += k[j]
		}
		for j := range k {
			k[j] /= sum
		}
	}
	x, y, _ := psfMoments(k)
	error = psfDistance(convolveProfiles(p, k), want)
	return k, error, error <= .08 && math.Hypot(x, y) < .1
}

func measureMatchingProfiles(m *Img, c int, stars []star) (*spatialPSF, error) {
	g := &spatialPSF{w: m.W, h: m.H, coma: true, profile: true, radius: 16}
	var bins [16][]comaSample
	for i, s := range stars {
		isolated := true
		for j, q := range stars {
			if i != j && math.Hypot(s.x-q.x, s.y-q.y) < 36 {
				isolated = false
				break
			}
		}
		if !isolated {
			continue
		}
		p, ok := measureProfileStar(m, c, s, 16)
		if ok {
			bin := diagnosticBin(s.x, s.y, m)
			bins[bin] = append(bins[bin], p)
		}
	}
	for i, ss := range bins {
		if len(ss) < 12 {
			return g, fmt.Errorf("ячейка [%d,%d]: нужно ≥12 изолированных звёзд", i/4+1, i%4+1)
		}
		var train, validation []comaSample
		for j, s := range ss {
			if j%3 == 0 {
				validation = append(validation, s)
			} else {
				train = append(train, s)
			}
		}
		p := medianPSF(train)
		var residual []float64
		for _, s := range validation {
			residual = append(residual, psfDistance(p, normalisePSF(s.psf)))
		}
		if median(residual) > .15 {
			return g, fmt.Errorf("ячейка [%d,%d]: непостоянная или шумная PSF", i/4+1, i%4+1)
		}
		p = medianPSF(ss)
		_, _, cov := psfMoments(p)
		g.nodes[i] = psfNode{cov: cov, empirical: p, n: len(ss), valid: true}
	}
	return g, nil
}

func positiveKernelPlane(src []float32, w, h int, k ellipticalKernel) []float32 {
	values, valid := make([]float32, len(src)), make([]float32, len(src))
	for i, v := range src {
		if v != 0 && finitePoint(float64(v), 0) {
			values[i], valid[i] = v, 1
		}
	}
	num, den := k.convolve(values, w, h), k.convolve(valid, w, h)
	out := append([]float32(nil), src...)
	for i, v := range src {
		if !finitePoint(float64(v), 0) {
			out[i] = 0
		} else if v != 0 && den[i] > .5 {
			out[i] = num[i] / den[i]
		}
	}
	return out
}

func spatialMatchPlane(m *Img, c int, kernels *spatialPSF) []float32 {
	const half, stride = 256, 384
	n := m.W * m.H
	out := make([]float32, n)
	sum, weight := make([]float64, n), make([]float64, n)
	bounds := image.Rect(0, 0, m.W, m.H)
	for cy := 0; cy-half < m.H; cy += stride {
		for cx := 0; cx-half < m.W; cx += stride {
			core := image.Rect(cx-half, cy-half, cx+half, cy+half).Intersect(bounds)
			context := core.Inset(-matchKernelRadius).Intersect(bounds)
			p, _ := kernels.empiricalAt(float64(min(cx, m.W-1)), float64(min(cy, m.H-1)))
			patch := cropPixels(imageChannelView(m, c), context)
			blur := positiveKernelPlane(patch.Pix, patch.W, patch.H, kernelFromPSF(p))
			for y := core.Min.Y; y < core.Max.Y; y++ {
				for x := core.Min.X; x < core.Max.X; x++ {
					w := (1 + math.Cos(math.Pi*float64(x-cx)/half)) * (1 + math.Cos(math.Pi*float64(y-cy)/half))
					i := y*m.W + x
					sum[i] += w * float64(blur[(y-context.Min.Y)*patch.W+x-context.Min.X])
					weight[i] += w
				}
			}
		}
	}
	for i, v := range m.Pix[c*n : (c+1)*n] {
		if !finitePoint(float64(v), 0) {
			continue
		}
		out[i] = v
		if weight[i] > 1e-12 && v != 0 {
			out[i] = float32(sum[i] / weight[i])
		}
	}
	return out
}

func imageChannelView(m *Img, c int) *Img {
	return &Img{W: m.W, H: m.H, C: 1, Pix: m.Pix[c*m.W*m.H : (c+1)*m.W*m.H]}
}

func matchFieldPSF(tiles []*Tile, warps []Warp) ([]*Img, psfMatchReport) {
	original := make([]*Img, len(tiles))
	for i, t := range tiles {
		original[i] = t.Img
	}
	report := psfMatchReport{channels: make([][]psfChannel, len(tiles)), field: true}
	fail := func(note string) ([]*Img, psfMatchReport) {
		report.note = "PSF поля не применено: " + note
		return original, report
	}
	profiles := make([][]*spatialPSF, len(tiles))
	allStars := make([][]star, len(tiles))
	sigma := 0.0
	for i, t := range tiles {
		allStars[i] = detectEmpiricalStars(t.Img, true)
		allStars[i] = allStars[i][:min(4000, len(allStars[i]))]
		profiles[i] = make([]*spatialPSF, t.Img.C)
		report.channels[i] = make([]psfChannel, t.Img.C)
		for c := 0; c < t.Img.C; c++ {
			g, err := measureMatchingProfiles(t.Img, c, allStars[i])
			if err != nil {
				return fail(t.Name + " " + channelName(c, t.Img.C) + ": " + err.Error())
			}
			profiles[i][c] = g
			var widths []float64
			for j, node := range g.nodes {
				x, y := (float64(j%4)+.5)*float64(g.w)/4, (float64(j/4)+.5)*float64(g.h)/4
				a, _, _ := transformCovariance(node.cov, warpJacobian(warps[i], x, y)).axes()
				sigma = math.Max(sigma, a)
				b, d, _ := node.cov.axes()
				widths = append(widths, gaussianFWHM*math.Sqrt(b*d))
			}
			report.channels[i][c] = psfChannel{n: len(allStars[i]), native: median(widths), scale: warpPixelScale(warps[i], float64(g.w)/2, float64(g.h)/2)}
		}
	}
	baseSigma := sigma
	plans := make([][]*spatialPSF, len(tiles))
	plan := func() error {
		for i, t := range tiles {
			plans[i] = make([]*spatialPSF, t.Img.C)
			for c, g := range profiles[i] {
				kernels := &spatialPSF{w: g.w, h: g.h, coma: true, radius: matchKernelRadius}
				for j, node := range g.nodes {
					x, y := (float64(j%4)+.5)*float64(g.w)/4, (float64(j/4)+.5)*float64(g.h)/4
					target, ok := nativeTargetCovariance(warps[i], x, y, sigma)
					if !ok {
						return fmt.Errorf("вырожденная геометрия")
					}
					p, _, ok := fitPositiveMatchingKernel(node.empirical, target)
					if !ok {
						return fmt.Errorf("%s %s [%d,%d]: положительное ядро не достигло цели", t.Name, channelName(c, t.Img.C), j/4+1, j%4+1)
					}
					kernels.nodes[j] = psfNode{valid: true, empirical: p}
				}
				plans[i][c] = kernels
			}
		}
		return nil
	}
	var planErr error
	for attempt := 0; attempt < 3; attempt++ {
		factor := 1.15 * math.Pow(1.18, float64(attempt))
		sigma = math.Sqrt(factor*factor*baseSigma*baseSigma + .25)
		report.target = gaussianFWHM * sigma
		if report.target > 14 {
			return fail("целевая ширина >14 px мозаики")
		}
		planErr = plan()
		if planErr == nil {
			break
		}
	}
	if planErr != nil {
		return fail(planErr.Error())
	}
	filtered := make([]*Img, len(tiles))
	for i, t := range tiles {
		filtered[i] = newImg(t.Img.W, t.Img.H, t.Img.C)
		filtered[i].Hdr, filtered[i].BottomUp = t.Img.Hdr, t.Img.BottomUp
		n := t.Img.W * t.Img.H
		for c := range profiles[i] {
			kernels := plans[i][c]
			copy(filtered[i].Pix[c*n:(c+1)*n], spatialMatchPlane(t.Img, c, kernels))
			var beforeFlux, afterFlux []float64
			var widths []float64
			var counts [16]int
			for _, s := range allStars[i] {
				a, ok := measureProfileStar(t.Img, c, s, 16)
				b, ok2 := measureProfileStar(filtered[i], c, s, 16)
				if !ok || !ok2 {
					continue
				}
				target, valid := nativeTargetCovariance(warps[i], s.x, s.y, sigma)
				if !valid {
					continue
				}
				A, B, _ := target.axes()
				expected := gaussianFWHM * math.Sqrt(A*B)
				if math.Abs(b.width/expected-1) > .08 || psfDistance(normalisePSF(b.psf), covarianceProfile(target, 16)) > .15 {
					return fail(t.Name + " " + channelName(c, t.Img.C) + ": профиль после пробы не достиг цели")
				}
				counts[diagnosticBin(s.x, s.y, t.Img)]++
				beforeFlux = append(beforeFlux, a.flux)
				afterFlux = append(afterFlux, b.flux/a.flux)
				widths = append(widths, b.width*warpPixelScale(warps[i], s.x, s.y))
			}
			for _, count := range counts {
				if count < 5 {
					return fail("мало контрольных звёзд после пробы")
				}
			}
			if len(beforeFlux) < 30 || math.Abs(median(afterFlux)-1) > .03 {
				return fail("апертурный поток изменился >3%")
			}
			report.channels[i][c].mosaic = report.channels[i][c].native * report.channels[i][c].scale
			report.channels[i][c].after = median(widths)
		}
	}
	report.applied = true
	report.note = fmt.Sprintf("PSF поля: положительные локальные ядра → общая круглая %.2f px; профили и поток проверены", report.target)
	return filtered, report
}
