package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/font/gofont"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"gioui.org/x/explorer"
)

const previewSide = 2000

type cell = [2]int // row, col

type cellPair struct{ A, B cell } // A is left of / above B

type tileState struct {
	path    string
	img     *Img
	prev    [nPrev]paint.ImageOp // linear, linked stretch, unlinked stretch
	prevW   int
	hasWCS  bool
	open    widget.Clickable
	clear   widget.Clickable
	loading bool
}

// panel is one zoomable image view in the points editor.
type panel struct {
	zoom    float32
	pan     f32.Point
	needFit bool
	press   f32.Point
	last    f32.Point
	button  pointer.Buttons
	drag    bool
	own     bool // the current left drag belongs to dragHooks, not to panning
}

// dragHooks lets a view take over left-button drags (drawing a frame); panning then uses the
// right or middle button.
type dragHooks struct {
	press func(pos f32.Point) bool // true: this drag is ours
	drag  func(pos f32.Point)
}

// saveFormat is one entry of the format selector; the file extension decides the writer.
type saveFormat struct {
	label, ext string
	float      bool // TIFF: 32-bit float instead of 16-bit
}

var saveFormats = []saveFormat{
	{"FITS 32", "fits", true}, {"XISF 32", "xisf", true}, {"TIFF 16", "tif", false},
	{"TIFF 32", "tif", true}, {"PNG 16", "png", false}, {"JPEG", "jpg", false},
}

// frame aspect presets; 0 = free, -1 = as the image.
var ratios = []struct {
	label string
	r     float64
}{{"свободно", 0}, {"как изображение", -1}, {"1:1", 1}, {"3:2", 1.5}, {"2:3", 2.0 / 3}, {"4:3", 4.0 / 3}, {"3:4", 0.75}, {"16:9", 16.0 / 9}, {"9:16", 9.0 / 16}}

// selDrag is the frame edit in progress.
type selDrag struct {
	mode   int // 1 new, 2 move, 3 resize
	anchor [2]float64
	start  frame
	edges  [4]bool // left, right, top, bottom
}

type ui struct {
	w     *app.Window
	th    *material.Theme
	expl  *explorer.Explorer
	tasks chan func()

	overlapSl                  widget.Float // overlap kept round the seams, 0..maxOverlap %
	overlapDrag                bool         // the slider was being dragged last frame
	stitchBtn, gridBtn         widget.Clickable
	grid                       gridPicker // Word-style rows × cols chooser under gridBtn
	saveBtn, clearPts          widget.Clickable
	viewGrid, viewPts, viewRes widget.Clickable
	openAll, weakBtn           widget.Clickable
	clearAll                   widget.Clickable // remove all tiles
	chAlign, lfit, bgEq        widget.Bool      // stitch aligns colour channels / applies LinearFit / evens out backgrounds
	more                       bool             // «Дополнительно» row shown
	weak                       []cellPair       // neighbours the stars could not tie together
	stretch, link              widget.Bool
	format                     int // index in saveFormats: the type last picked in the save dialog
	view                       int // 0 grid, 1 points, 2 result

	rows, cols int
	tiles      map[cell]*tileState
	points     map[cellPair][]PointPair
	pending    [2]*[2]float64

	pairBtns map[cellPair]*widget.Clickable
	pairList widget.List
	cur      *cellPair
	panels   [2]panel

	result   *Img
	resPrev  [nPrev]paint.ImageOp
	resPrevW int
	resPanel panel
	history  []*Img // undo for geometry operations
	busy     bool
	status   string

	// frame / rotation (Siril-style) on the result
	sel                                    *frame
	sd                                     selDrag
	angleEd                                widget.Editor
	angleSl                                widget.Float // -180..180° as 0..1
	angleDn, angleUp                       widget.Clickable
	ratio                                  int
	it                                     interp
	clamp                                  widget.Bool
	ratioBtn, interpBtn, cropBtn, rotBtn   widget.Clickable
	rotL, rotR, flipH, flipV, clrSel, undo widget.Clickable
}

func main() {
	go func() {
		w := new(app.Window)
		w.Option(app.Title("Astro Mosaic"), app.Size(unit.Dp(1200), unit.Dp(700)), app.Maximized.Option())
		th := material.NewTheme()
		th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
		th.TextSize = 14
		u := &ui{w: w, th: th, expl: explorer.NewExplorer(w), tasks: make(chan func(), 64),
			rows: 2, cols: 2, tiles: map[cell]*tileState{}, points: map[cellPair][]PointPair{},
			pairBtns: map[cellPair]*widget.Clickable{}, status: "Нажмите «Открыть плитки…» и выберите все плитки сразу"}
		for _, e := range []*widget.Editor{&u.angleEd} {
			e.SingleLine = true
		}
		u.overlapSl.Value = 15.0 / maxOverlap
		u.angleEd.SetText("0")
		u.link.Value, u.clamp.Value, u.it = true, true, interpLanczos4
		u.chAlign.Value, u.lfit.Value, u.bgEq.Value = true, true, true
		u.pairList.Axis = layout.Vertical
		var ops op.Ops
		for {
			e := w.Event()
			u.expl.ListenEvents(e)
			switch e := e.(type) {
			case app.DestroyEvent:
				os.Exit(0)
			case app.FrameEvent:
				for drained := false; !drained; {
					select {
					case f := <-u.tasks:
						f()
					default:
						drained = true
					}
				}
				gtx := app.NewContext(&ops, e)
				u.layout(gtx)
				e.Frame(gtx.Ops)
			}
		}
	}()
	app.Main()
}

// post runs f on the UI goroutine.
func (u *ui) post(f func()) { u.tasks <- f; u.w.Invalidate() }

