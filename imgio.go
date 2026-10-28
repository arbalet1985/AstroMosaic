package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
)

// inputExts lists the formats loadImage reads.
var inputExts = []string{".fits", ".fit", ".fts", ".xisf", ".tif", ".tiff", ".png", ".jpg", ".jpeg"}

// Img is a planar float image, values nominally 0..1, row 0 = top.
type Img struct {
	W, H, C  int
	Pix      []float32 // Pix[c*W*H + y*W + x]
	Hdr      map[string]string
	BottomUp bool // FITS WCS refers to bottom-up rows
}

func newImg(w, h, c int) *Img {
	return &Img{W: w, H: h, C: c, Pix: make([]float32, w*h*c), Hdr: map[string]string{}}
}

func (m *Img) at(c, x, y int) float32 { return m.Pix[c*m.W*m.H+y*m.W+x] }

func loadImage(path string) (*Img, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var m *Img
	switch strings.ToLower(filepath.Ext(path)) {
	case ".fit", ".fits", ".fts":
		m, err = readFITS(r)
	case ".xisf":
		m, err = readXISF(r)
	case ".png", ".jpg", ".jpeg":
		im, _, err := image.Decode(r)
		if err != nil {
			return nil, err
		}
		return fromImage(im), nil
	case ".tif", ".tiff":
		m, err = readTIFF(r)
	default:
		return nil, fmt.Errorf("unsupported format: %s", path)
	}
	if err != nil {
		return nil, err
	}
	if p := strings.ToUpper(m.Hdr["BAYERPAT"]); m.C == 1 && len(p) == 4 {
		m = debayer(m, p)
	}
	return m, nil
}

// debayer does bilinear CFA interpolation: each channel = mean of same-colour pixels in the 3×3 window.
// The pattern describes the first pixel stored in the file (FITS bottom-up files were flipped on read).
func debayer(m *Img, pat string) *Img {
	col := map[byte]int{'R': 0, 'G': 1, 'B': 2}
	xoff, _ := strconv.Atoi(m.Hdr["XBAYROFF"])
	yoff, _ := strconv.Atoi(m.Hdr["YBAYROFF"])
	cfa := func(x, y int) int {
		if m.BottomUp {
			y = m.H - 1 - y // back to file row order
		}
		return col[pat[((y+yoff)&1)*2+((x+xoff)&1)]]
	}
	out := newImg(m.W, m.H, 3)
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	n := m.W * m.H
	for y := 0; y < m.H; y++ {
		for x := 0; x < m.W; x++ {
			var sum [3]float32
			var cnt [3]int
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					xx, yy := x+dx, y+dy
					if xx < 0 || yy < 0 || xx >= m.W || yy >= m.H {
						continue
					}
					c := cfa(xx, yy)
					sum[c] += m.Pix[yy*m.W+xx]
					cnt[c]++
				}
			}
			for c := 0; c < 3; c++ {
				if cnt[c] > 0 {
					out.Pix[c*n+y*m.W+x] = sum[c] / float32(cnt[c])
				}
			}
		}
	}
	return out
}

func fromImage(im image.Image) *Img {
	b := im.Bounds()
	w, h := b.Dx(), b.Dy()
	switch g := im.(type) {
	case *image.Gray:
		m := newImg(w, h, 1)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				m.Pix[y*w+x] = float32(g.GrayAt(b.Min.X+x, b.Min.Y+y).Y) / 255
			}
		}
		return m
	case *image.Gray16:
		m := newImg(w, h, 1)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				m.Pix[y*w+x] = float32(g.Gray16At(b.Min.X+x, b.Min.Y+y).Y) / 65535
			}
		}
		return m
	}
	m := newImg(w, h, 3)
	n := w * h
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bb, _ := im.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := y*w + x
			m.Pix[i], m.Pix[n+i], m.Pix[2*n+i] = float32(r)/65535, float32(g)/65535, float32(bb)/65535
		}
	}
	return m
}

// ---------- FITS ----------

