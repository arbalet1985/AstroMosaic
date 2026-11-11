package main

import (
	"fmt"
	"path/filepath"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	sectionHome = iota
	sectionMosaic
	sectionEnhance
	sectionPalettes
)

type workspaceState struct {
	section                  int
	home, enhance, openImage widget.Clickable
	choices                  [3]widget.Clickable
	list                     widget.List
}

func (u *ui) selectWorkspace(section int) {
	u.workspace.section = section
	u.grid.open = false
	u.sidebarState = sidebarState{}
	if section == sectionEnhance {
		u.view = 2
		u.sidebarState.initialized = true
		u.sidebarState.open[0], u.sidebarState.open[4] = true, true
		if u.result == nil {
			u.status = "Откройте изображение для улучшения"
		} else {
			u.status = "Текущий результат доступен для улучшения и сохранения"
		}
	} else if section == sectionMosaic {
		u.view = 0
		u.status = "Откройте плитки и соберите мозаику"
	} else if section == sectionPalettes {
		u.palette.init()
		u.status = "Откройте линейные RGB-изображения HO и SO"
	}
}

func (u *ui) handleWorkspace(gtx layout.Context) {
	if u.workspace.home.Clicked(gtx) && !u.busy {
		u.selectWorkspace(sectionHome)
	}
	for i := range u.workspace.choices {
		if u.workspace.choices[i].Clicked(gtx) && !u.busy {
			u.selectWorkspace(i + 1)
		}
	}
	if u.workspace.enhance.Clicked(gtx) && !u.busy {
		u.selectWorkspace(sectionEnhance)
	}
	if u.workspace.openImage.Clicked(gtx) && !u.busy {
		u.openEnhancementImage()
	}
}

func (u *ui) homeView(gtx layout.Context) layout.Dimensions {
	labels := []string{"1 · Собрать мозаику", "2 · Улучшения", "3 · Палитры"}
	descriptions := []string{"Сшивка плиток, совмещение и коррекция швов.", "Отдельное изображение или текущий результат: фон, шум, детали и деконволюция.", "Линейные HO + SO с цветной камеры: HOO с жёлтой серой."}
	u.workspace.list.Axis = layout.Vertical
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(680))
		return layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return material.List(u.th, &u.workspace.list).Layout(gtx, 4, func(gtx layout.Context, i int) layout.Dimensions {
				if i == 0 {
					return layout.Inset{Bottom: unit.Dp(24)}.Layout(gtx, material.H4(u.th, "AstroMosaic").Layout)
				}
				return layout.Inset{Bottom: unit.Dp(24)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							gtx.Constraints.Min.X = gtx.Constraints.Max.X
							return material.Button(u.th, &u.workspace.choices[i-1], labels[i-1]).Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
						layout.Rigid(material.Body1(u.th, descriptions[i-1]).Layout))
				})
			})
		})
	})
}

func (u *ui) openEnhancementImage() {
	u.busy = true
	go func() {
		f, err := u.expl.ChooseFile(inputExts...)
		if err != nil {
			u.post(func() { u.busy = false })
			return
		}
		path := f.(interface{ Name() string }).Name()
		f.Close()
		u.post(func() { u.status = "Загрузка " + filepath.Base(path) + "…" })
		m, err := loadImage(path)
		if err != nil {
			u.post(func() { u.busy = false; u.status = "Ошибка: " + err.Error() })
			return
		}
		pv := previews(m, previewSide)
		u.post(func() {
			u.busy = false
			u.history, u.baseline, u.baselineHistory, u.sel = nil, nil, nil, nil
			u.setResult(m, pv)
			u.view = 2
			u.stretch.Value, u.link.Value = true, true
			u.diagnostics = fieldDiagnostics{}
			u.status = fmt.Sprintf("%s: %d×%d, каналов %d. Выберите улучшение слева.", filepath.Base(path), m.W, m.H, m.C)
		})
	}()
}
