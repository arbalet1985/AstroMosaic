package main

import (
	"math"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPaletteExportPedestalKeepsSignalAndInput(t *testing.T) {
	m := newImg(2, 1, 3)
	copy(m.Pix, []float32{-.125, .25, -.0625, .5, .125, 1.5})
	m.Hdr["AMPALET"] = "1"
	m.Hdr["CRVAL1"] = "23"
	out, shift := paletteExportImage(m)
	if shift != .125 || out == m || out.Hdr["AMSHIFT"] != "0.125" {
		t.Fatalf("missing pedestal: %g %v", shift, out.Hdr)
	}
	for i, want := range []float32{0, .375, .0625, .625, .25, 1.625} {
		if out.Pix[i] != want || out.Pix[i]-shift != m.Pix[i] {
			t.Fatalf("signal changed at %d: %g", i, out.Pix[i])
		}
	}
	// Every spatial contrast and every inter-channel difference is preserved.
	for i, v := range m.Pix {
		for j, w := range m.Pix {
			if out.Pix[i]-out.Pix[j] != v-w {
				t.Fatalf("relative signal changed: %d %d", i, j)
			}
		}
	}
	if m.Pix[0] != -.125 || m.Hdr["AMSHIFT"] != "" || out.Hdr["CRVAL1"] != "23" {
		t.Fatal("source changed or WCS lost")
	}
	again, extra := paletteExportImage(out)
	if again != out || extra != 0 {
		t.Fatal("pedestal was added twice")
	}
	delete(m.Hdr, "AMPALET")
	if got, shift := paletteExportImage(m); got != m || shift != 0 {
		t.Fatal("generic signed scientific image changed")
	}
}

func TestPaletteFloatSaveCompatibleWithNonnegativeViewers(t *testing.T) {
	m := newImg(2, 1, 3)
	copy(m.Pix, []float32{-.00016, .01, -.00007, .2, .00005, .8})
	m.Hdr["AMPALET"] = "1"
	m.Hdr["MIPS-FLO"] = ".0016"
	for _, ext := range []string{".fits", ".xisf", ".tif"} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "palette"+ext)
			if err := saveImage(path, m, true); err != nil {
				t.Fatal(err)
			}
			r, err := loadImage(path)
			if err != nil {
				t.Fatal(err)
			}
			shift := float64(-m.Pix[0])
			for i, v := range r.Pix {
				if v < 0 || math.Abs(float64(v)-shift-float64(m.Pix[i])) > 5e-8 {
					t.Fatalf("pixel %d changed or negative: %g", i, v)
				}
			}
			if ext != ".tif" {
				v, err := strconv.ParseFloat(r.Hdr["AMSHIFT"], 32)
				if err != nil || float32(v) != -m.Pix[0] || r.Hdr["MIPS-FLO"] != "" {
					t.Fatalf("invalid export metadata: %v", r.Hdr)
				}
				lo, _ := strconv.ParseFloat(r.Hdr["DATAMIN"], 64)
				hi, _ := strconv.ParseFloat(r.Hdr["DATAMAX"], 64)
				if lo != 0 || hi != float64(r.Pix[len(r.Pix)-1]) {
					t.Fatal("header does not describe the exported range")
				}
				// An already compatible FITS/XISF gets no second offset on re-save.
				path2 := filepath.Join(t.TempDir(), "again"+ext)
				if err := saveImage(path2, r, true); err != nil {
					t.Fatal(err)
				}
				r2, err := loadImage(path2)
				if err != nil {
					t.Fatal(err)
				}
				for i := range r.Pix {
					if r2.Pix[i] != r.Pix[i] {
						t.Fatal("re-save changed the pedestal")
					}
				}
			}
		})
	}
}
