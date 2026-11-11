package main

import (
	"encoding/binary"
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestForaxxMixAndFloatExportPreserveExtendedRange(t *testing.T) {
	a, b := newImg(16, 16, 3), newImg(16, 16, 3)
	n := a.W * a.H
	for i := 0; i < n; i++ {
		a.Pix[i] = .7
		if i%2 == 0 {
			a.Pix[i] = -.03
		}
		a.Pix[n+i], a.Pix[2*n+i] = -.01, -.01
		b.Pix[i], b.Pix[n+i], b.Pix[2*n+i] = .4, -.02, -.02
	}
	p := &palettePrepared{HO: a, SO: b, Region: image.Rect(0, 0, 16, 16), ForaxxMask: make([]float32, n), DynamicTone: [2]paletteFactorTone{{0, .25}, {0, .25}}}
	for i := range p.O {
		p.O[i] = paletteCalibration{Gain: 1, Noise: .001, Usable: true}
	}
	for i := range p.ForaxxMask {
		p.ForaxxMask[i] = float32(i%3) / 2
	}
	for _, mode := range []int{8, 9} {
		m := p.compose(paletteParams{Mode: mode, Hydrogen: 3, Oxygen: 1, Sulfur: .7, SourceSO: .5, ProtectForaxx: true}, 0)
		var below, above bool
		for _, v := range m.Pix {
			below = below || v < 0
			above = above || v > 1
		}
		if !below || !above {
			t.Fatal("compositor clipped float range", mode)
		}
		path := filepath.Join(t.TempDir(), "foraxx.fits")
		if err := writeFITS(path, m); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// This fixture's header fits one FITS block. Inspect raw float samples
		// so the reader's ADU normalization cannot hide a writer-side clamp.
		below, above = false, false
		for offset := 2880; offset < 2880+4*len(m.Pix); offset += 4 {
			v := math.Float32frombits(binary.BigEndian.Uint32(data[offset : offset+4]))
			below = below || v < 0
			above = above || v > 1
		}
		if !below || !above {
			t.Fatal("FITS writer clipped float range", mode)
		}
	}
}
