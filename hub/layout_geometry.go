package hub

type rect struct {
	x int
	y int
	w int
	h int
}

type hitRegion struct {
	sessionIndex int
	rect
}

func (r rect) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

type gridMetrics struct {
	cols        int
	rows        int
	panelW      int
	panelH      int
	visibleRows int
	startRow    int
	statusH     int
	availableH int
}

func (m *Model) gridMetrics(n int) (gridMetrics, bool) {
	status := m.statusBar(n)
	statusH := renderedLineCount(status)
	if statusH > m.height {
		return gridMetrics{statusH: statusH}, false
	}
	availableH := m.height - statusH
	if availableH <= 0 || n <= 0 {
		return gridMetrics{statusH: statusH, availableH: availableH}, false
	}

	cols := m.layout.gridCols(n, m.width, m.height)
	rows := (n + cols - 1) / cols
	panelW, panelH := m.panelSize(n, cols, availableH)
	if panelH < 1 {
		panelH = 1
	}
	visibleRows := rows
	if visibleRows*panelH > availableH {
		visibleRows = availableH / panelH
	}
	if visibleRows < 1 {
		return gridMetrics{
			cols:        cols,
			rows:        rows,
			panelW:      panelW,
			panelH:      panelH,
			statusH:     statusH,
			availableH: availableH,
		}, false
	}

	startRow := 0
	if visibleRows < rows && m.selected >= 0 && m.selected < n {
		selectedRow := m.selected / cols
		if selectedRow >= visibleRows {
			startRow = selectedRow - visibleRows + 1
		}
		if startRow+visibleRows > rows {
			startRow = rows - visibleRows
		}
	}

	return gridMetrics{
		cols:        cols,
		rows:        rows,
		panelW:      panelW,
		panelH:      panelH,
		visibleRows: visibleRows,
		startRow:    startRow,
		statusH:     statusH,
		availableH: availableH,
	}, true
}

func (m *Model) gridHitRegions(n int) []hitRegion {
	metrics, ok := m.gridMetrics(n)
	if !ok {
		return nil
	}
	regions := make([]hitRegion, 0, n)
	for displayRow := 0; displayRow < metrics.visibleRows; displayRow++ {
		r := metrics.startRow + displayRow
		for c := 0; c < metrics.cols; c++ {
			idx := r*metrics.cols + c
			if idx >= n {
				continue
			}
			regions = append(regions, hitRegion{
				sessionIndex: idx,
				rect: rect{
					x: c * metrics.panelW,
					y: displayRow * metrics.panelH,
					w: metrics.panelW,
					h: metrics.panelH,
				},
			})
		}
	}
	return regions
}

func (m *Model) layoutHitRegions() []hitRegion {
	n := len(m.orderedSessions())
	if n == 0 {
		return nil
	}
	if m.layout.mode == layoutFocusBig {
		return m.focusBigHitRegions(n)
	}
	return m.gridHitRegions(n)
}

func (m *Model) hitTestSession(x, y int) int {
	for _, region := range m.layoutHitRegions() {
		if region.contains(x, y) {
			return region.sessionIndex
		}
	}
	return -1
}

func (m *Model) focusBigHitRegions(n int) []hitRegion {
	return nil
}
