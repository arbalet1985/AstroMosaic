package main

import (
	"fmt"
	"math"
)

type lensModel [4]float64 // k1, k2, p1, p2 in half-diagonal normalised coordinates

func (w Warp) lensForward(x, y float64) (float64, float64) {
	if w.extra == [3]float64{} {
		if w.K == 0 {
			return x, y
		}
		dx, dy := x-w.cx, y-w.cy
		f := 1 + w.K*(dx*dx+dy*dy)/(w.rn*w.rn)
		return w.cx + dx*f, w.cy + dy*f
	}
	u, v := (x-w.cx)/w.rn, (y-w.cy)/w.rn
	r2 := u*u + v*v
	k2, p1, p2 := w.extra[0], w.extra[1], w.extra[2]
	f := 1 + w.K*r2 + k2*r2*r2
	return w.cx + w.rn*(u*f+2*p1*u*v+p2*(r2+2*u*u)), w.cy + w.rn*(v*f+p1*(r2+2*v*v)+2*p2*u*v)
}

func (w Warp) lensInverse(X, Y float64) (float64, float64) {
	U, V := (X-w.cx)/w.rn, (Y-w.cy)/w.rn
	u, v := U, V
	k2, p1, p2 := w.extra[0], w.extra[1], w.extra[2]
	for it := 0; it < 15; it++ {
		r2 := u*u + v*v
		f, g := 1+w.K*r2+k2*r2*r2, w.K+2*k2*r2
		rx, ry := u*f+2*p1*u*v+p2*(r2+2*u*u)-U, v*f+p1*(r2+2*v*v)+2*p2*u*v-V
		if math.Hypot(rx, ry)*w.rn < 1e-7 {
			return w.cx + w.rn*u, w.cy + w.rn*v
		}
		a, b, d := f+2*u*u*g+2*p1*v+6*p2*u, 2*u*v*g+2*p1*u+2*p2*v, f+2*v*v*g+6*p1*v+2*p2*u
		det := a*d - b*b
		if math.Abs(det) < 1e-10 || !finitePoint(rx, ry) {
			break
		}
		u, v = u-(d*rx-b*ry)/det, v-(-b*rx+a*ry)/det
	}
	return -1, -1
}

// Shared high-order distortion is only identifiable with many stars distributed
// over the detector, not a handful of stars along a single narrow overlap.
func canFitExtraLens(tiles []*Tile, ms []match) bool {
	if len(ms) < 200 {
		return false
	}
	var occupied [16]bool
	eligible := make([]bool, len(tiles))
	for i, t := range tiles {
		s, _ := parseSIP(t.Img)
		eligible[i] = s == nil
	}
	useful := 0
	participating := map[int]bool{}
	lo, hi := math.Inf(1), 0.0
	for _, m := range ms {
		if eligible[m.A] || eligible[m.B] {
			useful++
		}
		for _, p := range []struct {
			i int
			q [2]float64
		}{{m.A, m.pA}, {m.B, m.pB}} {
			img := tiles[p.i].Img
			if !eligible[p.i] {
				continue
			}
			participating[p.i] = true
			occupied[diagnosticBin(p.q[0], p.q[1], img)] = true
			u, v := (p.q[0]-.5*float64(img.W-1))/(.5*math.Hypot(float64(img.W), float64(img.H))), (p.q[1]-.5*float64(img.H-1))/(.5*math.Hypot(float64(img.W), float64(img.H)))
			r2 := u*u + v*v
			lo, hi = math.Min(lo, r2), math.Max(hi, r2)
		}
	}
	n := 0
	for _, v := range occupied {
		if v {
			n++
		}
	}
	return useful >= 200 && len(participating) >= 2 && n >= 8 && hi-lo > .3
}

// Coordinate descent profiles out per-tile similarities at every trial. Bounds
// keep this a small optical correction; a solution on a bound is not accepted.
func fitExtraLens(tiles []*Tile, init []Aff, ms []match, start lensModel) (lensModel, []Warp) {
	model := start
	bounds := lensModel{.05, .05, .02, .02}
	objective := func(p lensModel) float64 { return rms(solveWarpsModel(tiles, init, ms, p), ms) }
	best := objective(model)
	for pass := 0; pass < 16; pass++ {
		before := best
		for axis := 0; axis < 4; axis++ {
			lo, hi := -bounds[axis], bounds[axis]
			const ratio = .6180339887498949
			x1, x2 := hi-ratio*(hi-lo), lo+ratio*(hi-lo)
			eval := func(x float64) float64 { p := model; p[axis] = x; return objective(p) }
			f1, f2 := eval(x1), eval(x2)
			for it := 0; it < 24; it++ {
				if f1 < f2 {
					hi, x2, f2 = x2, x1, f1
					x1 = hi - ratio*(hi-lo)
					f1 = eval(x1)
				} else {
					lo, x1, f1 = x1, x2, f2
					x2 = lo + ratio*(hi-lo)
					f2 = eval(x2)
				}
			}
			x := (lo + hi) / 2
			if f := eval(x); f < best {
				model[axis], best = x, f
			}
		}
		if before-best < 1e-5 {
			break
		}
	}
	return model, solveWarpsModel(tiles, init, ms, model)
}

func extraLensBounded(model lensModel) bool {
	bounds := lensModel{.05, .05, .02, .02}
	for i, b := range bounds {
		if math.Abs(model[i]) >= .98*b {
			return false
		}
	}
	return true
}

func extraLensNote(base, trial float64, accepted bool) string {
	state := "оставлена k1"
	if accepted {
		state = "принята k1+k2+p1+p2"
	}
	return fmt.Sprintf("Проверка дисторсии: k1 %.3f px → расширенная %.3f px; %s", base, trial, state)
}

func validatedExtraLens(tiles []*Tile, init []Aff, train, validation, all []match, trainW, finalW []Warp, k float64) ([]Warp, float64, string) {
	baseError := rms(trainW, validation)
	if !canFitExtraLens(tiles, train) {
		return nil, baseError, "Расширенная дисторсия: недостаточно звёзд без SIP или покрытия поля"
	}
	model, candidate := fitExtraLens(tiles, init, train, lensModel{k, 0, 0, 0})
	trialError := rms(candidate, validation)
	accept := finitePoint(baseError, trialError) && extraLensBounded(model) && trialError < .9*baseError && baseError-trialError >= .05
	var selected []Warp
	if accept {
		finalModel, finalCandidate := fitExtraLens(tiles, init, all, model)
		if extraLensBounded(finalModel) && rms(finalCandidate, all) < rms(finalW, all) {
			selected = finalCandidate
		} else {
			accept = false
		}
	}
	note := extraLensNote(baseError, trialError, accept)
	if !accept {
		trialError = baseError
	}
	return selected, trialError, note
}
