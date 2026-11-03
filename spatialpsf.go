package main

import (
	"fmt"
	"image"
	"math"
)

type psfCovariance struct{ xx, xy, yy float64 }

func (c psfCovariance) axes() (major, minor, angle float64) {
	d := math.Hypot(c.xx-c.yy, 2*c.xy)
	return math.Sqrt(math.Max(0, (c.xx+c.yy+d)/2)), math.Sqrt(math.Max(0, (c.xx+c.yy-d)/2)), .5 * math.Atan2(2*c.xy, c.xx-c.yy)
}

// The isotropic component follows the existing fraction² rule. The additional
// directional blur is removed more strongly, by fraction, to reduce elongation.
func (c psfCovariance) removal(fraction float64) psfCovariance {
	_, minor, _ := c.axes()
	s := fraction * (1 - fraction) * minor * minor
	return psfCovariance{fraction*c.xx - s, fraction * c.xy, fraction*c.yy - s}
}

type psfNode struct {
	cov       psfCovariance
	n         int
	valid     bool
	empirical []float64
	asymmetry float64
}
type spatialPSF struct {
	w, h    int
	nodes   [16]psfNode
	coma    bool
	profile bool
	radius  int
}

func psfAsymmetry(m *Img, c int, p starPSF) float64 {
	var total, difference float64
	for dy := -5; dy <= 5; dy++ {
		for dx := -5; dx <= 5; dx++ {
			if dx*dx+dy*dy > 25 {
				continue
			}
			a := math.Max(0, float64(bilinear(m, c, p.x+float64(dx), p.y+float64(dy)))-p.sky)
			b := math.Max(0, float64(bilinear(m, c, p.x-float64(dx), p.y-float64(dy)))-p.sky)
			total, difference = total+a+b, difference+math.Abs(a-b)
		}
	}
	return difference / math.Max(total, 1e-12)
}

func estimateSpatialRestoration(m *Img) (restorationModel, error) {
	if m.C != 1 && m.C != 3 {
		return restorationModel{}, fmt.Errorf("локальная PSF поддерживает ч/б и RGB")
	}
	stars := detectStars(m)
	model := restorationModel{stars: stars[:min(len(stars), 4000)], channels: make([]restorationChannel, m.C)}
	for c := 0; c < m.C; c++ {
		var widths []float64
		for _, s := range model.stars {
			p, ok := fitStarPSF(m, s, c)
			if ok && p.ellipticity <= .5 {
				widths = append(widths, p.fwhm)
			}
		}
		if len(widths) < 30 {
			return model, fmt.Errorf("%s: нужно ≥30 измеримых звёзд", channelName(c, m.C))
		}
		f := median(widths)
		if f < 2 || f > 10 {
			return model, fmt.Errorf("%s: FWHM вне диапазона 2…10 px", channelName(c, m.C))
		}
		n := m.W * m.H
		sky, noise, offset := restorationBackground(m.Pix[c*n:(c+1)*n], m.W)
		model.channels[c] = restorationChannel{f, sky, noise, offset}
	}
	plane := m
	if m.C == 3 {
		plane = restorationLuminance(m)
		sky, noise, offset := restorationBackground(plane.Pix, plane.W)
		model.luminance = &restorationChannel{model.channels[1].fwhm, sky, noise, offset}
	}
	var cells [16][]psfCovariance
	for _, s := range model.stars {
		p, ok := fitStarPSF(plane, s, 0)
		if !ok || p.ellipticity > .5 || psfAsymmetry(plane, 0, p) > .2 {
			continue
		}
		major, minor, _ := p.covariance.axes()
		if minor*gaussianFWHM < 1.5 || major*gaussianFWHM > 10 {
			continue
		}
		i := diagnosticBin(s.x, s.y, m)
		cells[i] = append(cells[i], p.covariance)
	}
	grid := &spatialPSF{w: m.W, h: m.H}
	rows, cols := [4]bool{}, [4]bool{}
	valid := 0
	for i, cell := range cells {
		grid.nodes[i].n = len(cell)
		if len(cell) < 5 {
			continue
		}
		var xx, xy, yy, widths []float64
		for _, c := range cell {
			xx, xy, yy = append(xx, c.xx), append(xy, c.xy), append(yy, c.yy)
			a, b, _ := c.axes()
			widths = append(widths, math.Sqrt(a*b))
		}
		width := median(widths)
		for j := range widths {
			widths[j] = math.Abs(widths[j] - width)
		}
		if 1.4826*median(widths) > .2*width {
			continue
		}
		cov := psfCovariance{median(xx), median(xy), median(yy)}
		if cov.xx*cov.yy-cov.xy*cov.xy <= 0 {
			continue
		}
		grid.nodes[i].cov, grid.nodes[i].valid = cov, true
		rows[i/4], cols[i%4] = true, true
		valid++
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
		return model, fmt.Errorf("локальная PSF: нужно ≥4 ячеек (≥5 симметричных звёзд в каждой), минимум 2 ряда и 2 столбца")
	}
	model.spatial = grid
	return model, nil
}

