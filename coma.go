package main

import (
	"fmt"
	"image"
	"math"
	"strings"
)

const comaRadius = 8
const comaSide = 2*comaRadius + 1

type comaSample struct {
	psf                     []float64
	x, y, flux, width, asym float64
}

func psfMoments(p []float64) (cx, cy float64, cov psfCovariance) {
	var sum float64
	for i, v := range p {
		sum += v
		cx += v * float64(i%comaSide-comaRadius)
		cy += v * float64(i/comaSide-comaRadius)
	}
	if sum <= 0 {
		return
	}
	cx, cy = cx/sum, cy/sum
	for i, v := range p {
		x, y := float64(i%comaSide-comaRadius)-cx, float64(i/comaSide-comaRadius)-cy
		cov.xx += v * x * x / sum
		cov.xy += v * x * y / sum
		cov.yy += v * y * y / sum
	}
	return
}

func empiricalAsymmetry(p []float64) float64 {
	var diff, sum float64
	for i, v := range p {
		diff += math.Abs(v - p[len(p)-1-i])
		sum += v
	}
	return diff / math.Max(2*sum, 1e-12)
}

func normalisePSF(p []float64) []float64 {
	q := append([]float64(nil), p...)
	for pass := 0; pass < 3; pass++ {
		var sum float64
		for i, v := range q {
			q[i] = math.Max(0, v)
			sum += q[i]
		}
		if sum <= 0 {
			return nil
		}
		for i := range q {
			q[i] /= sum
		}
		cx, cy, _ := psfMoments(q)
		if math.Hypot(cx, cy) < 1e-4 {
			break
		}
		out := make([]float64, len(q))
		for i, v := range q {
			x, y := float64(i%comaSide)-cx, float64(i/comaSide)-cy
			ix, iy := int(math.Floor(x)), int(math.Floor(y))
			fx, fy := x-float64(ix), y-float64(iy)
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					X, Y := ix+dx, iy+dy
					if X < 0 || Y < 0 || X >= comaSide || Y >= comaSide {
						continue
					}
					wx, wy := 1-fx, 1-fy
					if dx == 1 {
						wx = fx
					}
					if dy == 1 {
						wy = fy
					}
					out[Y*comaSide+X] += v * wx * wy
				}
			}
		}
		q = out
	}
	var sum float64
	for _, v := range q {
		sum += v
	}
	for i := range q {
		q[i] /= sum
	}
	return q
}

func measureComaStar(m *Img, c int, s star) (comaSample, bool) {
	x, y, ok := centroid(m, c, s.x, s.y)
	if !ok {
		return comaSample{}, false
	}
	ix, iy := int(math.Round(x)), int(math.Round(y))
	if ix < 11 || iy < 11 || ix >= m.W-11 || iy >= m.H-11 {
		return comaSample{}, false
	}
	var ring []float64
	for d := -11; d <= 11; d++ {
		ring = append(ring, float64(m.at(c, ix+d, iy-11)), float64(m.at(c, ix+d, iy+11)), float64(m.at(c, ix-11, iy+d)), float64(m.at(c, ix+11, iy+d)))
	}
	bg := median(ring)
	for _, v := range ring {
		if v == 0 || !finitePoint(v, 0) {
			return comaSample{}, false
		}
	}
	for i := range ring {
		ring[i] = math.Abs(ring[i] - bg)
	}
	noise := math.Max(1e-7, 1.4826*median(ring))
	p := make([]float64, comaSide*comaSide)
	var flux, peak float64
	for dy := -comaRadius; dy <= comaRadius; dy++ {
		for dx := -comaRadius; dx <= comaRadius; dx++ {
			value := float64(bilinear(m, c, x+float64(dx), y+float64(dy)))
			if value == 0 || !finitePoint(value, 0) {
				return comaSample{}, false
			}
			v := value - bg
			p[(dy+comaRadius)*comaSide+dx+comaRadius] = v
			flux += v
			peak = math.Max(peak, v)
		}
	}
	if peak < 20*noise || flux <= 100*noise {
		return comaSample{}, false
	}
	for i := range p {
		p[i] /= flux
	}
	raw := make([]float64, len(p))
	var rawSum float64
	for i, v := range p {
		raw[i] = math.Max(0, v)
		rawSum += raw[i]
	}
	for i := range raw {
		raw[i] /= rawSum
	}
	cx, cy, _ := psfMoments(raw)
	positive := normalisePSF(p)
	if positive == nil {
		return comaSample{}, false
	}
	_, _, cov := psfMoments(positive)
	major, minor, _ := cov.axes()
	if minor < .6 || major > 4.5 {
		return comaSample{}, false
	}
	// The PSF used for stacking remains signed until the robust median is formed.
	return comaSample{p, x + cx, y + cy, flux, gaussianFWHM * math.Sqrt(major*minor), empiricalAsymmetry(positive)}, true
}

