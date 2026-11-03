package main

import (
	"fmt"
	"image"
	"math"
	"strings"
)

type restorationParams struct {
	iterations int
	fraction   float64
	regularize bool
}
type restorationChannel struct{ fwhm, sky, noise, offset float64 }
type restorationModel struct {
	channels    []restorationChannel
	stars       []star
	luminance   *restorationChannel
	spatial     *spatialPSF
	patchKernel *ellipticalKernel
	colorModels []restorationModel
}

// Sample background differences, excluding bright structures. The offset makes
// the multiplicative iteration positive without clipping negative linear data.
func restorationBackground(src []float32, w int) (sky, noise, offset float64) {
	var values []float64
	minimum := 0.0
	for _, v := range src {
		if finitePoint(float64(v), 0) {
			minimum = math.Min(minimum, float64(v))
		}
	}
	step := max(1, len(src)/100000)
	for i := 0; i < len(src); i += step {
		v := float64(src[i])
		if v != 0 && finitePoint(v, 0) {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return 0, 1e-7, 1e-6 - minimum
	}
	sky = median(values)
	for i := range values {
		values[i] = math.Abs(values[i] - sky)
	}
	limit := sky + 2*1.4826*median(values)
	var diff []float64
	for i := 0; i+1 < len(src); i += step {
		if i%w == w-1 {
			continue
		}
		a, b := float64(src[i]), float64(src[i+1])
		if a != 0 && b != 0 && finitePoint(a, b) && a <= limit && b <= limit {
			diff = append(diff, math.Abs(a-b))
		}
	}
	if len(diff) > 0 {
		noise = median(diff) / (.67448975 * math.Sqrt2)
	}
	noise = math.Max(noise, 1e-7)
	return sky, noise, math.Max(0, -minimum+5*noise)
}

func estimateRestoration(m *Img) (restorationModel, error) {
	stars := detectStars(m)
	model := restorationModel{stars: stars[:min(len(stars), 4000)], channels: make([]restorationChannel, m.C)}
	unitWarp := Warp{S: identity, Si: identity, rn: 1}
	for c := 0; c < m.C; c++ {
		p, reason := estimatePSF(m, unitWarp, model.stars, c)
		if reason != "" {
			return model, fmt.Errorf("%s: %s", channelName(c, m.C), reason)
		}
		if p.native < 2 || p.native > 10 {
			return model, fmt.Errorf("%s: FWHM %.2f px вне диапазона 2…10 px", channelName(c, m.C), p.native)
		}
		n := m.W * m.H
		sky, noise, offset := restorationBackground(m.Pix[c*n:(c+1)*n], m.W)
		model.channels[c] = restorationChannel{p.native, sky, noise, offset}
	}
	if m.C == 3 {
		luma := restorationLuminance(m)
		p, reason := estimatePSF(luma, unitWarp, model.stars, 0)
		if reason != "" {
			return model, fmt.Errorf("яркость RGB: %s", reason)
		}
		if p.native < 2 || p.native > 10 {
			return model, fmt.Errorf("яркость RGB: FWHM вне диапазона 2…10 px")
		}
		sky, noise, offset := restorationBackground(luma.Pix, luma.W)
		model.luminance = &restorationChannel{p.native, sky, noise, offset}
	}
	return model, nil
}

func restorationLuminance(m *Img) *Img {
	out := newImg(m.W, m.H, 1)
	n := m.W * m.H
	for i := 0; i < n; i++ {
		valid := true
		var value float64
		for c := 0; c < m.C; c++ {
			v := float64(m.Pix[c*n+i])
			valid = valid && v != 0 && finitePoint(v, 0)
			value += v / float64(m.C)
		}
		if valid {
			out.Pix[i] = float32(value)
		}
	}
	return out
}

func restorationRadius(model restorationModel, p restorationParams) int {
	r := 1
	for _, sub := range model.colorModels {
		r = max(r, restorationRadius(sub, p))
	}
	for _, c := range model.channels {
		r = max(r, int(math.Ceil(4*p.fraction*c.fwhm/gaussianFWHM)))
	}
	if model.luminance != nil {
		r = max(r, int(math.Ceil(4*p.fraction*model.luminance.fwhm/gaussianFWHM)))
	}
	if model.spatial != nil {
		if model.spatial.coma {
			r = max(r, model.spatial.radius)
		}
		for _, node := range model.spatial.nodes {
			if node.valid {
				a, _, _ := node.cov.removal(p.fraction).axes()
				r = max(r, int(math.Ceil(4*a)))
			}
		}
	}
	if p.regularize {
		r += 3
	}
	return r
}

// Saturated cores and their surroundings are shared across colour channels.
// This prevents a protected red core being altered in green or blue.
func restorationProtection(m *Img, radius int) []bool {
	protect := make([]bool, m.W*m.H)
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			if x < 2*radius || y < 2*radius || x >= m.W-2*radius || y >= m.H-2*radius {
				protect[y*m.W+x] = true
			}
			saturated := false
			for c := 0; c < m.C; c++ {
				saturated = saturated || float64(m.at(c, x, y)) >= fitHigh
				if v := float64(m.at(c, x, y)); v <= 0 || !finitePoint(v, 0) {
					protect[y*m.W+x] = true
				}
			}
			if !saturated {
				continue
			}
			for yy := max(0, y-2*radius); yy <= min(m.H-1, y+2*radius); yy++ {
				for xx := max(0, x-2*radius); xx <= min(m.W-1, x+2*radius); xx++ {
					protect[yy*m.W+xx] = true
				}
			}
		}
	}
	return protect
}

