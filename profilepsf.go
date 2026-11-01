package main

import "math"

func detectEmpiricalStars(m *Img, profile bool) []star {
	if !profile {
		return detectStars(m)
	}
	mono := restorationLuminance(m)
	mono.Pix = gaussianPSFPlane(mono.Pix, mono.W, mono.H, 2)
	return detectStars(mono)
}

func gaussianProfile(p []float64) []float64 {
	_, _, c := psfMoments(p)
	det := c.xx*c.yy - c.xy*c.xy
	q := make([]float64, len(p))
	side := int(math.Sqrt(float64(len(p))))
	r := side / 2
	if det <= 1e-8 {
		return q
	}
	for i := range q {
		x, y := float64(i%side-r), float64(i/side-r)
		q[i] = math.Exp(-.5 * (c.yy*x*x - 2*c.xy*x*y + c.xx*y*y) / det)
	}
	return normalisePSF(q)
}
