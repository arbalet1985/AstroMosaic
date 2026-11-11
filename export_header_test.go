package main

import (
	"path/filepath"
	"strconv"
	"testing"
)

func TestExportDropsInheritedDisplayLimits(t *testing.T) {
	m := newImg(2, 2, 1)
	copy(m.Pix, []float32{-0.0001, 0, 0.0002, 0.25})
	m.Hdr["MIPS-FLO"] = "0.001663233"
	m.Hdr["MIPS-FHI"] = "0.1383993"
	m.Hdr["DATAMIN"] = "0"
	m.Hdr["DATAMAX"] = "1"
	m.Hdr["DATASUM"] = "12345"
	m.Hdr["CRVAL1"] = "10.5"
	for _, ext := range []string{".fits", ".xisf"} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "result"+ext)
			if err := saveImage(path, m, true); err != nil {
				t.Fatal(err)
			}
			r, err := loadImage(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"MIPS-FLO", "MIPS-FHI", "DATASUM"} {
				if _, exists := r.Hdr[k]; exists {
					t.Errorf("inherited %s survived export", k)
				}
			}
			for k, want := range map[string]float64{"DATAMIN": float64(m.Pix[0]), "DATAMAX": 0.25} {
				got, err := strconv.ParseFloat(r.Hdr[k], 64)
				if err != nil || got != want {
					t.Errorf("%s = %q, want %g", k, r.Hdr[k], want)
				}
			}
			for i, want := range m.Pix {
				if r.Pix[i] != want {
					t.Errorf("pixel %d: got %g want %g", i, r.Pix[i], want)
				}
			}
			if r.Hdr["CRVAL1"] != "10.5" {
				t.Error("lost WCS metadata")
			}
		})
	}
	if m.Hdr["MIPS-FLO"] != "0.001663233" || m.Hdr["DATAMIN"] != "0" || m.Hdr["DATASUM"] != "12345" {
		t.Fatal("export mutated the source header")
	}
}
