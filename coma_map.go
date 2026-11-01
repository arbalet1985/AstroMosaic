package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

func comaPSFImage(g *spatialPSF) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, 640, 640))
	for i, n := range g.nodes {
		x, y := (i%4)*160, (i/4)*160
		draw.Draw(out, image.Rect(x+1, y+1, x+159, y+159), image.NewUniform(color.RGBA{35, 38, 43, 255}), image.Point{}, draw.Src)
		label := fmt.Sprintf("N=%d NO COMA", n.n)
		if n.valid {
			label = fmt.Sprintf("N=%d A=%.3f", n.n, n.asymmetry)
			peak := 0.0
			for _, v := range n.empirical {
				peak = math.Max(peak, v)
			}
			for yy := 0; yy < 110; yy++ {
				for xx := 0; xx < 110; xx++ {
					v := n.empirical[min(comaSide-1, yy*comaSide/110)*comaSide+min(comaSide-1, xx*comaSide/110)]
					c := uint8(255 * math.Sqrt(v/peak))
					out.SetRGBA(x+25+xx, y+35+yy, color.RGBA{c, c, c, 255})
				}
			}
		}
		d := font.Drawer{Dst: out, Src: image.White, Face: basicfont.Face7x13, Dot: fixed.P(x+8, y+23)}
		d.DrawString(label)
	}
	return out
}