func (u *ui) layout(gtx layout.Context) {
	u.handle(gtx)
	paint.Fill(gtx.Ops, color.NRGBA{R: 0x20, G: 0x22, B: 0x26, A: 0xff})
	u.th.Palette.Fg = color.NRGBA{R: 0xe0, G: 0xe0, B: 0xe0, A: 0xff}
	u.th.Palette.Bg = color.NRGBA{R: 0x20, G: 0x22, B: 0x26, A: 0xff}
	layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(u.toolbar),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if u.view != 2 || u.result == nil {
				return layout.Dimensions{}
			}
			return u.geomBar(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			view := func(gtx layout.Context) layout.Dimensions {
				switch u.view {
				case 1:
					return u.pointsView(gtx)
				case 2:
					return u.resultView(gtx)
				}
				return u.gridView(gtx)
			}
			if !u.grid.open {
				return view(gtx)
			}
			return layout.Stack{Alignment: layout.NW}.Layout(gtx,
				layout.Expanded(view),
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					defer clip.Rect{Max: gtx.Constraints.Min}.Push(gtx.Ops).Pop()
					event.Op(gtx.Ops, &u.grid.open)
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						d, r, c, ok := u.grid.Layout(gtx, u.th, u.rows, u.cols)
						if ok {
							u.setGrid(r, c)
						}
						return d
					})
				}))
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(6)).Layout(gtx, material.Body2(u.th, u.status).Layout)
		}),
	)
}

func (u *ui) handle(gtx layout.Context) {
	if u.gridBtn.Clicked(gtx) {
		u.grid.open = !u.grid.open
	}
	for { // a press outside the open picker closes it
		ev, ok := gtx.Event(pointer.Filter{Target: &u.grid.open, Kinds: pointer.Press})
		if !ok {
			break
		}
		if _, ok := ev.(pointer.Event); ok {
			u.grid.open = false
		}
	}
	if ch := u.chAlign.Update(gtx); (u.lfit.Update(gtx) || u.bgEq.Update(gtx) || ch) && u.result != nil && !u.busy {
		u.stitch()
	}
	if u.clearAll.Clicked(gtx) && !u.busy {
		u.tiles, u.points, u.weak, u.cur = map[cell]*tileState{}, map[cellPair][]PointPair{}, nil, nil
		u.pending = [2]*[2]float64{}
		u.view = 0
		u.status = "Плитки убраны"
	}
	if u.openAll.Clicked(gtx) && !u.busy {
		u.openMany()
	}
	// a changed overlap re-stitches once the slider is let go, not on every step of the drag
	if u.overlapSl.Update(gtx) || u.overlapDrag && !u.overlapSl.Dragging() {
		if !u.overlapSl.Dragging() && u.result != nil && !u.busy {
			u.stitch()
		}
	}
	u.overlapDrag = u.overlapSl.Dragging()
	if u.weakBtn.Clicked(gtx) && len(u.weak) > 0 {
		p := u.weak[0]
		u.cur, u.view = &p, 1
		u.pending = [2]*[2]float64{}
		u.panels[0].needFit, u.panels[1].needFit = true, true
	}
	if u.viewGrid.Clicked(gtx) {
		u.view = 0
	}
	if u.viewPts.Clicked(gtx) {
		u.view = 1
	}
	if u.viewRes.Clicked(gtx) {
		u.view = 2
	}
	u.stretch.Update(gtx)
	u.link.Update(gtx)
	u.clamp.Update(gtx)
	u.handleGeom(gtx)
	if u.stitchBtn.Clicked(gtx) && !u.busy {
		u.stitch()
	}
	if u.saveBtn.Clicked(gtx) && u.result != nil && !u.busy {
		u.save()
	}
	if u.clearPts.Clicked(gtx) && u.cur != nil {
		delete(u.points, *u.cur)
		u.pending = [2]*[2]float64{}
	}
	for k, t := range u.tiles {
		if t.clear.Clicked(gtx) && !t.loading {
			delete(u.tiles, k)
			if u.cur != nil && (u.cur.A == k || u.cur.B == k) {
				u.cur, u.pending = nil, [2]*[2]float64{}
			}
			for p := range u.points {
				if p.A == k || p.B == k {
					delete(u.points, p)
				}
			}
		}
	}
	for r := 0; r < u.rows; r++ {
		for c := 0; c < u.cols; c++ {
			t := u.tile(cell{r, c})
			if t.open.Clicked(gtx) && !t.loading {
				u.open(cell{r, c})
			}
		}
	}
	for p, b := range u.pairBtns {
		if b.Clicked(gtx) {
			pp := p
			u.cur = &pp
			u.pending = [2]*[2]float64{}
			u.panels[0].needFit, u.panels[1].needFit = true, true
		}
	}
}

// tile returns the state for a cell, creating an empty one.
func (u *ui) tile(k cell) *tileState {
	t := u.tiles[k]
	if t == nil {
		t = &tileState{}
		u.tiles[k] = t
	}
	return t
}

func (u *ui) open(k cell) {
	t := u.tile(k)
	t.loading = true
	go func() {
		f, err := u.expl.ChooseFile(inputExts...)
		if err != nil {
			u.post(func() { t.loading = false })
			return
		}
		path := f.(interface{ Name() string }).Name()
		f.Close()
		u.post(func() { u.status = "Загрузка " + filepath.Base(path) + "…" })
		img, err := loadImage(path)
		if err != nil {
			u.post(func() { t.loading = false; u.status = "Ошибка: " + err.Error() })
			return
		}
		pv := previews(img, previewSide)
		u.post(func() {
			t.loading = false
			t.path, t.img, t.hasWCS = path, img, parseWCS(img) != nil
			for i, p := range pv {
				t.prev[i] = paint.NewImageOp(p)
			}
			t.prevW = pv[0].Bounds().Dx()
			for p := range u.points { // new image invalidates old points on it
				if p.A == k || p.B == k {
					delete(u.points, p)
				}
			}
			u.status = fmt.Sprintf("%s: %d×%d, каналов %d, WCS: %v", filepath.Base(path), img.W, img.H, img.C, t.hasWCS)
		})
	}()
}

