package main

import (
	"gioui.org/io/pointer"
	"gioui.org/layout"
)

// Each pane owns its event target; both navigate the same camera. The leader
// drains both event queues before either image is painted, preventing a frame lag.
func linkPanels(p *[2]panel) {
	p[0].peer = &p[1]
	p[1].leader = &p[0]
}

func updatePanel(gtx layout.Context, pn, nav *panel) {
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
			pn.own = pn.hooks != nil && e.Buttons.Contain(pointer.ButtonPrimary) && pn.hooks.press(e.Position)
		case pointer.Drag:
			if d := e.Position.Sub(pn.press); d.X*d.X+d.Y*d.Y > 16 {
				pn.drag = true
			}
			if pn.drag && pn.own {
				pn.hooks.drag(e.Position)
			} else if pn.drag {
				nav.pan = nav.pan.Add(e.Position.Sub(pn.last))
			}
			pn.last = e.Position
		case pointer.Release:
			if !pn.drag {
				if pn.click != nil {
					pn.click(e.Position, pn.button)
				}
			}
			pn.own = false
		case pointer.Scroll:
			f := float32(1.2)
			if e.Scroll.Y > 0 {
				f = 1 / f
			}
			nav.pan = e.Position.Sub(e.Position.Sub(nav.pan).Mul(f))
			nav.zoom *= f
		}
	}
}
