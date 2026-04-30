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
	if m.width <= 0 || m.height <= 0 || n == 0 || m.selected < 0 || m.selected >= n {
		return nil
	}

	status := m.focusBigStatusBar(n)
	statusH := renderedLineCount(status)
	if statusH > m.height {
		statusH = m.height
	}
	availableH := m.height - statusH
	if availableH <= 0 {
		return nil
	}

	if n == 1 {
		return []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: m.width, h: availableH}}}
	}
	if m.width >= 120 {
		return m.focusBigWideHitRegions(n, availableH)
	}
	return m.focusBigNarrowHitRegions(n, availableH)
}

func (m *Model) focusBigWideHitRegions(n, availableH int) []hitRegion {
	if availableH < minPanelH {
		return []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: m.width, h: availableH}}}
	}
	mainW := m.width * 7 / 10
	sideW := m.width - mainW
	if sideW < minPanelW {
		sideW = minPanelW
		mainW = m.width - sideW
	}
	if mainW < minPanelW {
		mainW = minPanelW
	}

	regions := []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: mainW, h: availableH}}}
	secondaryCount := n - 1
	visible, hidden := fitSecondaryCount(availableH, minPanelH, secondaryCount)
	renderedSlots := visible
	if hidden > 0 && availableH >= minPanelH {
		renderedSlots++
	}
	if renderedSlots <= 0 {
		return regions
	}

	y := 0
	slotIndex := 0
	for idx := 0; idx < n; idx++ {
		if idx == m.selected {
			continue
		}
		if slotIndex >= visible {
			break
		}
		panelH := distributedSlotSize(availableH, renderedSlots, slotIndex)
		regions = append(regions, hitRegion{sessionIndex: idx, rect: rect{x: mainW, y: y, w: sideW, h: panelH}})
		y += panelH
		slotIndex++
	}
	return regions
}

func (m *Model) focusBigNarrowHitRegions(n, availableH int) []hitRegion {
	if availableH < minPanelH*2 {
		return []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: m.width, h: availableH}}}
	}
	mainPanelH := availableH * 65 / 100
	if mainPanelH < minPanelH {
		mainPanelH = minPanelH
	}
	if maxMainH := availableH - minPanelH; mainPanelH > maxMainH {
		mainPanelH = maxMainH
	}
	regions := []hitRegion{{sessionIndex: m.selected, rect: rect{x: 0, y: 0, w: m.width, h: mainPanelH}}}

	secondaryCount := n - 1
	visible, hidden := fitSecondaryCount(m.width, minPanelW, secondaryCount)
	renderedSlots := visible
	if hidden > 0 && m.width >= minPanelW {
		renderedSlots++
	}
	if renderedSlots <= 0 {
		return regions
	}

	x := 0
	slotIndex := 0
	for idx := 0; idx < n; idx++ {
		if idx == m.selected {
			continue
		}
		if slotIndex >= visible {
			break
		}
		panelW := distributedSlotSize(m.width, renderedSlots, slotIndex)
		regions = append(regions, hitRegion{sessionIndex: idx, rect: rect{x: x, y: mainPanelH, w: panelW, h: availableH - mainPanelH}})
		x += panelW
		slotIndex++
	}
	return regions
}