// openMany loads several tiles from one dialog and lays them out with autoGrid.
func (u *ui) openMany() {
	u.busy = true
	go func() {
		fs, err := u.expl.ChooseFiles(inputExts...)
		if err != nil || len(fs) == 0 {
			u.post(func() { u.busy = false })
			return
		}
		var paths []string
		for _, f := range fs {
			paths = append(paths, f.(interface{ Name() string }).Name())
			f.Close()
		}
		if len(paths) > 100 {
			u.post(func() { u.busy = false; u.status = "Не больше 100 плиток" })
			return
		}
		u.post(func() { u.status = fmt.Sprintf("Загружаю %d плиток…", len(paths)) })
		imgs := make([]*Img, len(paths))
		pvs := make([][nPrev]*image.RGBA, len(paths))
		errs := make([]error, len(paths))
		var wg sync.WaitGroup
		for i, p := range paths {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if imgs[i], errs[i] = loadImage(p); errs[i] == nil {
					pvs[i] = previews(imgs[i], previewSide)
				}
			}()
		}
		wg.Wait()
		for i, e := range errs {
			if e != nil {
				u.post(func() { u.busy = false; u.status = "Ошибка " + filepath.Base(paths[i]) + ": " + e.Error() })
				return
			}
		}
		cells, rows, cols, how := autoGrid(imgs, paths)
		u.post(func() {
			u.busy = false
			u.rows, u.cols = rows, cols
			u.tiles, u.points, u.weak, u.cur = map[cell]*tileState{}, map[cellPair][]PointPair{}, nil, nil
			wcs := 0
			for i, k := range cells {
				t := &tileState{path: paths[i], img: imgs[i], hasWCS: parseWCS(imgs[i]) != nil, prevW: pvs[i][0].Bounds().Dx()}
				for j, p := range pvs[i] {
					t.prev[j] = paint.NewImageOp(p)
				}
				if t.hasWCS {
					wcs++
				}
				u.tiles[k] = t
			}
			u.view = 0
			u.status = fmt.Sprintf("Загружено %d плиток, сетка %d×%d (%s), с WCS: %d. Проверьте раскладку и нажмите «Склеить»",
				len(paths), rows, cols, how, wcs)
		})
	}()
}

// gather collects the loaded tiles with their cells, manual points and the overlap setting (0..1).
func (u *ui) gather() (tiles []*Tile, cells []cell, pts map[pairKey][]PointPair, ov float64) {
	idx := map[cell]int{}
	for r := 0; r < u.rows; r++ {
		for c := 0; c < u.cols; c++ {
			if t := u.tiles[cell{r, c}]; t != nil && t.img != nil {
				idx[cell{r, c}] = len(tiles)
				tiles = append(tiles, &Tile{Img: t.img, Row: r, Col: c, Name: fmt.Sprintf("[%d,%d]", r+1, c+1)})
				cells = append(cells, cell{r, c})
			}
		}
	}
	pts = map[pairKey][]PointPair{}
	for p, list := range u.points {
		a, okA := idx[p.A]
		b, okB := idx[p.B]
		if okA && okB && len(list) > 0 {
			pts[pairKey{a, b}] = list
		}
	}
	return tiles, cells, pts, u.overlapPct() / 100
}

func (t *tileState) setPrev(pv [nPrev]*image.RGBA) {
	for i, p := range pv {
		t.prev[i] = paint.NewImageOp(p)
	}
	t.prevW = pv[0].Bounds().Dx()
}

func (u *ui) stitch() {
	tiles, _, pts, ov := u.gather()
	if len(tiles) < 2 {
		u.status = "Нужно минимум две плитки"
		return
	}
	ov *= 100
	u.busy = true
	u.status = "Сшиваю…"
	chAlign, lfit, bgEq := u.chAlign.Value, u.lfit.Value, u.bgEq.Value
	go func() {
		var chNotes []string
		if chAlign { // per tile, in its own optics frame, before the stars are matched across tiles
			u.post(func() { u.status = "Совмещаю цветовые каналы по звёздам…" })
			for _, t := range tiles {
				if img, note := alignChannels(t.Img); note != "" {
					t.Img = img
					chNotes = append(chNotes, t.Name+": "+note)
				}
			}
		}
		W, notes, weak := place(tiles, pts, ov/100)
		if len(chNotes) > 0 {
			notes = append(notes, "каналы: "+strings.Join(chNotes, "; "))
		}
		if lfit {
			u.post(func() { u.status = "LinearFit: подгоняю яркость…" })
			fit, ln := linearFit(tiles, W)
			for i := range tiles {
				tiles[i].Img = fit[i]
			}
			notes = append(notes, "LinearFit: "+strings.Join(ln, "; "))
		}
		if bgEq {
			u.post(func() { u.status = "Выравниваю фон по перекрытиям…" })
			imgs, bn := matchBackground(tiles, W)
			for i := range tiles {
				tiles[i].Img = imgs[i]
			}
			notes = append(notes, "фон: "+strings.Join(bn, ", "))
		}
		res := render(tiles, W, seamCuts(tiles, W, ov/100))
		pv := previews(res, previewSide)
		u.post(func() {
			u.busy = false
			u.setResult(res, pv)
			u.history, u.sel = nil, nil
			u.view = 2
			u.weak = nil
			for _, k := range weak {
				a, b := tiles[k.A], tiles[k.B]
				p := cellPair{cell{a.Row, a.Col}, cell{b.Row, b.Col}}
				if b.Row < a.Row || b.Col < a.Col {
					p = cellPair{p.B, p.A}
				}
				u.weak = append(u.weak, p)
			}
			u.status = fmt.Sprintf("Готово %d×%d. Связи: %s", res.W, res.H, strings.Join(notes, "; "))
			if len(u.weak) > 0 {
				u.status = fmt.Sprintf("Готово %d×%d. Пар без общих звёзд: %d — проверьте швы; при ошибке поставьте точки («Проверить пары»). %s",
					res.W, res.H, len(u.weak), strings.Join(notes, "; "))
			}
		})
	}()
}

func (u *ui) setResult(m *Img, pv [nPrev]*image.RGBA) {
	u.result = m
	for i, p := range pv {
		u.resPrev[i] = paint.NewImageOp(p)
	}
	u.resPrevW = pv[0].Bounds().Dx()
	u.resPanel.needFit = true
}

