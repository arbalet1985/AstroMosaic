package main

import (
	"fmt"
	"image"
	"math"
)

// This is a local star-model correction, not a colour desaturation filter.
// Positive Gaussian mixtures describe radial cores and wings independently in
// RGB. Only their difference from a common profile is added to the source;
// the measured residual (including background and noise) is retained.
const haloRadius = 22
const haloBorder = 30

var haloSigmas = [...]float64{1, 1.4, 2, 2.8, 4, 5.6, 8}

type haloFit struct {
	x, y, noise, peak float64
	bg                [3]float64
	a                 [7]float64
}

type haloReport struct {
	stars         []star
	before, after float64
	note          string
}

func haloBasis(r2 float64) [7]float64 {
	var b [7]float64
	for j, s := range haloSigmas {
		b[j] = math.Exp(-r2 / (2 * s * s))
	}
	return b
}

func (f haloFit) value(x, y float64) float64 {
	b := haloBasis((x-f.x)*(x-f.x) + (y-f.y)*(y-f.y))
	v := 0.0
	for j := range b {
		v += b[j] * f.a[j]
	}
	return v
}

func (f haloFit) background(x, y float64) float64 {
	return f.bg[0] + f.bg[1]*(x-f.x) + f.bg[2]*(y-f.y)
}

func fitHaloStar(m *Img, c int, s star) (haloFit, bool) {
	f := haloFit{}
	x, y, ok := centroid(m, c, s.x, s.y)
	if !ok {
		return f, false
	}
	f.x, f.y = x, y
	ix, iy := int(math.Round(x)), int(math.Round(y))
	if ix < haloBorder || iy < haloBorder || ix >= m.W-haloBorder || iy >= m.H-haloBorder {
		return f, false
	}
	// An outer annulus estimates a local tilted background, with robust rejection
	// of faint neighbours. A curved/structured background fails the residual QA.
	type point struct{ x, y, v float64 }
	var ring []point
	for dy := -haloBorder; dy <= haloBorder; dy++ {
		for dx := -haloBorder; dx <= haloBorder; dx++ {
			r2 := dx*dx + dy*dy
			v := float64(m.at(c, ix+dx, iy+dy))
			if !finitePoint(v, 0) || v == 0 {
				return f, false
			}
			if r2 >= 26*26 && r2 <= haloBorder*haloBorder {
				ring = append(ring, point{float64(ix+dx) - x, float64(iy+dy) - y, v})
			}
		}
	}
	for pass := 0; pass < 3; pass++ {
		ls := newLSQ(3)
		for _, q := range ring {
			if pass > 0 && math.Abs(q.v-f.bg[0]-f.bg[1]*q.x-f.bg[2]*q.y) > 3*f.noise {
				continue
			}
			ls.add([]int{0, 1, 2}, []float64{1, q.x, q.y}, q.v, 1)
		}
		copy(f.bg[:], ls.solve())
		var residual []float64
		for _, q := range ring {
			residual = append(residual, math.Abs(q.v-f.bg[0]-f.bg[1]*q.x-f.bg[2]*q.y))
		}
		f.noise = math.Max(1e-8, 1.4826*median(residual))
	}
	var gram [7][7]float64
	var rhs [7]float64
	for dy := -haloRadius; dy <= haloRadius; dy++ {
		for dx := -haloRadius; dx <= haloRadius; dx++ {
			px, py := float64(ix+dx), float64(iy+dy)
			r2 := (px-x)*(px-x) + (py-y)*(py-y)
			if r2 > haloRadius*haloRadius {
				continue
			}
			v := float64(m.at(c, ix+dx, iy+dy))
			if v >= fitHigh {
				return f, false
			}
			v -= f.background(px, py)
			f.peak = math.Max(f.peak, v)
			// Hold out half of the actual pixels, not samples of the fitted model.
			if (dx+dy)&1 != 0 {
				continue
			}
			b := haloBasis(r2)
			for j := range b {
				rhs[j] += b[j] * v
				for k := range b {
					gram[j][k] += b[j] * b[k]
				}
			}
		}
	}
	if f.peak < 60*f.noise {
		return f, false
	}
	// Small non-negative least-squares solve by cyclic coordinate descent.
	for it := 0; it < 250; it++ {
		for j := range f.a {
			r := rhs[j]
			for k := range f.a {
				r -= gram[j][k] * f.a[k]
			}
			f.a[j] = math.Max(0, f.a[j]+r/gram[j][j])
		}
	}
	var residual, signal, excess float64
	for dy := -haloRadius; dy <= haloRadius; dy++ {
		for dx := -haloRadius; dx <= haloRadius; dx++ {
			px, py := float64(ix+dx), float64(iy+dy)
			if (px-x)*(px-x)+(py-y)*(py-y) > haloRadius*haloRadius || (dx+dy)&1 == 0 {
				continue
			}
			v := float64(m.at(c, ix+dx, iy+dy)) - f.background(px, py)
			p := f.value(px, py)
			residual += (v - p) * (v - p)
			signal += p * p
			excess += math.Max(0, math.Abs(v-p)-3*f.noise)
		}
	}
	var flux float64
	for j, sigma := range haloSigmas {
		flux += 2 * math.Pi * sigma * sigma * f.a[j]
	}
	// Reject ellipticity, coma, blends and structured nebulosity which cannot be
	// represented by this radial model. Such stars need the other PSF tools.
	return f, signal > 0 && math.Sqrt(residual/signal) < .12 && excess/math.Max(flux/2, 1e-12) < .15
}

