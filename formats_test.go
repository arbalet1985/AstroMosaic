package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/tiff"
)

func TestFormats(t *testing.T) {
	dir := t.TempDir()
	m := newImg(9, 6, 3)
	for i := range m.Pix {
		m.Pix[i] = float32(i%37) / 37
	}
	m.Hdr["CRVAL1"] = "10.5"
	for _, c := range []struct {
		ext   string
		float bool
		tol   float64
	}{{".xisf", true, 1e-6}, {".tif", true, 1e-6}, {".tif", false, 1e-4}} {
		p := filepath.Join(dir, "y"+c.ext)
		if err := saveImage(p, m, c.float); err != nil {
			t.Fatal(c.ext, err)
		}
		r, err := loadImage(p)
		if err != nil {
			t.Fatal(c.ext, err)
		}
		if r.W != m.W || r.H != m.H || r.C != 3 {
			t.Fatalf("%s geometry %dx%dx%d", c.ext, r.W, r.H, r.C)
		}
		var e float64
		for i := range m.Pix {
			e = math.Max(e, math.Abs(float64(r.Pix[i]-m.Pix[i])))
		}
		if e > c.tol {
			t.Fatalf("%s (float=%v) max error %.5f", c.ext, c.float, e)
		}
		if c.ext == ".xisf" && r.Hdr["CRVAL1"] != "10.5" {
			t.Fatalf("xisf keywords %v", r.Hdr)
		}
	}

	// JPEG is lossy with chroma subsampling: check a flat colour
	flat := newImg(16, 16, 3)
	for i := range flat.Pix {
		flat.Pix[i] = []float32{0.8, 0.5, 0.2}[i/256]
	}
	jp := filepath.Join(dir, "f.jpg")
	if err := saveImage(jp, flat, false); err != nil {
		t.Fatal(err)
	}
	if r, err := loadImage(jp); err != nil || r.C != 3 || math.Abs(float64(r.at(0, 8, 8)-0.8)) > 0.02 || math.Abs(float64(r.at(2, 8, 8)-0.2)) > 0.02 {
		t.Fatalf("jpeg: %v", err)
	}

	// compressed XISF: zlib + byte shuffle, Float32
	vals := []float32{0, 0.25, 0.5, 1}
	raw := make([]byte, 16)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(raw[4*i:], math.Float32bits(v))
	}
	sh := make([]byte, 16)
	for i := 0; i < 4; i++ {
		for b := 0; b < 4; b++ {
			sh[b*4+i] = raw[i*4+b]
		}
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write(sh)
	zw.Close()
	h := fmt.Sprintf(`<xisf version="1.0"><Image geometry="4:1:1" sampleFormat="Float32" compression="zlib+sh:16:4" location="attachment:512:%d"/></xisf>`, z.Len())
	var buf bytes.Buffer
	buf.WriteString("XISF0100")
	binary.Write(&buf, binary.LittleEndian, uint32(len(h)))
	buf.Write(make([]byte, 4))
	buf.WriteString(h)
	buf.Write(make([]byte, 512-buf.Len()))
	buf.Write(z.Bytes())
	p := filepath.Join(dir, "z.xisf")
	os.WriteFile(p, buf.Bytes(), 0o644)
	r, err := loadImage(p)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vals {
		if r.Pix[i] != v {
			t.Fatalf("compressed xisf %v", r.Pix)
		}
	}

	// 16-bit gray TIFF, Deflate + horizontal predictor, written by x/image
	g := image.NewGray16(image.Rect(0, 0, 5, 3))
	for i := range g.Pix {
		g.Pix[i] = byte(i * 7)
	}
	var tb bytes.Buffer
	if err := tiff.Encode(&tb, g, &tiff.Options{Compression: tiff.Deflate, Predictor: true}); err != nil {
		t.Fatal(err)
	}
	r, err = readTIFF(&tb)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 5; x++ {
			if want := float32(g.Gray16At(x, y).Y) / 65535; math.Abs(float64(r.at(0, x, y)-want)) > 1e-6 {
				t.Fatalf("tiff deflate+predictor (%d,%d) %v want %v", x, y, r.at(0, x, y), want)
			}
		}
	}
}

func TestGeometry(t *testing.T) {
	m := newImg(6, 4, 1)
	for i := range m.Pix {
		m.Pix[i] = float32(i)
	}
	same := func(a, b *Img) bool {
		if a.W != b.W || a.H != b.H {
			return false
		}
		for i := range a.Pix {
			if math.Abs(float64(a.Pix[i]-b.Pix[i])) > 1e-4 {
				return false
			}
		}
		return true
	}
	if !same(rot90(rot90(rot90(rot90(m, false), false), false), false), m) || !same(rot90(rot90(m, true), false), m) {
		t.Fatal("rot90")
	}
	if !same(mirror(mirror(m, true), true), m) || mirror(m, false).at(0, 0, 0) != m.at(0, 0, 3) {
		t.Fatal("mirror")
	}
	if !same(rotateWhole(m, 90, interpLanczos4, true), rot90(m, false)) {
		t.Fatal("rotateWhole 90")
	}
	// axis-aligned frame on whole pixels = exact crop, for every interpolation
	for it := interp(0); it < nInterp; it++ {
		c := cutFrame(m, frame{CX: 3, CY: 2.5, W: 4, H: 3}, it, false)
		if c.W != 4 || c.H != 3 || c.at(0, 0, 0) != m.at(0, 1, 1) || c.at(0, 3, 2) != m.at(0, 4, 3) {
			t.Fatalf("crop %s: %v", interpNames[it], c.Pix)
		}
	}
	// a frame turned by 90° equals the quarter turn of that region: Angle>0 turns the image counter-clockwise
	sq := newImg(5, 5, 1)
	for i := range sq.Pix {
		sq.Pix[i] = float32(i)
	}
	if !same(cutFrame(sq, frame{CX: 2.5, CY: 2.5, W: 5, H: 5, Angle: 90}, interpBicubic, false), rot90(sq, false)) {
		t.Fatal("frame 90° != rot90 ccw")
	}
	// smooth image: rotating there and back stays close (interpolation quality)
	sm := newImg(64, 64, 1)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			sm.Pix[y*64+x] = float32(0.5 + 0.4*math.Sin(float64(x)/7)*math.Cos(float64(y)/9))
		}
	}
	back := cutFrame(cutFrame(sm, frame{CX: 32, CY: 32, W: 64, H: 64, Angle: 23}, interpLanczos4, true),
		frame{CX: 32, CY: 32, W: 64, H: 64, Angle: -23}, interpLanczos4, true)
	var e float64
	for y := 20; y < 44; y++ {
		for x := 20; x < 44; x++ {
			e = math.Max(e, math.Abs(float64(back.at(0, x, y)-sm.at(0, x, y))))
		}
	}
	if e > 0.002 {
		t.Fatalf("rotate round trip error %.4f", e)
	}
}