// save writes the linear result: Авторастяжение and Связать каналы only affect the preview.
// A name typed without the format's extension (or with another one) gets it appended, so the
// writer is always the selected format.
func (u *ui) save() {
	res := u.result
	def := u.format
	u.busy = true
	go func() {
		path, k, ok := u.saveDialog("mosaic", def)
		if !ok {
			u.post(func() { u.busy = false })
			return
		}
		sf := saveFormats[k]
		if ext := strings.ToLower(filepath.Ext(path)); ext != "."+sf.ext && !(sf.ext == "fits" && (ext == ".fit" || ext == ".fts")) && !(sf.ext == "tif" && ext == ".tiff") && !(sf.ext == "jpg" && ext == ".jpeg") {
			path += "." + sf.ext // the chosen type decides the format, not a typed extension
		}
		u.post(func() { u.status = "Сохраняю…"; u.format = k })
		err := saveImage(path, res, sf.float)
		u.post(func() {
			u.busy = false
			if err != nil {
				os.Remove(path)
				u.status = "Ошибка сохранения: " + err.Error()
			} else {
				u.status = "Сохранено: " + path
			}
		})
	}()
}

// ---------- layout pieces ----------

func (u *ui) toolbar(gtx layout.Context) layout.Dimensions {
	th := u.th
	sp := layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout)
	btn := func(c *widget.Clickable, s string) layout.FlexChild {
		return layout.Rigid(material.Button(th, c, s).Layout)
	}
	tab := func(c *widget.Clickable, s string, on bool) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, c, s)
			if !on {
				b.Background = color.NRGBA{R: 0x40, G: 0x44, B: 0x4c, A: 0xff}
			}
			return b.Layout(gtx)
		})
	}
	// row 1: grid, view, then the stitching steps in order; row 2: display and stitching details
	resTab := "Результат"
	if u.result == nil {
		resTab = "Результат (нет)"
	}
	row1 := []layout.FlexChild{
		btn(&u.gridBtn, fmt.Sprintf("Сетка %d × %d ▾", u.rows, u.cols)), sp, sp,
		layout.Rigid(material.Body1(th, "Вид:").Layout), sp,
		tab(&u.viewGrid, "Плитки", u.view != 2), tab(&u.viewRes, resTab, u.view == 2), sp, sp,
		btn(&u.openAll, "Открыть плитки…"), sp,
		layout.Rigid(checkBox(th, &u.chAlign, "Совместить каналы").Layout), sp,
		layout.Rigid(checkBox(th, &u.lfit, "LinearFit").Layout), sp,
		layout.Rigid(checkBox(th, &u.bgEq, "Фон по перекрытию").Layout), sp,
		btn(&u.stitchBtn, "Склеить"), sp,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if u.result == nil || u.busy { // nothing to save yet
				gtx = gtx.Disabled()
				b := material.Button(th, &u.saveBtn, "Сохранить…")
				b.Background = color.NRGBA{R: 0x33, G: 0x36, B: 0x3d, A: 0xff}
				b.Color = color.NRGBA{R: 0x78, G: 0x7c, B: 0x84, A: 0xff}
				return b.Layout(gtx)
			}
			return material.Button(th, &u.saveBtn, "Сохранить…").Layout(gtx)
		}),
		layout.Flexed(1, layout.Spacer{}.Layout), // «Очистить плитки» stands apart, at the far right
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, &u.clearAll, "Очистить плитки")
			b.Background = color.NRGBA{R: 0x80, G: 0x30, B: 0x30, A: 0xff}
			return b.Layout(gtx)
		}),
	}
	row2 := []layout.FlexChild{
		layout.Rigid(checkBox(th, &u.stretch, "Авторастяжение").Layout),
		layout.Rigid(checkBox(th, &u.link, "Связать каналы").Layout), sp, sp,
		layout.Rigid(material.Body1(th, fmt.Sprintf("Перекрытие у шва: %2.0f%%", u.overlapPct())).Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(200), gtx.Dp(200)
			return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, material.Slider(th, &u.overlapSl).Layout)
		}), sp,
		btn(&u.viewPts, "Ручные точки"),
	}
	if len(u.weak) > 0 {
		row2 = append(row2, sp, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			b := material.Button(th, &u.weakBtn, fmt.Sprintf("Проверить пары (%d)", len(u.weak)))
			b.Background = color.NRGBA{R: 0xc0, G: 0x60, B: 0x20, A: 0xff}
			return b.Layout(gtx)
		}))
	}
	row := func(ch []layout.FlexChild) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx, ch...)
			})
		})
	}
	return layout.UniformInset(unit.Dp(6)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, row(row1), row(row2))
	})
}

// maxOverlap is the slider's top, in % of the tile. The overlap kept round a seam is the zone
// where the background is feathered; 0 is a butt joint. Past the tiles' real overlap (typically
// 10–30%) nothing changes, and past half a tile two frames are no longer side by side.
const maxOverlap = 50

// overlapPct is the slider value in whole per cent.
func (u *ui) overlapPct() float64 { return math.Round(float64(u.overlapSl.Value) * maxOverlap) }

func (u *ui) si() int {
	switch {
	case !u.stretch.Value:
		return prevLinear
	case u.link.Value:
		return prevLinked
	}
	return prevUnlinked
}

func (u *ui) gridView(gtx layout.Context) layout.Dimensions {
	rows := make([]layout.FlexChild, u.rows)
	for r := 0; r < u.rows; r++ {
		cols := make([]layout.FlexChild, u.cols)
		for c := 0; c < u.cols; c++ {
			k := cell{r, c}
			cols[c] = layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(3)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return u.cellView(gtx, k)
				})
			})
		}
		rows[r] = layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{}.Layout(gtx, cols...)
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

