package main

import (
	"math"
	"math/rand"
	"os"
	"testing"
)

// rgbStars renders a 3-channel tile whose red channel sees the sky through rmap (lateral colour).
func rgbStars(w, h int, rmap Aff) (*Img, [][3]float64) {
	rng := rand.New(rand.NewSource(7))
	var stars [][3]float64
	for i := 0; i < 600; i++ {
		stars = append(stars, [3]float64{8 + rng.Float64()*float64(w-16), 8 + rng.Float64()*float64(h-16), 0.1 + 0.5*rng.Float64()})
	}
	m := newImg(w, h, 3)
	for c := 0; c < 3; c++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				X, Y := float64(x), float64(y)
				if c == 0 {
					X, Y = rmap.apply(X, Y)
				}
				m.Pix[c*w*h+y*w+x] = sky(X, Y, stars) + float32(0.002*rng.NormFloat64())
			}
		}
	}
	return m, stars
}

// chanOffset is the largest median R−G centroid offset over a 3×3 grid of zones: lateral colour
// shows as a systematic shift per zone, while single centroids are noisy.
func chanOffset(m *Img) float64 {
	var dx, dy [3][3][]float64
	for _, s := range detectStars(m) {
		rx, ry, ok1 := centroid(m, 0, s.x, s.y)
		gx, gy, ok2 := centroid(m, 1, s.x, s.y)
		if ok1 && ok2 {
			i, j := min(int(3*s.y/float64(m.H)), 2), min(int(3*s.x/float64(m.W)), 2)
			dx[i][j], dy[i][j] = append(dx[i][j], rx-gx), append(dy[i][j], ry-gy)
		}
	}
	var worst float64
	for i := range dx {
		for j := range dx[i] {
			if len(dx[i][j]) >= 10 {
				worst = math.Max(worst, math.Hypot(median(dx[i][j]), median(dy[i][j])))
			}
		}
	}
	return worst
}

func TestAlignChannels(t *testing.T) {
	// red magnified by 0.4% about the centre and shifted (0.5, −0.3) px
	const k = 1.004
	rmap := Aff{1 / k, 0, 300*(1-1/k) - 0.5, 0, 1 / k, 250*(1-1/k) + 0.3}
	m, _ := rgbStars(600, 500, rmap)
	before := chanOffset(m)
	out, note := alignChannels(m)
	after := chanOffset(out)
	t.Log(note, before, after)
	if out == m || after > 0.1 || after > before/5 {
		t.Fatalf("R−G offset %.3f px → %.3f px (%s)", before, after, note)
	}
	// already aligned: left alone
	if same, _ := alignChannels(out); same != out {
		t.Fatal("aligned tile was resampled again")
	}
}

// TestAlignChannelsReal checks the user's tiles (skipped when absent): red is offset from green by up
// to ~1.6 px towards the corners; after alignment the stars' channels must coincide.
func TestAlignChannelsReal(t *testing.T) {
	m, err := loadImage(`C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\1-1.xisf`)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	before := chanOffset(m)
	out, note := alignChannels(m)
	after := chanOffset(out)
	t.Logf("%s: median R−G offset %.3f → %.3f px", note, before, after)
	if after > 0.15 || after > before/3 {
		t.Fatalf("offset %.3f → %.3f px", before, after)
	}
}