// Damped Richardson-Lucy with the adjoint of the normalised masked convolution.
// Fixed iteration count, bounded updates and a smooth signal mask regularise
// the inverse problem. There is no clipping of the restored linear range to 0…1.
func restorePlane(src []float32, w, h int, model restorationChannel, p restorationParams, protect []bool, progress func(int)) []float32 {
	k := gaussianKernel(p.fraction * model.fwhm / gaussianFWHM)
	return restorePlaneKernel(src, w, h, model, p, protect, progress, func(values []float32) []float32 { return gaussianRaw(values, w, h, k) })
}

func restorePlaneKernel(src []float32, w, h int, model restorationChannel, p restorationParams, protect []bool, progress func(int), convolve func([]float32) []float32) []float32 {
	return restorePlaneOperators(src, w, h, model, p, protect, progress, convolve, convolve)
}

func restorePlaneOperators(src []float32, w, h int, model restorationChannel, p restorationParams, protect []bool, progress func(int), convolve, adjoint func([]float32) []float32) []float32 {
	valid, estimate, active := make([]float32, len(src)), make([]float32, len(src)), make([]float32, len(src))
	for i, v := range src {
		value := float64(v)
		if value == 0 || !finitePoint(value, 0) {
			continue
		}
		valid[i], estimate[i] = 1, float32(value+model.offset)
		if protect[i] || value <= 0 {
			continue
		}
		signal := math.Max(0, value-model.sky-3*model.noise)
		active[i] = float32(signal / (signal + 6*model.noise + 1e-12))
	}
	norm := convolve(valid)
	for i := range valid {
		if valid[i] > 0 && norm[i] > 1e-8 {
			valid[i] /= norm[i]
		}
	}
	sensitivity := adjoint(valid)
	for iteration := 0; iteration < p.iterations; iteration++ {
		if progress != nil {
			progress(iteration + 1)
		}
		blurred := convolve(estimate)
		ratio := make([]float32, len(src))
		for i, v := range src {
			if valid[i] == 0 || norm[i] < 1e-8 {
				continue
			}
			predicted := float64(blurred[i] / norm[i])
			residual := float64(v) + model.offset - predicted
			residual = math.Copysign(math.Max(0, math.Abs(residual)-2*model.noise), residual)
			correction := math.Min(2, math.Max(.5, 1+residual/math.Max(predicted, 1e-8)))
			ratio[i] = float32(correction) * valid[i]
		}
		back := adjoint(ratio)
		for i := range estimate {
			if active[i] == 0 || sensitivity[i] < 1e-8 {
				continue
			}
			factor := math.Min(1.25, math.Max(.8, float64(back[i]/sensitivity[i])))
			estimate[i] *= float32(1 + float64(active[i])*(factor-1))
		}
		if p.regularize {
			// Sparse two-scale projection after each RL step. Noise level is in
			// the same linear units; significant star/detail coefficients survive.
			settings := waveParams{Strength: .35, Gain: [waveLevels]float64{1, 1, 1, 1}}
			projected := filterWavePlane(estimate, w, h, []float64{model.noise * .89, model.noise * .20}, settings, nil)
			for i := range estimate {
				if active[i] > 0 {
					estimate[i] = projected[i]
				}
			}
		}
	}
	out := append([]float32(nil), src...)
	for i := range out {
		if !finitePoint(float64(src[i]), 0) {
			out[i] = 0
		} else if active[i] > 0 {
			out[i] = float32(float64(estimate[i]) - model.offset)
		}
	}
	return out
}

