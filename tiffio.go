package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"golang.org/x/image/tiff"
	"golang.org/x/image/tiff/lzw"
)

// readTIFF reads the first image of a baseline TIFF: 8/16/32-bit integer or 32/64-bit float samples,
// strips or tiles, chunky or planar, no/LZW/Deflate/PackBits compression, predictors 2 and 3.
// Anything else (palette, bilevel, odd bit depths) goes to x/image/tiff.
func readTIFF(r io.Reader) (*Img, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(data) < 8 {
		return nil, errors.New("tiff: too short")
	}
	var bo binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, errors.New("tiff: bad byte order")
	}
	if bo.Uint16(data[2:]) != 42 {
		return nil, errors.New("tiff: BigTIFF not supported")
	}
	ifd := int(bo.Uint32(data[4:]))
	if ifd+2 > len(data) {
		return nil, errors.New("tiff: bad IFD offset")
	}
	tags := map[uint16][]uint64{}
	n := int(bo.Uint16(data[ifd:]))
	for i := 0; i < n; i++ {
		e := ifd + 2 + 12*i
		if e+12 > len(data) {
			return nil, errors.New("tiff: truncated IFD")
		}
		id, typ, cnt := bo.Uint16(data[e:]), bo.Uint16(data[e+2:]), int(bo.Uint32(data[e+4:]))
		size := map[uint16]int{1: 1, 3: 2, 4: 4, 16: 8}[typ]
		if size == 0 || cnt <= 0 {
			continue // rationals, ascii etc. are not needed
		}
		p := e + 8
		if size*cnt > 4 {
			p = int(bo.Uint32(data[e+8:]))
		}
		if p < 0 || p+size*cnt > len(data) {
			return nil, errors.New("tiff: tag out of range")
		}
		v := make([]uint64, cnt)
		for k := range v {
			switch size {
			case 1:
				v[k] = uint64(data[p+k])
			case 2:
				v[k] = uint64(bo.Uint16(data[p+2*k:]))
			case 4:
				v[k] = uint64(bo.Uint32(data[p+4*k:]))
			case 8:
				v[k] = bo.Uint64(data[p+8*k:])
			}
		}
		tags[id] = v
	}
	get := func(id uint16, def uint64) uint64 {
		if v := tags[id]; len(v) > 0 {
			return v[0]
		}
		return def
	}
	w, h := int(get(256, 0)), int(get(257, 0))
	spp := int(get(277, 1))
	bits := int(get(258, 1))
	format := get(339, 1)
	photo := get(262, 1)
	comp := get(259, 1)
	planar := get(284, 1) == 2
	pred := get(317, 1)
	bps := bits / 8
	known := (bits == 8 || bits == 16 || bits == 32 || bits == 64) && (format == 1 || format == 2 || format == 3) &&
		(format != 3 || bits >= 32) && photo <= 2 &&
		(comp == 1 || comp == 5 || comp == 8 || comp == 32946 || comp == 32773)
	if !known {
		im, err := tiff.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		return fromImage(im), nil
	}
	if w <= 0 || h <= 0 || spp <= 0 {
		return nil, errors.New("tiff: bad geometry")
	}

	// chunk layout
	cw, ch := w, int(get(278, uint64(h)))
	offs, cnts := tags[273], tags[279]
	if tw := int(get(322, 0)); tw > 0 {
		cw, ch = tw, int(get(323, 0))
		offs, cnts = tags[324], tags[325]
	}
	if ch <= 0 || ch > h {
		ch = h
	}
	across, down := (w+cw-1)/cw, (h+ch-1)/ch
	perPlane := across * down
	sppc := spp // samples per pixel inside one chunk
	if planar {
		sppc = 1
	}
	need := perPlane
	if planar {
		need *= spp
	}
	if len(offs) < need || len(cnts) < need {
		return nil, errors.New("tiff: missing strip/tile offsets")
	}

	C := 1
	if spp >= 3 && photo == 2 {
		C = 3
	}
	m := newImg(w, h, C)
	np := w * h
	maxv := 0.0
	rowLen := cw * sppc * bps
	for k := 0; k < need; k++ {
		o, c := int(offs[k]), int(cnts[k])
		if o < 0 || o+c > len(data) {
			return nil, errors.New("tiff: chunk out of range")
		}
		buf, err := tiffDecompress(data[o:o+c], comp, rowLen*ch)
		if err != nil {
			return nil, err
		}
		plane, idx := 0, k
		if planar {
			plane, idx = k/perPlane, k%perPlane
		}
		x0, y0 := (idx%across)*cw, (idx/across)*ch
		for ry := 0; ry < ch && y0+ry < h; ry++ {
			row := buf[min(len(buf), ry*rowLen):min(len(buf), (ry+1)*rowLen)]
			if len(row) < rowLen {
				break // short last strip
			}
			tiffUnpredict(row, pred, bo, sppc, bps)
			for rx := 0; rx < cw && x0+rx < w; rx++ {
				for s := 0; s < sppc; s++ {
					sample := plane + s
					if sample >= C {
						continue
					}
					b := row[(rx*sppc+s)*bps:]
					var v float64
					if pred == 3 { // floating-point predictor leaves big-endian values
						switch bits {
						case 32:
							v = float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
						case 64:
							v = math.Float64frombits(binary.BigEndian.Uint64(b))
						}
					} else {
						v = tiffSample(b, bo, bits, format)
					}
					if format != 3 && photo == 0 {
						v = 1 - v
					}
					if v > maxv {
						maxv = v
					}
					m.Pix[sample*np+(y0+ry)*w+x0+rx] = float32(v)
				}
			}
		}
	}
	if format == 3 && maxv > 1 {
		k := float32(1 / maxv) // e.g. 0..65535 floats
		for i := range m.Pix {
			m.Pix[i] *= k
		}
	}
	return m, nil
}

