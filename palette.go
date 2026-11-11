package main

import (
	"fmt"
	"image"
	"math"
	"sort"
	"strconv"
)

// All mixing is linear. RGB inputs have already been demosaiced; G and B
// are two measurements of OIII, not two separate emission lines.
type paletteParams struct {
	Mode                          int
	Hydrogen, Oxygen, Sulfur, Hue float64
	OxygenHue                     float64 // 0 cyan (G=B); 1 blue (G=0.2B)
	Auto                          bool
	BlueHO, BlueSO, SourceSO      float64
	ProtectSulfur, ProtectOxygen  bool
	ProtectForaxx                 bool
	SuppressGrid                  bool
}

type paletteCalibration struct {
	Offset, Gain, Noise float64
	Usable              bool
}

type palettePrepared struct {
	HO, SO                   *Img // SO is on the HO pixel grid
	Region                   image.Rectangle
	O                        [4]paletteCalibration // HO G/B, SO G/B, all in HO G units
	HBackground, SBackground float64
	Note                     string
	Thumbnail                *palettePrepared
	SulfurMask, OxygenMask   []float32 // ROI coordinates, independent of artistic gains
	ForaxxMask               []float32 // final HOO-to-Foraxx blend, independent of source masks' switches
	MaskedSulfur             []float32 // thumbnail: average of signal*mask, not mean(signal)*mean(mask)
	MaskedO                  [4][]float32
	DynamicTone              [2]paletteFactorTone
	PresetBalance            [3]float64 // H, O, S gains estimated from diffuse signal
	GridHydrogen             []float32  // optional corrected HO/R, in ROI coordinates
}

func paletteFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func palettePixel(m *Img, x, y int) bool {
	if x < 0 || y < 0 || x >= m.W || y >= m.H {
		return false
	}
	i, n := y*m.W+x, m.W*m.H
	var nonzero bool
	for c := 0; c < 3; c++ {
		v := float64(m.Pix[c*n+i])
		if !paletteFinite(v) {
			return false
		}
		nonzero = nonzero || v != 0
	}
	return nonzero
}

// Largest rectangle entirely inside both footprints, also removing zero-filled
// registration borders. Histogram algorithm: O(width*height), O(width) memory.
func paletteCommonRegion(a, b *Img) image.Rectangle {
	w, h := min(a.W, b.W), min(a.H, b.H)
	heights := make([]int, w+1)
	stack := make([]int, 0, w+1)
	best := image.Rectangle{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if palettePixel(a, x, y) && palettePixel(b, x, y) {
				heights[x]++
			} else {
				heights[x] = 0
			}
		}
		stack = stack[:0]
		for x := 0; x <= w; x++ {
			for len(stack) > 0 && heights[stack[len(stack)-1]] > heights[x] {
				i := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				left := 0
				if len(stack) > 0 {
					left = stack[len(stack)-1] + 1
				}
				if heights[i]*(x-left) > best.Dx()*best.Dy() {
					best = image.Rect(left, y+1-heights[i], x, y+1)
				}
			}
			stack = append(stack, x)
		}
	}
	return best
}

