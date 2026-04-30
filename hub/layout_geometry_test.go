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