func readFITS(r io.Reader) (*Img, error) {
	hdr := map[string]string{}
	block := make([]byte, 2880)
	for done := false; !done; {
		if _, err := io.ReadFull(r, block); err != nil {
			return nil, fmt.Errorf("fits header: %w", err)
		}
		for i := 0; i < 2880; i += 80 {
			card := string(block[i : i+80])
			key := strings.TrimSpace(card[:8])
			if key == "END" {
				done = true
				break
			}
			if card[8:10] == "= " {
				hdr[key] = cardValue(card[10:])
			}
		}
	}
	bitpix, _ := strconv.Atoi(hdr["BITPIX"])
	w, _ := strconv.Atoi(hdr["NAXIS1"])
	h, _ := strconv.Atoi(hdr["NAXIS2"])
	c := 1
	if hdr["NAXIS"] == "3" {
		c, _ = strconv.Atoi(hdr["NAXIS3"])
	}
	if w <= 0 || h <= 0 || c <= 0 {
		return nil, errors.New("fits: no image in primary HDU")
	}
	bscale, bzero := 1.0, 0.0
	if v, ok := hdr["BSCALE"]; ok {
		bscale, _ = strconv.ParseFloat(v, 64)
	}
	if v, ok := hdr["BZERO"]; ok {
		bzero, _ = strconv.ParseFloat(v, 64)
	}
	var norm float64
	switch bitpix {
	case 8:
		norm = 255
	case 16:
		norm = 32767
		if bzero == 32768 {
			norm = 65535
		}
	case 32:
		norm = 2147483647
		if bzero == 2147483648 {
			norm = 4294967295
		}
	case -32, -64:
		norm = 0 // decided after reading
	default:
		return nil, fmt.Errorf("fits: BITPIX %d unsupported", bitpix)
	}
	m := newImg(w, h, c)
	m.Hdr = hdr
	m.BottomUp = !strings.EqualFold(hdr["ROWORDER"], "TOP-DOWN")
	bpp := abs(bitpix) / 8
	row := make([]byte, w*bpp)
	maxv := 0.0
	for ch := 0; ch < c; ch++ {
		for fy := 0; fy < h; fy++ {
			if _, err := io.ReadFull(r, row); err != nil {
				return nil, fmt.Errorf("fits data: %w", err)
			}
			y := fy
			if m.BottomUp {
				y = h - 1 - fy
			}
			out := m.Pix[ch*w*h+y*w:]
			for x := 0; x < w; x++ {
				b := row[x*bpp:]
				var v float64
				switch bitpix {
				case 8:
					v = float64(b[0])
				case 16:
					v = float64(int16(binary.BigEndian.Uint16(b)))
				case 32:
					v = float64(int32(binary.BigEndian.Uint32(b)))
				case -32:
					v = float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
				case -64:
					v = math.Float64frombits(binary.BigEndian.Uint64(b))
				}
				v = v*bscale + bzero
				if v > maxv {
					maxv = v
				}
				out[x] = float32(v)
			}
		}
	}
	if norm == 0 && maxv > 1 {
		norm = maxv // e.g. DSS-style 0..65535 floats
	}
	if norm > 1 {
		k := float32(1 / norm)
		for i := range m.Pix {
			m.Pix[i] *= k
		}
	}
	return m, nil
}

func cardValue(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "'") {
		if j := strings.Index(s[1:], "'"); j >= 0 {
			return strings.TrimSpace(s[1 : 1+j])
		}
	}
	if j := strings.Index(s, "/"); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// writeFITS writes 32-bit float, bottom-up (standard orientation).
func writeFITS(path string, m *Img) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	var hb bytes.Buffer
	card := func(k, v string) { fmt.Fprintf(&hb, "%-8s= %20s%50s", k, v, "") }
	card("SIMPLE", "T")
	card("BITPIX", "-32")
	if m.C == 1 {
		card("NAXIS", "2")
	} else {
		card("NAXIS", "3")
	}
	card("NAXIS1", strconv.Itoa(m.W))
	card("NAXIS2", strconv.Itoa(m.H))
	if m.C > 1 {
		card("NAXIS3", strconv.Itoa(m.C))
	}
	for k, v := range m.Hdr {
		if _, err := strconv.ParseFloat(v, 64); err == nil && len(k) <= 8 && !strings.HasPrefix(k, "NAXIS") &&
			k != "SIMPLE" && k != "BITPIX" && k != "BZERO" && k != "BSCALE" && k != "ROWORDER" && k != "EXTEND" {
			card(k, v)
		}
	}
	hb.WriteString(fmt.Sprintf("%-80s", "END"))
	for hb.Len()%2880 != 0 {
		hb.WriteByte(' ')
	}
	w.Write(hb.Bytes())
	buf := make([]byte, 4)
	for c := 0; c < m.C; c++ {
		for fy := 0; fy < m.H; fy++ {
			y := m.H - 1 - fy
			for x := 0; x < m.W; x++ {
				binary.BigEndian.PutUint32(buf, math.Float32bits(m.at(c, x, y)))
				w.Write(buf)
			}
		}
	}
	n := 4 * m.W * m.H * m.C
	w.Write(make([]byte, (2880-n%2880)%2880))
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

// ---------- XISF ----------

