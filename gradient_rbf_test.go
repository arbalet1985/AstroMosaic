package main

import (
	"math"
	"testing"
)

func TestRBFGradient(t *testing.T) {
	m := newImg(600, 600, 1)
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			u, v := float64(x)/599, float64(y)/599
			m.Pix[y*m.W+x] = float32(.07 + .012*u + .018*math.Exp(-((u-.2)*(u-.2)+(v-.3)*(v-.3))/.1) - .02*math.Exp(-((u-.75)*(u-.75)+(v-.7)*(v-.7))/.1))
		}
	}
	g, err := estimateGradientMode(m, [400]bool{}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(g.note)
	if len(g.table) == 0 {
		t.Fatal("complex gradient did not select RBF")
	}
	out := subtractGradient(m, g)
	var residual []float64
	for y := 20; y < 580; y += 20 {
		for x := 20; x < 580; x += 20 {
			residual = append(residual, math.Abs(float64(out.at(0, x, y))-g.level[0]))
		}
	}
	if median(residual) > .0005 {
		t.Fatalf("RBF background residual %.5f", median(residual))
	}
	rgb := newImg(m.W, m.H, 3)
	copy(rgb.Pix, m.Pix)
	for i := m.W * m.H; i < len(rgb.Pix); i++ {
		rgb.Pix[i] = .05
	}
	colorModel, err := estimateGradientMode(rgb, [400]bool{}, true)
	if err != nil {
		t.Fatal(err)
	}
	colorOut := subtractGradient(rgb, colorModel)
	for i := m.W * m.H; i < len(rgb.Pix); i++ {
		if math.Abs(float64(colorOut.Pix[i]-.05)) > 1e-7 {
			t.Fatal("RBF changed a constant colour channel")
		}
	}
	for i := range m.Pix {
		m.Pix[i] = .05
	}
	if _, err := estimateGradientMode(m, [400]bool{}, true); err == nil {
		t.Fatal("flat field got RBF")
	}
}
