package main

import (
	"fmt"
	"math"
)

type qualityStar struct {
	Width, Ellipticity [2][]float64
	Color, Shift       [2]float64
	Changed            bool
}
type qualityReport struct {
	W, H, C          int
	Changed, Invalid int
	Area             [1600]int
	Flux, Noise      [2][]float64
	Stars            []qualityStar
	Profiles         [2][][]float64
	Recommendations  []string
}

func measureQuality(before, after *Img) qualityReport {
	r := qualityReport{W: after.W, H: after.H, C: after.C}
	for t, m := range []*Img{before, after} {
		r.Flux[t] = make([]float64, m.C)
		r.Noise[t] = make([]float64, m.C)
		r.Profiles[t] = make([][]float64, m.C)
		for c := 0; c < m.C; c++ {
			r.Profiles[t][c] = make([]float64, 16)
			_, r.Noise[t][c], _ = restorationBackground(m.Pix[c*m.W*m.H:(c+1)*m.W*m.H], m.W)
		}
	}
	for y := 0; y < after.H; y++ {
		for x := 0; x < after.W; x++ {
			changed, invalid := false, false
			for c := 0; c < after.C; c++ {
				a, b := float64(before.at(c, x, y)), float64(after.at(c, x, y))
				if !finitePoint(a, b) {
					invalid = true
					continue
				}
				r.Flux[0][c] += a
				r.Flux[1][c] += b
				if a != b {
					changed = true
				}
			}
			if invalid {
				r.Invalid++
			}
			if changed {
				r.Changed++
				r.Area[min(39, y*40/after.H)*40+min(39, x*40/after.W)]++
			}
		}
	}
	stars := detectEmpiricalStars(before, true)
	for i, s := range stars {
		if len(r.Stars) >= 400 {
			break
		}
		if s.x < 32 || s.y < 32 || s.x >= float64(before.W-32) || s.y >= float64(before.H-32) {
			continue
		}
		isolated := true
		for j, q := range stars {
			if i != j && q.f > .05*s.f && math.Hypot(s.x-q.x, s.y-q.y) < 36 {
				isolated = false
				break
			}
		}
		if !isolated {
			continue
		}
		record := qualityStar{}
		var ps [2][][]float64
		valid := true
		for t, m := range []*Img{before, after} {
			record.Width[t] = make([]float64, m.C)
			record.Ellipticity[t] = make([]float64, m.C)
			ps[t] = make([][]float64, m.C)
			var centres [][2]float64
			for c := 0; c < m.C; c++ {
				shape, ok := fitStarPSF(m, s, c)
				p, good := measureProfileStar(m, c, s, 16)
				if !ok || !good {
					valid = false
					break
				}
				record.Width[t][c], record.Ellipticity[t][c] = shape.fwhm, shape.ellipticity
				centres = append(centres, [2]float64{shape.x, shape.y})
				bins := make([]float64, 16)
				sum := 0.
				for j, v := range p.psf {
					rad := math.Hypot(float64(j%33-16), float64(j/33-16))
					if rad < 16 {
						bins[int(rad)] += v
						sum += v
					}
				}
				if sum <= 0 {
					valid = false
					break
				}
				for j := range bins {
					bins[j] /= sum
				}
				ps[t][c] = bins
			}
			if !valid {
				break
			}
			if m.C == 3 {
				for c := 0; c < 3; c++ {
					for j, v := range ps[t][c] {
						record.Color[t] += math.Abs(v-ps[t][(c+1)%3][j]) / 3
					}
					record.Shift[t] = math.Max(record.Shift[t], math.Hypot(centres[c][0]-centres[1][0], centres[c][1]-centres[1][1]))
				}
			}
		}
		if !valid {
			continue
		}
		ix, iy := int(s.x), int(s.y)
		for c := 0; c < before.C; c++ {
			for y := iy - 22; y <= iy+22; y++ {
				for x := ix - 22; x <= ix+22; x++ {
					if before.at(c, x, y) != after.at(c, x, y) {
						record.Changed = true
					}
				}
			}
		}
		for t := 0; t < 2; t++ {
			for c := 0; c < before.C; c++ {
				for j, v := range ps[t][c] {
					r.Profiles[t][c][j] += v
				}
			}
		}
		r.Stars = append(r.Stars, record)
	}
	if len(r.Stars) > 0 {
		for t := 0; t < 2; t++ {
			for c := 0; c < r.C; c++ {
				for j := range r.Profiles[t][c] {
					r.Profiles[t][c][j] /= float64(len(r.Stars))
				}
			}
		}
	}
	r.Recommendations = qualityRecommendations(r)
	return r
}