func (g *spatialPSF) covariance(x, y float64) (psfCovariance, bool) {
	cellX, cellY := min(3, max(0, int(x*4/float64(g.w)))), min(3, max(0, int(y*4/float64(g.h))))
	if !g.nodes[cellY*4+cellX].valid {
		return psfCovariance{}, false
	}
	u, v := math.Max(0, math.Min(3, x*4/float64(g.w)-.5)), math.Max(0, math.Min(3, y*4/float64(g.h)-.5))
	ix, iy := int(u), int(v)
	var cov psfCovariance
	var sum float64
	for dy := 0; dy < 2; dy++ {
		for dx := 0; dx < 2; dx++ {
			n := g.nodes[min(3, iy+dy)*4+min(3, ix+dx)]
			wx, wy := 1-(u-float64(ix)), 1-(v-float64(iy))
			if dx == 1 {
				wx = 1 - wx
			}
			if dy == 1 {
				wy = 1 - wy
			}
			w := wx * wy
			if !n.valid {
				continue
			}
			cov.xx, cov.xy, cov.yy, sum = cov.xx+w*n.cov.xx, cov.xy+w*n.cov.xy, cov.yy+w*n.cov.yy, sum+w
		}
	}
	return psfCovariance{cov.xx / sum, cov.xy / sum, cov.yy / sum}, sum > 0
}

// Measured cells taper to the untouched source at an unmeasured neighbour.
func (g *spatialPSF) confidence(x, y float64) float64 {
	cx, cy := min(3, max(0, int(x*4/float64(g.w)))), min(3, max(0, int(y*4/float64(g.h))))
	if !g.nodes[cy*4+cx].valid {
		return 0
	}
	w, h := float64(g.w)/4, float64(g.h)/4
	x, y = x-float64(cx)*w, y-float64(cy)*h
	f := 1.0
	if cx > 0 && !g.nodes[cy*4+cx-1].valid {
		f = math.Min(f, x/(.25*w))
	}
	if cx < 3 && !g.nodes[cy*4+cx+1].valid {
		f = math.Min(f, (w-x)/(.25*w))
	}
	if cy > 0 && !g.nodes[(cy-1)*4+cx].valid {
		f = math.Min(f, y/(.25*h))
	}
	if cy < 3 && !g.nodes[(cy+1)*4+cx].valid {
		f = math.Min(f, (h-y)/(.25*h))
	}
	return math.Max(0, math.Min(1, f))
}

func (g *spatialPSF) summary() string {
	n := 0
	for _, node := range g.nodes {
		if node.valid {
			n++
		}
	}
	name := "Локальная эллиптическая PSF"
	if g.coma {
		name = "Локальная асимметричная PSF (кома)"
	}
	if g.profile {
		name = "Локальная эмпирическая PSF (крылья/дефокус)"
	}
	return fmt.Sprintf("%s: %d/16 ячеек; вне измеренного покрытия сохраняется исходник", name, n)
}

type psfTap struct {
	dx, dy int
	weight float64
}
type ellipticalKernel struct {
	taps   []psfTap
	rx, ry int
}

func makeEllipticalKernel(c psfCovariance) ellipticalKernel {
	k := ellipticalKernel{rx: max(1, int(math.Ceil(4*math.Sqrt(c.xx)))), ry: max(1, int(math.Ceil(4*math.Sqrt(c.yy))))}
	det := c.xx*c.yy - c.xy*c.xy
	var sum float64
	for y := -k.ry; y <= k.ry; y++ {
		for x := -k.rx; x <= k.rx; x++ {
			q := (c.yy*float64(x*x) - 2*c.xy*float64(x*y) + c.xx*float64(y*y)) / det
			if q > 16 {
				continue
			}
			v := math.Exp(-.5 * q)
			k.taps = append(k.taps, psfTap{x, y, v})
			sum += v
		}
	}
	for i := range k.taps {
		k.taps[i].weight /= sum
	}
	return k
}

