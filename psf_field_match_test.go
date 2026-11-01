package main

import (
	"math"
	"testing"
)

func TestPositiveMatchingKernel(t *testing.T) {
	p := covarianceProfile(psfCovariance{4, 1, 2}, 16)
	k, e, ok := fitPositiveMatchingKernel(p, psfCovariance{9, 0, 9})
	if !ok || e > .08 {
		t.Fatalf("kernel fit error %g", e)
	}
	var sum float64
	for _, v := range k {
		if v < 0 {
			t.Fatal("negative kernel")
		}
		sum += v
	}
	if math.Abs(sum-1) > 1e-6 {
		t.Fatal("unnormalised kernel")
	}
	j := [4]float64{0, -2, 1, 0}
	c := transformCovariance(psfCovariance{4, 1, 2}, j)
	if c.xx != 8 || c.xy != -2 || c.yy != 4 {
		t.Fatal("warp covariance transform")
	}
}

func TestFieldPSFMatching(t *testing.T) {
	m := ellipticalTestImage(true)
	orig := append([]float32(nil), m.Pix...)
	w := newWarp(identity, 0, m)
	imgs, report := matchFieldPSF([]*Tile{{Img: m, Name: "synthetic"}}, []Warp{w})
	t.Log(report.note)
	if !report.applied || imgs[0] == m {
		t.Fatal("field matching was rejected")
	}
	for i, v := range orig {
		if m.Pix[i] != v {
			t.Fatal("modified source")
		}
	}
	// Missing coverage cancels the entire operation, rather than mixing PSF modes.
	blank := newImg(800, 800, 1)
	out, r := matchFieldPSF([]*Tile{{Img: blank}}, []Warp{w})
	if r.applied || out[0] != blank {
		t.Fatal("unmeasured field was blurred")
	}
}

func TestFieldPSFMatchingAsymmetric(t *testing.T) {
	m := comaTestImage()
	_, report := matchFieldPSF([]*Tile{{Img: m, Name: "coma"}}, []Warp{newWarp(identity, 0, m)})
	t.Log(report.note)
	if !report.applied {
		t.Fatal("moderate asymmetric profile not matched")
	}
}
