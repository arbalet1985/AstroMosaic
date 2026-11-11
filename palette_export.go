package main

import (
	"math"
	"strconv"
)

// paletteExportImage supplies a common RGB pedestal for viewers that mishandle
// negative float samples. Only palette exports opt in; the processing image and
// generic scientific-image writers retain their signed samples. No channel is
// clipped or independently balanced. AMSHIFT records the added value in the
// same units as the output samples (FITS/XISF).
func paletteExportImage(m *Img) (*Img, float32) {
	if m.C != 3 || m.Hdr["AMPALET"] != "1" {
		return m, 0
	}
	var lo float32
	for _, v := range m.Pix {
		if !math.IsInf(float64(v), 0) && v < lo {
			lo = v
		}
	}
	if lo >= 0 {
		return m, 0
	}
	shift := -lo
	out := *m
	out.Pix = make([]float32, len(m.Pix))
	for i, v := range m.Pix {
		out.Pix[i] = v + shift
	}
	out.Hdr = make(map[string]string, len(m.Hdr)+1)
	for k, v := range m.Hdr {
		out.Hdr[k] = v
	}
	out.Hdr["AMSHIFT"] = strconv.FormatFloat(float64(shift), 'g', -1, 32)
	return &out, shift
}