// Zero extension makes this centrosymmetric kernel exactly self-adjoint, also
// at boundaries. Missing support is handled by the RL operator's normalisation.
func (k ellipticalKernel) convolve(src []float32, w, h int) []float32 {
	out := make([]float32, len(src))
	offsets := make([]int, len(k.taps))
	for i, t := range k.taps {
		offsets[i] = t.dy*w + t.dx
	}
	parallel(h, func(lo, hi int) {
		for y := lo; y < hi; y++ {
			for x := 0; x < w; x++ {
				var sum float64
				if x >= k.rx && x < w-k.rx && y >= k.ry && y < h-k.ry {
					for i, t := range k.taps {
						sum += t.weight * float64(src[y*w+x+offsets[i]])
					}
				} else {
					for _, t := range k.taps {
						xx, yy := x+t.dx, y+t.dy
						if xx >= 0 && xx < w && yy >= 0 && yy < h {
							sum += t.weight * float64(src[yy*w+xx])
						}
					}
				}
				out[y*w+x] = float32(sum)
			}
		}
	})
	return out
}

// A fixed global patch lattice is shared by the preview and full operation.
// Each patch has sufficient RL context; raised-cosine overlap weights blend it.
func restoreSpatialRegion(m *Img, model restorationModel, p restorationParams, region image.Rectangle, progress func(int, int)) *Img {
	if model.spatial != nil && model.spatial.coma {
		p.skipBalance = true
	}
	if len(model.colorModels) > 0 {
		return restoreColorRegion(m, model, p, region, progress)
	}
	const half, stride = 256, 384
	bounds := image.Rect(0, 0, m.W, m.H)
	region = region.Intersect(bounds)
	out := cropPixels(m, region)
	sum := make([]float64, out.W*out.H)
	weights := make([]float64, out.W*out.H)
	halo := 2 * restorationRadius(model, p) * (p.iterations + 2)
	patchNumber := 0
	for cy := 0; cy-half < m.H; cy += stride {
		for cx := 0; cx-half < m.W; cx += stride {
			core := image.Rect(cx-half, cy-half, cx+half, cy+half).Intersect(bounds)
			part := core.Intersect(region)
			if part.Empty() {
				continue
			}
			patchNumber++
			if progress != nil {
				progress(-2, patchNumber)
			}
			cov, valid := model.spatial.covariance(float64(min(cx, m.W-1)), float64(min(cy, m.H-1)))
			var trial *Img
			context := core.Inset(-halo).Intersect(bounds)
			if valid {
				patch := cropPixels(m, context)
				local := model
				local.channels = append([]restorationChannel(nil), model.channels...)
				if model.spatial.coma {
					values, _ := model.spatial.empiricalAt(float64(min(cx, m.W-1)), float64(min(cy, m.H-1)))
					kernel := kernelFromPSF(values)
					local.patchKernel = &kernel
				}
				plane := patch
				if patch.C == 3 {
					plane = restorationLuminance(patch)
					c := *model.luminance
					local.luminance = &c
				}
				sky, noise, offset := restorationBackground(plane.Pix[:plane.W*plane.H], plane.W)
				if local.luminance != nil {
					local.luminance.sky, local.luminance.noise, local.luminance.offset = sky, noise, offset
				} else {
					local.channels[0].sky, local.channels[0].noise, local.channels[0].offset = sky, noise, offset
				}
				trial = restoreStationaryImage(patch, local, p, nil, &cov)
			}
			for y := part.Min.Y; y < part.Max.Y; y++ {
				for x := part.Min.X; x < part.Max.X; x++ {
					weight := (1 + math.Cos(math.Pi*float64(x-cx)/half)) * (1 + math.Cos(math.Pi*float64(y-cy)/half))
					i := (y-region.Min.Y)*out.W + x - region.Min.X
					weights[i] += weight
					gain := 1.0
					if original := float64(m.at(0, x, y)); trial != nil && original > 0 {
						gain = float64(trial.at(0, x-context.Min.X, y-context.Min.Y)) / original
					}
					sum[i] += weight * gain
				}
			}
		}
	}
	r := restorationRadius(model, p)
	protectBounds := region.Inset(-2 * r).Intersect(bounds)
	protectedImage := m
	if protectBounds != bounds {
		protectedImage = cropPixels(m, protectBounds)
	}
	protect := restorationProtection(protectedImage, r)
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			i := (y-region.Min.Y)*out.W + x - region.Min.X
			for c := 0; c < m.C; c++ {
				if !finitePoint(float64(m.at(c, x, y)), 0) {
					out.Pix[c*out.W*out.H+i] = 0
				}
			}
			if protect[(y-protectBounds.Min.Y)*protectedImage.W+x-protectBounds.Min.X] || weights[i] < 1e-12 {
				continue
			}
			confidence := model.spatial.confidence(float64(x), float64(y))
			if confidence == 0 {
				continue
			}
			for c := 0; c < m.C; c++ {
				v := float64(m.at(c, x, y))
				if finitePoint(v, 0) {
					out.Pix[c*out.W*out.H+i] = float32(v * (1 + confidence*(sum[i]/weights[i]-1)))
				}
			}
		}
	}
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	return out
}
