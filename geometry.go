package main

import "math"

// Geometry tools modelled on Siril's Rotation & Crop: a frame (centre, size, angle) is cut out of the
// image and becomes the new upright image; without a frame the whole image is rotated onto a canvas
// that holds all of it. Interpolation choices and clamping follow Siril/OpenCV.

type interp int

const (
	interpNearest interp = iota
	interpBilinear
	interpBicubic
	interpLanczos4
	nInterp
)

var interpNames = [nInterp]string{"ближайший", "билинейная", "бикубическая", "Ланцош-4"}

// frame is a rectangle of size W×H centred at (CX,CY) in pixel-edge coordinates, turned by Angle
// degrees clockwise on screen (Siril/OpenCV: the image turns counter-clockwise by Angle).
type frame struct{ CX, CY, W, H, Angle float64 }

// corners returns the frame corners in image coordinates (TL, TR, BR, BL of the output).
func (f frame) corners() [4][2]float64 {
	s, c := math.Sincos(f.Angle * math.Pi / 180)
	var out [4][2]float64
	for i, d := range [4][2]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}} {
		dx, dy := d[0]*f.W/2, d[1]*f.H/2
		out[i] = [2]float64{f.CX + c*dx - s*dy, f.CY + s*dx + c*dy}
	}
	return out
}

// toLocal maps an image point into the frame's unrotated coords (origin at centre).
func (f frame) toLocal(x, y float64) (float64, float64) {
	s, c := math.Sincos(f.Angle * math.Pi / 180)
	dx, dy := x-f.CX, y-f.CY
	return c*dx + s*dy, -s*dx + c*dy
}

// fromLocal is the inverse of toLocal.
func (f frame) fromLocal(lx, ly float64) (float64, float64) {
	s, c := math.Sincos(f.Angle * math.Pi / 180)
	return f.CX + c*lx - s*ly, f.CY + s*lx + c*ly
}

func cubicW(x float64) float64 { // Keys, a = -0.75 as in OpenCV
	const a = -0.75
	x = math.Abs(x)
	if x < 1 {
		return ((a+2)*x-(a+3))*x*x + 1
	}
	if x < 2 {
		return ((a*x-5*a)*x+8*a)*x - 4*a
	}
	return 0
}

func lanczos4W(x float64) float64 {
	if x == 0 {
		return 1
	}
	if math.Abs(x) >= 4 {
		return 0
	}
	px := math.Pi * x
	return 4 * math.Sin(px) * math.Sin(px/4) / (px * px)
}