// tiffSample converts one integer or float sample to 0..1 (floats are returned as stored).
func tiffSample(b []byte, bo binary.ByteOrder, bits int, format uint64) float64 {
	switch {
	case format == 3 && bits == 32:
		return float64(math.Float32frombits(bo.Uint32(b)))
	case format == 3:
		return math.Float64frombits(bo.Uint64(b))
	case bits == 8 && format == 2:
		return (float64(int8(b[0])) + 128) / 255
	case bits == 8:
		return float64(b[0]) / 255
	case bits == 16 && format == 2:
		return (float64(int16(bo.Uint16(b))) + 32768) / 65535
	case bits == 16:
		return float64(bo.Uint16(b)) / 65535
	case bits == 32 && format == 2:
		return (float64(int32(bo.Uint32(b))) + 2147483648) / 4294967295
	case bits == 32:
		return float64(bo.Uint32(b)) / 4294967295
	}
	return float64(int64(bo.Uint64(b))) / math.MaxInt64
}

func tiffDecompress(src []byte, comp uint64, want int) ([]byte, error) {
	switch comp {
	case 1:
		return src, nil
	case 5:
		return io.ReadAll(io.LimitReader(lzw.NewReader(bytes.NewReader(src), lzw.MSB, 8), int64(want)))
	case 8, 32946:
		zr, err := zlib.NewReader(bytes.NewReader(src))
		if err != nil {
			return nil, err
		}
		return io.ReadAll(io.LimitReader(zr, int64(want)))
	case 32773: // PackBits
		out := make([]byte, 0, want)
		for i := 0; i < len(src) && len(out) < want; {
			n := int(int8(src[i]))
			i++
			switch {
			case n >= 0:
				if i+n+1 > len(src) {
					return nil, errors.New("tiff: bad PackBits run")
				}
				out = append(out, src[i:i+n+1]...)
				i += n + 1
			case n != -128 && i < len(src):
				for k := 0; k < 1-n; k++ {
					out = append(out, src[i])
				}
				i++
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("tiff: compression %d unsupported", comp)
}

// tiffUnpredict undoes horizontal differencing in place: integer (2) or floating-point byte-plane (3).
func tiffUnpredict(row []byte, pred uint64, bo binary.ByteOrder, spp, bps int) {
	switch pred {
	case 2:
		n := len(row) / bps
		for i := spp; i < n; i++ {
			a, p := row[i*bps:], row[(i-spp)*bps:]
			switch bps {
			case 1:
				a[0] += p[0]
			case 2:
				bo.PutUint16(a, bo.Uint16(a)+bo.Uint16(p))
			case 4:
				bo.PutUint32(a, bo.Uint32(a)+bo.Uint32(p))
			case 8:
				bo.PutUint64(a, bo.Uint64(a)+bo.Uint64(p))
			}
		}
	case 3:
		for i := spp; i < len(row); i++ {
			row[i] += row[i-spp]
		}
		n := len(row) / bps
		tmp := make([]byte, len(row))
		for i := 0; i < n; i++ {
			for b := 0; b < bps; b++ {
				tmp[i*bps+b] = row[b*n+i]
			}
		}
		copy(row, tmp)
	}
}

// writeTIFF writes an uncompressed chunky TIFF without alpha: 16-bit integer or 32-bit float.
func writeTIFF(path string, m *Img, bits int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	c := m.C
	if c != 1 {
		c = 3
	}
	bps := bits / 8
	dataLen := m.W * m.H * c * bps
	le := binary.LittleEndian
	const nTags = 11
	dataOff := uint32(8)
	ifdOff := dataOff + uint32(dataLen)
	extraOff := ifdOff + 2 + nTags*12 + 4 // per-sample arrays for RGB: BitsPerSample, SampleFormat
	w.Write([]byte{'I', 'I', 42, 0})
	binary.Write(w, le, ifdOff)
	n := m.W * m.H
	buf := make([]byte, bps)
	for i := 0; i < n; i++ {
		for ch := 0; ch < c; ch++ {
			v := m.Pix[min(ch, m.C-1)*n+i]
			if bits == 32 {
				le.PutUint32(buf, math.Float32bits(v))
			} else {
				le.PutUint16(buf, to16(v))
			}
			w.Write(buf)
		}
	}
	tag := func(id, typ uint16, count, val uint32) {
		binary.Write(w, le, id)
		binary.Write(w, le, typ)
		binary.Write(w, le, count)
		binary.Write(w, le, val)
	}
	sf := uint32(1)
	if bits == 32 {
		sf = 3
	}
	binary.Write(w, le, uint16(nTags))
	tag(256, 4, 1, uint32(m.W))
	tag(257, 4, 1, uint32(m.H))
	if c == 1 {
		tag(258, 3, 1, uint32(bits))
	} else {
		tag(258, 3, 3, extraOff)
	}
	tag(259, 3, 1, 1) // no compression
	photometric := uint32(1)
	if c == 3 {
		photometric = 2
	}
	tag(262, 3, 1, photometric)
	tag(273, 4, 1, dataOff)
	tag(277, 3, 1, uint32(c))
	tag(278, 4, 1, uint32(m.H))
	tag(279, 4, 1, uint32(dataLen))
	tag(284, 3, 1, 1) // chunky
	if c == 1 {
		tag(339, 3, 1, sf)
	} else {
		tag(339, 3, 3, extraOff+6)
	}
	binary.Write(w, le, uint32(0))
	if c == 3 {
		binary.Write(w, le, [3]uint16{uint16(bits), uint16(bits), uint16(bits)})
		binary.Write(w, le, [3]uint16{uint16(sf), uint16(sf), uint16(sf)})
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return f.Close()
}
