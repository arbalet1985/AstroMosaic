package main

import (
	"image"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
)

const waveLevels = 4

type waveParams struct {
	Strength float64
	Gain     [waveLevels]float64
}
type waveModel struct{ Sigma [][waveLevels]float64 }

func reflectWave(x, n int) int {
	if n <= 1 {
		return 0
	}
	for x < 0 || x >= n {
		if x < 0 {
			x = -x - 1
		}
		if x >= n {
			x = 2*n - x - 1
		}
	}
	return x
}

// Undecimated B3-spline à trous transform. A fixed validity mask prevents
// missing pixels from leaking zeros into neighbours. Negative samples are valid.
func waveSmooth(src []float32, w, h, step int, valid []bool) []float32 {
	weights := [5]float64{1, 4, 6, 4, 1}
	tmp, out := make([]float32, len(src)), make([]float32, len(src))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sum, norm := 0., 0.
			for j, k := range weights {
				i := y*w + reflectWave(x+(j-2)*step, w)
				if valid == nil || valid[i] {
					sum += k * float64(src[i])
					norm += k
				}
			}
			if norm > 0 {
				tmp[y*w+x] = float32(sum / norm)
			}
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sum, norm := 0., 0.
			for j, k := range weights {
				i := reflectWave(y+(j-2)*step, h)*w + x
				if valid == nil || valid[i] {
					sum += k * float64(tmp[i])
					norm += k
				}
			}
			if norm > 0 {
				out[y*w+x] = float32(sum / norm)
			}
		}
	}
	return out
}

func waveCoefficients(src []float32, w, h, levels int, valid []bool) ([][]float32, []float32) {
	current := src
	layers := make([][]float32, levels)
	for level := 0; level < levels; level++ {
		next := waveSmooth(current, w, h, 1<<level, valid)
		layers[level] = make([]float32, len(src))
		for i := range src {
			layers[level][i] = current[i] - next[i]
		}
		current = next
	}
	return layers, current
}

func estimateWaveModel(m *Img) waveModel {
	model := waveModel{Sigma: make([][waveLevels]float64, m.C)}
	for c := 0; c < m.C; c++ {
		var samples [waveLevels][]float64
		for gy := 0; gy < 4; gy++ {
			for gx := 0; gx < 4; gx++ {
				x, y := (2*gx+1)*m.W/8, (2*gy+1)*m.H/8
				region := image.Rect(x-64, y-64, x+64, y+64).Intersect(image.Rect(0, 0, m.W, m.H))
				part := cropPixels(imageChannelView(m, c), region)
				valid := waveValid(part.Pix)
				layers, _ := waveCoefficients(part.Pix, part.W, part.H, waveLevels, valid)
				for level, d := range layers {
					for i, v := range d {
						if valid[i] && i%3 == 0 {
							samples[level] = append(samples[level], float64(v))
						}
					}
				}
			}
		}
		for level, values := range samples {
			centre := median(append([]float64(nil), values...))
			for i := range values {
				values[i] = math.Abs(values[i] - centre)
			}
			model.Sigma[c][level] = math.Max(1e-9, median(values)/.67448975)
		}
	}
	return model
}
func waveValid(src []float32) []bool {
	v := make([]bool, len(src))
	for i, x := range src {
		v[i] = x != 0 && finitePoint(float64(x), 0)
	}
	return v
}

func filterWavePlane(src []float32, w, h int, sigma []float64, p waveParams, valid []bool) []float32 {
	layers, coarse := waveCoefficients(src, w, h, len(sigma), valid)
	out := append([]float32(nil), coarse...)
	for level, d := range layers {
		threshold := p.Strength * 2.5 * sigma[level] * []float64{1, .8, .5, .25}[level]
		for i, v := range d {
			value := float64(v)
			// Smooth shrinkage; significant coefficients survive, weak ones shrink.
			keep := math.Max(0, 1-threshold*threshold/(value*value+1e-30))
			confidence := math.Max(0, 1-9*sigma[level]*sigma[level]/(value*value+1e-30))
			out[i] += float32(value * keep * (1 + (p.Gain[level]-1)*confidence))
		}
	}
	for i := range out {
		if valid != nil && !valid[i] {
			out[i] = src[i]
		}
	}
	return out
}

// Fixed source-aligned cores and a 30-pixel context make full processing and
// arbitrary crop previews identical with the same noise model.
func filterWaveRegion(m *Img, model waveModel, p waveParams, region image.Rectangle, progress func(int, int)) *Img {
	bounds := image.Rect(0, 0, m.W, m.H)
	region = region.Intersect(bounds)
	out := cropPixels(m, region)
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	identity := p.Strength == 0
	for _, gain := range p.Gain {
		identity = identity && gain == 1
	}
	if identity {
		return out
	}
	const core, halo = 384, 30
	total := ((m.W + core - 1) / core) * ((m.H + core - 1) / core) * m.C
	var completed atomic.Int64
	jobs := make(chan image.Rectangle)
	var workers sync.WaitGroup
	for worker := 0; worker < min(4, runtime.GOMAXPROCS(0)); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				part := job
				x, y := job.Min.X/core*core, job.Min.Y/core*core
				context := image.Rect(x-halo, y-halo, x+core+halo, y+core+halo).Intersect(bounds)
				original := cropPixels(m, context)
				protect := restorationProtection(original, 2)
				for c := 0; c < m.C; c++ {
					plane := original.Pix[c*original.W*original.H : (c+1)*original.W*original.H]
					valid := waveValid(plane)
					filtered := filterWavePlane(plane, original.W, original.H, model.Sigma[c][:], p, valid)
					for Y := part.Min.Y; Y < part.Max.Y; Y++ {
						for X := part.Min.X; X < part.Max.X; X++ {
							i := (Y-context.Min.Y)*original.W + X - context.Min.X
							if !protect[i] {
								out.Pix[c*out.W*out.H+(Y-region.Min.Y)*out.W+X-region.Min.X] = filtered[i]
							}
						}
					}
					done := int(completed.Add(1))
					if progress != nil {
						progress(done, total)
					}
				}
			}
		}()
	}
	for y := region.Min.Y / core * core; y < region.Max.Y; y += core {
		for x := region.Min.X / core * core; x < region.Max.X; x += core {
			jobs <- image.Rect(x, y, x+core, y+core).Intersect(region)
		}
	}
	close(jobs)
	workers.Wait()

	return out
}

func waveScalePower(m *Img) [waveLevels]float64 {
	var power [waveLevels]float64
	var counts [waveLevels]int
	for gy := 0; gy < 4; gy++ {
		for gx := 0; gx < 4; gx++ {
			x, y := (2*gx+1)*m.W/8, (2*gy+1)*m.H/8
			r := image.Rect(x-48, y-48, x+48, y+48).Intersect(image.Rect(0, 0, m.W, m.H))
			for c := 0; c < m.C; c++ {
				part := cropPixels(imageChannelView(m, c), r)
				valid := waveValid(part.Pix)
				layers, _ := waveCoefficients(part.Pix, part.W, part.H, waveLevels, valid)
				for l, d := range layers {
					for i, v := range d {
						if valid[i] {
							power[l] += float64(v) * float64(v)
							counts[l]++
						}
					}
				}
			}
		}
	}
	for l := range power {
		if counts[l] > 0 {
			power[l] = math.Sqrt(power[l] / float64(counts[l]))
		}
	}
	return power
}