type xisfDoc struct {
	Image struct {
		Geometry     string `xml:"geometry,attr"`
		SampleFormat string `xml:"sampleFormat,attr"`
		Location     string `xml:"location,attr"`
		PixelStorage string `xml:"pixelStorage,attr"`
		ByteOrder    string `xml:"byteOrder,attr"`
		Compression  string `xml:"compression,attr"`
		Bounds       string `xml:"bounds,attr"`
		Keywords     []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"FITSKeyword"`
	} `xml:"Image"`
}

func readXISF(r io.Reader) (*Img, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < 16 || string(data[:8]) != "XISF0100" {
		return nil, errors.New("xisf: bad signature")
	}
	hl := int(binary.LittleEndian.Uint32(data[8:12]))
	var doc xisfDoc
	if err := xml.Unmarshal(data[16:16+hl], &doc); err != nil {
		return nil, fmt.Errorf("xisf header: %w", err)
	}
	im := doc.Image
	g := strings.Split(im.Geometry, ":")
	loc := strings.Split(im.Location, ":")
	if len(g) < 2 || len(loc) != 3 || loc[0] != "attachment" {
		return nil, errors.New("xisf: unsupported geometry/location")
	}
	w, _ := strconv.Atoi(g[0])
	h, _ := strconv.Atoi(g[1])
	c := 1
	if len(g) > 2 {
		c, _ = strconv.Atoi(g[2])
	}
	pos, _ := strconv.Atoi(loc[1])
	size, _ := strconv.Atoi(loc[2])
	if pos < 0 || size < 0 || pos+size > len(data) {
		return nil, errors.New("xisf: truncated data")
	}
	raw := data[pos : pos+size]
	if im.Compression != "" {
		if raw, err = xisfDecompress(raw, im.Compression); err != nil {
			return nil, err
		}
	}
	var bo binary.ByteOrder = binary.LittleEndian
	if im.ByteOrder == "big" {
		bo = binary.BigEndian
	}
	bpp := map[string]int{"UInt8": 1, "UInt16": 2, "UInt32": 4, "Float32": 4, "Float64": 8}[im.SampleFormat]
	if bpp == 0 {
		return nil, fmt.Errorf("xisf: sample format %q unsupported", im.SampleFormat)
	}
	if w <= 0 || h <= 0 || c <= 0 || w*h*c*bpp > len(raw) {
		return nil, errors.New("xisf: truncated data")
	}
	m := newImg(w, h, c)
	for _, k := range im.Keywords {
		m.Hdr[k.Name] = strings.TrimSpace(strings.Trim(strings.TrimSpace(k.Value), "'"))
	}
	normal := im.PixelStorage == "Normal" // interleaved
	n := w * h
	for i := 0; i < n*c; i++ {
		b := raw[i*bpp:]
		var v float32
		switch im.SampleFormat {
		case "UInt8":
			v = float32(b[0]) / 255
		case "UInt16":
			v = float32(bo.Uint16(b)) / 65535
		case "UInt32":
			v = float32(float64(bo.Uint32(b)) / 4294967295)
		case "Float32":
			v = math.Float32frombits(bo.Uint32(b))
		case "Float64":
			v = float32(math.Float64frombits(bo.Uint64(b)))
		}
		dst := i
		if normal {
			dst = (i%c)*n + i/c
		}
		m.Pix[dst] = v
	}
	if lo, hi, ok := strings.Cut(im.Bounds, ":"); ok && strings.HasPrefix(im.SampleFormat, "Float") {
		l, _ := strconv.ParseFloat(lo, 64)
		h, _ := strconv.ParseFloat(hi, 64)
		if h > l && (l != 0 || h != 1) {
			for i, v := range m.Pix {
				m.Pix[i] = float32((float64(v) - l) / (h - l))
			}
		}
	}
	return m, nil
}

// xisfDecompress handles "codec:size[:itemSize]" with zlib, lz4, lz4hc, zstd and their +sh (byte shuffle) forms.
func xisfDecompress(src []byte, spec string) ([]byte, error) {
	f := strings.Split(spec, ":")
	if len(f) < 2 {
		return nil, fmt.Errorf("xisf: bad compression %q", spec)
	}
	usize, _ := strconv.Atoi(f[1])
	item := 1
	if len(f) > 2 {
		item, _ = strconv.Atoi(f[2])
	}
	if usize <= 0 {
		return nil, fmt.Errorf("xisf: bad compression %q", spec)
	}
	codec, shuffled := strings.CutSuffix(f[0], "+sh")
	out := make([]byte, usize)
	switch codec {
	case "zlib":
		zr, err := zlib.NewReader(bytes.NewReader(src))
		if err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(zr, out); err != nil {
			return nil, fmt.Errorf("xisf zlib: %w", err)
		}
	case "lz4", "lz4hc":
		if n, err := lz4.UncompressBlock(src, out); err != nil || n != usize {
			return nil, fmt.Errorf("xisf lz4: %v", err)
		}
	case "zstd":
		d, err := zstd.NewReader(nil)
		if err != nil {
			return nil, err
		}
		defer d.Close()
		if out, err = d.DecodeAll(src, out[:0]); err != nil || len(out) != usize {
			return nil, fmt.Errorf("xisf zstd: %v", err)
		}
	default:
		return nil, fmt.Errorf("xisf: codec %q unsupported", codec)
	}
	if shuffled && item > 1 {
		n := usize / item
		u := make([]byte, usize)
		for b := 0; b < item; b++ {
			for i := 0; i < n; i++ {
				u[i*item+b] = out[b*n+i]
			}
		}
		copy(u[n*item:], out[n*item:])
		out = u
	}
	return out, nil
}

// writeXISF writes uncompressed planar Float32 with the numeric header keywords.
func writeXISF(path string, m *Img) error {
	cs := "Gray"
	if m.C == 3 {
		cs = "RGB"
	}
	var kw strings.Builder
	for _, k := range sortedKeys(m.Hdr) {
		if _, err := strconv.ParseFloat(m.Hdr[k], 64); err == nil && !strings.HasPrefix(k, "NAXIS") &&
			k != "SIMPLE" && k != "BITPIX" && k != "BZERO" && k != "BSCALE" && k != "ROWORDER" && k != "EXTEND" {
			fmt.Fprintf(&kw, `<FITSKeyword name="%s" value="%s" comment=""/>`, k, m.Hdr[k])
		}
	}
	size := 4 * m.W * m.H * m.C
	header := func(pos int) []byte {
		return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>`+
			`<xisf version="1.0" xmlns="http://www.pixinsight.com/xisf" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`+
			`<Image geometry="%d:%d:%d" sampleFormat="Float32" bounds="0:1" colorSpace="%s" location="attachment:%d:%d">%s</Image>`+
			`<Metadata><Property id="XISF:CreatorApplication" type="String">Astro Mosaic</Property></Metadata></xisf>`,
			m.W, m.H, m.C, cs, pos, size, kw.String()))
	}
	pos := 4096
	for len(header(pos))+16 > pos {
		pos += 4096
	}
	h := header(pos)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	w.WriteString("XISF0100")
	binary.Write(w, binary.LittleEndian, uint32(len(h)))
	w.Write(make([]byte, 4))
	w.Write(h)
	w.Write(make([]byte, pos-16-len(h)))
	buf := make([]byte, 4)
	for _, v := range m.Pix {
		binary.LittleEndian.PutUint32(buf, math.Float32bits(v))
		w.Write(buf)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}

