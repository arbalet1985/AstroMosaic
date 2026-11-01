package main

import (
	"fmt"
	"math"
	"path/filepath"

	"gioui.org/layout"
	"gioui.org/widget"
)

type flatState struct {
	open, clear widget.Clickable
	image       *Img
	name        string
}

// Master flats must already have bias/dark removed and match the optical setup.
// Normalisation is per channel so a colour flat does not impose its light source's colour.
func applyMasterFlat(m, flat *Img) (*Img, string, error) {
	if m.W != flat.W || m.H != flat.H || (flat.C != 1 && flat.C != m.C) {
		return nil, "", fmt.Errorf("master flat должен совпадать по размеру, иметь 1 или %d каналов", m.C)
	}
	n := m.W * m.H
	levels := make([]float64, flat.C)
	for c := 0; c < flat.C; c++ {
		var samples []float64
		for i := 0; i < n; i += max(1, n/200000) {
			v := float64(flat.Pix[c*n+i])
			if v > 0 && finitePoint(v, 0) {
				samples = append(samples, v)
			}
		}
		if len(samples) < n/max(1, n/200000)*9/10 {
			return nil, "", fmt.Errorf("master flat: недостаточно положительных данных")
		}
		levels[c] = median(samples)
		if levels[c] <= 1e-12 {
			return nil, "", fmt.Errorf("master flat: уровень около нуля")
		}
	}
	bad := make([]bool, n)
	badCount := 0
	lo, hi := math.Inf(1), 0.0
	for i := 0; i < n; i++ {
		for c := 0; c < flat.C; c++ {
			v := float64(flat.Pix[c*n+i]) / levels[c]
			if !finitePoint(v, 0) || v < .2 || v > 5 {
				bad[i] = true
			} else {
				lo, hi = math.Min(lo, 1/v), math.Max(hi, 1/v)
			}
		}
		if bad[i] {
			badCount++
		}
	}
	if badCount > n/100 {
		return nil, "", fmt.Errorf("master flat: >1%% недопустимых пикселей или усиление >5×")
	}
	out := newImg(m.W, m.H, m.C)
	out.Hdr, out.BottomUp = m.Hdr, m.BottomUp
	parallel(m.H, func(a, b int) {
		for y := a; y < b; y++ {
			for x := 0; x < m.W; x++ {
				i := y*m.W + x
				if bad[i] {
					continue
				}
				for c := 0; c < m.C; c++ {
					v := float64(m.Pix[c*n+i])
					if finitePoint(v, 0) {
						out.Pix[c*n+i] = float32(v * levels[min(c, flat.C-1)] / float64(flat.Pix[min(c, flat.C-1)*n+i]))
					}
				}
			}
		}
	})
	return out, fmt.Sprintf("master flat: gain %.2f…%.2f×; маска %d px", lo, hi, badCount), nil
}

func (u *ui) handleFlat(gtx layout.Context) {
	s := &u.flat
	if s.clear.Clicked(gtx) && !u.busy {
		s.image = nil
		s.name = ""
		u.status = "Master flat отключён; пересшейте результат"
	}
	if s.open.Clicked(gtx) && !u.busy {
		u.busy = true
		go func() {
			f, err := u.expl.ChooseFile(inputExts...)
			if err != nil {
				u.post(func() { u.busy = false })
				return
			}
			path := f.(interface{ Name() string }).Name()
			f.Close()
			m, err := loadImage(path)
			if err == nil {
				_, _, err = applyMasterFlat(m, m)
			}
			u.post(func() {
				u.busy = false
				if err != nil {
					u.status = "Master flat: " + err.Error()
					return
				}
				s.image = m
				s.name = filepath.Base(path)
				u.status = "Master flat: " + s.name + ". Только для исходников с той же оптикой, ориентацией и размером; flat должен быть калиброван bias/dark. Пересшейте результат."
			})
		}()
	}
}