func medianPSF(samples []comaSample) []float64 {
	p := make([]float64, comaSide*comaSide)
	for i := range p {
		v := make([]float64, len(samples))
		for j, s := range samples {
			v[j] = s.psf[i]
		}
		p[i] = median(v)
	}
	return normalisePSF(p)
}

func psfDistance(a, b []float64) float64 {
	var sum float64
	for i, v := range a {
		sum += math.Abs(v - b[i])
	}
	return sum
}

func estimateComaRestoration(m *Img) (restorationModel, error) {
	if m.C != 1 && m.C != 3 {
		return restorationModel{}, fmt.Errorf("PSF комы поддерживает ч/б и RGB")
	}
	stars := detectStars(m)
	model := restorationModel{stars: stars[:min(len(stars), 4000)], channels: make([]restorationChannel, m.C)}
	for c := 0; c < m.C; c++ {
		var widths []float64
		for _, s := range model.stars {
			p, ok := measureComaStar(m, c, s)
			if ok {
				widths = append(widths, p.width)
			}
		}
		if len(widths) < 30 {
			return model, fmt.Errorf("%s: нужно ≥30 ненасыщенных звёзд", channelName(c, m.C))
		}
		n := m.W * m.H
		sky, noise, offset := restorationBackground(m.Pix[c*n:(c+1)*n], m.W)
		model.channels[c] = restorationChannel{median(widths), sky, noise, offset}
	}
	plane := m
	if m.C == 3 {
		plane = restorationLuminance(m)
		sky, noise, offset := restorationBackground(plane.Pix, plane.W)
		model.luminance = &restorationChannel{model.channels[1].fwhm, sky, noise, offset}
	}
	var cells [16][]comaSample
	for i, s := range model.stars {
		isolated := true
		for j, q := range model.stars {
			if i != j && math.Hypot(s.x-q.x, s.y-q.y) < 2*comaRadius+4 {
				isolated = false
				break
			}
		}
		if !isolated {
			continue
		}
		p, ok := measureComaStar(plane, 0, s)
		if !ok {
			continue
		}
		cells[diagnosticBin(s.x, s.y, m)] = append(cells[diagnosticBin(s.x, s.y, m)], p)
	}
	g := &spatialPSF{w: m.W, h: m.H, coma: true}
	valid := 0
	rows, cols := [4]bool{}, [4]bool{}
	for i, samples := range cells {
		g.nodes[i].n = len(samples)
		if len(samples) < 12 {
			continue
		}
		var train, validation []comaSample
		for j, s := range samples {
			if j%3 == 0 {
				validation = append(validation, s)
			} else {
				train = append(train, s)
			}
		}
		p := medianPSF(train)
		if p == nil {
			continue
		}
		asym := empiricalAsymmetry(p)
		if asym < .04 {
			continue
		}
		sym := make([]float64, len(p))
		for j := range p {
			sym[j] = .5 * (p[j] + p[len(p)-1-j])
		}
		var empiricalError, symmetricError float64
		for _, s := range validation {
			q := normalisePSF(s.psf)
			empiricalError += psfDistance(p, q)
			symmetricError += psfDistance(sym, q)
		}
		if empiricalError >= .95*symmetricError {
			continue
		}
		p = medianPSF(samples)
		_, _, cov := psfMoments(p)
		g.nodes[i] = psfNode{cov: cov, n: len(samples), valid: true, empirical: p, asymmetry: empiricalAsymmetry(p)}
		valid++
		rows[i/4], cols[i%4] = true, true
	}
	nr, nc := 0, 0
	for i := 0; i < 4; i++ {
		if rows[i] {
			nr++
		}
		if cols[i] {
			nc++
		}
	}
	if valid < 4 || nr < 2 || nc < 2 {
		return model, fmt.Errorf("кома не подтверждена: нужно ≥4 ячеек с ≥12 изолированными звёздами и улучшением на отложенных звёздах")
	}
	model.spatial = g
	return model, nil
}

func (g *spatialPSF) empiricalAt(x, y float64) ([]float64, bool) {
	i := min(3, max(0, int(y*4/float64(g.h))))*4 + min(3, max(0, int(x*4/float64(g.w))))
	if !g.nodes[i].valid {
		return nil, false
	}
	u, v := math.Max(0, math.Min(3, x*4/float64(g.w)-.5)), math.Max(0, math.Min(3, y*4/float64(g.h)-.5))
	ix, iy := int(u), int(v)
	p := make([]float64, comaSide*comaSide)
	var sum float64
	for dy := 0; dy < 2; dy++ {
		for dx := 0; dx < 2; dx++ {
			n := g.nodes[min(3, iy+dy)*4+min(3, ix+dx)]
			if !n.valid {
				continue
			}
			wx, wy := 1-(u-float64(ix)), 1-(v-float64(iy))
			if dx == 1 {
				wx = 1 - wx
			}
			if dy == 1 {
				wy = 1 - wy
			}
			w := wx * wy
			for j, a := range n.empirical {
				p[j] += w * a
			}
			sum += w
		}
	}
	for i := range p {
		p[i] /= sum
	}
	return p, true
}

