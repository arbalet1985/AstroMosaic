package main

import (
	"image/color"
	"testing"
)

func TestChromatismMissingData(t *testing.T) {
	for _, c := range []int{1, 3} {
		m := newImg(64, 64, c)
		r := measureChromatism(m)
		if r.W != m.W || r.H != m.H || r.Channels[0].Valid || r.Channels[1].Valid {
			t.Fatalf("empty or monochrome image produced a model: %+v", r)
		}
		if out, note := applyChannelAlignment(m, r); out != m || note != "" {
			t.Fatal("unmeasurable image was corrected")
		}
	}
}

func TestChromatismMapDirectionAndScale(t *testing.T) {
	r := chromatism{W: 720, H: 360}
	// Source red is one pixel to the right: correction arrows must point left.
	r.Channels[0] = channelAlignment{Map: Aff{1, 0, 1, 0, 1, 0}, MaxShift: 1, Valid: true}
	r.Channels[1] = channelAlignment{Map: identity, Valid: true}
	red, blue := chromatismImage(r, 0, 1), chromatismImage(r, 1, 1)
	if red.Bounds().Dx() != 720 || red.Bounds().Dy() != 360 {
		t.Fatal("map aspect ratio changed")
	}
	if red.RGBAAt(0, 0) != chromColor(1) || blue.RGBAAt(0, 0) != chromColor(0) {
		t.Fatal("heat maps do not share the displacement scale")
	}
	white := color.RGBA{255, 255, 255, 255}
	if red.RGBAAt(6, 30) != white || red.RGBAAt(54, 30) == white {
		t.Fatal("arrow must show correction towards green, opposite to sampling offset")
	}
}