func (u *ui) cellView(gtx layout.Context, k cell) layout.Dimensions {
	t := u.tile(k)
	sz := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0x30, G: 0x33, B: 0x38, A: 0xff}, clip.Rect{Max: sz}.Op())
	label := fmt.Sprintf("[%d,%d] нажмите, чтобы открыть", k[0]+1, k[1]+1)
	if t.loading {
		label = fmt.Sprintf("[%d,%d] загрузка…", k[0]+1, k[1]+1)
	}
	if t.img != nil {
		w := "нет WCS"
		if t.hasWCS {
			w = "WCS ✓"
		}
		cs := "ч/б"
		if t.img.C >= 3 {
			cs = "RGB"
		}
		label = fmt.Sprintf("[%d,%d] %s  %d×%d  %s  %s", k[0]+1, k[1]+1, filepath.Base(t.path), t.img.W, t.img.H, cs, w)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			preview := func(gtx layout.Context) layout.Dimensions {
				return t.open.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = gtx.Constraints.Max
					if t.img == nil {
						return layout.Center.Layout(gtx, material.Body1(u.th, "+").Layout)
					}
					return widget.Image{Src: t.prev[u.si()], Fit: widget.Contain, Position: layout.Center, Scale: 1 / gtx.Metric.PxPerDp}.Layout(gtx)
				})
			}
			if t.img == nil {
				return preview(gtx)
			}
			// remove button in the corner, drawn over the preview
			return layout.Stack{Alignment: layout.NE}.Layout(gtx,
				layout.Expanded(preview),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(4)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						b := material.Button(u.th, &t.clear, "×")
						b.Background = color.NRGBA{R: 0xb0, G: 0x30, B: 0x30, A: 0xff}
						b.TextSize = unit.Sp(18)
						b.Inset = layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2), Left: unit.Dp(10), Right: unit.Dp(10)}
						return b.Layout(gtx)
					})
				}))
		}),
		layout.Rigid(material.Body2(u.th, label).Layout),
	)
}

// adjacent returns neighbour pairs where both tiles are loaded.
func (u *ui) adjacent() []cellPair {
	var out []cellPair
	ok := func(k cell) bool { t := u.tiles[k]; return t != nil && t.img != nil }
	for r := 0; r < u.rows; r++ {
		for c := 0; c < u.cols; c++ {
			a := cell{r, c}
			for _, b := range []cell{{r, c + 1}, {r + 1, c}} {
				if ok(a) && ok(b) {
					out = append(out, cellPair{a, b})
				}
			}
		}
	}
	return out
}

func (u *ui) pointsView(gtx layout.Context) layout.Dimensions {
	pairs := u.adjacent()
	return layout.Flex{}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = gtx.Dp(200)
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(material.Body2(u.th, "Пары соседних плиток.\nЛКМ: точка на левой, потом на правой (или наоборот).\nКолесо: масштаб, перетаскивание: сдвиг.\nПКМ по метке: удалить.").Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
				layout.Rigid(material.Button(u.th, &u.clearPts, "Очистить точки пары").Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return material.List(u.th, &u.pairList).Layout(gtx, len(pairs), func(gtx layout.Context, i int) layout.Dimensions {
						p := pairs[i]
						b := u.pairBtns[p]
						if b == nil {
							b = new(widget.Clickable)
							u.pairBtns[p] = b
						}
						s := fmt.Sprintf("[%d,%d] ↔ [%d,%d]  (%d)", p.A[0]+1, p.A[1]+1, p.B[0]+1, p.B[1]+1, len(u.points[p]))
						if slices.Contains(u.weak, p) {
							s = "(!) " + s // the stars did not tie this pair
						}
						bs := material.Button(u.th, b, s)
						if u.cur != nil && *u.cur == p {
							bs.Background = color.NRGBA{R: 0xc0, G: 0x60, B: 0x20, A: 0xff}
						}
						return layout.UniformInset(unit.Dp(2)).Layout(gtx, bs.Layout)
					})
				}),
			)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			if u.cur == nil || u.tiles[u.cur.A] == nil || u.tiles[u.cur.A].img == nil || u.tiles[u.cur.B] == nil || u.tiles[u.cur.B].img == nil {
				return layout.Center.Layout(gtx, material.Body1(u.th, "Выберите пару слева").Layout)
			}
			cp := *u.cur
			return layout.Flex{}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return u.pairPanel(gtx, cp, 0) }),
				layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions { return u.pairPanel(gtx, cp, 1) }),
			)
		}),
	)
}

var markerCol = []color.NRGBA{
	{R: 0xff, G: 0x40, B: 0x40, A: 0xff}, {R: 0x40, G: 0xff, B: 0x40, A: 0xff}, {R: 0x40, G: 0xa0, B: 0xff, A: 0xff},
	{R: 0xff, G: 0xff, B: 0x40, A: 0xff}, {R: 0xff, G: 0x40, B: 0xff, A: 0xff}, {R: 0x40, G: 0xff, B: 0xff, A: 0xff},
}

