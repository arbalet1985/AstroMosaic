package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func headerFloat(h map[string]string, key string) (float64, bool) {
	v, ok := h[key]
	if !ok {
		return 0, false
	}
	x, err := strconv.ParseFloat(strings.ReplaceAll(strings.ReplaceAll(v, "D", "E"), "d", "e"), 64)
	return x, err == nil && !math.IsNaN(x) && !math.IsInf(x, 0)
}

type sipTerm struct {
	i, j  int
	value float64
}
type sipPolynomial []sipTerm

// SIP polynomials use FITS offsets from CRPIX, before the CD matrix.
// Only forward A/B are needed: inversion solves the actual forward map, rather
// than relying on optional, approximate AP/BP coefficients.
type sipDistortion struct {
	a, b         sipPolynomial
	cx, cy, sign float64 // image origin and conversion of top-down y to FITS y
}

func parseSIP(m *Img) (*sipDistortion, error) {
	_, a := m.Hdr["A_ORDER"]
	_, b := m.Hdr["B_ORDER"]
	declared := strings.Contains(m.Hdr["CTYPE1"], "-SIP") || strings.Contains(m.Hdr["CTYPE2"], "-SIP")
	for key := range m.Hdr {
		declared = declared || strings.HasPrefix(key, "A_") || strings.HasPrefix(key, "B_")
	}
	if !a && !b && !declared {
		return nil, nil
	}
	cx, okX := headerFloat(m.Hdr, "CRPIX1")
	cy, okY := headerFloat(m.Hdr, "CRPIX2")
	if !okX || !okY {
		return nil, fmt.Errorf("SIP: отсутствует корректный CRPIX")
	}
	s := &sipDistortion{cx: cx - 1, cy: cy - 1, sign: 1}
	if m.BottomUp {
		s.cy, s.sign = float64(m.H)-cy, -1
	}
	for _, p := range []struct {
		name string
		dst  *sipPolynomial
	}{{"A", &s.a}, {"B", &s.b}} {
		n, ok := headerFloat(m.Hdr, p.name+"_ORDER")
		if !ok || n != math.Trunc(n) || n < 2 || n > 9 {
			return nil, fmt.Errorf("SIP: %s_ORDER должен быть целым от 2 до 9", p.name)
		}
		for i := 0; i <= int(n); i++ {
			for j := 0; j <= int(n)-i; j++ {
				key := fmt.Sprintf("%s_%d_%d", p.name, i, j)
				if _, exists := m.Hdr[key]; !exists {
					continue
				}
				v, ok := headerFloat(m.Hdr, key)
				if !ok || (i+j < 2 && v != 0) {
					return nil, fmt.Errorf("SIP: некорректный коэффициент %s", key)
				}
				if v != 0 {
					*p.dst = append(*p.dst, sipTerm{i, j, v})
				}
			}
		}
	}
	return s, nil
}

func (p sipPolynomial) eval(u, v float64) (value, du, dv float64) {
	var U, V [10]float64
	U[0], V[0] = 1, 1
	for i := 1; i < 10; i++ {
		U[i], V[i] = U[i-1]*u, V[i-1]*v
	}
	for _, t := range p {
		value += t.value * U[t.i] * V[t.j]
		if t.i > 0 {
			du += t.value * float64(t.i) * U[t.i-1] * V[t.j]
		}
		if t.j > 0 {
			dv += t.value * float64(t.j) * U[t.i] * V[t.j-1]
		}
	}
	return
}

func (s *sipDistortion) forward(x, y float64) (float64, float64) {
	u, v := x-s.cx, s.sign*(y-s.cy)
	a, _, _ := s.a.eval(u, v)
	b, _, _ := s.b.eval(u, v)
	return x + a, y + s.sign*b
}

func (s *sipDistortion) inverse(X, Y float64) (x, y float64, ok bool) {
	x, y = X, Y
	for it := 0; it < 20; it++ {
		u, v := x-s.cx, s.sign*(y-s.cy)
		a, au, av := s.a.eval(u, v)
		b, bu, bv := s.b.eval(u, v)
		rx, ry := x+a-X, y+s.sign*b-Y
		if math.Hypot(rx, ry) < 1e-7 {
			return x, y, true
		}
		j11, j12, j21, j22 := 1+au, s.sign*av, s.sign*bu, 1+bv
		det := j11*j22 - j12*j21
		if math.Abs(det) < 1e-12 || math.IsNaN(det) || math.IsInf(det, 0) {
			break
		}
		x -= (j22*rx - j12*ry) / det
		y -= (-j21*rx + j11*ry) / det
	}
	return 0, 0, false
}
