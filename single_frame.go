package main

func processSingleFrame(tile *Tile, match, field bool) (*Img, fieldDiagnostics, string) {
	m := tile.Img
	w := newWarp(identity, 0, m)
	w.sip = nil // retain native pixels; no mosaic projection
	d := measureFieldDiagnostics([]*Tile{tile}, []Warp{w}, registrationMeasurements{})
	note := ""
	if match {
		var images []*Img
		var report psfMatchReport
		if field {
			images, report = matchFieldPSF([]*Tile{tile}, []Warp{w})
		} else {
			images, report = matchPSF([]*Tile{tile}, []Warp{w})
		}
		m, note = images[0], report.note
		d.tiles[0].psfNote = report.tileNote(0)
	}
	return m, d, note
}
