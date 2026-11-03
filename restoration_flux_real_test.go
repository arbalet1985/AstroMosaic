package main

import (
	"image"
	"os"
	"testing"
)

func TestRealMosaicRestorationFlux(t *testing.T) {
	path := `C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\mosaic333.fits`
	if _, err := os.Stat(path); err != nil {
		t.Skip("real image absent")
	}
	m, err := loadImage(path)
	if err != nil {
		t.Fatal(err)
	}
	model, err := estimateRestoration(m)
	if err != nil {
		t.Fatal(err)
	}
	region := restorationPreviewRect(m, model.stars, nil)
	p := restorationParams{iterations: 5, fraction: .35, regularize: true}
	halo := 2 * restorationRadius(model, p) * (p.iterations + 2)
	ctx := region.Inset(-halo).Intersect(image.Rect(0, 0, m.W, m.H))
	after := cropPixels(restoreImage(cropPixels(m, ctx), model, p, nil), region.Sub(ctx.Min))
	note, err := assessRestorationModel(cropPixels(m, region), after, model, region.Min)
	t.Log(note)
	if err != nil {
		t.Fatal(err)
	}
}
