package hub

import "testing"

func TestModel_GridHitRegionsMatchVisiblePanels(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFixedGrid
	m.layout.fixedCols = 2

	regions := m.layoutHitRegions()
	if len(regions) != 2 {
		t.Fatalf("regions len = %d, want 2: %#v", len(regions), regions)
	}
	if regions[0].sessionIndex != 0 || regions[1].sessionIndex != 1 {
		t.Fatalf("region indexes = %d,%d want 0,1", regions[0].sessionIndex, regions[1].sessionIndex)
	}
	if got := m.hitTestSession(regions[0].x+1, regions[0].y+1); got != 0 {
		t.Fatalf("hit first panel = %d, want 0", got)
	}
	if got := m.hitTestSession(regions[1].x+1, regions[1].y+1); got != 1 {
		t.Fatalf("hit second panel = %d, want 1", got)
	}
}

func TestModel_GridHitTestIgnoresStatusAndEmptyCells(t *testing.T) {
	m := NewModel(New())
	m.width = 80
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
		NewClientSession("gamma", "gamma", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFixedGrid
	m.layout.fixedCols = 2

	regions := m.layoutHitRegions()
	if len(regions) != 3 {
		t.Fatalf("regions len = %d, want 3: %#v", len(regions), regions)
	}
	emptyX := regions[1].x + 1
	emptyY := regions[2].y + 1
	if got := m.hitTestSession(0, m.height-1); got != -1 {
		t.Fatalf("status hit = %d, want -1", got)
	}
	if got := m.hitTestSession(emptyX, emptyY); got != -1 {
		t.Fatalf("empty filler hit = %d, want -1", got)
	}
}

func TestModel_FocusBigWideHitRegionsSelectMainAndSecondary(t *testing.T) {
	m := NewModel(New())
	m.width = 140
	m.height = 30
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
		NewClientSession("gamma", "gamma", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFocusBig
	m.selected = 1

	regions := m.layoutHitRegions()
	if len(regions) < 2 {
		t.Fatalf("focus-big regions len = %d, want at least 2: %#v", len(regions), regions)
	}
	if got := m.hitTestSession(1, 1); got != 1 {
		t.Fatalf("main panel hit = %d, want selected index 1", got)
	}
	foundSecondary := false
	for _, region := range regions {
		if region.sessionIndex == 0 {
			foundSecondary = true
			if got := m.hitTestSession(region.x+1, region.y+1); got != 0 {
				t.Fatalf("secondary hit = %d, want 0", got)
			}
		}
	}
	if !foundSecondary {
		t.Fatalf("expected a visible secondary region for session 0: %#v", regions)
	}
}

func TestModel_FocusBigHiddenSecondaryDoesNotHit(t *testing.T) {
	m := NewModel(New())
	m.width = 40
	m.height = 8
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
		NewClientSession("gamma", "gamma", 80, 24, nil),
		NewClientSession("delta", "delta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFocusBig
	m.selected = 0

	for _, region := range m.layoutHitRegions() {
		if region.sessionIndex >= len(m.sessions) {
			t.Fatalf("hidden +N more region should not be selectable: %#v", region)
		}
	}
}

func TestModel_FocusBigMorePanelAreaDoesNotHit(t *testing.T) {
	m := NewModel(New())
	m.width = 40
	m.height = 24
	m.sessions = []*ClientSession{
		NewClientSession("alpha", "alpha", 80, 24, nil),
		NewClientSession("beta", "beta", 80, 24, nil),
		NewClientSession("gamma", "gamma", 80, 24, nil),
		NewClientSession("delta", "delta", 80, 24, nil),
	}
	m.layout.syncSessions(m.sessions)
	m.layout.mode = layoutFocusBig
	m.selected = 0

	n := len(m.sessions)
	statusH := renderedLineCount(m.focusBigStatusBar(n))
	availableH := m.height - statusH
	mainPanelH := availableH * 65 / 100
	if mainPanelH < minPanelH {
		mainPanelH = minPanelH
	}
	if maxMainH := availableH - minPanelH; mainPanelH > maxMainH {
		mainPanelH = maxMainH
	}
	visible, hidden := fitSecondaryCount(m.width, minPanelW, n-1)
	if visible != 1 || hidden != 2 {
		t.Fatalf("test setup visible, hidden = %d, %d; want 1, 2", visible, hidden)
	}

	morePanelX := distributedSlotSize(m.width, visible+1, 0) + 1
	morePanelY := mainPanelH + 1
	if got := m.hitTestSession(morePanelX, morePanelY); got != -1 {
		t.Fatalf("+N more panel hit = %d, want -1", got)
	}
}