// cutFrame resamples the frame into a new upright image. Outside the source is black. With clamp,
// bicubic/Lanczos results are kept within the range of the 2×2 nearest pixels (no ringing halos).
func cutFrame(m *Img, f frame, it interp, clamp bool) *Img {
	W, H := max(1, int(math.Round(f.W))), max(1, int(math.Round(f.H))) // ponytail: drop fractional size
	out := newImg(W, H, m.C)
	s, c := math.Sincos(f.Angle * math.Pi / 180)
	n := m.W * m.H
	taps := map[interp]int{interpNearest: 0, interpBilinear: 1, interpBicubic: 2, interpLanczos4: 4}[it]
	kern := map[interp]func(float64) float64{
		interpBilinear: func(x float64) float64 { return math.Max(0, 1-math.Abs(x)) },
		interpBicubic:  cubicW, interpLanczos4: lanczos4W}[it]
	parallel(H, func(lo, hi int) {
		wx, wy := make([]float64, 2*taps), make([]float64, 2*taps)
		for j := lo; j < hi; j++ {
			for i := 0; i < W; i++ {
				// output pixel centre, relative to the frame centre, into the source (pixel-centre coords)
				dx, dy := float64(i)+0.5-float64(W)/2, float64(j)+0.5-float64(H)/2
				u := f.CX + c*dx - s*dy - 0.5
				v := f.CY + s*dx + c*dy - 0.5
				if u < -0.5 || v < -0.5 || u > float64(m.W)-0.5 || v > float64(m.H)-0.5 {
					continue
				}
				if taps == 0 {
					x, y := min(int(math.Round(u)), m.W-1), min(int(math.Round(v)), m.H-1)
					for ch := 0; ch < m.C; ch++ {
						out.Pix[ch*W*H+j*W+i] = m.Pix[ch*n+max(y, 0)*m.W+max(x, 0)]
					}
					continue
				}
				x0, y0 := int(math.Floor(u))-taps+1, int(math.Floor(v))-taps+1
				var sx, sy float64
				for k := range wx {
					wx[k] = kern(u - float64(x0+k))
					wy[k] = kern(v - float64(y0+k))
					sx += wx[k]
					sy += wy[k]
				}
				cx := func(x int) int { return min(max(x, 0), m.W-1) }
				cy := func(y int) int { return min(max(y, 0), m.H-1) }
				for ch := 0; ch < m.C; ch++ {
					p := m.Pix[ch*n:]
					var acc float64
					for b := range wy {
						row := p[cy(y0+b)*m.W:]
						var r float64
						for a := range wx {
							r += wx[a] * float64(row[cx(x0+a)])
						}
						acc += wy[b] * r
					}
					acc /= sx * sy
					if clamp && taps > 1 {
						fx, fy := int(math.Floor(u)), int(math.Floor(v))
						lo, hi := math.Inf(1), math.Inf(-1)
						for _, q := range [4][2]int{{fx, fy}, {fx + 1, fy}, {fx, fy + 1}, {fx + 1, fy + 1}} {
							t := float64(p[cy(q[1])*m.W+cx(q[0])])
							lo, hi = math.Min(lo, t), math.Max(hi, t)
						}
						acc = math.Min(math.Max(acc, lo), hi)
					}
					out.Pix[ch*W*H+j*W+i] = float32(acc)
				}
			}
		}
	})
	return out
}

// rotateWhole turns the full image by angle onto a canvas that holds all of it (Siril with "crop" off).
// Multiples of 90° are exact.
func rotateWhole(m *Img, angle float64, it interp, clamp bool) *Img {
	if q := angle / 90; q == math.Trunc(q) {
		k := ((int(q) % 4) + 4) % 4
		for ; k > 0; k-- {
			m = rot90(m, false)
		}
		return m
	}
	s, c := math.Sincos(angle * math.Pi / 180)
	w, h := float64(m.W), float64(m.H)
	f := frame{CX: w / 2, CY: h / 2, W: math.Abs(w*c) + math.Abs(h*s), H: math.Abs(w*s) + math.Abs(h*c), Angle: angle}
	return cutFrame(m, f, it, clamp)
}

// rot90 turns the image a quarter: counter-clockwise (Siril "rotate 90") or clockwise.
func rot90(m *Img, cw bool) *Img {
	out := newImg(m.H, m.W, m.C)
	n := m.W * m.H
	for ch := 0; ch < m.C; ch++ {
		for y := 0; y < m.H; y++ {
			for x := 0; x < m.W; x++ {
				nx, ny := y, m.W-1-x // counter-clockwise
				if cw {
					nx, ny = m.H-1-y, x
				}
				out.Pix[ch*n+ny*out.W+nx] = m.Pix[ch*n+y*m.W+x]
			}
		}
	}
	return out
}

// mirror flips left-right (horizontal) or top-bottom.
func mirror(m *Img, horizontal bool) *Img {
	out := newImg(m.W, m.H, m.C)
	n := m.W * m.H
	for ch := 0; ch < m.C; ch++ {
		for y := 0; y < m.H; y++ {
			for x := 0; x < m.W; x++ {
				sx, sy := m.W-1-x, y
				if !horizontal {
					sx, sy = x, m.H-1-y
				}
				out.Pix[ch*n+y*m.W+x] = m.Pix[ch*n+sy*m.W+sx]
			}
		}
	}
	return out
}
