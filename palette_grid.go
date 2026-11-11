package main

import (
	"fmt"
	"math"
)

// Remove a locally coherent 4x4 phase pattern, not the broadband high-frequency
// image. Overlapping Hann windows avoid introducing tile seams. Robust clipping
// keeps bright stars and nebular edges from setting the phase offsets.
func suppressFourPixelGrid(src []float32, w, h int) ([]float32, int) {
	out := append([]float32(nil), src...)
	residual := waveSmooth(waveSmooth(src, w, h, 1, nil), w, h, 2, nil)
	for i := range residual {
		residual[i] = src[i] - residual[i]
	}
	var window [64]float64
	for i := range window {
		v := math.Sin(math.Pi * (float64(i) + .5) / 64)
		window[i] = v * v
	}
	sample := make([]float64, 0, 600)
	active := 0
	for y0 := -32; y0 < h; y0 += 32 {
		for x0 := -32; x0 < w; x0 += 32 {
			x1, x2, y1, y2 := max(0, x0), min(w, x0+64), max(0, y0), min(h, y0+64)
			tw, th := x2-x1, y2-y1
			sample = sample[:0]
			for k := 0; k < tw*th; k += 7 {
				sample = append(sample, float64(residual[(y1+k/tw)*w+x1+k%tw]))
			}
			med := median(sample)
			for i, v := range sample {
				sample[i] = math.Abs(v - med)
			}
			sigma := 1.4826 * median(sample)
			if sigma < 1e-12 {
				continue
			}
			var sums [16]float64
			var counts [16]int
			for y := y1; y < y2; y++ {
				for x := x1; x < x2; x++ {
					v := float64(residual[y*w+x])
					if math.Abs(v-med) > 3*sigma {
						continue
					}
					phase := (y&3)*4 + (x & 3)
					sums[phase] += v
					counts[phase]++
				}
			}
			minimum := tw * th
			var average float64
			for i := range sums {
				minimum = min(minimum, counts[i])
				if counts[i] > 0 {
					sums[i] /= float64(counts[i])
				}
				average += sums[i] / 16
			}
			if minimum < 16 {
				continue
			}
			var power float64
			for i := range sums {
				sums[i] -= average
				power += sums[i] * sums[i] / 16
			}
			floor := 6.25 * sigma * sigma / float64(minimum)
			if power <= floor {
				continue
			}
			amount := 1 - floor/power
			active++
			for y := y1; y < y2; y++ {
				for x := x1; x < x2; x++ {
					correction := sums[(y&3)*4+(x&3)] * amount * window[y-y0] * window[x-x0]
					out[y*w+x] -= float32(correction)
				}
			}
		}
	}
	return out, active
}

func paletteGridPlanes(p *palettePrepared) (full, small []float32, note string) {
	r := p.Region
	w, h := r.Dx(), r.Dy()
	src := make([]float32, w*h)
	for y := 0; y < h; y++ {
		copy(src[y*w:(y+1)*w], p.HO.Pix[(y+r.Min.Y)*p.HO.W+r.Min.X:][:w])
	}
	var active int
	full, active = suppressFourPixelGrid(src, w, h)
	sw, sh := p.Thumbnail.Region.Dx(), p.Thumbnail.Region.Dy()
	small = paletteGridAverage(full, w, h, sw, sh)
	return full, small, fmt.Sprintf("Сетка 4×4: скорректировано участков %d. Проверьте звёзды и детали при 100%%.", active)
}

func paletteGridAverage(full []float32, w, h, sw, sh int) []float32 {
	stride := max((w+sw-1)/sw, (h+sh-1)/sh)
	small := make([]float32, sw*sh)
	for y := 0; y < sh; y++ {
		for x := 0; x < sw; x++ {
			var sum, count float64
			for yy := y * stride; yy < min((y+1)*stride, h); yy++ {
				for xx := x * stride; xx < min((x+1)*stride, w); xx++ {
					sum += float64(full[yy*w+xx])
					count++
				}
			}
			small[y*sw+x] = float32(sum / count)
		}
	}
	return small
}

func (u *ui) preparePaletteGrid() {
	p := &u.palette
	prepared := p.prepared
	if prepared == nil || len(prepared.GridHydrogen) > 0 || u.busy {
		return
	}
	u.busy = true
	u.status = "Измеряю и ослабляю периодический рисунок 4×4 в HO…"
	go func() {
		full, small, note := paletteGridPlanes(prepared)
		u.post(func() {
			u.busy = false
			if p.prepared != prepared {
				return
			}
			prepared.GridHydrogen, prepared.Thumbnail.GridHydrogen = full, small
			p.gridNote = note
			p.dirty = true
			u.status = note
		})
	}()
}