func kernelFromPSF(p []float64) ellipticalKernel {
	k := ellipticalKernel{rx: comaRadius, ry: comaRadius}
	for i, v := range p {
		if v > 0 {
			k.taps = append(k.taps, psfTap{comaRadius - i%comaSide, comaRadius - i/comaSide, v})
		}
	}
	return k
}

func (k ellipticalKernel) adjoint() ellipticalKernel {
	out := ellipticalKernel{rx: k.rx, ry: k.ry, taps: make([]psfTap, len(k.taps))}
	for i, t := range k.taps {
		out.taps[i] = psfTap{-t.dx, -t.dy, t.weight}
	}
	return out
}

func assessComaRestoration(before, after *Img, model restorationModel, origin image.Point) (string, error) {
	stars := detectStars(before)
	stars = stars[:min(len(stars), 4000)]
	var selected []star
	for _, s := range stars {
		if model.spatial.confidence(s.x+float64(origin.X), s.y+float64(origin.Y)) >= .8 {
			selected = append(selected, s)
		}
	}
	if len(selected) < 10 {
		return model.spatial.summary(), fmt.Errorf("для проверки комы нужно ≥10 звёзд измеренного покрытия")
	}
	var notes []string
	for c := 0; c < before.C; c++ {
		var widths, afterWidths, flux, drift, asym, newAsym []float64
		for _, s := range selected {
			a, ok := measureComaStar(before, c, s)
			b, ok2 := measureComaStar(after, c, s)
			if !ok || !ok2 {
				continue
			}
			widths = append(widths, a.width)
			afterWidths = append(afterWidths, b.width)
			flux = append(flux, b.flux/a.flux)
			drift = append(drift, math.Hypot(b.x-a.x, b.y-a.y))
			asym = append(asym, a.asym)
			newAsym = append(newAsym, b.asym)
		}
		if len(widths) < 10 {
			return "", fmt.Errorf("%s: недостаточно контрольных звёзд", channelName(c, before.C))
		}
		w, W, a, A, f, d := median(widths), median(afterWidths), median(asym), median(newAsym), median(flux), median(drift)
		n := before.W * before.H
		_, n1, _ := restorationBackground(before.Pix[c*n:(c+1)*n], before.W)
		_, n2, _ := restorationBackground(after.Pix[c*n:(c+1)*n], after.W)
		notes = append(notes, fmt.Sprintf("%s ширина %.2f→%.2f px; асимметрия %.3f→%.3f; поток ×%.3f; шум ×%.2f", channelName(c, before.C), w, W, a, A, f, n2/n1))
		if W >= .99*w || W < .65*w || A > .98*a || math.Abs(f-1) > .05 || d > .15 || n2 > math.Max(1.2*n1, n1+1e-6) {
			return fmt.Sprint(notes), fmt.Errorf("%s: проба комы не прошла проверки профиля, потока, центров или шума", channelName(c, before.C))
		}
	}
	a, b := before, after
	if before.C == 3 {
		a, b = restorationLuminance(before), restorationLuminance(after)
	}
	var oldA, newA [16][]float64
	for _, s := range selected {
		x, ok := measureComaStar(a, 0, s)
		y, ok2 := measureComaStar(b, 0, s)
		if ok && ok2 {
			ix := min(3, max(0, int((s.x+float64(origin.X))*4/float64(model.spatial.w))))
			iy := min(3, max(0, int((s.y+float64(origin.Y))*4/float64(model.spatial.h))))
			oldA[iy*4+ix] = append(oldA[iy*4+ix], x.asym)
			newA[iy*4+ix] = append(newA[iy*4+ix], y.asym)
		}
	}
	for i := range oldA {
		if len(oldA[i]) >= 5 && median(newA[i]) > .98*median(oldA[i]) {
			return strings.Join(notes, "; "), fmt.Errorf("PSF[%d,%d]: асимметрия не уменьшилась", i/4+1, i%4+1)
		}
		if len(oldA[i]) >= 5 {
			g := model.spatial
			x, y := i%4, i/4
			r := image.Rect((x*g.w+3)/4, (y*g.h+3)/4, ((x+1)*g.w+3)/4, ((y+1)*g.h+3)/4).Sub(origin).Intersect(image.Rect(0, 0, a.W, a.H))
			if r.Dx() >= 32 && r.Dy() >= 32 {
				A, B := cropPixels(a, r), cropPixels(b, r)
				_, n1, _ := restorationBackground(A.Pix, A.W)
				_, n2, _ := restorationBackground(B.Pix, B.W)
				if n2 > math.Max(1.2*n1, n1+1e-6) {
					return strings.Join(notes, "; "), fmt.Errorf("PSF[%d,%d]: локальный шум вырос >20%%", i/4+1, i%4+1)
				}
			}
		}
	}
	return model.spatial.summary() + ". " + strings.Join(notes, "; "), nil
}
