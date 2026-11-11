package main

import (
	"fmt"
	"image"

	"gioui.org/layout"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"golang.org/x/image/draw"
)

type paletteChooser struct {
	open, levels, back, close widget.Clickable
	choices                   [paletteChoiceCount]widget.Clickable
	images                    [paletteChoiceCount]paint.ImageOp
	params                    [paletteChoiceCount]paletteParams
	stage                     int // 0 image, 1 palette gallery, 2 mixing levels
	loading                   bool
	list                      widget.List
}

func (u *ui) handlePaletteChooser(gtx layout.Context) {
	p := &u.palette
	c := &p.chooser
	if c.open.Clicked(gtx) && p.prepared != nil && !u.busy {
		u.generatePaletteChoices(1)
	}
	if c.levels.Clicked(gtx) && p.prepared != nil && !u.busy {
		u.generatePaletteChoices(2)
	}
	if c.close.Clicked(gtx) && !u.busy {
		c.stage = 0
	}
	if c.back.Clicked(gtx) && !u.busy {
		u.generatePaletteChoices(1)
	}
	for i := range c.choices {
		if !c.choices[i].Clicked(gtx) || c.loading || u.busy || c.stage == 0 {
			continue
		}
		s := c.params[i]
		p.mode = s.Mode
		for k, v := range []float64{s.Hydrogen, s.Oxygen, s.Sulfur} {
			p.setStrength(k, v)
		}
		p.hue.Value, p.oxygenHue.Value = float32(s.Hue), float32(s.OxygenHue)
		p.maskView = 0
		p.dirty = true
		p.revision++
		if c.stage == 1 {
			u.generatePaletteChoices(2)
		} else {
			c.stage = 0
			u.status = fmt.Sprintf("%s · вариант %d/10 · H %.3f · O %.3f · S %.3f. Можно уточнить регуляторами.", paletteName(s.Mode), i+1, s.Hydrogen, s.Oxygen, s.Sulfur)
		}
	}
}

func (u *ui) generatePaletteChoices(stage int) {
	p := &u.palette
	c := &p.chooser
	prepared, base, tones, locked := p.prepared, p.settings(), p.tones, p.lockStretch.Value
	if prepared == nil {
		return
	}
	c.stage, c.loading = stage, true
	c.list = widget.List{}
	u.busy = true
	u.status = "Рассчитываю 10 вариантов предпросмотра…"
	go func() {
		var images [paletteChoiceCount]paint.ImageOp
		var params [paletteChoiceCount]paletteParams
		for i := range params {
			mode, level := base.Mode, i
			if stage == 1 {
				mode, level = i, 4
			}
			params[i] = prepared.preset(mode, level, base)
			m := prepared.compose(params[i], 1200)
			var im *image.RGBA
			if locked {
				im = renderDisplay(m, image.Rect(0, 0, m.W, m.H), image.Pt(m.W, m.H), tones[prevLinked], nil)
			} else {
				im = preview(m, 1200, prevLinked)
			}
			// Cards are small; keep only their display-sized textures.
			factor := max(1, (max(im.Bounds().Dx(), im.Bounds().Dy())+359)/360)
			tiny := image.NewRGBA(image.Rect(0, 0, max(1, im.Bounds().Dx()/factor), max(1, im.Bounds().Dy()/factor)))
			draw.ApproxBiLinear.Scale(tiny, tiny.Bounds(), im, im.Bounds(), draw.Src, nil)
			images[i] = paint.NewImageOp(tiny)
		}
		u.post(func() {
			u.busy = false
			c.loading = false
			if p.prepared != prepared {
				c.stage = 0
				return
			}
			c.images, c.params = images, params
			u.status = "Выберите палитру; затем один из 10 балансов смешивания."
		})
	}()
}

func (u *ui) paletteChooserView(gtx layout.Context) layout.Dimensions {
	c := &u.palette.chooser
	if c.loading {
		return layout.Center.Layout(gtx, material.Body1(u.th, "Создаю 10 миниатюр…").Layout)
	}
	columns := 3
	if gtx.Constraints.Max.X < gtx.Dp(720) {
		columns = 2
	}
	if gtx.Constraints.Max.X < gtx.Dp(400) {
		columns = 1
	}
	rows := (paletteChoiceCount + columns - 1) / columns
	title := "1 · Выберите палитру"
	if c.stage == 2 {
		title = "2 · " + paletteName(u.palette.mode) + " · выберите баланс (1 — водород, 10 — кислород / сера)"
	}
	c.list.Axis = layout.Vertical
	return layout.UniformInset(unit.Dp(12)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(material.Body1(u.th, title).Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				axis := layout.Horizontal
				if gtx.Constraints.Max.X < gtx.Dp(520) {
					axis = layout.Vertical
				}
				return layout.Flex{Axis: axis}.Layout(gtx, layout.Rigid(material.Button(u.th, &c.back, "← Палитры").Layout), layout.Rigid(layout.Spacer{Width: unit.Dp(8), Height: unit.Dp(8)}.Layout), layout.Rigid(material.Button(u.th, &c.close, "Вернуться к изображению").Layout))
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.List(u.th, &c.list).Layout(gtx, rows, func(gtx layout.Context, row int) layout.Dimensions {
					children := make([]layout.FlexChild, 0, columns)
					for col := 0; col < columns; col++ {
						i := row*columns + col
						children = append(children, layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							if i >= paletteChoiceCount {
								return layout.Dimensions{}
							}
							return layout.UniformInset(unit.Dp(5)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return c.choices[i].Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									label := paletteName(c.params[i].Mode)
									if c.stage == 2 {
										s := c.params[i]
										label = fmt.Sprintf("%d · H %.2f · O %.2f · S %.3f", i+1, s.Hydrogen, s.Oxygen, s.Sulfur)
									}
									return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, gtx.Dp(180))
											gtx.Constraints.Max.Y = gtx.Dp(180)
											return widget.Image{Src: c.images[i], Fit: widget.Contain, Position: layout.Center}.Layout(gtx)
										}),
										layout.Rigid(func(gtx layout.Context) layout.Dimensions {
											return layout.UniformInset(unit.Dp(6)).Layout(gtx, material.Body2(u.th, label).Layout)
										}))
								})
							})
						}))
					}
					return layout.Flex{}.Layout(gtx, children...)
				})
			}))
	})
}
