package main

import (
	"image"
	"image/png"
	"os"
	"testing"

	"gioui.org/font/gofont"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// TestShot renders the toolbar to SHOT (skipped without it).
func TestShot(t *testing.T) {
	path := os.Getenv("SHOT")
	if path == "" {
		t.Skip("set SHOT")
	}
	sz := image.Pt(1600, 200)
	if os.Getenv("SHOTDIAG") != "" {
		sz = image.Pt(1600, 1000)
		if os.Getenv("SHOTSMALL") != "" {
			sz = image.Pt(1200, 700)
		}
	}
	if os.Getenv("SHOTRESTORE") != "" || os.Getenv("SHOTGRADIENT") != "" || os.Getenv("SHOTHALO") != "" || os.Getenv("SHOTWIPE") != "" || os.Getenv("SHOTQUALITY") != "" || os.Getenv("SHOTNOISE") != "" {
		sz = image.Pt(1200, 750)
	}
	w, err := headless.NewWindow(sz.X, sz.Y)
	if err != nil {
		t.Skip(err)
	}
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th.TextSize = 14
	u := &ui{th: th, rows: 2, cols: 2, tiles: map[cell]*tileState{}, points: map[cellPair][]PointPair{}, pairBtns: map[cellPair]*widget.Clickable{}}
	u.link.Value, u.chAlign.Value, u.lfit.Value, u.bgEq.Value = true, true, true, true
	u.overlapSl.Value = 15.0 / maxOverlap
	if os.Getenv("SHOTNOISE") != "" {
		_, m := waveTestImage(384, 384)
		model := estimateWaveModel(m)
		p := standardWaveParams()
		after := filterWaveRegion(m, model, p, image.Rect(0, 0, m.W, m.H), nil)
		pv := restorationPreviews(m, after)
		u.result, u.view, u.stretch.Value = m, 9, true
		u.noise = noiseState{source: m, model: model, params: p, cached: p, hasPreview: true, region: image.Rect(0, 0, m.W, m.H), note: "Проба Starlet; проверьте удалённый сигнал", panels: [2]panel{{needFit: true}, {needFit: true}}}
		for side := range pv {
			for mode, im := range pv[side] {
				u.noise.images[side][mode] = paint.NewImageOp(im)
			}
		}
		u.noise.residual = paint.NewImageOp(preview(removedSignal(m, after), 384, prevLinear))
	}

	if os.Getenv("SHOTQUALITY") != "" {
		m := haloTestImage()
		out, _, err := correctColorHalos(m, .75)
		if err != nil {
			t.Fatal(err)
		}
		r := measureQuality(m, out)
		u.result, u.view = out, 8
		u.quality.source, u.quality.data = out, r
	}

	if os.Getenv("SHOTWIPE") != "" {
		m := haloTestImage()
		out, _, err := correctColorHalos(m, .75)
		if err != nil {
			t.Fatal(err)
		}
		u.baseline = m
		u.setResult(out, previews(out, previewSide))
		u.view, u.stretch.Value = 2, true
	}
	if os.Getenv("SHOTHALO") != "" {
		m := haloTestImage()
		out, r, err := correctColorHalos(m, .75)
		if err != nil {
			t.Fatal(err)
		}
		u.result, u.view, u.stretch.Value = m, 7, true
		u.colorHalo = colorHaloState{source: m, candidate: out, cachedStrength: .75, region: image.Rect(0, 0, m.W, m.H), note: r.note}
		u.refreshHaloPreviews()
	}
	if os.Getenv("SHOTRESTORE") != "" {
		m := psfTestTile([]float64{2.2, 2.1, 2.3})
		if os.Getenv("SHOTSINGLE") != "" {
			u.tiles[cell{0, 0}] = &tileState{img: m}
		}
		model, err := estimateRestoration(m)
		spatial := os.Getenv("SHOTSPATIAL") != ""
		if spatial {
			m = ellipticalTestImage(true)
			model, err = estimateSpatialRestoration(m)
		}
		coma := os.Getenv("SHOTCOMA") != ""
		if coma {
			spatial = true
			m = comaTestImage()
			model, err = estimateComaRestoration(m)
		}
		profile := os.Getenv("SHOTPROFILE") != ""
		if profile {
			spatial, coma = true, true
			m = profileTestImage(false)
			model, err = estimateProfileRestoration(m)
		}
		independent := os.Getenv("SHOTCOLOR") != ""
		if independent {
			model, err = estimateRestorationMode(m, spatial, coma, profile, true)
		}
		if err != nil {
			t.Fatal(err)
		}
		p := restorationParams{iterations: 5, fraction: .35}
		region := image.Rect(0, 0, m.W, m.H)
		if spatial {
			region = restorationPreviewRect(m, model.stars, nil)
		}
		before := cropPixels(m, region)
		var out *Img
		if spatial {
			out = restoreSpatialRegion(m, model, p, region, nil)
		} else {
			out = restoreImage(m, model, p, nil)
		}
		note, err := assessRestorationModel(before, out, model, region.Min)
		if err != nil {
			t.Fatal(err)
		}
		pv := restorationPreviews(before, out)
		u.result, u.view, u.stretch.Value = m, 5, true
		u.restoration = restorationState{source: m, model: model, modelValid: true, hasPreview: true, approved: true, params: p, cachedParams: p, region: region, note: "Проба прошла проверку. " + note, panels: [2]panel{{needFit: true}, {needFit: true}}, spatial: spatial, cachedSpatial: spatial}
		u.restoration.coma, u.restoration.cachedComa = coma, coma
		u.restoration.profile, u.restoration.cachedProfile = profile, profile
		u.restoration.independent, u.restoration.cachedIndependent = independent, independent
		if spatial {
			u.restoration.psfMap = paint.NewImageOp(spatialPSFImage(model.spatial))
			u.restoration.mapPanel.needFit = true
			u.restoration.showMap = os.Getenv("SHOTPSFMAP") != ""
		}
		for side := range pv {
			for mode, img := range pv[side] {
				u.restoration.images[side][mode] = paint.NewImageOp(img)
			}
		}
	}
	if os.Getenv("SHOTGRADIENT") != "" {
		m := gradientTestImage()
		g, err := estimateGradient(m, [400]bool{})
		if err != nil {
			t.Fatal(err)
		}
		pv, modelImg := gradientPreviews(m, g, [400]bool{})
		u.result, u.view, u.stretch.Value = m, 6, true
		u.gradient = gradientState{source: m, model: g, valid: true, note: g.note, showModel: true, modelImage: paint.NewImageOp(modelImg), panels: [2]panel{{needFit: true}, {needFit: true}}}
		for side := range pv {
			for mode, img := range pv[side] {
				u.gradient.images[side][mode] = paint.NewImageOp(img)
			}
		}
	}
	if os.Getenv("SHOTDIAG") != "" {
		d := tileDiagnostic{name: "[1,1] — проверка диагностики"}
		if os.Getenv("SHOTPSF") != "" {
			u.psfMatch.Value = true
			d.psfNote = "PSF: ядра согласованы до 6.28 px мозаики; более резкие каналы сглажены\nR 6.11 → 6.23 px (σ 0.61 px исходника, N=3228); G 5.90 → 6.32 px (σ 0.91 px исходника, N=3121); B 5.87 → 6.36 px (σ 0.94 px исходника, N=2419)"
		}
		for i := range d.bins {
			if i == 0 {
				continue
			} // explicitly show an area without overlap measurements
			d.bins[i] = fieldBin{count: 25, dx: float64(i) * .6, dy: -.5, square: float64(i) * 2, fwhm: []float64{4 + float64(i)*.2}, ellipticity: []float64{.1}}
		}
		for c := range d.maps {
			d.maps[c] = paint.NewImageOp(diagnosticImage(d, c))
		}
		u.diagnostics = fieldDiagnostics{tiles: []tileDiagnostic{d}, matchCount: 245, rms: .56, validationN: 49, validationRMS: .57, modelNote: extraLensNote(.803, .791, false)}
		u.diagPanels, u.view = [2]panel{{needFit: true}, {needFit: true}}, 4
	}
	if os.Getenv("SHOTRES") != "" {
		u.result = newImg(1, 1, 1)
	}
	if os.Getenv("SHOTGEOM") != "" { // result view: toolbar and the frame/rotate bar only
		u.result, u.view = newImg(1, 1, 1), 2
		var ops op.Ops
		gtx := layout.Context{Ops: &ops, Constraints: layout.Exact(image.Pt(1600, 200)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
		layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(u.toolbar), layout.Rigid(u.geomBar))
		w.Frame(&ops)
		img := image.NewRGBA(image.Rectangle{Max: sz})
		w.Screenshot(img)
		f, _ := os.Create(path)
		defer f.Close()
		png.Encode(f, img)
		return
	}
	if os.Getenv("SHOTGRID") != "" {
		u.grid.open, u.grid.hr, u.grid.hc = true, 3, 4
	}
	var ops op.Ops
	gtx := layout.Context{Ops: &ops, Constraints: layout.Exact(sz), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
	u.layout(gtx)
	w.Frame(&ops)
	img := image.NewRGBA(image.Rectangle{Max: sz})
	w.Screenshot(img)
	f, _ := os.Create(path)
	defer f.Close()
	png.Encode(f, img)
}
