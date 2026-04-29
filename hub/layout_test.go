package hub

import (
	"reflect"
	"testing"
)

func layoutTestSession(id string) *ClientSession {
	return NewClientSession(id, id, 80, 24, nil)
}

func TestLayoutSyncSessionsPreservesUserOrderAndTracksConnections(t *testing.T) {
	a := layoutTestSession("a")
	b := layoutTestSession("b")
	c := layoutTestSession("c")
	d := layoutTestSession("d")

	layout := &layoutState{}
	layout.syncSessions([]*ClientSession{a, b, c})
	assertLayoutOrder(t, layout.order, []string{"a", "b", "c"})

	layout.order = []string{"c", "a", "b"}
	layout.syncSessions([]*ClientSession{b, c, d})
	assertLayoutOrder(t, layout.order, []string{"c", "b", "d"})
}

func TestLayoutOrderedSessionsUsesUserOrder(t *testing.T) {
	a := layoutTestSession("a")
	b := layoutTestSession("b")
	c := layoutTestSession("c")
	d := layoutTestSession("d")

	layout := &layoutState{order: []string{"missing", "c", "a"}}
	ordered := layout.orderedSessions([]*ClientSession{a, b, c, d})

	assertLayoutSessionIDs(t, ordered, []string{"c", "a", "b", "d"})
}

func TestLayoutMoveSelectedSwapsAndClamps(t *testing.T) {
	left := &layoutState{order: []string{"a", "b", "c"}}
	if got := left.moveSelected(1, -1); got != 0 {
		t.Fatalf("move left returned selected index %d, want 0", got)
	}
	assertLayoutOrder(t, left.order, []string{"b", "a", "c"})

	right := &layoutState{order: []string{"a", "b", "c"}}
	if got := right.moveSelected(1, 1); got != 2 {
		t.Fatalf("move right returned selected index %d, want 2", got)
	}
	assertLayoutOrder(t, right.order, []string{"a", "c", "b"})

	outOfBounds := &layoutState{order: []string{"a", "b", "c"}}
	if got := outOfBounds.moveSelected(0, -1); got != 0 {
		t.Fatalf("out-of-bounds move left returned selected index %d, want 0", got)
	}
	if got := outOfBounds.moveSelected(2, 1); got != 2 {
		t.Fatalf("out-of-bounds move right returned selected index %d, want 2", got)
	}
	assertLayoutOrder(t, outOfBounds.order, []string{"a", "b", "c"})
}

func TestLayoutGridColsFixedModeClampsAndPreservesMinimumPanelWidth(t *testing.T) {
	tests := []struct {
		name      string
		fixedCols int
		n         int
		width     int
		height    int
		want      int
	}{
		{
			name:      "clamps to session count",
			fixedCols: 4,
			n:         2,
			width:     200,
			height:    30,
			want:      2,
		},
		{
			name:      "reduces columns for minimum panel width",
			fixedCols: 4,
			n:         4,
			width:     60,
			height:    30,
			want:      3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			layout := &layoutState{mode: layoutFixedGrid, fixedCols: tt.fixedCols}
			if got := layout.gridCols(tt.n, tt.width, tt.height); got != tt.want {
				t.Fatalf("gridCols(%d, %d, %d) = %d, want %d", tt.n, tt.width, tt.height, got, tt.want)
			}
		})
	}
}

func TestLayoutAutoGridColsMatchesHubHeuristic(t *testing.T) {
	tests := []struct {
		name   string
		n      int
		width  int
		height int
		want   int
	}{
		{name: "single session", n: 1, width: 120, height: 40, want: 1},
		{name: "four sessions prefer two columns", n: 4, width: 120, height: 40, want: 2},
		{name: "six sessions prefer three columns", n: 6, width: 160, height: 40, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := autoGridCols(tt.n, tt.width, tt.height); got != tt.want {
				t.Fatalf("autoGridCols(%d, %d, %d) = %d, want %d", tt.n, tt.width, tt.height, got, tt.want)
			}
		})
	}
}

func TestLayoutModeName(t *testing.T) {
	tests := []struct {
		mode layoutMode
		want string
	}{
		{mode: layoutFixedGrid, want: "fixed"},
		{mode: layoutFocusBig, want: "focus-big"},
		{mode: layoutAutoGrid, want: "auto"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			layout := &layoutState{mode: tt.mode}
			if got := layout.modeName(); got != tt.want {
				t.Fatalf("modeName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func assertLayoutSessionIDs(t *testing.T, sessions []*ClientSession, want []string) {
	t.Helper()

	got := make([]string, 0, len(sessions))
	for _, session := range sessions {
		got = append(got, session.ID)
	}
	assertLayoutOrder(t, got, want)
}

func assertLayoutOrder(t *testing.T, got, want []string) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("layout order = %v, want %v", got, want)
	}
}
