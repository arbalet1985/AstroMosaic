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

func assessRestorationModel(before, after *Img, model restorationModel, origin image.Point) (string, error) {
	if model.spatial != nil && model.spatial.coma {
		return assessComaRestoration(before, after, model, origin)
	}
	if model.spatial == nil {
		return assessRestoration(before, after, nil)
	}
	g := model.spatial
	stars := model.stars
	if origin != (image.Point{}) || before.W != g.w || before.H != g.h {
		stars = detectStars(before)
		stars = stars[:min(len(stars), 4000)]
	}
	var selected []star
	for _, s := range stars {
		if g.confidence(s.x+float64(origin.X), s.y+float64(origin.Y)) >= .8 {
			selected = append(selected, s)
		}
	}
	if len(selected) < 10 {
		return g.summary(), fmt.Errorf("в пробе нужно ≥10 звёзд внутри измеренного покрытия PSF")
	}
	note, err := assessRestoration(before, after, selected)
	if err != nil {
		return note, err
	}
	a, b := before, after
	if before.C == 3 {
		a, b = restorationLuminance(before), restorationLuminance(after)
	}
	var oldE, newE, oldF, newF [16][]float64
	for _, s := range selected {
		p, ok := fitStarPSF(a, s, 0)
		q, ok2 := fitStarPSF(b, s, 0)
		if !ok || !ok2 {
			continue
		}
		cx, cy := min(3, int((s.x+float64(origin.X))*4/float64(g.w))), min(3, int((s.y+float64(origin.Y))*4/float64(g.h)))
		i := cy*4 + cx
		oldE[i], newE[i], oldF[i], newF[i] = append(oldE[i], p.ellipticity), append(newE[i], q.ellipticity), append(oldF[i], p.fwhm), append(newF[i], q.fwhm)
	}
	checked := 0
	var allOld, allNew []float64
	for i := range oldE {
		if len(oldE[i]) < 5 {
			continue
		}
		checked++
		e, E, f, F := median(oldE[i]), median(newE[i]), median(oldF[i]), median(newF[i])
		allOld, allNew = append(allOld, e), append(allNew, E)
		if F >= .99*f || E > e+.03 || (e >= .15 && E > .98*e) {
			return note, fmt.Errorf("PSF [%d,%d]: локальный профиль не улучшился (FWHM %.2f→%.2f, e %.3f→%.3f)", i/4+1, i%4+1, f, F, e, E)
		}
		x, y := i%4, i/4
		r := image.Rect((x*g.w+3)/4, (y*g.h+3)/4, ((x+1)*g.w+3)/4, ((y+1)*g.h+3)/4).Sub(origin).Intersect(image.Rect(0, 0, a.W, a.H))
		if r.Dx() >= 32 && r.Dy() >= 32 {
			A, B := cropPixels(a, r), cropPixels(b, r)
			_, n1, _ := restorationBackground(A.Pix, A.W)
			_, n2, _ := restorationBackground(B.Pix, B.W)
			if n2 > math.Max(1.2*n1, n1+1e-6) {
				return note, fmt.Errorf("PSF [%d,%d]: рост локального шума >20%%", i/4+1, i%4+1)
			}
		}
	}
	if checked == 0 {
		return note, fmt.Errorf("нужно ≥5 контрольных звёзд в измеренной ячейке")
	}
	return fmt.Sprintf("%s. %s. Проверено ячеек: %d; e %.3f → %.3f", g.summary(), note, checked, median(allOld), median(allNew)), nil
}

func spatialPSFImage(g *spatialPSF) *image.RGBA {
	if g.coma {
		return comaPSFImage(g)
	}
	const size = 160
	out := image.NewRGBA(image.Rect(0, 0, 4*size, 4*size))
	for i, node := range g.nodes {
		x, y := (i%4)*size, (i/4)*size
		col := color.RGBA{45, 48, 53, 255}
		labels := []string{fmt.Sprintf("N=%d", node.n), "NO MODEL"}
		major, minor, angle := node.cov.axes()
		if node.valid {
			f := math.Sqrt(major*minor) * gaussianFWHM
			col = chromColor((f - 2) / 8)
			labels = []string{fmt.Sprintf("N=%d", node.n), fmt.Sprintf("%.2f x %.2f px", major*gaussianFWHM, minor*gaussianFWHM), fmt.Sprintf("e %.3f; %.0f deg", 1-minor/major, angle*180/math.Pi)}
		}
		draw.Draw(out, image.Rect(x+1, y+1, x+size-1, y+size-1), image.NewUniform(col), image.Point{}, draw.Src)
		for j, label := range labels {
			for _, offset := range []image.Point{{1, 1}, {0, 0}} {
				ink := color.White
				if offset.X == 1 {
					ink = color.Black
				}
				d := font.Drawer{Dst: out, Src: image.NewUniform(ink), Face: basicfont.Face7x13, Dot: fixed.P(x+10+offset.X, y+22+j*22+offset.Y)}
				d.DrawString(label)
			}
		}
		if !node.valid {
			continue
		}
		for k := 0; k < 720; k++ {
			t := float64(k) * 2 * math.Pi / 720
			u, v := 3*gaussianFWHM*major*math.Cos(t), 3*gaussianFWHM*minor*math.Sin(t)
			X, Y := float64(x+80)+math.Cos(angle)*u-math.Sin(angle)*v, float64(y+118)+math.Sin(angle)*u+math.Cos(angle)*v
			out.SetRGBA(int(math.Round(X)), int(math.Round(Y)), color.RGBA{255, 255, 255, 255})
		}
	}
	return out
}