func (u *ui) pairPanel(gtx layout.Context, cp cellPair, side int) layout.Dimensions {
	k := cp.A
	if side == 1 {
		k = cp.B
	}
	t := u.tiles[k]
	pn := &u.panels[side]
	scale := float32(t.img.W) / float32(t.prevW) // full-res px per preview px
	toFull := func(p f32.Point) [2]float64 {
		q := p.Sub(pn.pan).Div(pn.zoom).Mul(scale)
		return [2]float64{float64(q.X), float64(q.Y)}
	}
	toScreen := func(p [2]float64) f32.Point {
		return f32.Pt(float32(p[0]), float32(p[1])).Div(scale).Mul(pn.zoom).Add(pn.pan)
	}
	click := func(pos f32.Point, b pointer.Buttons) {
		if b == pointer.ButtonSecondary {
			list := u.points[cp]
			for i, pp := range list {
				q := pp.A
				if side == 1 {
					q = pp.B
				}
				d := toScreen(q).Sub(pos)
				if d.X*d.X+d.Y*d.Y < 144 {
					u.points[cp] = append(list[:i], list[i+1:]...)
					return
				}
			}
			u.pending[side] = nil
			return
		}
		p := toFull(pos)
		if p[0] < 0 || p[1] < 0 || p[0] >= float64(t.img.W) || p[1] >= float64(t.img.H) {
			return
		}
		u.pending[side] = &p
		if u.pending[0] != nil && u.pending[1] != nil {
			u.points[cp] = append(u.points[cp], PointPair{A: *u.pending[0], B: *u.pending[1]})
			u.pending = [2]*[2]float64{}
		}
	}
	dims := u.zoomView(gtx, pn, t.prev[u.si()], click, nil)

	// markers
	draw := func(p [2]float64, col color.NRGBA, label string) {
		s := toScreen(p)
		r := float32(gtx.Dp(7))
		ell := clip.Ellipse{Min: s.Sub(f32.Pt(r, r)).Round(), Max: s.Add(f32.Pt(r, r)).Round()}
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: ell.Path(gtx.Ops), Width: 2}.Op())
		st := op.Offset(s.Add(f32.Pt(r, -2*r)).Round()).Push(gtx.Ops)
		l := material.Body2(u.th, label)
		l.Color = col
		g := gtx
		g.Constraints.Min = image.Point{}
		l.Layout(g)
		st.Pop()
	}
	cl := clip.Rect{Max: dims.Size}.Push(gtx.Ops)
	for i, pp := range u.points[cp] {
		q := pp.A
		if side == 1 {
			q = pp.B
		}
		draw(q, markerCol[i%len(markerCol)], strconv.Itoa(i+1))
	}
	if p := u.pending[side]; p != nil {
		draw(*p, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, "?")
	}
	cl.Pop()
	return dims
}

func (u *ui) resultView(gtx layout.Context) layout.Dimensions {
	if u.result == nil {
		return layout.Center.Layout(gtx, material.Body1(u.th, "Нажмите «Склеить»").Layout)
	}
	pn := &u.resPanel
	scale := float32(u.result.W) / float32(u.resPrevW)
	toFull := func(p f32.Point) [2]float64 {
		q := p.Sub(pn.pan).Div(pn.zoom).Mul(scale)
		return [2]float64{float64(q.X), float64(q.Y)}
	}
	toScreen := func(p [2]float64) f32.Point {
		return f32.Pt(float32(p[0]), float32(p[1])).Div(scale).Mul(pn.zoom).Add(pn.pan)
	}
	click := func(_ f32.Point, b pointer.Buttons) {
		if b.Contain(pointer.ButtonPrimary) {
			u.sel = nil // like Siril: a plain click drops the selection
		}
	}
	hooks := &dragHooks{
		press: func(pos f32.Point) bool { u.selPress(toFull(pos), float64(8*scale/pn.zoom)); return true },
		drag:  func(pos f32.Point) { u.selMove(toFull(pos)) },
	}
	dims := u.zoomView(gtx, pn, u.resPrev[u.si()], click, hooks)

	f, ok := u.effFrame()
	if !ok {
		return dims
	}
	defer clip.Rect{Max: dims.Size}.Push(gtx.Ops).Pop()
	line := func(a, b [2]float64, col color.NRGBA, w float32) {
		var path clip.Path
		path.Begin(gtx.Ops)
		path.MoveTo(toScreen(a))
		path.LineTo(toScreen(b))
		paint.FillShape(gtx.Ops, col, clip.Stroke{Path: path.End(), Width: w}.Op())
	}
	c := f.corners()
	frameCol := color.NRGBA{R: 0xff, G: 0xc0, B: 0x30, A: 0xff}
	for i := range c {
		line(c[i], c[(i+1)%4], frameCol, 2)
	}
	guide := color.NRGBA{R: 0xff, G: 0xc0, B: 0x30, A: 0x70}
	for _, t := range []float64{1.0 / 3, 2.0 / 3} { // rule of thirds, as Siril's guides
		lx := -f.W/2 + t*f.W
		ly := -f.H/2 + t*f.H
		a, b := [2]float64{}, [2]float64{}
		a[0], a[1] = f.fromLocal(lx, -f.H/2)
		b[0], b[1] = f.fromLocal(lx, f.H/2)
		line(a, b, guide, 1)
		a[0], a[1] = f.fromLocal(-f.W/2, ly)
		b[0], b[1] = f.fromLocal(f.W/2, ly)
		line(a, b, guide, 1)
	}
	st := op.Offset(toScreen(c[0]).Add(f32.Pt(4, 4)).Round()).Push(gtx.Ops)
	l := material.Body2(u.th, fmt.Sprintf("%.0f × %.0f   %.1f°", f.W, f.H, f.Angle))
	l.Color = frameCol
	g := gtx
	g.Constraints.Min = image.Point{}
	l.Layout(g)
	st.Pop()
	return dims
}

// zoomView draws img with wheel zoom and drag pan; click gets screen position of a non-drag press.
// With hooks, left-button drags go to the hooks and panning needs the right or middle button.
func (u *ui) zoomView(gtx layout.Context, pn *panel, img paint.ImageOp, click func(f32.Point, pointer.Buttons), hooks *dragHooks) layout.Dimensions {
	size := gtx.Constraints.Max
	isz := img.Size()
	if pn.needFit || pn.zoom == 0 {
		pn.zoom = float32(math.Min(float64(size.X)/float64(isz.X), float64(size.Y)/float64(isz.Y)))
		pn.pan = f32.Pt((float32(size.X)-float32(isz.X)*pn.zoom)/2, (float32(size.Y)-float32(isz.Y)*pn.zoom)/2)
		pn.needFit = false
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: pn, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Scroll,
			ScrollY: pointer.ScrollRange{Min: -1e6, Max: 1e6}})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			pn.press, pn.last, pn.button, pn.drag = e.Position, e.Position, e.Buttons, false
			pn.own = hooks != nil && e.Buttons.Contain(pointer.ButtonPrimary) && hooks.press(e.Position)
		case pointer.Drag:
			if d := e.Position.Sub(pn.press); d.X*d.X+d.Y*d.Y > 16 {
				pn.drag = true
			}
			if pn.drag && pn.own {
				hooks.drag(e.Position)
			} else if pn.drag {
				pn.pan = pn.pan.Add(e.Position.Sub(pn.last))
			}
			pn.last = e.Position
		case pointer.Release:
			if !pn.drag {
				click(e.Position, pn.button)
			}
			pn.own = false
		case pointer.Scroll:
			f := float32(1.2)
			if e.Scroll.Y > 0 {
				f = 1 / f
			}
			pn.pan = e.Position.Sub(e.Position.Sub(pn.pan).Mul(f))
			pn.zoom *= f
		}
	}
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, color.NRGBA{A: 0xff})
	event.Op(gtx.Ops, pn)
	st := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(pn.zoom, pn.zoom)).Offset(pn.pan)).Push(gtx.Ops)
	img.Filter = paint.FilterNearest
	img.Add(gtx.Ops)
	cr := clip.Rect{Max: isz}.Push(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	cr.Pop()
	st.Pop()
	return layout.Dimensions{Size: size}
}

