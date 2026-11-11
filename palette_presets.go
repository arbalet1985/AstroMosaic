package main

import (
	"math"
	"sort"
)

const paletteChoiceCount = 10

var paletteNames = [paletteChoiceCount]string{
	"HOO + жёлтая сера", "HOO", "SHO", "HSO", "HOS", "OHS", "OSH", "SOH", "Foraxx HOO", "Foraxx SHO",
}

type paletteFactorTone struct{ Low, Mid float64 }

func paletteName(mode int) string {
	if mode < 0 || mode >= len(paletteNames) {
		mode = 0
	}
	return paletteNames[mode]
}

// Foraxx expressions by The Coldest Nights / Ludo (ForaxX):
// https://thecoldestnights.com/2020/06/pixinsight-dynamic-narrowband-combinations-with-pixelmath/
// Linear adaptation explicitly described there: calculate dynamic factors on
// stretched copies, then mix the original, unstretched emission channels.
func palettePIP(v float64) float64 {
	v = math.Max(0, math.Min(1, v))
	if v == 0 {
		return 0
	}
	if v == 1 {
		return 1
	}
	return math.Pow(v, 1-v)
}

func (p *palettePrepared) mapPalette(s paletteParams, h, o, og, ss float64) [3]float64 {
	switch s.Mode {
	case 1:
		return [3]float64{h, og, o}
	case 2:
		return [3]float64{ss, h, o}
	case 3:
		return [3]float64{h, ss, o}
	case 4:
		return [3]float64{h, og, ss}
	case 5:
		return [3]float64{o, h, ss}
	case 6:
		return [3]float64{o, ss, h}
	case 7:
		return [3]float64{ss, og, h}
	case 8, 9:
		ht, ot := p.DynamicTone[0], p.DynamicTone[1]
		hv, ov := mtf(h, ht.Mid, ht.Low, 1), mtf(o, ot.Mid, ot.Low, 1)
		f := palettePIP(hv * ov)
		r := h
		if s.Mode == 9 {
			fo := palettePIP(ov)
			r = fo*ss + (1-fo)*h
		}
		return [3]float64{r, f*h + (1-f)*og, o}
	default:
		return [3]float64{h + ss, og + ss*(.45+.55*s.Hue), o}
	}
}

func (p *palettePrepared) preparePresetBalance() {
	small := p.Thumbnail
	if small == nil {
		small = p
	}
	base := small.compose(paletteParams{Hydrogen: 1, Oxygen: 1, Auto: true}, 0)
	low, mid := stretchParams(base, false)
	p.DynamicTone = [2]paletteFactorTone{{low[0], mid[0]}, {low[1], mid[1]}}
	// Gains compare signal amplitudes, not arbitrary file maxima. Use a fixed
	// coarse sample and the 95th percentile so single bright stars do not set the scale.
	var samples [3][]float64
	r := small.Region
	w := small.weights(paletteParams{Auto: true})
	step := max(1, int(math.Sqrt(float64(r.Dx()*r.Dy())/30000)))
	for y := r.Min.Y; y < r.Max.Y; y += step {
		for x := r.Min.X; x < r.Max.X; x += step {
			var o float64
			for i, src := range [4]*Img{small.HO, small.HO, small.SO, small.SO} {
				o += w[i] * (small.O[i].Offset + small.O[i].Gain*float64(src.at(1+i%2, x, y)))
			}
			v := [3]float64{float64(small.HO.at(0, x, y)) - small.HBackground, o, float64(small.SO.at(0, x, y)) - small.SBackground}
			for i, a := range v {
				if paletteFinite(a) {
					samples[i] = append(samples[i], math.Max(0, a))
				}
			}
		}
	}
	var amplitude [3]float64
	for i := range samples {
		sort.Float64s(samples[i])
		if len(samples[i]) > 0 {
			amplitude[i] = samples[i][(len(samples[i])-1)*95/100]
		}
	}
	p.PresetBalance = [3]float64{1, 1, .2}
	for i := 1; i < 3; i++ {
		if amplitude[i] > 1e-10 && amplitude[0] > 1e-10 {
			p.PresetBalance[i] = math.Max(.02, math.Min(2, amplitude[0]/amplitude[i]))
		}
	}
	if p.Thumbnail != nil {
		p.Thumbnail.DynamicTone = p.DynamicTone
		p.Thumbnail.PresetBalance = p.PresetBalance
	}
}

func (p *palettePrepared) preset(mode, level int, base paletteParams) paletteParams {
	level = max(0, min(9, level))
	t := float64(level) / 9
	base.Mode = max(0, min(paletteChoiceCount-1, mode))
	balance := p.PresetBalance
	if balance[0] <= 0 {
		balance = [3]float64{1, 1, .2}
	}
	base.Hydrogen = math.Round((1.3-.45*t)*1000) / 1000
	base.Oxygen = math.Round(math.Min(3, balance[1]*(.6+1.1*t))*1000) / 1000
	base.Sulfur = math.Round(math.Min(3, balance[2]*(.35+1.15*t))*1000) / 1000
	if mode == 1 || mode == 8 {
		base.Sulfur = 0
	}
	base.OxygenHue = 0
	base.Hue = 1
	return base
}
