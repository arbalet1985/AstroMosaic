package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestPaletteOxygenHueKeepsRedAndMaskedBackground(t *testing.T) {
	a, b := paletteTestInputs()
	p, err := preparePalette(a, b, false, true)
	if err != nil {
		t.Fatal(err)
	}
	s := paletteParams{Hydrogen: 1.3, Oxygen: 1.7, Sulfur: .04, Hue: 1, Auto: true, ProtectSulfur: true, ProtectOxygen: true}
	cyan := p.compose(s, 0)
	s.OxygenHue = 1
	blue := p.compose(s, 0)
	n := cyan.W * cyan.H
	var changed bool
	for i := 0; i < n; i++ {
		if cyan.Pix[i] != blue.Pix[i] || cyan.Pix[2*n+i] != blue.Pix[2*n+i] {
			t.Fatal("oxygen hue changed hydrogen/sulfur red or blue intensity")
		}
		if p.OxygenMask[i] == 0 && cyan.Pix[n+i] != blue.Pix[n+i] {
			t.Fatal("oxygen hue changed background outside mask")
		}
		if cyan.Pix[n+i] > blue.Pix[n+i]+1e-8 {
			changed = true
		}
	}
	if !changed {
		t.Fatal("oxygen hue did not reduce green")
	}
	// Preserve the red-plane H contribution exactly when S is added.
	s.Sulfur = 0
	h := p.compose(s, 0)
	for i := 0; i < n; i++ {
		if !paletteFinite(float64(h.Pix[i])) || math.Abs(float64(h.Pix[i])-(float64(a.Pix[i])-p.HBackground)*s.Hydrogen) > 1e-8 {
			t.Fatal("hydrogen was replaced")
		}
	}
}

// Explicit opt-in rendering of the user's IC 443 example with the production
// compositor. Normal tests do not write artifacts or require real inputs.
func TestPaletteRenderIC443(t *testing.T) {
	dest := os.Getenv("ASTROMOSAIC_RENDER_DIR")
	if dest == "" {
		t.Skip("set ASTROMOSAIC_RENDER_DIR and ASTROMOSAIC_RENDER_PARAMS to render examples")
	}
	var configs []paletteParams
	if err := json.Unmarshal([]byte(os.Getenv("ASTROMOSAIC_RENDER_PARAMS")), &configs); err != nil {
		t.Fatal(err)
	}
	if len(configs) == 0 {
		t.Fatal("no render settings")
	}
	dir := `C:\Users\Admin\Desktop\ASTRO\asiair\IC 443`
	a, err := loadImage(filepath.Join(dir, "HO.fit"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadImage(filepath.Join(dir, "SO.fit"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := preparePalette(a, b, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range configs {
		if s.SuppressGrid {
			p.GridHydrogen, p.Thumbnail.GridHydrogen, _ = paletteGridPlanes(p)
			break
		}
	}
	if err := os.MkdirAll(dest, 0700); err != nil {
		t.Fatal(err)
	}
	base := p.compose(paletteParams{Hydrogen: 1, Oxygen: 1, Auto: true, ProtectOxygen: true}, 1200)
	tones := imageTones(base)
	for i, s := range configs {
		small := p.compose(s, 1200)
		name := fmt.Sprintf("variant_%02d", i+1)
		if err := writeFITS(filepath.Join(dest, name+".fits"), small); err != nil {
			t.Fatal(err)
		}
		im := renderDisplay(small, image.Rect(0, 0, small.W, small.H), image.Pt(small.W, small.H), tones[prevLinked], nil)
		f, err := os.Create(filepath.Join(dest, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, im)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s H=%.3f O=%.3f S=%.3f O-hue=%.2f", name, s.Hydrogen, s.Oxygen, s.Sulfur, s.OxygenHue)
		if os.Getenv("ASTROMOSAIC_RENDER_FULL") != "" {
			full := p.compose(s, 0)
			if err := writeFITS(filepath.Join(dest, name+"_linear.fits"), full); err != nil {
				t.Fatal(err)
			}
			// Verify the delivered full-size image through the application's reader.
			reread, err := loadImage(filepath.Join(dest, name+"_linear.fits"))
			if err != nil {
				t.Fatal(err)
			}
			if reread.W != full.W || reread.H != full.H || reread.C != 3 {
				t.Fatal("rendered FITS dimensions changed")
			}
			// loadImage normalizes ADU-like floats above one by their common
			// maximum. The FITS itself retains the original float values.
			maxValue := float32(1)
			for _, v := range full.Pix {
				maxValue = max(maxValue, v)
			}
			scale := float32(1 / float64(maxValue))
			for k, v := range full.Pix {
				if reread.Pix[k] != v*scale {
					t.Fatalf("rendered pixel %d changed", k)
				}
			}
		}
	}
	settings := struct {
		Inputs       []string
		Region       image.Rectangle
		Calibrations [4]paletteCalibration
		Weights      [4]float64
		Variants     []paletteParams
		Note         string
	}{[]string{filepath.Join(dir, "HO.fit"), filepath.Join(dir, "SO.fit")}, p.Region, p.O, p.weights(configs[0]), configs, p.Note}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "settings.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestPaletteRealGallery(t *testing.T) {
	dest := os.Getenv("ASTROMOSAIC_GALLERY_EXPORT")
	if dest == "" {
		t.Skip("optional IC 443 palette gallery")
	}
	dir := `C:\Users\Admin\Desktop\ASTRO\asiair\IC 443`
	a, err := loadImage(filepath.Join(dir, "HO.fit"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := loadImage(filepath.Join(dir, "SO.fit"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := preparePalette(a, b, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0700); err != nil {
		t.Fatal(err)
	}
	base := paletteParams{Auto: true, ProtectSulfur: true, ProtectOxygen: true}
	tones := imageTones(p.compose(paletteParams{Hydrogen: 1, Oxygen: 1, Auto: true, ProtectOxygen: true}, 1200))
	for kind := 0; kind < 2; kind++ {
		for i := 0; i < paletteChoiceCount; i++ {
			mode, level := i, 4
			if kind == 1 {
				mode, level = 9, i
			}
			s := p.preset(mode, level, base)
			m := p.compose(s, 1200)
			for _, v := range m.Pix {
				if !paletteFinite(float64(v)) {
					t.Fatal("non-finite real gallery", kind, i)
				}
			}
			im := renderDisplay(m, image.Rect(0, 0, m.W, m.H), image.Pt(m.W, m.H), tones[prevLinked], nil)
			f, err := os.Create(filepath.Join(dest, fmt.Sprintf("gallery_%d_%02d.png", kind, i)))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, im)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%d/%d %s H %.3f O %.3f S %.3f", kind, i, paletteName(s.Mode), s.Hydrogen, s.Oxygen, s.Sulfur)
		}
	}
}
