package hub

type layoutMode int

const (
	layoutAutoGrid layoutMode = iota
	layoutFixedGrid
	layoutFocusBig
)

type layoutState struct {
	mode      layoutMode
	fixedCols int
	order     []string
	prefix    bool
}

func (l *layoutState) syncSessions(sessions []*ClientSession) {
	present := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		if session != nil {
			present[session.ID] = true
		}
	}

	nextOrder := make([]string, 0, len(sessions))
	seen := make(map[string]bool, len(sessions))
	for _, id := range l.order {
		if present[id] && !seen[id] {
			nextOrder = append(nextOrder, id)
			seen[id] = true
		}
	}
	for _, session := range sessions {
		if session != nil && !seen[session.ID] {
			nextOrder = append(nextOrder, session.ID)
			seen[session.ID] = true
		}
	}
	l.order = nextOrder
}

func (l *layoutState) orderedSessions(sessions []*ClientSession) []*ClientSession {
	byID := make(map[string]*ClientSession, len(sessions))
	for _, session := range sessions {
		if session != nil {
			byID[session.ID] = session
		}
	}

	ordered := make([]*ClientSession, 0, len(sessions))
	seen := make(map[string]bool, len(sessions))
	for _, id := range l.order {
		if session, ok := byID[id]; ok && !seen[id] {
			ordered = append(ordered, session)
			seen[id] = true
		}
	}
	for _, session := range sessions {
		if session != nil && !seen[session.ID] {
			ordered = append(ordered, session)
			seen[session.ID] = true
		}
	}
	return ordered
}

func (l *layoutState) moveSelected(selected, delta int) int {
	target := selected + delta
	if selected < 0 || selected >= len(l.order) || target < 0 || target >= len(l.order) {
		return selected
	}
	l.order[selected], l.order[target] = l.order[target], l.order[selected]
	return target
}

func (l *layoutState) gridCols(n, width, height int) int {
	if n <= 1 {
		return 1
	}

	if l.mode == layoutFixedGrid {
		cols := l.fixedCols
		if cols < 1 {
			cols = 1
		}
		if cols > n {
			cols = n
		}
		for cols > 1 && width/cols < 20 {
			cols--
		}
		return cols
	}

	return autoGridCols(n, width, height)
}

func (l *layoutState) modeName() string {
	switch l.mode {
	case layoutFixedGrid:
		return "fixed"
	case layoutFocusBig:
		return "focus-big"
	default:
		return "auto"
	}
}

func autoGridCols(n, width, height int) int {
	if n <= 1 {
		return 1
	}

	usableH := height - 2
	if usableH < 1 {
		usableH = 1
	}

	bestCols := 1
	bestDist := 999.0
	for c := 1; c <= n; c++ {
		if c > 4 {
			break
		}
		rows := (n + c - 1) / c
		pw := width / c
		ph := usableH / rows
		if ph < 4 || pw < 20 {
			continue
		}
		ratio := float64(pw) / float64(ph)
		dist := ratio - 3.0
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			bestDist = dist
			bestCols = c
		}
	}
	return bestCols
}