func paletteRegistration(ho, so *Img) (Aff, string, error) {
	green := func(m *Img) *Img { n := m.W * m.H; return &Img{W: m.W, H: m.H, C: 1, Pix: m.Pix[n : 2*n]} }
	sa, sb := detectStars(green(ho)), detectStars(green(so))
	t, n, ok := findOverlap(sa, sb, ho, so)
	if !ok {
		pairs := matchStars(sa, sb, ho.W, ho.H, so.W, so.H, identity, 200)
		if len(pairs) < 30 {
			return Aff{}, "", fmt.Errorf("не удалось совместить HO и SO по звёздам; проверьте общее поле или используйте уже совмещённые файлы")
		}
		src, dst := make([][2]float64, len(pairs)), make([][2]float64, len(pairs))
		for i, p := range pairs {
			src[i], dst[i] = p.B, p.A
		}
		t, n, ok = fitAffine(src, dst, float64(so.W), float64(so.H))
		if !ok {
			return Aff{}, "", fmt.Errorf("недостаточно надёжных звёзд для совмещения")
		}
	}
	pairs := matchStars(sa, sb, ho.W, ho.H, so.W, so.H, t, 8)
	if len(pairs) < 20 {
		return Aff{}, "", fmt.Errorf("совмещение не подтверждено: только %d звёзд", len(pairs))
	}
	src, dst := make([][2]float64, len(pairs)), make([][2]float64, len(pairs))
	for i, p := range pairs {
		src[i], dst[i] = p.A, p.B
	}
	mapToSO, k, valid := fitAffine(src, dst, float64(ho.W), float64(ho.H))
	if !valid {
		mapToSO = t.inv()
		k = n
	}
	res := make([]float64, len(pairs))
	for i, p := range pairs {
		x, y := mapToSO.apply(p.A[0], p.A[1])
		res[i] = math.Hypot(x-p.B[0], y-p.B[1])
	}
	rms := median(res)
	if rms > 1.5 {
		return Aff{}, "", fmt.Errorf("неточное совмещение: медианная ошибка %.2f px", rms)
	}
	return mapToSO, fmt.Sprintf("По звёздам: %d, ошибка %.2f px", k, rms), nil
}

func paletteAlignSO(ho, so *Img, mapping Aff) *Img {
	if ho.W == so.W && ho.H == so.H && mapping == identity {
		return so
	}
	out := newImg(ho.W, ho.H, 3)
	// Integer shifts copy exactly: no smoothing of already registered data.
	integer := mapping[0] == 1 && mapping[1] == 0 && mapping[3] == 0 && mapping[4] == 1 && mapping[2] == math.Trunc(mapping[2]) && mapping[5] == math.Trunc(mapping[5])
	parallel(ho.H, func(lo, hi int) {
		for y := lo; y < hi; y++ {
			for x := 0; x < ho.W; x++ {
				u, v := mapping.apply(float64(x), float64(y))
				if !inside(so, u, v) {
					continue
				}
				if integer {
					for c := 0; c < 3; c++ {
						out.Pix[c*ho.W*ho.H+y*ho.W+x] = so.at(c, int(u), int(v))
					}
				} else {
					ix, iy := int(math.Floor(u)), int(math.Floor(v))
					if !palettePixel(so, ix, iy) || !palettePixel(so, min(ix+1, so.W-1), iy) || !palettePixel(so, ix, min(iy+1, so.H-1)) || !palettePixel(so, min(ix+1, so.W-1), min(iy+1, so.H-1)) {
						continue
					}
					for c := 0; c < 3; c++ {
						out.Pix[c*ho.W*ho.H+y*ho.W+x] = bilinear(so, c, u, v)
					}
				}
			}
		}
	})
	return out
}

// A diagonal difference cancels a local plane. Spacing 3 reduces the bias from
// neighbouring-pixel correlations in demosaiced/drizzled images. This is a
// robust relative noise estimate, not a camera read-noise measurement.
func palettePlaneStats(m *Img, c int, r image.Rectangle) (background, noise float64) {
	step := max(1, int(math.Sqrt(float64(r.Dx()*r.Dy())/40000)))
	var values, diffs []float64
	for y := r.Min.Y + 3; y < r.Max.Y-3; y += step {
		for x := r.Min.X + 3; x < r.Max.X-3; x += step {
			v := float64(m.at(c, x, y))
			if !paletteFinite(v) || v == 0 || v >= fitHigh {
				continue
			}
			values = append(values, v)
			a, b, d := float64(m.at(c, x+3, y)), float64(m.at(c, x, y+3)), float64(m.at(c, x+3, y+3))
			if paletteFinite(a) && paletteFinite(b) && paletteFinite(d) && a != 0 && b != 0 && d != 0 {
				diffs = append(diffs, (v-a-b+d)/2)
			}
		}
	}
	if len(values) == 0 {
		return 0, 0
	}
	sort.Float64s(values)
	background = values[len(values)/20] // conservative sky pedestal, not the nebula median
	med := median(diffs)
	for i, v := range diffs {
		diffs[i] = math.Abs(v - med)
	}
	noise = 1.4826 * median(diffs)
	return
}