// ---------- frame & rotation ----------

func (u *ui) angle() float64 {
	a, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(u.angleEd.Text()), ",", "."), 64)
	if err != nil || math.IsNaN(a) || math.IsInf(a, 0) {
		return 0
	}
	return math.Mod(a, 360)
}

// ratioValue is the frame aspect (w/h) to keep, 0 = free.
func (u *ui) ratioValue() float64 {
	r := ratios[u.ratio].r
	if r < 0 && u.result != nil {
		r = float64(u.result.W) / float64(u.result.H)
	}
	return r
}

// effFrame is what "Кадрировать" will cut: the drawn frame, or the whole image when only an angle is set.
func (u *ui) effFrame() (frame, bool) {
	a := u.angle()
	if u.sel != nil {
		f := *u.sel
		f.Angle = a
		return f, true
	}
	if a != 0 && u.result != nil {
		return frame{CX: float64(u.result.W) / 2, CY: float64(u.result.H) / 2, W: float64(u.result.W), H: float64(u.result.H), Angle: a}, true
	}
	return frame{}, false
}

// selPress starts a new frame, a move (inside) or a resize (near an edge, tol in image px).
func (u *ui) selPress(p [2]float64, tol float64) {
	u.sd = selDrag{mode: 1, anchor: p}
	if u.sel == nil {
		return
	}
	f := *u.sel
	f.Angle = u.angle()
	lx, ly := f.toLocal(p[0], p[1])
	hw, hh := f.W/2, f.H/2
	in := math.Abs(lx) < hw+tol && math.Abs(ly) < hh+tol
	e := [4]bool{in && math.Abs(lx+hw) < tol, in && math.Abs(lx-hw) < tol, in && math.Abs(ly+hh) < tol, in && math.Abs(ly-hh) < tol}
	switch {
	case e[0] || e[1] || e[2] || e[3]:
		u.sd = selDrag{mode: 3, anchor: p, start: f, edges: e}
	case in:
		u.sd = selDrag{mode: 2, anchor: p, start: f}
	}
}

func (u *ui) selMove(p [2]float64) {
	r := u.ratioValue()
	a := u.angle()
	sd := &u.sd
	var f frame
	switch sd.mode {
	case 1:
		t := frame{CX: sd.anchor[0], CY: sd.anchor[1], Angle: a}
		lx, ly := t.toLocal(p[0], p[1])
		if r > 0 {
			sx, sy := math.Copysign(1, lx), math.Copysign(1, ly)
			if math.Abs(lx)/r > math.Abs(ly) {
				ly = sy * math.Abs(lx) / r
			} else {
				lx = sx * math.Abs(ly) * r
			}
		}
		f = frame{W: math.Abs(lx), H: math.Abs(ly), Angle: a}
		f.CX, f.CY = t.fromLocal(lx/2, ly/2)
	case 2:
		f = sd.start
		f.CX += p[0] - sd.anchor[0]
		f.CY += p[1] - sd.anchor[1]
	case 3:
		s := sd.start
		lx, ly := s.toLocal(p[0], p[1])
		l, rr, t, b := -s.W/2, s.W/2, -s.H/2, s.H/2
		e := sd.edges
		if e[0] {
			l = math.Min(lx, rr-2)
		}
		if e[1] {
			rr = math.Max(lx, l+2)
		}
		if e[2] {
			t = math.Min(ly, b-2)
		}
		if e[3] {
			b = math.Max(ly, t+2)
		}
		if r > 0 {
			if e[0] || e[1] {
				m, h := (t+b)/2, (rr-l)/r
				t, b = m-h/2, m+h/2
			} else {
				m, w := (l+rr)/2, (b-t)*r
				l, rr = m-w/2, m+w/2
			}
		}
		f = frame{W: rr - l, H: b - t, Angle: a}
		f.CX, f.CY = s.fromLocal((l+rr)/2, (t+b)/2)
	default:
		return
	}
	if f.W < 2 || f.H < 2 {
		return
	}
	u.sel = &f
}

// geom runs an operation on the result in the background, keeping the previous result for undo.
func (u *ui) geom(name string, op func(*Img) *Img) {
	if u.busy || u.result == nil {
		return
	}
	res := u.result
	u.busy = true
	u.status = name + "…"
	go func() {
		out := op(res)
		pv := previews(out, previewSide)
		u.post(func() {
			u.busy = false
			if u.history = append(u.history, res); len(u.history) > 3 { // each step is a full-size copy
				u.history = u.history[1:]
			}
			u.setResult(out, pv)
			u.sel = nil
			u.angleEd.SetText("0")
			u.status = fmt.Sprintf("%s: %d×%d", name, out.W, out.H)
		})
	}()
}

// setAngle writes the angle to the input, wrapped to -180..180 and rounded to 0.01°.
func (u *ui) setAngle(a float64) {
	a = math.Mod(a+540, 360) - 180
	u.angleEd.SetText(strconv.FormatFloat(math.Round(a*100)/100, 'f', -1, 64))
}

