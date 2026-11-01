package main

import (
	"math"
	"testing"
)

func TestMasterFlat(t *testing.T) {
	m, flat := newImg(200, 180, 3), newImg(200, 180, 3)
	n := m.W * m.H
	for c := 0; c < 3; c++ {
		for y := 0; y < m.H; y++ {
			for x := 0; x < m.W; x++ {
				u, v := float64(x-100)/100, float64(y-90)/90
				s := 1 - .25*(u*u+v*v)
				flat.Pix[c*n+y*m.W+x] = float32(s * float64(c+1) * .2)
				m.Pix[c*n+y*m.W+x] = float32(.1 * s * float64(c+1))
			}
		}
	}
	orig := append([]float32(nil), m.Pix...)
	out, note, err := applyMasterFlat(m, flat)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(note)
	for c := 0; c < 3; c++ {
		reference := out.Pix[c*n]
		for i := 0; i < n; i++ {
			if math.Abs(float64(out.Pix[c*n+i]-reference)) > 1e-6 {
				t.Fatal("vignetting remains")
			}
		}
	}
	for i, v := range orig {
		if m.Pix[i] != v {
			t.Fatal("mutated source")
		}
	}
	flat.Pix[0] = 0
	out, _, err = applyMasterFlat(m, flat)
	if err != nil {
		t.Fatal(err)
	}
	for c := 0; c < 3; c++ {
		if out.Pix[c*n] != 0 {
			t.Fatal("bad flat pixel not masked in all channels")
		}
	}
	for i := 0; i < n/10; i++ {
		flat.Pix[i] = 0
	}
	if _, _, err = applyMasterFlat(m, flat); err == nil {
		t.Fatal("bad flat accepted")
	}
	if _, _, err = applyMasterFlat(m, newImg(100, 100, 1)); err == nil {
		t.Fatal("size mismatch accepted")
	}
}