func haloTaper(r float64) float64 {
	if r >= haloRadius {
		return 0
	}
	if r <= 18 {
		return 1
	}
	return .5 + .5*math.Cos(math.Pi*(r-18)/(haloRadius-18))
}

// Measured annular RGB disagreement after subtracting the fitted background.
// Flux normalisation permits intrinsically coloured stars: no neutral colour
// or fixed RGB flux ratio is imposed.
func haloDisagreement(pixels [3][]float64, fits [3]haloFit, box image.Rectangle) float64 {
	var rings [3][11]float64
	for c := 0; c < 3; c++ {
		var total float64
		for i, v := range pixels[c] {
			x, y := float64(box.Min.X+i%box.Dx()), float64(box.Min.Y+i/box.Dx())
			r := math.Hypot(x-fits[c].x, y-fits[c].y)
			if r >= haloRadius {
				continue
			}
			v -= fits[c].background(x, y)
			rings[c][min(10, int(r/2))] += v
			total += v
		}
		if total <= 0 {
			return math.Inf(1)
		}
		for j := range rings[c] {
			rings[c][j] /= total
		}
	}
	var d float64
	for j := range rings[0] {
		for c := 0; c < 3; c++ {
			d += math.Abs(rings[c][j]-rings[(c+1)%3][j]) / 3
		}
	}
	return d
}

