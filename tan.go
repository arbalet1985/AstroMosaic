package main

import "math"

// projective maps between ideal TAN detector planes. A change of tangent point
// is a homography of rays, so rendering needs no trigonometry per pixel.
type projective [9]float64

func (h projective) apply(x, y float64) (float64, float64) {
	d := h[6]*x + h[7]*y + h[8]
	if d <= 1e-12 || math.IsNaN(d) || math.IsInf(d, 0) {
		return math.NaN(), math.NaN()
	}
	return (h[0]*x + h[1]*y + h[2]) / d, (h[3]*x + h[4]*y + h[5]) / d
}

func (h projective) mul(b projective) (out projective) {
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			for k := 0; k < 3; k++ {
				out[r*3+c] += h[r*3+k] * b[k*3+c]
			}
		}
	}
	return
}

func (h projective) inverse() (projective, bool) {
	q := projective{h[4]*h[8] - h[5]*h[7], h[2]*h[7] - h[1]*h[8], h[1]*h[5] - h[2]*h[4], h[5]*h[6] - h[3]*h[8], h[0]*h[8] - h[2]*h[6], h[2]*h[3] - h[0]*h[5], h[3]*h[7] - h[4]*h[6], h[1]*h[6] - h[0]*h[7], h[0]*h[4] - h[1]*h[3]}
	det := h[0]*q[0] + h[1]*q[3] + h[2]*q[6]
	if det == 0 || math.IsNaN(det) || math.IsInf(det, 0) {
		return projective{}, false
	}
	for i := range q {
		q[i] /= det
	}
	return q, true
}

type tangentProjection struct{ forward, inverse projective }

func skyBasis(w *WCS) [3][3]float64 {
	sa, ca := math.Sincos(w.ra0)
	sd, cd := math.Sincos(w.dec0)
	return [3][3]float64{{-sa, ca, 0}, {-sd * ca, -sd * sa, cd}, {cd * ca, cd * sa, sd}}
}

func tanProjection(source, ref *WCS) *tangentProjection {
	a, b := skyBasis(source), skyBasis(ref)
	var rotation projective
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				rotation[3*i+j] += b[i][k] * a[j][k]
			}
		}
	}
	const rad = math.Pi / 180
	sy, y0 := 1.0, 1-source.crpix2
	if source.bottomUp {
		sy, y0 = -1, float64(source.h)-source.crpix2
	}
	x0 := 1 - source.crpix1
	input := projective{rad * source.cd[0], rad * source.cd[1] * sy, rad * (source.cd[0]*x0 + source.cd[1]*y0), rad * source.cd[2], rad * source.cd[3] * sy, rad * (source.cd[2]*x0 + source.cd[3]*y0), 0, 0, 1}
	sy, y0 = 1, ref.crpix2-1
	if ref.bottomUp {
		sy, y0 = -1, float64(ref.h)-ref.crpix2
	}
	output := projective{ref.icd[0] / rad, ref.icd[1] / rad, ref.crpix1 - 1, sy * ref.icd[2] / rad, sy * ref.icd[3] / rad, y0, 0, 0, 1}
	h := output.mul(rotation).mul(input)
	inv, ok := h.inverse()
	if !ok {
		return nil
	}
	return &tangentProjection{h, inv}
}

func finitePoint(x, y float64) bool {
	return !math.IsNaN(x) && !math.IsNaN(y) && !math.IsInf(x, 0) && !math.IsInf(y, 0)
}

// The first tile anchors the output. With no reference WCS the established
// star-only pipeline is kept; uncalibrated tiles can still join a TAN mosaic by stars.
func initialWarps(tiles []*Tile, K float64) []Warp {
	W := make([]Warp, len(tiles))
	var ref *WCS
	if len(tiles) > 0 {
		ref = parseWCS(tiles[0].Img)
	}
	for i, t := range tiles {
		W[i] = newWarp(identity, K, t.Img)
		if ref == nil {
			continue
		}
		source := parseWCS(t.Img)
		if source == nil {
			continue
		}
		p := tanProjection(source, ref)
		if i == 0 {
			identityPlane := projective{1, 0, 0, 0, 1, 0, 0, 0, 1}
			p = &tangentProjection{identityPlane, identityPlane}
		}
		if p == nil {
			continue
		}
		valid := true
		for _, q := range [][2]float64{{0, 0}, {float64(t.Img.W - 1), 0}, {0, float64(t.Img.H - 1)}, {float64(t.Img.W - 1), float64(t.Img.H - 1)}} {
			x, y := q[0], q[1]
			if W[i].sip != nil {
				x, y = W[i].sip.forward(x, y)
			}
			X, Y := p.forward.apply(x, y)
			valid = valid && finitePoint(X, Y)
		}
		if valid {
			W[i].plane = p
		}
	}
	return W
}