// Fit patch means instead of noisy single pixels to avoid attenuation of the
// gain of a weak blue channel. Low correlation disables that measurement;
// blindly amplifying a channel with no detected OIII would amplify only noise.
func paletteNormalize(ref, src *Img, c int, r image.Rectangle, refBG float64) paletteCalibration {
	_, noise := palettePlaneStats(src, c, r)
	step := max(7, int(math.Sqrt(float64(r.Dx()*r.Dy())/3000)))
	var xs, ys []float64
	for y := r.Min.Y + 3; y < r.Max.Y-3; y += step {
		for x := r.Min.X + 3; x < r.Max.X-3; x += step {
			var sx, sy float64
			ok := true
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					a, b := float64(src.at(c, x+dx, y+dy)), float64(ref.at(1, x+dx, y+dy))
					if !paletteFinite(a) || !paletteFinite(b) || a == 0 || b == 0 || a >= fitHigh || b >= fitHigh {
						ok = false
					}
					sx += a
					sy += b
				}
			}
			if ok {
				xs = append(xs, sx/25)
				ys = append(ys, sy/25)
			}
		}
	}
	if len(xs) < 20 {
		return paletteCalibration{}
	}
	var mx, my float64
	for i := range xs {
		mx += xs[i]
		my += ys[i]
	}
	mx /= float64(len(xs))
	my /= float64(len(xs))
	var xx, yy, xy float64
	for i := range xs {
		x, y := xs[i]-mx, ys[i]-my
		xx += x * x
		yy += y * y
		xy += x * y
	}
	if xx <= 0 || yy <= 0 || xy/math.Sqrt(xx*yy) < .6 {
		return paletteCalibration{}
	}
	a, b := fitLine(xs, ys)
	if !paletteFinite(a) || !paletteFinite(b) || b < .02 || b > 50 {
		return paletteCalibration{}
	}
	return paletteCalibration{Offset: a - refBG, Gain: b, Noise: math.Max(noise*b, 1e-9), Usable: true}
}

func preparePalette(ho, so *Img, auto, center bool) (*palettePrepared, error) {
	if ho == nil || so == nil || ho.C != 3 || so.C != 3 {
		return nil, fmt.Errorf("нужны два цветных RGB-изображения: HO и SO")
	}
	if min(ho.W, ho.H, so.W, so.H) < 16 {
		return nil, fmt.Errorf("изображения слишком малы для оценки сигнала и шума")
	}
	mapping, note := identity, "Уже совмещены: левый верхний угол"
	if auto {
		var err error
		mapping, note, err = paletteRegistration(ho, so)
		if err != nil {
			return nil, err
		}
	} else if center {
		mapping[2], mapping[5] = float64((so.W-ho.W)/2), float64((so.H-ho.H)/2)
		note = "Уже совмещены: по центру"
	}
	aligned := paletteAlignSO(ho, so, mapping)
	r := paletteCommonRegion(ho, aligned)
	if r.Dx() < 16 || r.Dy() < 16 {
		return nil, fmt.Errorf("общая область слишком мала или отсутствует")
	}
	p := &palettePrepared{HO: ho, SO: aligned, Region: r}
	p.HBackground, _ = palettePlaneStats(ho, 0, r)
	p.SBackground, _ = palettePlaneStats(aligned, 0, r)
	bg, noise := palettePlaneStats(ho, 1, r)
	p.O[0] = paletteCalibration{Offset: -bg, Gain: 1, Noise: math.Max(noise, 1e-9), Usable: true}
	p.O[1] = paletteNormalize(ho, ho, 2, r, bg)
	p.O[2] = paletteNormalize(ho, aligned, 1, r, bg)
	p.O[3] = paletteNormalize(ho, aligned, 2, r, bg)
	p.Note = fmt.Sprintf("HO %d×%d, SO %d×%d → %d×%d. %s.", ho.W, ho.H, so.W, so.H, r.Dx(), r.Dy(), note)
	if r != image.Rect(0, 0, ho.W, ho.H) || ho.W != so.W || ho.H != so.H {
		p.Note += " Обрезано до общей области; края за её пределами удалены."
	}
	for i, name := range []string{"HO G", "HO B", "SO G", "SO B"} {
		if !p.O[i].Usable {
			p.Note += " " + name + ": недостаточно общего сигнала OIII, канал исключён."
		}
	}
	p.buildMasks()
	p.Thumbnail = p.thumbnail(1200)
	p.preparePresetBalance()
	return p, nil
}