func restoreImage(m *Img, model restorationModel, p restorationParams, progress func(int, int)) *Img {
	if len(model.colorModels) > 0 {
		return restoreColorRegion(m, model, p, image.Rect(0, 0, m.W, m.H), progress)
	}
	if model.spatial != nil {
		return restoreSpatialRegion(m, model, p, image.Rect(0, 0, m.W, m.H), progress)
	}
	return restoreStationaryImage(m, model, p, progress, nil)
}

func restoreStationaryImage(m *Img, model restorationModel, p restorationParams, progress func(int, int), cov *psfCovariance) *Img {
	out := newImg(m.W, m.H, m.C)
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	protect := restorationProtection(m, restorationRadius(model, p))
	n := m.W * m.H
	restore := func(src []float32, channel restorationChannel, notify func(int)) []float32 {
		if model.patchKernel != nil {
			k := *model.patchKernel
			reverse := k.adjoint()
			raw := restorePlaneOperators(src, m.W, m.H, channel, p, protect, notify, func(v []float32) []float32 { return k.convolve(v, m.W, m.H) }, func(v []float32) []float32 { return reverse.convolve(v, m.W, m.H) })
			for i := range raw {
				if finitePoint(float64(src[i]), 0) {
					raw[i] = src[i] + float32(p.fraction)*(raw[i]-src[i])
				}
			}
			return raw
		}
		if cov == nil {
			return restorePlane(src, m.W, m.H, channel, p, protect, notify)
		}
		kernel := makeEllipticalKernel(cov.removal(p.fraction))
		return restorePlaneKernel(src, m.W, m.H, channel, p, protect, notify, func(values []float32) []float32 { return kernel.convolve(values, m.W, m.H) })
	}
	if m.C == 3 && model.luminance != nil {
		luma := restorationLuminance(m)
		var notify func(int)
		if progress != nil {
			notify = func(it int) { progress(-1, it) }
		}
		result := restore(luma.Pix, *model.luminance, notify)
		for i, value := range luma.Pix {
			gain := 1.0
			if !protect[i] && value > 0 {
				gain = float64(result[i]) / float64(value)
			}
			for c := 0; c < m.C; c++ {
				v := float64(m.Pix[c*n+i])
				if finitePoint(v, 0) {
					out.Pix[c*n+i] = float32(v * gain)
				}
			}
		}
		return out
	}
	for c, channel := range model.channels {
		var notify func(int)
		if progress != nil {
			notify = func(it int) { progress(c, it) }
		}
		copy(out.Pix[c*n:(c+1)*n], restore(m.Pix[c*n:(c+1)*n], channel, notify))
	}
	return out
}

func cropPixels(m *Img, r image.Rectangle) *Img {
	r = r.Intersect(image.Rect(0, 0, m.W, m.H))
	out := newImg(r.Dx(), r.Dy(), m.C)
	for c := 0; c < m.C; c++ {
		for y := 0; y < out.H; y++ {
			copy(out.Pix[c*out.W*out.H+y*out.W:][:out.W], m.Pix[c*m.W*m.H+(y+r.Min.Y)*m.W+r.Min.X:][:out.W])
		}
	}
	return out
}

