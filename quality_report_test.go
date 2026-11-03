package main

import (
	"math"
	"os"
	"strings"
	"testing"
)

func TestQualityReportMeasuresAndExplainsEffect(t *testing.T) {
	before := haloTestImage()
	after, _, err := correctColorHalos(before, .75)
	if err != nil {
		t.Fatal(err)
	}
	r := measureQuality(before, after)
	if r.Changed == 0 || r.Changed >= r.W*r.H || len(r.Stars) != 4 {
		t.Fatalf("invalid coverage/sample: %d %d", r.Changed, len(r.Stars))
	}
	if median(r.values(1, 0, "color", false)) >= median(r.values(0, 0, "color", false)) {
		t.Fatal("report did not detect reduced RGB mismatch")
	}
	total := 0
	for _, n := range r.Area {
		total += n
	}
	if total != r.Changed {
		t.Fatal("area map lost pixels")
	}
	for c := 0; c < 3; c++ {
		if math.Abs(r.Flux[1][c]-r.Flux[0][c]) > 1e-5 {
			t.Fatal("reported flux inconsistent")
		}
	}
	if !strings.Contains(strings.Join(r.Recommendations, " "), "≥20") {
		t.Fatal("unreliable sample generated confident recommendations")
	}
	same := measureQuality(before, before)
	if same.Changed != 0 || !strings.Contains(same.Recommendations[0], "совпадает") {
		t.Fatal("unchanged result not explained")
	}
}

func TestQualityRecommendationsPrioritizeAlignmentAndNoise(t *testing.T) {
	r := qualityReport{W: 100, H: 100, C: 3, Changed: 500, Noise: [2][]float64{{1, 1, 1}, {1.3, 1, 1}}}
	for i := 0; i < 25; i++ {
		r.Stars = append(r.Stars, qualityStar{Width: [2][]float64{{4, 4, 4}, {4, 4, 4}}, Ellipticity: [2][]float64{{.05, .05, .05}, {.05, .05, .05}}, Shift: [2]float64{1, .5}, Color: [2]float64{.2, .2}})
	}
	text := strings.Join(qualityRecommendations(r), " ")
	if !strings.Contains(text, "совмещение каналов") || !strings.Contains(text, "шума выросла") || strings.Contains(text, "попробуйте «Цветные ореолы") {
		t.Fatal(text)
	}
}

func TestQualityRealImage(t *testing.T) {
	path := `C:\Users\Admin\Desktop\ASTRO\asiair\c33_mosaic\mosaic333.fits`
	if _, err := os.Stat(path); err != nil {
		t.Skip("real image absent")
	}
	before, err := loadImage(path)
	if err != nil {
		t.Fatal(err)
	}
	after, _, err := correctColorHalos(before, .75)
	if err != nil {
		t.Fatal(err)
	}
	r := measureQuality(before, after)
	area := 100 * float64(r.Changed) / float64(r.W*r.H)
	if area <= 0 || area > 2 || len(r.Stars) < 20 {
		t.Fatalf("coverage %.3f%%; stars %d", area, len(r.Stars))
	}
	t.Logf("Changed %.3f%%; common stars %d; changed neighbourhoods %d; RGB %.3f → %.3f", area, len(r.Stars), len(r.values(0, 0, "color", true)), median(r.values(0, 0, "color", false)), median(r.values(1, 0, "color", false)))
}