func (p *palettePrepared) thumbnail(maxSide int) *palettePrepared {
	r := p.Region
	stride := max(1, (max(r.Dx(), r.Dy())+maxSide-1)/maxSide)
	w, h := (r.Dx()+stride-1)/stride, (r.Dy()+stride-1)/stride
	small := *p
	small.Thumbnail = nil
	small.Region = image.Rect(0, 0, w, h)
	images := [2]*Img{newImg(w, h, 3), newImg(w, h, 3)}
	small.SulfurMask, small.OxygenMask = make([]float32, w*h), make([]float32, w*h)
	small.ForaxxMask = make([]float32, w*h)
	small.MaskedSulfur = make([]float32, w*h)
	for i := range small.MaskedO {
		small.MaskedO[i] = make([]float32, w*h)
	}
	for k, src := range [2]*Img{p.HO, p.SO} {
		dst := images[k]
		parallel(h, func(lo, hi int) {
			for y := lo; y < hi; y++ {
				for x := 0; x < w; x++ {
					var sum [3]float64
					var maskSum [3]float64
					var maskedSulfur float64
					var maskedO [2]float64
					var count float64
					for sy := r.Min.Y + y*stride; sy < min(r.Min.Y+(y+1)*stride, r.Max.Y); sy++ {
						for sx := r.Min.X + x*stride; sx < min(r.Min.X+(x+1)*stride, r.Max.X); sx++ {
							for c := 0; c < 3; c++ {
								sum[c] += float64(src.at(c, sx, sy))
							}
							mi := (sy-r.Min.Y)*r.Dx() + sx - r.Min.X
							sm, om := float64(p.SulfurMask[mi]), float64(p.OxygenMask[mi])
							maskSum[0] += sm
							maskSum[1] += om
							maskSum[2] += float64(p.ForaxxMask[mi])
							if k == 1 {
								maskedSulfur += (float64(src.at(0, sx, sy)) - p.SBackground) * sm
							}
							for j := 0; j < 2; j++ {
								c := p.O[2*k+j]
								maskedO[j] += (c.Offset + c.Gain*float64(src.at(1+j, sx, sy))) * om
							}
							count++
						}
					}
					for c := 0; c < 3; c++ {
						dst.Pix[c*w*h+y*w+x] = float32(sum[c] / count)
					}
					if k == 1 {
						small.MaskedSulfur[y*w+x] = float32(maskedSulfur / count)
					}
					for j := 0; j < 2; j++ {
						small.MaskedO[2*k+j][y*w+x] = float32(maskedO[j] / count)
					}
					if k == 0 {
						small.SulfurMask[y*w+x] = float32(maskSum[0] / count)
						small.OxygenMask[y*w+x] = float32(maskSum[1] / count)
						small.ForaxxMask[y*w+x] = float32(maskSum[2] / count)
					}
				}
			}
		})
	}
	small.HO, small.SO = images[0], images[1]
	if len(p.GridHydrogen) > 0 {
		small.GridHydrogen = paletteGridAverage(p.GridHydrogen, r.Dx(), r.Dy(), w, h)
	}
	return &small
}