func sortedKeys(h map[string]string) []string {
	k := make([]string, 0, len(h))
	for s := range h {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

// ---------- 16-bit outputs ----------

func to16(v float32) uint16 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 65535
	}
	return uint16(v*65535 + 0.5)
}

func writePNG16(path string, m *Img) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var im image.Image
	if m.C == 1 {
		g := image.NewGray16(image.Rect(0, 0, m.W, m.H))
		for i := 0; i < m.W*m.H; i++ {
			binary.BigEndian.PutUint16(g.Pix[2*i:], to16(m.Pix[i]))
		}
		im = g
	} else {
		g := image.NewRGBA64(image.Rect(0, 0, m.W, m.H)) // opaque -> png writes RGB without alpha
		n := m.W * m.H
		for i := 0; i < n; i++ {
			for c := 0; c < 3; c++ {
				binary.BigEndian.PutUint16(g.Pix[8*i+2*c:], to16(m.Pix[c*n+i]))
			}
			g.Pix[8*i+6], g.Pix[8*i+7] = 255, 255
		}
		im = g
	}
	if err := png.Encode(f, im); err != nil {
		return err
	}
	return f.Close()
}

func writeJPEG(path string, m *Img) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	im := image.NewRGBA(image.Rect(0, 0, m.W, m.H))
	n := m.W * m.H
	for i := 0; i < n; i++ {
		for c := 0; c < 3; c++ {
			im.Pix[4*i+c] = uint8(to16(m.Pix[min(c, m.C-1)*n+i]) >> 8)
		}
		im.Pix[4*i+3] = 255
	}
	if err := jpeg.Encode(f, im, &jpeg.Options{Quality: 95}); err != nil {
		return err
	}
	return f.Close()
}

