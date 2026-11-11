package main

import (
	"image"
	"math"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
)

type displayTone struct{ low, mid []float64 }

func imageTones(m *Img) [nPrev]displayTone {
	var t [nPrev]displayTone
	for mode := prevLinked; mode < nPrev; mode++ {
		t[mode].low, t[mode].mid = stretchParams(m, mode == prevLinked)
	}
	return t
}

func displayByte(v float64, c int, tone displayTone) uint8 {
	if !finitePoint(v, 0) {
		return 0
	}
	if len(tone.mid) > 0 {
		v = mtf(v, tone.mid[c], tone.low[c], 1)
	}
	return uint8(math.Min(1, math.Max(0, v))*255 + .5)
}

// Average source samples for overview pixels; at native resolution every float
// pixel is evaluated directly. No low-resolution or quantised intermediate.
func renderDisplay(m *Img, region image.Rectangle, size image.Point, tone displayTone, gradient *gradientModel) *image.RGBA {
	out := image.NewRGBA(image.Rectangle{Max: size})
	parallel(size.Y, func(lo, hi int) {
		for y := lo; y < hi; y++ {
			y0 := region.Min.Y + y*region.Dy()/size.Y
			y1 := region.Min.Y + (y+1)*region.Dy()/size.Y
			for x := 0; x < size.X; x++ {
				x0 := region.Min.X + x*region.Dx()/size.X
				x1 := region.Min.X + (x+1)*region.Dx()/size.X
				var rgb [3]uint8
				for c := 0; c < min(3, m.C); c++ {
					var sum float64
					n, nonzero := 0, 0
					for Y := y0; Y < max(y0+1, y1); Y++ {
						for X := x0; X < max(x0+1, x1); X++ {
							v := float64(m.at(c, X, Y))
							if !finitePoint(v, 0) {
								continue
							}
							if v != 0 {
								nonzero++
							}
							sum += v
							n++
						}
					}
					if n > 0 {
						v := sum / float64(n)
						if gradient != nil && nonzero > 0 {
							v -= (gradient.surface(c, float64(x0+x1-1)/2, float64(y0+y1-1)/2) - gradient.level[c]) * float64(nonzero) / float64(n)
						}
						rgb[c] = displayByte(v, c, tone)
					}
				}
				if m.C == 1 {
					rgb[1], rgb[2] = rgb[0], rgb[0]
				}
				i := out.PixOffset(x, y)
				out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = rgb[0], rgb[1], rgb[2], 255
			}
		}
	})
	return out
}

type detailView struct {
	source     *Img
	tones      [nPrev]displayTone
	gradient   *gradientModel
	region     image.Rectangle
	renderSize image.Point
	mode       int
	img        paint.ImageOp
}

// The viewport cache is bounded independently of the full image dimensions.
// On zoom-in the overview is replaced by original pixels of the visible ROI.
func (d *detailView) draw(gtx layout.Context, pn *panel, overview image.Point, mode int) bool {
	if d == nil || d.source == nil || overview.X == 0 || overview.Y == 0 {
		return false
	}
	sx := pn.zoom * float32(overview.X) / float32(d.source.W)
	// Overview dimensions are rounded. Use one scale for both axes so that
	// 100% means square, exactly native pixels even for odd image dimensions.
	sy := sx
	if sx <= 0 || sy <= 0 {
		return false
	}
	r := image.Rect(int(math.Floor(float64(-pn.pan.X/sx)))-2, int(math.Floor(float64(-pn.pan.Y/sy)))-2,
		int(math.Ceil(float64((float32(gtx.Constraints.Max.X)-pn.pan.X)/sx)))+2, int(math.Ceil(float64((float32(gtx.Constraints.Max.Y)-pn.pan.Y)/sy)))+2)
	r = r.Intersect(image.Rect(0, 0, d.source.W, d.source.H))
	// Cache a small border so short pans reuse the same native texture.
	r = image.Rect(r.Min.X/128*128, r.Min.Y/128*128, (r.Max.X+127)/128*128, (r.Max.Y+127)/128*128).Intersect(image.Rect(0, 0, d.source.W, d.source.H))
	if r.Empty() {
		return true
	}
	// At intermediate scales the native ROI can exceed 4096 pixels while
	// its on-screen texture is small. Render directly from that ROI rather
	// than enlarging the overview's already-binned stars.
	texture := r.Size()
	if sx < 1 {
		texture = image.Pt(max(1, int(math.Ceil(float64(float32(r.Dx())*sx)))), max(1, int(math.Ceil(float64(float32(r.Dy())*sy)))))
	}
	if texture.X > 8192 || texture.Y > 8192 || texture.X*texture.Y > 16*1024*1024 || int64(r.Dx())*int64(r.Dy()) > 64*1024*1024 {
		return false
	}
	if r != d.region || mode != d.mode || texture != d.renderSize {
		d.img = paint.NewImageOp(renderDisplay(d.source, r, texture, d.tones[mode], d.gradient))
		d.region, d.mode, d.renderSize = r, mode, texture
	}
	transform := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(sx*float32(r.Dx())/float32(texture.X), sy*float32(r.Dy())/float32(texture.Y))).Offset(pn.pan.Add(f32.Pt(float32(r.Min.X)*sx, float32(r.Min.Y)*sy)))).Push(gtx.Ops)
	d.img.Filter = paint.FilterLinear
	if sx >= 1 && sy >= 1 {
		d.img.Filter = paint.FilterNearest
	}
	d.img.Add(gtx.Ops)
	c := clip.Rect{Max: texture}.Push(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	c.Pop()
	transform.Pop()
	return true
}
