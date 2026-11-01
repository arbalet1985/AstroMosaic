package main

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestXISFPrimaryBeforeCropMask(t *testing.T) {
	header := `<xisf><Image id="main" geometry="2:2:3" sampleFormat="Float32" bounds="0:1" location="attachment:4096:48"><FITSKeyword name="OBJECT" value="'primary'"/><FITSKeyword name="BAYERPAT" value="'RGGB'"/></Image><Image id="crop_mask" geometry="3:3:1" sampleFormat="UInt8" location="attachment:8192:9"><FITSKeyword name="MASK" value="1"/></Image></xisf>`
	data := make([]byte, 8201)
	copy(data, "XISF0100")
	binary.LittleEndian.PutUint32(data[8:], uint32(len(header)))
	copy(data[16:], header)
	for i := 0; i < 12; i++ {
		binary.LittleEndian.PutUint32(data[4096+i*4:], math.Float32bits(float32(i+1)*.001))
	}
	for i := 8192; i < len(data); i++ {
		data[i] = 255
	}
	m, err := readXISF(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if m.W != 2 || m.H != 2 || m.C != 3 || m.Hdr["OBJECT"] != "primary" || m.Hdr["MASK"] != "" {
		t.Fatalf("primary image/metadata replaced by auxiliary: %+v", m)
	}
	for i, v := range m.Pix {
		if math.Abs(float64(v)-float64(i+1)*.001) > 1e-8 {
			t.Fatal("wrong primary image pixels")
		}
	}
	// Loading must also avoid treating the auxiliary mono mask as a CFA image.
	path := filepath.Join(t.TempDir(), "with-mask.xisf")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadImage(path)
	if err != nil || loaded.W != 2 || loaded.C != 3 {
		t.Fatalf("loadImage: %v, %+v", err, loaded)
	}
}

func TestXISFTruncatedXML(t *testing.T) {
	data := make([]byte, 16)
	copy(data, "XISF0100")
	binary.LittleEndian.PutUint32(data[8:], 1000)
	if _, err := readXISF(bytes.NewReader(data)); err == nil {
		t.Fatal("truncated XML accepted")
	}
}

func TestFaintFloatPreviewPrecision(t *testing.T) {
	m := newImg(256, 16, 1)
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			m.Pix[y*m.W+x] = .0001 + float32(x)*.002/255
		}
	}
	pv := preview(m, 256, prevLinked)
	levels := map[uint8]bool{}
	for x := 0; x < m.W; x++ {
		levels[pv.RGBAAt(x, 0).R] = true
	}
	if len(levels) < 60 {
		t.Fatalf("faint float data quantised before stretch: only %d levels", len(levels))
	}
	if background := pv.RGBAAt(128, 0).R; background < 60 || background > 68 {
		t.Fatalf("median not mapped near 0.25: %d", background)
	}
	m.Pix[3] = float32(math.NaN())
	m.Pix[5] = float32(math.Inf(1))
	if pv := preview(m, 256, prevLinked); pv.RGBAAt(3, 0).R != 0 || pv.RGBAAt(5, 0).R != 0 {
		t.Fatal("nonfinite preview samples not masked")
	}
}

func TestXISFRealMasterLightPrimary(t *testing.T) {
	if testing.Short() {
		t.Skip("real master light")
	}
	path := `C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\masterLight_BIN-1_3008x3008_EXPOSURE-300.00s_FILTER-ho7n_RGB_drizzle_2x_autocrop.xisf`
	m, err := loadImage(path)
	if os.IsNotExist(err) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if m.W != 5820 || m.H != 5842 || m.C != 3 {
		t.Fatalf("loaded crop mask instead of RGB master: %dx%dx%d", m.W, m.H, m.C)
	}
	pv := preview(m, 1000, prevLinked)
	var sum float64
	levels := map[uint8]bool{}
	for y := 0; y < pv.Rect.Dy(); y++ {
		for x := 0; x < pv.Rect.Dx(); x++ {
			p := pv.RGBAAt(x, y)
			sum += (float64(p.R) + float64(p.G) + float64(p.B)) / 3
			levels[p.R], levels[p.G], levels[p.B] = true, true, true
		}
	}
	mean := sum / float64(pv.Rect.Dx()*pv.Rect.Dy())
	t.Logf("primary %dx%dx%d; stretched RGB mean %.1f/255, %d levels", m.W, m.H, m.C, mean, len(levels))
	if mean < 10 || mean > 220 || len(levels) < 100 {
		t.Fatal("real master light preview has no useful stretch")
	}
	if path := os.Getenv("MASTERLIGHT_SHOT"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, pv); err != nil {
			t.Fatal(err)
		}
		neutral, err := os.Create(path + ".unlinked.png")
		if err != nil {
			t.Fatal(err)
		}
		defer neutral.Close()
		if err := png.Encode(neutral, preview(m, 1000, prevUnlinked)); err != nil {
			t.Fatal(err)
		}
	}
}