func haloStarPatch(m *Img, s star, strength float64) (image.Rectangle, [3][]float64, float64, float64, bool) {
	var fits [3]haloFit
	var result [3][]float64
	box := image.Rect(int(math.Round(s.x))-haloRadius-2, int(math.Round(s.y))-haloRadius-2, int(math.Round(s.x))+haloRadius+3, int(math.Round(s.y))+haloRadius+3)
	for c := 0; c < 3; c++ {
		f, ok := fitHaloStar(m, c, s)
		if !ok {
			return box, result, 0, 0, false
		}
		fits[c] = f
		if c > 0 && math.Hypot(f.x-fits[0].x, f.y-fits[0].y) > .35 {
			return box, result, 0, 0, false
		}
	}
	var norm [3]float64
	var common [7]float64
	for c, f := range fits {
		for j, sigma := range haloSigmas {
			norm[c] += 2 * math.Pi * sigma * sigma * f.a[j]
		}
		if norm[c] <= 0 {
			return box, result, 0, 0, false
		}
		for j := range common {
			common[j] += f.a[j] / norm[c] / 3
		}
	}
	var original [3][]float64
	for c, f := range fits {
		original[c] = make([]float64, box.Dx()*box.Dy())
		result[c] = make([]float64, len(original[c]))
		delta := make([]float64, len(original[c]))
		weights := make([]float64, len(delta))
		var sum, weight, flux float64
		for i := range delta {
			x, y := float64(box.Min.X+i%box.Dx()), float64(box.Min.Y+i/box.Dx())
			v := float64(m.at(c, int(x), int(y)))
			original[c][i] = v
			r := math.Hypot(x-f.x, y-f.y)
			b := haloBasis(r * r)
			var target float64
			for j := range b {
				target += common[j] * b[j] * norm[c]
			}
			taper := haloTaper(r)
			delta[i] = taper * (target - f.value(x, y)) * strength
			weights[i] = taper * target
			sum += delta[i]
			weight += weights[i]
			if r < haloRadius {
				flux += v - f.background(x, y)
			}
		}
		if weight <= 0 || flux <= 0 {
			return box, result, 0, 0, false
		}
		var mx, my float64
		for i := range delta {
			// Compensate the finite aperture/taper: total channel flux is unchanged.
			delta[i] -= sum * weights[i] / weight
			result[c][i] = original[c][i] + delta[i]
			x, y := float64(box.Min.X+i%box.Dx()), float64(box.Min.Y+i/box.Dx())
			mx += delta[i] * (x - f.x)
			my += delta[i] * (y - f.y)
			if math.Abs(delta[i]) > .35*f.peak || (delta[i] < 0 && result[c][i] < f.background(x, y)-3*f.noise) {
				return box, result, 0, 0, false
			}
		}
		if math.Hypot(mx, my)/flux > .03 {
			return box, result, 0, 0, false
		}
	}
	before, after := haloDisagreement(original, fits, box), haloDisagreement(result, fits, box)
	return box, result, before, after, finitePoint(before, after) && before > .025 && after < .9*before
}

func correctColorHalos(m *Img, strength float64) (*Img, haloReport, error) {
	r := haloReport{}
	if m.C != 3 || !finitePoint(strength, 0) || strength <= 0 || strength > 1 {
		return nil, r, fmt.Errorf("нужно RGB-изображение и сила коррекции 0–100%%")
	}
	stars := detectEmpiricalStars(m, true)
	out := newImg(m.W, m.H, m.C)
	copy(out.Pix, m.Pix)
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	var before, after []float64
	for i, s := range stars {
		isolated := true
		for j, q := range stars {
			if i != j && q.f > .05*s.f && math.Hypot(s.x-q.x, s.y-q.y) < 2*(haloRadius+3) {
				isolated = false
				break
			}
		}
		if !isolated {
			continue
		}
		box, p, b, a, ok := haloStarPatch(m, s, strength)
		if !ok {
			continue
		}
		// Do not let another accepted patch overwrite an earlier correction.
		// Detection centres can differ slightly from the fitted channel centres.
		overlaps := false
		for c := 0; c < 3; c++ {
			for j, v := range p[c] {
				idx := c*m.W*m.H + (box.Min.Y+j/box.Dx())*m.W + box.Min.X + j%box.Dx()
				if float32(v) != m.Pix[idx] && out.Pix[idx] != m.Pix[idx] {
					overlaps = true
				}
			}
		}
		if overlaps {
			continue
		}
		for c := 0; c < 3; c++ {
			for j, v := range p[c] {
				idx := c*m.W*m.H + (box.Min.Y+j/box.Dx())*m.W + box.Min.X + j%box.Dx()
				if float32(v) != m.Pix[idx] {
					out.Pix[idx] = float32(v)
				}
			}
		}
		r.stars = append(r.stars, s)
		before = append(before, b)
		after = append(after, a)
	}
	if len(r.stars) == 0 {
		return nil, r, fmt.Errorf("нет надёжных звёзд для коррекции: нужны изолированные, ненасыщенные RGB-звёзды с совмещёнными центрами и различимыми крыльями")
	}
	r.before, r.after = median(before), median(after)
	r.note = fmt.Sprintf("Скорректировано %d звёзд из %d кандидатов. Расхождение радиальных RGB-профилей: %.3f → %.3f. Поток каждого канала сохранён; неподходящие звёзды пропущены.", len(r.stars), len(stars), r.before, r.after)
	return out, r, nil
}
