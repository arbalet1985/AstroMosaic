package main

import (
	"math"
	"sort"
)

// The mask is a detection aid, not a photometric measurement. It deliberately
// leaves marginal emission out of the added layer; the UI provides an off switch.
// Work at full resolution, before preview binning, and never derive the mask
// from the artistically amplified result (which would amplify noise feedback).
func paletteSignalMask(signal []float32, w, h int) []float32 {
	smooth := waveSmooth(signal, w, h, 1, nil)
	smooth = waveSmooth(smooth, w, h, 2, nil)
	step := max(1, int(math.Sqrt(float64(w*h)/40000)))
	var raw, rawDiff, smoothDiff []float64
	lag := min(12, max(1, min(w, h)/4))
	for y := lag; y < h-lag; y += step {
		for x := lag; x < w-lag; x += step {
			i := y*w + x
			raw = append(raw, float64(signal[i]))
			diff := func(a []float32) float64 {
				return (float64(a[i]) - float64(a[i+lag]) - float64(a[i+lag*w]) + float64(a[i+lag*w+lag])) / 2
			}
			rawDiff = append(rawDiff, diff(signal))
			smoothDiff = append(smoothDiff, diff(smooth))
		}
	}
	mad := func(v []float64) float64 {
		m := median(v)
		for i, x := range v {
			v[i] = math.Abs(x - m)
		}
		return 1.4826 * median(v)
	}
	rawNoise, smoothedNoise := mad(rawDiff), mad(smoothDiff)
	// The lower 5% includes negative noise fluctuations. Correct that bias
	// before asking whether the smoothed emission is significantly above sky.
	sort.Float64s(raw)
	sky := 0.
	if len(raw) > 0 {
		sky = raw[len(raw)/20] + 1.645*rawNoise
	}
	sigma := math.Max(smoothedNoise, 1e-10)
	for i, v := range smooth {
		t := math.Max(0, math.Min(1, ((float64(v)-sky)/sigma-2)/3))
		smooth[i] = float32(t * t * (3 - 2*t)) // smooth transition: 2..5 sigma
	}
	return smooth
}

func (p *palettePrepared) buildMasks() {
	r := p.Region
	w, h := r.Dx(), r.Dy()
	for band := 0; band < 2; band++ {
		signal := make([]float32, w*h)
		weights := p.weights(paletteParams{Auto: true})
		parallel(h, func(lo, hi int) {
			for y := lo; y < hi; y++ {
				for x := 0; x < w; x++ {
					sx, sy := x+r.Min.X, y+r.Min.Y
					v := float64(p.SO.at(0, sx, sy)) - p.SBackground
					if band == 1 {
						v = 0
						for i, src := range [4]*Img{p.HO, p.HO, p.SO, p.SO} {
							v += weights[i] * (p.O[i].Offset + p.O[i].Gain*float64(src.at(1+i%2, sx, sy)))
						}
					}
					signal[y*w+x] = float32(v)
				}
			}
		})
		mask := paletteSignalMask(signal, w, h)
		if band == 0 {
			p.SulfurMask = mask
		} else {
			p.OxygenMask = mask
		}
	}
	// Union of independently detected emission, with an additional feather.
	// Use pre-gain masks so increasing artistic strength cannot expose more noise.
	union := make([]float32, w*h)
	for i := range union {
		a, b := p.SulfurMask[i], p.OxygenMask[i]
		union[i] = 1 - (1-a)*(1-b)
	}
	p.ForaxxMask = waveSmooth(union, w, h, 2, nil)
}

func (p *palettePrepared) foraxxMaskImage() *Img {
	small := p
	if p.Thumbnail != nil {
		small = p.Thumbnail
	}
	m := newImg(small.Region.Dx(), small.Region.Dy(), 1)
	copy(m.Pix, small.ForaxxMask)
	return m
}

func (p *palettePrepared) maskImage(oxygen bool) *Img {
	small := p
	if p.Thumbnail != nil {
		small = p.Thumbnail
	}
	r := small.Region
	m := newImg(r.Dx(), r.Dy(), 1)
	if oxygen {
		copy(m.Pix, small.OxygenMask)
	} else {
		copy(m.Pix, small.SulfurMask)
	}
	m.Hdr = map[string]string{}
	return m
}