// saveImage picks the format by extension; TIFF is 32-bit float when floatTIFF, else 16-bit.
func saveImage(path string, m *Img, floatTIFF bool) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".fit", ".fits", ".fts":
		return writeFITS(path, m)
	case ".xisf":
		return writeXISF(path, m)
	case ".tif", ".tiff":
		if floatTIFF {
			return writeTIFF(path, m, 32)
		}
		return writeTIFF(path, m, 16)
	case ".png":
		return writePNG16(path, m)
	case ".jpg", ".jpeg":
		return writeJPEG(path, m)
	}
	return fmt.Errorf("save: use .fits, .xisf, .tif, .png or .jpg")
}

// ---------- Autostretch (Siril MTF, port of VeraLux_Alchemy.py) ----------

func mtf(x, m, lo, hi float64) float64 {
	if hi-lo < 1e-9 {
		if x > lo {
			return 1
		}
		return 0
	}
	x = math.Min(math.Max((x-lo)/(hi-lo), 0), 1)
	den := (2*m-1)*x - m
	if den == 0 {
		return 0
	}
	return (m - 1) * x / den
}

// stretchParams returns Siril autostretch shadows and midtones per channel (Siril's
// find_[un]linked_midtones_balance: shadows at median − 2.8·MADN, background mapped to 0.25). Linked uses the
// channel means for all channels (keeps colour balance); unlinked neutralises each channel.
func stretchParams(m *Img, linked bool) (c0, mid []float64) {
	n := m.W * m.H
	stride := max(1, n/500000)
	meds, lows := make([]float64, m.C), make([]float64, m.C)
	for c := 0; c < m.C; c++ {
		s := make([]float64, 0, n/stride+1)
		for i := 0; i < n; i += stride {
			if v := m.Pix[c*n+i]; v != 0 { // as Siril: zero pixels (empty mosaic corners) are not data
				s = append(s, float64(v))
			}
		}
		if len(s) == 0 {
			s = append(s, 0)
		}
		med := median(s)
		for i := range s {
			s[i] = math.Abs(s[i] - med)
		}
		mad := median(s) * 1.4826
		if mad == 0 {
			mad = 0.001
		}
		meds[c], lows[c] = med, med-2.8*mad
	}
	if linked {
		var sl, sm float64
		for c := range meds {
			sl += lows[c] / float64(m.C)
			sm += meds[c] / float64(m.C)
		}
		for c := range meds {
			lows[c], meds[c] = sl, sm
		}
	}
	c0, mid = make([]float64, m.C), make([]float64, m.C)
	for c := range meds {
		c0[c] = math.Max(lows[c], 0)
		mid[c] = mtf(meds[c]-c0[c], 0.25, 0, 1)
	}
	return c0, mid
}

func median(s []float64) float64 {
	if len(s) == 0 {
		return 0
	}
	sort.Float64s(s)
	return s[len(s)/2]
}

// Preview modes.
const (
	prevLinear = iota
	prevLinked
	prevUnlinked
	nPrev
)

// previews returns the linear, linked-stretch and unlinked-stretch previews.
func previews(m *Img, maxSide int) [nPrev]*image.RGBA {
	return [nPrev]*image.RGBA{preview(m, maxSide, prevLinear), preview(m, maxSide, prevLinked), preview(m, maxSide, prevUnlinked)}
}

// preview downsamples to maxSide (nearest) and optionally autostretches.
func preview(m *Img, maxSide int, mode int) *image.RGBA {
	k := max(1, (max(m.W, m.H)+maxSide-1)/maxSide)
	pw, ph := m.W/k, m.H/k
	out := image.NewRGBA(image.Rect(0, 0, pw, ph))
	lut := make([][4096]uint8, m.C)
	c0, mid := make([]float64, m.C), make([]float64, m.C)
	if mode != prevLinear {
		c0, mid = stretchParams(m, mode == prevLinked)
	}
	for c := range lut {
		for i := range lut[c] {
			v := float64(i) / 4095
			if mode != prevLinear {
				v = mtf(v, mid[c], c0[c], 1)
			}
			lut[c][i] = uint8(v*255 + 0.5)
		}
	}
	q := func(c int, v float32) uint8 { return lut[c][int(math.Min(math.Max(float64(v), 0), 1)*4095)] }
	n := m.W * m.H
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			i := y*k*m.W + x*k
			o := out.PixOffset(x, y)
			r := q(0, m.Pix[i])
			g, b := r, r
			if m.C >= 3 {
				g, b = q(1, m.Pix[n+i]), q(2, m.Pix[2*n+i])
			}
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = r, g, b, 255
		}
	}
	return out
}