func (r qualityReport) values(t, c int, metric string, affected bool) []float64 {
	var v []float64
	for _, s := range r.Stars {
		if affected && !s.Changed {
			continue
		}
		switch metric {
		case "width":
			v = append(v, s.Width[t][c])
		case "ellipse":
			v = append(v, s.Ellipticity[t][c])
		case "color":
			v = append(v, s.Color[t])
		case "shift":
			v = append(v, s.Shift[t])
		}
	}
	return v
}

func qualityRecommendations(r qualityReport) []string {
	var tips []string
	if r.Changed == 0 {
		tips = append(tips, "Результат совпадает с исходной версией: улучшения ещё не применены либо алгоритмы сохранили исходные пиксели. Рассчитанная проба действует только после «Применить».")
	}
	if r.Invalid > 0 {
		tips = append(tips, fmt.Sprintf("Есть нечисловые данные (%d пикселей): проверьте исходник и маски до дальнейшей обработки.", r.Invalid))
	}
	noisy := false
	for c := 0; c < r.C; c++ {
		if r.Noise[1][c] > 1.2*r.Noise[0][c] {
			noisy = true
			tips = append(tips, fmt.Sprintf("%s: оценка шума выросла в %.2f раза. Уменьшите силу/итерации последней коррекции и сравните результат; мелкая структура объекта тоже влияет на эту оценку.", channelName(c, r.C), r.Noise[1][c]/r.Noise[0][c]))
		}
	}
	if len(r.Stars) < 20 {
		return append(tips, fmt.Sprintf("Для надёжных рекомендаций по звёздам нужно ≥20 общих измерений, получено %d. Участок может быть насыщенным, плотным или слишком шумным; выводы по резкости и ореолам пока не делаются.", len(r.Stars)))
	}
	if r.C == 3 {
		shift := median(r.values(1, 0, "shift", false))
		if shift > .3 {
			tips = append(tips, fmt.Sprintf("Остаточное смещение RGB %.2f px: сначала проверьте карту хроматизма и включите совмещение каналов при обработке.", shift))
		} else {
			color := median(r.values(1, 0, "color", false))
			if color > .08 {
				tips = append(tips, fmt.Sprintf("Остаточное расхождение радиальных RGB-профилей %.3f при совмещённых центрах: попробуйте «Цветные ореолы…». Сравните пробу; насыщенные звёзды и отражения требуют отдельной оценки.", color))
			}
		}
	}
	width, ell := 0., 0.
	for c := 0; c < r.C; c++ {
		width = math.Max(width, median(r.values(1, c, "width", false)))
		ell = math.Max(ell, median(r.values(1, c, "ellipse", false)))
		oldWidth, newWidth := median(r.values(0, c, "width", false)), median(r.values(1, c, "width", false))
		if newWidth > oldWidth*1.1 {
			tips = append(tips, fmt.Sprintf("%s: ядра стали шире (%.2f → %.2f px). Это возможно при согласовании профилей; если целью была резкость, сравните при 100%% и ослабьте или отмените соответствующую коррекцию.", channelName(c, r.C), oldWidth, newWidth))
		}
	}
	if ell > .15 {
		tips = append(tips, fmt.Sprintf("Эллиптичность достигает %.2f: откройте диагностику поля/карту PSF. Эллиптическую модель или кому выбирайте после проверки формы; одной эллиптичности недостаточно для диагноза комы.", ell))
	}
	if width > 3 && !noisy {
		tips = append(tips, fmt.Sprintf("Ширина звёзд до %.2f px: можно проверить слабую деконволюцию с 3–5 итерациями. Цель — уменьшение ширины при стабильных потоке и шуме; крупная звезда сама по себе не доказывает дефокус.", width))
	}
	if float64(r.Changed)/float64(r.W*r.H) < .02 {
		tips = append(tips, "Изменено менее 2% площади: общий вид может почти не отличаться. Смотрите звёзды при 100% и метрики изменённых участков; это не основание повышать силу всех коррекций.")
	}
	tips = append(tips, "Градиент и виньетирование не диагностируются по звёздам. Если фон неоднороден, проверьте модель «Градиент фона…» с исключённой туманностью; master flat применяйте только при подходящем калибровочном кадре.")
	return tips
}