func (p *palettePrepared) autoFractions() (blueHO, blueSO, sourceSO float64) {
	var iv [4]float64
	for i, c := range p.O {
		if c.Usable {
			iv[i] = 1 / math.Pow(math.Max(c.Noise, 1e-9), 2)
		}
	}
	a, b := iv[0]+iv[1], iv[2]+iv[3]
	if a > 0 {
		blueHO = iv[1] / a
	}
	if b > 0 {
		blueSO = iv[3] / b
	}
	if a+b > 0 {
		sourceSO = b / (a + b)
	}
	return
}

func (p *palettePrepared) weights(s paletteParams) [4]float64 {
	b1, b2, so := s.BlueHO, s.BlueSO, s.SourceSO
	if s.Auto {
		b1, b2, so = p.autoFractions()
	}
	w := [4]float64{(1 - so) * (1 - b1), (1 - so) * b1, so * (1 - b2), so * b2}
	var total float64
	for i := range w {
		if !p.O[i].Usable {
			w[i] = 0
		}
		total += w[i]
	}
	if total == 0 {
		w[0] = 1
		return w
	}
	for i := range w {
		w[i] /= total
	}
	return w
}

// Average source footprints for an interactive preview; calibrations and noise
// weights always come from full-resolution inputs, never their thumbnails.
func (p *palettePrepared) compose(s paletteParams, maxSide int) *Img {
	if maxSide > 0 && p.Thumbnail != nil {
		return p.Thumbnail.compose(s, 0)
	}
	r := p.Region
	stride := 1
	if maxSide > 0 {
		stride = max(1, (max(r.Dx(), r.Dy())+maxSide-1)/maxSide)
	}
	w, h := (r.Dx()+stride-1)/stride, (r.Dy()+stride-1)/stride
	out := newImg(w, h, 3)
	weights := p.weights(s)
	oxygenGreenFactor := 1 - .8*math.Max(0, math.Min(1, s.OxygenHue))
	// HO alone is the baseline outside the oxygen mask. Normalize the HO
	// fractions independently of the SO fraction; artistic O gain acts in-mask.
	baseWeights := p.weights(paletteParams{Auto: s.Auto, BlueHO: s.BlueHO, BlueSO: s.BlueSO})
	if s.Auto {
		b, _, _ := p.autoFractions()
		baseWeights = [4]float64{1 - b, b, 0, 0}
	}
	parallel(h, func(lo, hi int) {
		for y := lo; y < hi; y++ {
			for x := 0; x < w; x++ {
				var hydrogen, sulfur, oxygen, oxygenGreen, baselineOxygen, foraxxMask, count float64
				for sy := r.Min.Y + y*stride; sy < min(r.Min.Y+(y+1)*stride, r.Max.Y); sy++ {
					for sx := r.Min.X + x*stride; sx < min(r.Min.X+(x+1)*stride, r.Max.X); sx++ {
						mi := (sy-r.Min.Y)*r.Dx() + sx - r.Min.X
						h := float64(p.HO.at(0, sx, sy))
						if s.SuppressGrid && len(p.GridHydrogen) > 0 {
							h = float64(p.GridHydrogen[mi])
						}
						hydrogen += h - p.HBackground
						if len(p.ForaxxMask) > 0 {
							foraxxMask += float64(p.ForaxxMask[mi])
						}
						ss := float64(p.SO.at(0, sx, sy)) - p.SBackground
						if s.ProtectSulfur && len(p.SulfurMask) > 0 {
							if p.MaskedSulfur != nil {
								ss = float64(p.MaskedSulfur[mi])
							} else {
								ss *= float64(p.SulfurMask[mi])
							}
						}
						sulfur += ss
						for i, src := range [4]*Img{p.HO, p.HO, p.SO, p.SO} {
							o := p.O[i].Offset + p.O[i].Gain*float64(src.at(1+i%2, sx, sy))
							baselineOxygen += baseWeights[i] * o
							if s.ProtectOxygen && len(p.OxygenMask) > 0 {
								masked := o * float64(p.OxygenMask[mi])
								if p.MaskedO[i] != nil {
									masked = float64(p.MaskedO[i][mi])
								}
								oxygen += baseWeights[i]*(o-masked) + s.Oxygen*weights[i]*masked
								oxygenGreen += baseWeights[i]*(o-masked) + oxygenGreenFactor*s.Oxygen*weights[i]*masked
							} else {
								oxygen += s.Oxygen * weights[i] * o
								oxygenGreen += oxygenGreenFactor * s.Oxygen * weights[i] * o
							}
						}
						count++
					}
				}
				hydrogen *= s.Hydrogen / count
				sulfur *= s.Sulfur / count
				oxygen /= count
				oxygenGreen /= count
				i := y*w + x
				rgb := p.mapPalette(s, hydrogen, oxygen, oxygenGreen, sulfur)
				if s.ProtectForaxx && (s.Mode == 8 || s.Mode == 9) && len(p.ForaxxMask) > 0 {
					base := [3]float64{hydrogen, baselineOxygen / count, baselineOxygen / count}
					mask := math.Max(0, math.Min(1, foraxxMask/count))
					for c := range rgb {
						rgb[c] = base[c] + mask*(rgb[c]-base[c])
					}
				}
				out.Pix[i], out.Pix[w*h+i], out.Pix[2*w*h+i] = float32(rgb[0]), float32(rgb[1]), float32(rgb[2])
			}
		}
	})
	// Copy WCS from the HO reference and adjust its origin for the crop. Do not
	// retain single-filter or Bayer tags on a synthetic colour composite.
	out.BottomUp = p.HO.BottomUp
	for k, v := range p.HO.Hdr {
		out.Hdr[k] = v
	}
	delete(out.Hdr, "BAYERPAT")
	delete(out.Hdr, "WBPPCROP")
	delete(out.Hdr, "AMSHIFT") // an input export offset does not describe this new mix
	out.Hdr["FILTER"] = "HOO+SII"
	if s.Mode > 0 {
		out.Hdr["FILTER"] = paletteName(s.Mode)
	}
	if stride == 1 {
		for _, item := range []struct {
			key   string
			delta int
		}{{"CRPIX1", r.Min.X}, {"CRPIX2", r.Min.Y}} {
			delta := item.delta
			if item.key == "CRPIX2" && out.BottomUp {
				delta = p.HO.H - r.Max.Y
			}
			if v, err := strconv.ParseFloat(out.Hdr[item.key], 64); err == nil {
				out.Hdr[item.key] = strconv.FormatFloat(v-float64(delta), 'g', 16, 64)
			}
		}
	} else { // The preview is display-only; its original WCS would be misleading.
		out.Hdr = map[string]string{}
	}
	out.Operations = []string{fmt.Sprintf("HO+SO → %s: H=%.3f O=%.3f S=%.3f sulfurHue=%.3f oxygenHue=%.3f; masks S=%v O=%v finalForaxx=%v grid4=%v; OIII weights HO G/B=%.4f/%.4f SO G/B=%.4f/%.4f; %s", paletteName(s.Mode), s.Hydrogen, s.Oxygen, s.Sulfur, s.Hue, s.OxygenHue, s.ProtectSulfur, s.ProtectOxygen, s.ProtectForaxx, s.SuppressGrid, weights[0], weights[1], weights[2], weights[3], p.Note)}
	out.Hdr["AMPALET"] = "1"
	return out
}