func (u *ui) handleGeom(gtx layout.Context) {
	if u.angleSl.Update(gtx) {
		u.setAngle(math.Round((float64(u.angleSl.Value)*360-180)*10) / 10)
	}
	if u.angleDn.Clicked(gtx) {
		u.setAngle(u.angle() - 0.1)
	}
	if u.angleUp.Clicked(gtx) {
		u.setAngle(u.angle() + 0.1)
	}
	if !u.angleSl.Dragging() { // typed or stepped angle moves the slider
		u.angleSl.Value = float32((math.Mod(u.angle()+540, 360) - 180 + 180) / 360)
	}
	if u.ratioBtn.Clicked(gtx) {
		u.ratio = (u.ratio + 1) % len(ratios)
		if r := u.ratioValue(); r > 0 && u.sel != nil { // reshape the current frame keeping its width
			u.sel.H = u.sel.W / r
		}
	}
	if u.interpBtn.Clicked(gtx) {
		u.it = (u.it + 1) % nInterp
	}
	if u.clrSel.Clicked(gtx) {
		u.sel = nil
		u.angleEd.SetText("0")
	}
	it, clamp := u.it, u.clamp.Value
	if u.cropBtn.Clicked(gtx) {
		if f, ok := u.effFrame(); !ok {
			u.status = "Нарисуйте рамку левой кнопкой мыши или задайте угол"
		} else {
			if f.Angle == 0 { // snap to whole pixels: an exact crop, no resampling
				x0, y0 := math.Round(f.CX-f.W/2), math.Round(f.CY-f.H/2)
				x1, y1 := math.Round(f.CX+f.W/2), math.Round(f.CY+f.H/2)
				f = frame{CX: (x0 + x1) / 2, CY: (y0 + y1) / 2, W: x1 - x0, H: y1 - y0}
			}
			u.geom("Кадрирование", func(m *Img) *Img { return cutFrame(m, f, it, clamp) })
		}
	}
	if u.rotBtn.Clicked(gtx) {
		if a := u.angle(); a == 0 {
			u.status = "Задайте угол поворота"
		} else {
			u.geom(fmt.Sprintf("Поворот %.2f°", a), func(m *Img) *Img { return rotateWhole(m, a, it, clamp) })
		}
	}
	if u.rotL.Clicked(gtx) {
		u.geom("Поворот 90° влево", func(m *Img) *Img { return rot90(m, false) })
	}
	if u.rotR.Clicked(gtx) {
		u.geom("Поворот 90° вправо", func(m *Img) *Img { return rot90(m, true) })
	}
	if u.flipH.Clicked(gtx) {
		u.geom("Отражение ↔", func(m *Img) *Img { return mirror(m, true) })
	}
	if u.flipV.Clicked(gtx) {
		u.geom("Отражение ↕", func(m *Img) *Img { return mirror(m, false) })
	}
	if u.undo.Clicked(gtx) && !u.busy && len(u.history) > 0 {
		prev := u.history[len(u.history)-1]
		u.history = u.history[:len(u.history)-1]
		u.busy = true
		go func() {
			pv := previews(prev, previewSide)
			u.post(func() {
				u.busy = false
				u.setResult(prev, pv)
				u.sel = nil
				u.status = fmt.Sprintf("Отменено: %d×%d", prev.W, prev.H)
			})
		}()
	}
}

func (u *ui) geomBar(gtx layout.Context) layout.Dimensions {
	th := u.th
	sp := layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout)
	btn := func(c *widget.Clickable, s string) layout.FlexChild {
		return layout.Rigid(material.Button(th, c, s).Layout)
	}
	return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					btn(&u.ratioBtn, "Рамка: "+ratios[u.ratio].label), sp,
					layout.Rigid(material.Body1(th, "Угол ° ").Layout),
					btn(&u.angleDn, "−0.1"),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(360), gtx.Dp(360) // 1 dp ≈ 1°
						return layout.UniformInset(unit.Dp(6)).Layout(gtx, material.Slider(th, &u.angleSl).Layout)
					}),
					btn(&u.angleUp, "+0.1"), sp,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X, gtx.Constraints.Max.X = gtx.Dp(64), gtx.Dp(64)
						return material.Editor(th, &u.angleEd, "").Layout(gtx)
					}), sp,
					btn(&u.interpBtn, "Интерполяция: "+interpNames[u.it]), sp,
					layout.Rigid(checkBox(th, &u.clamp, "Ограничение").Layout),
				)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					btn(&u.rotBtn, "Повернуть всё"), sp,
					btn(&u.rotL, "90° влево"), sp, btn(&u.rotR, "90° вправо"), sp,
					btn(&u.flipH, "Отразить ↔"), sp, btn(&u.flipV, "Отразить ↕"), sp,
					btn(&u.clrSel, "Снять рамку"), sp, btn(&u.undo, "Отменить"),
					layout.Flexed(1, layout.Spacer{}.Layout), // «Кадрировать» applies the frame: apart, at the far right
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						b := material.Button(th, &u.cropBtn, "Кадрировать")
						b.Background = color.NRGBA{R: 0x2e, G: 0x8b, B: 0x57, A: 0xff}
						return b.Layout(gtx)
					}),
				)
			}),
		)
	})
}

// checkBox is material.CheckBox with a tick that stands out on the dark theme.
func checkBox(th *material.Theme, b *widget.Bool, label string) material.CheckBoxStyle {
	c := material.CheckBox(th, b, label)
	c.IconColor = color.NRGBA{R: 0x6c, G: 0xb4, B: 0xff, A: 0xff}
	return c
}

// setGrid resizes the tile grid; tiles that fall outside it are dropped.
func (u *ui) setGrid(r, c int) {
	u.rows, u.cols = r, c
	for k := range u.tiles {
		if k[0] >= r || k[1] >= c {
			delete(u.tiles, k)
		}
	}
	for p := range u.points {
		if p.A[0] >= r || p.A[1] >= c || p.B[0] >= r || p.B[1] >= c {
			delete(u.points, p)
		}
	}
	u.cur, u.weak, u.view = nil, nil, 0
	u.status = fmt.Sprintf("Сетка %d × %d. Щёлкните по ячейке, чтобы открыть или заменить плитку", r, c)
}
