package main

import (
	"fmt"
	"image"
	"strings"
)

func imageChannel(m *Img, c int) *Img {
	out := newImg(m.W, m.H, 1)
	copy(out.Pix, m.Pix[c*m.W*m.H:(c+1)*m.W*m.H])
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	return out
}

func estimateRestorationMode(m *Img, spatial, coma, profile, independent bool) (restorationModel, error) {
	if independent && m.C == 3 {
		base := restorationModel{colorModels: make([]restorationModel, 3), channels: make([]restorationChannel, 3)}
		for c := 0; c < 3; c++ {
			sub, err := estimateRestorationMode(imageChannel(m, c), spatial, coma, profile, false)
			if err != nil {
				return base, fmt.Errorf("%s: %w", channelName(c, 3), err)
			}
			base.colorModels[c] = sub
			base.channels[c] = sub.channels[0]
			if c == 0 {
				base.stars, base.spatial = sub.stars, sub.spatial
			}
		}
		return base, nil
	}
	if profile {
		return estimateProfileRestoration(m)
	}
	if coma {
		return estimateComaRestoration(m)
	}
	if spatial {
		return estimateSpatialRestoration(m)
	}
	return estimateRestoration(m)
}

func restoreColorRegion(m *Img, model restorationModel, p restorationParams, region image.Rectangle, progress func(int, int)) *Img {
	region = region.Intersect(image.Rect(0, 0, m.W, m.H))
	out := cropPixels(m, region)
	n := out.W * out.H
	for c, sub := range model.colorModels {
		plane := imageChannel(m, c)
		var notify func(int, int)
		if progress != nil {
			notify = func(_ int, it int) { progress(c, it) }
		}
		var after *Img
		if sub.spatial != nil {
			after = restoreSpatialRegion(plane, sub, p, region, notify)
		} else {
			halo := 2 * restorationRadius(sub, p) * (p.iterations + 2)
			context := region.Inset(-halo).Intersect(image.Rect(0, 0, m.W, m.H))
			after = cropPixels(restoreImage(cropPixels(plane, context), sub, p, notify), region.Sub(context.Min))
		}
		copy(out.Pix[c*n:(c+1)*n], after.Pix)
	}
	r := restorationRadius(model, p)
	context := region.Inset(-2 * r).Intersect(image.Rect(0, 0, m.W, m.H))
	protected := m
	if context != image.Rect(0, 0, m.W, m.H) {
		protected = cropPixels(m, context)
	}
	mask := restorationProtection(protected, r)
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			if !mask[(y-context.Min.Y)*protected.W+x-context.Min.X] {
				continue
			}
			for c := 0; c < m.C; c++ {
				v := m.at(c, x, y)
				i := c*n + (y-region.Min.Y)*out.W + x - region.Min.X
				out.Pix[i] = 0
				if finitePoint(float64(v), 0) {
					out.Pix[i] = v
				}
			}
		}
	}
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	return out
}

func assessColorRestoration(before, after *Img, model restorationModel, origin image.Point) (string, error) {
	var notes []string
	for c, sub := range model.colorModels {
		note, err := assessRestorationModel(imageChannel(before, c), imageChannel(after, c), sub, origin)
		notes = append(notes, channelName(c, 3)+": "+note)
		if err != nil {
			return strings.Join(notes, "; "), fmt.Errorf("%s: %w", channelName(c, 3), err)
		}
	}
	return "Раздельные PSF R/G/B. " + strings.Join(notes, "; "), nil
}