func restorationPreviewRect(m *Img, stars []star, selection *frame) image.Rectangle {
	cx, cy := float64(m.W)/2, float64(m.H)/2
	if selection != nil {
		cx, cy = selection.CX, selection.CY
	} else if len(stars) > 0 {
		var cells [16][]star
		for _, s := range stars {
			i := diagnosticBin(s.x, s.y, m)
			cells[i] = append(cells[i], s)
		}
		best := 0
		for i := range cells {
			if len(cells[i]) > len(cells[best]) {
				best = i
			}
		}
		var xs, ys []float64
		for _, s := range cells[best] {
			xs, ys = append(xs, s.x), append(ys, s.y)
		}
		cx, cy = median(xs), median(ys)
	}
	w, h := min(m.W, 768), min(m.H, 768)
	x, y := min(max(0, int(cx)-w/2), m.W-w), min(max(0, int(cy)-h/2), m.H-h)
	return image.Rect(x, y, x+w, y+h)
}

func apertureFlux(m *Img, c int, x, y float64) float64 {
	ix, iy := int(math.Round(x)), int(math.Round(y))
	if ix < 11 || iy < 11 || ix >= m.W-11 || iy >= m.H-11 {
		return 0
	}
	var ring []float64
	for d := -10; d <= 10; d++ {
		ring = append(ring, float64(m.at(c, ix+d, iy-10)), float64(m.at(c, ix+d, iy+10)), float64(m.at(c, ix-10, iy+d)), float64(m.at(c, ix+10, iy+d)))
	}
	bg := median(ring)
	var flux float64
	for dy := -8; dy <= 8; dy++ {
		for dx := -8; dx <= 8; dx++ {
			if dx*dx+dy*dy <= 64 {
				flux += float64(m.at(c, ix+dx, iy+dy)) - bg
			}
		}
	}
	return flux
}

func assessRestoration(before, after *Img, stars []star) (string, error) {
	if stars == nil {
		stars = detectStars(before)
		stars = stars[:min(len(stars), 4000)]
	}
	var notes []string
	for c := 0; c < before.C; c++ {
		var oldWidths, newWidths, drift, fluxRatios []float64
		for _, s := range stars {
			a, _, ok1 := starShapeChannel(before, s, c)
			b, _, ok2 := starShapeChannel(after, s, c)
			if !ok1 || !ok2 {
				continue
			}
			x, y, ok1 := centroid(before, c, s.x, s.y)
			X, Y, ok2 := centroid(after, c, s.x, s.y)
			if !ok1 || !ok2 {
				continue
			}
			oldWidths, newWidths, drift = append(oldWidths, a), append(newWidths, b), append(drift, math.Hypot(X-x, Y-y))
			fa, fb := apertureFlux(before, c, x, y), apertureFlux(after, c, X, Y)
			if fa > 0 && fb > 0 {
				fluxRatios = append(fluxRatios, fb/fa)
			}
		}
		if len(oldWidths) < 10 || len(fluxRatios) < 10 {
			return "", fmt.Errorf("%s: для проверки пробы нужно ≥10 несатурированных звёзд", channelName(c, before.C))
		}
		a, b := median(oldWidths), median(newWidths)
		flux := median(fluxRatios)
		n := before.W * before.H
		_, noiseBefore, _ := restorationBackground(before.Pix[c*n:(c+1)*n], before.W)
		_, noiseAfter, _ := restorationBackground(after.Pix[c*n:(c+1)*n], after.W)
		notes = append(notes, fmt.Sprintf("%s FWHM %.2f → %.2f px; шум ×%.2f; поток ×%.3f (N=%d)", channelName(c, before.C), a, b, noiseAfter/noiseBefore, flux, len(oldWidths)))
		if b >= .98*a {
			return strings.Join(notes, "; "), fmt.Errorf("%s: уменьшение ширины <2%%", channelName(c, before.C))
		}
		if b < .65*a || median(drift) > .15 {
			return strings.Join(notes, "; "), fmt.Errorf("%s: чрезмерное изменение профиля или центров звёзд", channelName(c, before.C))
		}
		if math.Abs(flux-1) > .05 {
			return strings.Join(notes, "; "), fmt.Errorf("%s: изменение апертурного потока >5%%", channelName(c, before.C))
		}
		if noiseAfter > math.Max(noiseBefore*1.2, noiseBefore+1e-6) {
			return strings.Join(notes, "; "), fmt.Errorf("%s: рост оценки шума >20%%", channelName(c, before.C))
		}
	}
	return strings.Join(notes, "; "), nil
}
