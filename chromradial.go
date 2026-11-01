package main

import (
	"image"
	"image/color"
	"math"
)

type chromResidual struct{ x, y, dx, dy float64 }

func chromResidualImage(r chromatism, c int, scale float64) *image.RGBA {
	w, h := max(2, int(720*float64(r.W)/float64(max(r.W, r.H)))), max(2, int(720*float64(r.H)/float64(max(r.W, r.H))))
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.SetRGBA(x, y, color.RGBA{4, 5, 8, 255})
		}
	}
	for _, p := range r.Channels[c].Residuals {
		x, y := p.x*float64(w)/float64(r.W), p.y*float64(h)/float64(r.H)
		dx, dy := p.dx*24/scale, p.dy*24/scale
		col := chromColor(math.Hypot(p.dx, p.dy) / scale)
		for yy := -3; yy <= 3; yy++ {
			for xx := -3; xx <= 3; xx++ {
				out.SetRGBA(int(x)+xx, int(y)+yy, col)
			}
		}
		n := max(1, int(math.Hypot(dx, dy)*2))
		for i := 0; i <= n; i++ {
			f := float64(i) / float64(n)
			out.SetRGBA(int(math.Round(x+f*dx)), int(math.Round(y+f*dy)), color.RGBA{255, 255, 255, 255})
		}
	}
	return out
}

func (f channelAlignment) apply(x, y float64) (float64, float64) {
	a, b := f.Map.apply(x, y)
	if f.Radial != 0 && f.Scale > 0 {
		u, v := (x-f.CX)/f.Scale, (y-f.CY)/f.Scale
		r := u*u + v*v
		a += f.Radial * u * r
		b += f.Radial * v * r
	}
	return a, b
}

// One shared radial coefficient augments six affine displacement coefficients.
// All coordinates are normalised, while fitted displacements stay in pixels.
func fitChromRadial(src, dst [][2]float64, w, h float64) (channelAlignment, bool) {
	f := channelAlignment{CX: w / 2, CY: h / 2, Scale: math.Max(w, h) / 2}
	keep := make([]bool, len(src))
	for i := range keep {
		keep[i] = true
	}
	for it := 0; it < 4; it++ {
		ls := newLSQ(7)
		count := 0
		for i, p := range src {
			if !keep[i] {
				continue
			}
			u, v := (p[0]-f.CX)/f.Scale, (p[1]-f.CY)/f.Scale
			r := u*u + v*v
			ls.add([]int{0, 1, 2, 6}, []float64{1, u, v, u * r}, dst[i][0]-p[0], 1)
			ls.add([]int{3, 4, 5, 6}, []float64{1, u, v, v * r}, dst[i][1]-p[1], 1)
			count++
		}
		if count < 50 {
			return f, false
		}
		a := ls.solve()
		s := f.Scale
		f.Map = Aff{1 + a[1]/s, a[2] / s, a[0] - (a[1]*f.CX+a[2]*f.CY)/s, a[4] / s, 1 + a[5]/s, a[3] - (a[4]*f.CX+a[5]*f.CY)/s}
		f.Radial, f.Stars, f.Valid = a[6], count, true
		if !finitePoint(f.Radial, 0) || math.Abs(f.Radial) > 4 {
			return f, false
		}
		var residual []float64
		for i, p := range src {
			if keep[i] {
				x, y := f.apply(p[0], p[1])
				residual = append(residual, math.Hypot(x-dst[i][0], y-dst[i][1]))
			}
		}
		lim := math.Max(.05, 3*1.4826*median(residual))
		for i, p := range src {
			x, y := f.apply(p[0], p[1])
			keep[i] = math.Hypot(x-dst[i][0], y-dst[i][1]) <= lim
		}
	}
	return f, true
}

func selectChromModel(src, dst [][2]float64, w, h float64, affine channelAlignment) channelAlignment {
	var train, trainDst, validation, validationDst [][2]float64
	for i, p := range src {
		if i%5 == 0 {
			validation = append(validation, p)
			validationDst = append(validationDst, dst[i])
		} else {
			train = append(train, p)
			trainDst = append(trainDst, dst[i])
		}
	}
	if len(train) < 30 || len(validation) < 10 {
		return affine
	}
	a, _, ok := fitAffine(train, trainDst, w, h)
	if !ok {
		return affine
	}
	baseline := channelAlignment{Map: a}
	var be []float64
	for i, p := range validation {
		x, y := baseline.apply(p[0], p[1])
		be = append(be, math.Hypot(x-validationDst[i][0], y-validationDst[i][1]))
	}
	affine.ValidationBefore, affine.ValidationAfter = median(be), median(be)
	chosen := baseline
	var cells [16]int
	var quadrants [4]int
	outer := 0
	for _, p := range train {
		x, y := min(3, max(0, int(p[0]*4/w))), min(3, max(0, int(p[1]*4/h)))
		cells[y*4+x]++
		quadrants[(y/2)*2+x/2]++
		if math.Hypot((p[0]-w/2)/(w/2), (p[1]-h/2)/(h/2)) > .7 {
			outer++
		}
	}
	covered := 0
	for _, n := range cells {
		if n >= 3 {
			covered++
		}
	}
	coverage := covered >= 8 && outer >= 20
	for _, n := range quadrants {
		coverage = coverage && n >= 10
	}
	if coverage && len(train) >= 80 {
		trial, valid := fitChromRadial(train, trainDst, w, h)
		if valid {
			var err []float64
			for i, p := range validation {
				x, y := trial.apply(p[0], p[1])
				err = append(err, math.Hypot(x-validationDst[i][0], y-validationDst[i][1]))
			}
			if median(err) < .9*median(be) && median(be)-median(err) > .03 {
				if full, valid := fitChromRadial(src, dst, w, h); valid {
					full.ValidationBefore, full.ValidationAfter = median(be), median(err)
					affine = full
					chosen = trial
				}
			}
		}
	}
	for i, p := range validation {
		x, y := chosen.apply(p[0], p[1])
		affine.Residuals = append(affine.Residuals, chromResidual{p[0], p[1], validationDst[i][0] - x, validationDst[i][1] - y})
	}
	return affine
}
