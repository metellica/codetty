package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	tuiMinPanelW = 14
	tuiMinPanelH = 3
	// Side panels shorter than this threshold show only the title, no content.
	tuiSidePanelContentThreshold = 6
)

// sidePanelTitleColor is a muted teal used for all side panel title bars.
var sidePanelTitleColor = lipgloss.Color("#7aa4a1") // muted teal

// Agent state accent colors: blocked demands attention, working is a hint,
// done marks a finished run awaiting review. Hues are kept far apart
// (rose / orange / green) so the states read at a glance.
var (
	agentBlockedColor = lipgloss.Color("#e78284") // soft rose
	agentWorkingColor = lipgloss.Color("#e5a84e") // amber orange
	agentDoneColor    = lipgloss.Color("#7cc96e") // fresh green
)

// fullscreenView renders only the selected session filling the entire terminal.
func (m *tuiModel) fullscreenView() string {
	sess := m.selectedSession()
	if sess == nil {
		m.mode = modeList
		return m.listView()
	}

	// Always reserve the last line for session info (and tab bar if multi-tab).
	contentH := m.height - 1

	var content string
	if m.sel.session == sess.Name && (m.sel.active || m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY) {
		selFn := func(col, row int) bool {
			return m.selectionCovers(sess.Name, col, row)
		}
		lines := sess.RenderANSIWithSelection(m.width, contentH, selFn)
		content = strings.Join(lines, "\n")
	} else {
		lines, _, _ := sess.RenderANSI(m.width, contentH)
		content = strings.Join(lines, "\n")
	}

	return content + "\n" + m.bottomInfoLine(sess, m.width)
}

// bottomInfoLine builds the borderless bottom line shared by fullscreen and
// tab mode: the per-session tab bar on the left (if the session has multiple
// tabs) and "[n] name" session info right-aligned.
func (m *tuiModel) bottomInfoLine(sess *tuiSession, w int) string {
	sessInfo := fmt.Sprintf("[%d] %s", m.selected+1, sess.Name)
	infoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	if len(sess.Tabs) > 1 {
		tabBar := renderTabBar(sess.Tabs, sess.ActiveTab, w, false)
		pad := w - lipgloss.Width(tabBar) - lipgloss.Width(sessInfo)
		if pad > 0 {
			return tabBar + strings.Repeat(" ", pad) + infoStyle.Render(sessInfo)
		}
		return tabBar
	}

	infoRendered := infoStyle.Render(sessInfo)
	pad := w - lipgloss.Width(infoRendered)
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + infoRendered
}

// focusBigLayoutWidths returns the big-panel and side-column widths for
// focus-big mode. The big panel takes ~70% of the width; the side column
// never shrinks below tuiMinPanelW.
func (m *tuiModel) focusBigLayoutWidths() (mainW, sideW int) {
	mainW = m.width * 7 / 10
	sideW = m.width - mainW
	if sideW < tuiMinPanelW {
		sideW = tuiMinPanelW
		mainW = m.width - sideW
	}
	if mainW < 1 {
		mainW = 1
	}
	return mainW, sideW
}

// focusBigSideSlots computes the side-panel slot height and how many side
// panels fit; shared by rendering and hit-testing so they stay in sync.
func focusBigSideSlots(secondaryCount, availH int) (slotH, visible int) {
	if secondaryCount < 1 {
		return availH, 0
	}
	slotH = availH / secondaryCount
	if slotH < tuiMinPanelH {
		slotH = tuiMinPanelH
	}
	visible = availH / slotH
	if visible > secondaryCount {
		visible = secondaryCount
	}
	if visible < 1 {
		visible = 1
	}
	return slotH, visible
}

// focusBigView renders focus-big mode: the same big panel as list mode on
// the left, small switch-only session panels stacked on the right.
func (m *tuiModel) focusBigView() string {
	sessions := m.visibleSessions()
	n := len(sessions)
	if n == 0 {
		return m.statusBar()
	}
	if m.selected >= n {
		m.selected = n - 1
	}

	status := m.statusBar()
	statusH := lipgloss.Height(status)
	availH := m.height - statusH
	if availH < 1 {
		return status
	}

	primary := sessions[m.selected]
	if n == 1 {
		panel := m.renderBigPanelNoTitle(primary, m.width, availH, true)
		return lipgloss.JoinVertical(lipgloss.Left, panel, status)
	}

	mainW, sideW := m.focusBigLayoutWidths()
	mainPanel := m.renderBigPanelNoTitle(primary, mainW, availH, true)

	slotH, visible := focusBigSideSlots(n-1, availH)
	var sides []string
	idx := 0
	for i, sess := range sessions {
		if i == m.selected {
			continue
		}
		if idx >= visible {
			break
		}
		h := slotH
		if idx == visible-1 {
			h = availH - slotH*idx
		}
		sides = append(sides, m.renderFocusSidePanel(sess, i+1, sideW, h, i == m.focusCursor))
		idx++
	}
	side := lipgloss.JoinVertical(lipgloss.Left, sides...)
	row := lipgloss.JoinHorizontal(lipgloss.Top, mainPanel, side)
	return lipgloss.JoinVertical(lipgloss.Left, row, status)
}

// focusSidePositions returns the visible-session indices shown as side
// panels in focus-big mode, ordered by agent priority (blocked → done →
// rest) consistent with the list-mode Shift+Tab traversal.
func (m *tuiModel) focusSidePositions() []int {
	sessions := m.visibleSessions()
	var blocked, done, rest []int
	for i, sess := range sessions {
		if i == m.selected {
			continue
		}
		hasBlocked, hasDone := false, false
		for _, tab := range sess.Tabs {
			if tab.Agent == "" {
				continue
			}
			state := effectiveAgentState(tab)
			if state == "blocked" {
				hasBlocked = true
			} else if state == "done" {
				hasDone = true
			}
		}
		switch {
		case hasBlocked:
			blocked = append(blocked, i)
		case hasDone:
			done = append(done, i)
		default:
			rest = append(rest, i)
		}
	}
	result := append(blocked, done...)
	return append(result, rest...)
}

// focusSideCycle advances the Shift+Tab side-panel highlight: big panel →
// first side panel → … → last side panel → back to the big panel.
func (m *tuiModel) focusSideCycle() {
	pos := m.focusSidePositions()
	if len(pos) == 0 {
		m.focusCursor = -1
		return
	}
	if m.focusCursor < 0 {
		m.focusCursor = pos[0]
		return
	}
	for pi, p := range pos {
		if p == m.focusCursor {
			if pi+1 < len(pos) {
				m.focusCursor = pos[pi+1]
			} else {
				m.focusCursor = -1
			}
			return
		}
	}
	m.focusCursor = pos[0]
}

// handleFocusSideKey processes keyboard input while a focus-big side panel
// is highlighted. Side panels never receive session input: Enter opens the
// highlighted session in a floating window (for quick, temporary work),
// Space promotes it to the big panel, ↑/↓ move the highlight, and any
// other key returns focus to the big panel without leaking into the PTY.
func (m *tuiModel) handleFocusSideKey(msg tea.KeyMsg) tea.Cmd {
	pos := m.focusSidePositions()
	if len(pos) == 0 {
		m.focusCursor = -1
		return nil
	}
	pi := 0
	for i, p := range pos {
		if p == m.focusCursor {
			pi = i
			break
		}
	}
	switch msg.Type {
	case tea.KeyUp:
		m.focusCursor = pos[(pi-1+len(pos))%len(pos)]
	case tea.KeyDown:
		m.focusCursor = pos[(pi+1)%len(pos)]
	case tea.KeyEnter:
		idx := m.focusCursor
		m.focusCursor = -1
		m.openFloating(idx)
	case tea.KeySpace:
		idx := m.focusCursor
		m.focusCursor = -1
		m.promoteToBig(idx)
	default:
		m.focusCursor = -1
	}
	return nil
}

// sessionHasAgent reports whether any tab of the session hosts an agent.
func sessionHasAgent(sess *tuiSession) bool {
	for _, tab := range sess.Tabs {
		if tab.Agent != "" {
			return true
		}
	}
	return false
}

// sessionAgentBadge returns the aggregate agent indicator for a session's
// side-panel title: "" when no tab hosts an agent, otherwise the badge for
// the most urgent state using the list-mode convention (■ blocked,
// ●/○ blinking working, ● done, ○ idle).
func sessionAgentBadge(sess *tuiSession) string {
	if !sessionHasAgent(sess) {
		return ""
	}
	switch sessionAgentState(sess) {
	case "blocked":
		return "■"
	case "working":
		return agentBadge("working")
	case "done":
		return "●"
	}
	return "○"
}

// focusSideTitleColors picks the side-panel title bar palette from the
// aggregate agent state, mirroring the list-mode colors. States without an
// agent event (idle / no agent) keep the default teal bar.
func focusSideTitleColors(state string) (fg, bg lipgloss.Color) {
	switch state {
	case "blocked":
		return lipgloss.Color("231"), agentBlockedColor
	case "working":
		return lipgloss.Color("232"), agentWorkingColor
	case "done":
		return lipgloss.Color("232"), agentDoneColor
	}
	return lipgloss.Color("232"), sidePanelTitleColor
}

// renderFocusSidePanel renders one small panel in focus-big mode: a title
// bar carrying the aggregate agent badge plus a dimmed content preview.
// Small panels never receive session input — click/Enter opens a floating
// window, Ctrl+click/Space promotes to big — so no per-tab agent details,
// tab bar, or window buttons are shown. highlighted marks the Shift+Tab
// cursor with a ▶ marker and a list-cursor border.
func (m *tuiModel) renderFocusSidePanel(sess *tuiSession, num, w, h int, highlighted bool) string {
	state := sessionAgentState(sess)
	style := tuiNormalBorder.Width(w - 2).Height(h - 2).
		BorderForeground(listSessionBorderColor(sess, false, highlighted))

	innerW := w - 2
	if innerW < 1 {
		innerW = 1
	}

	title := fmt.Sprintf(" [%d] %s", num, sess.Name)
	if highlighted {
		title = " ▶" + title
	}
	if sess.Exited {
		title += " (exited)"
	}
	if badge := sessionAgentBadge(sess); badge != "" {
		title += " " + badge
	}
	if lipgloss.Width(title) > innerW {
		title = ansi.Truncate(title, innerW, "…")
	}
	fg, bg := focusSideTitleColors(state)
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(fg).
		Background(bg).
		Width(innerW).
		Render(title)

	innerH := h - 3 // borders + title line
	if innerH < 1 || h < tuiSidePanelContentThreshold {
		return style.Render(header)
	}
	lines := sess.RenderDimmed(innerW, innerH)
	return style.Render(header + "\n" + strings.Join(lines, "\n"))
}

// renderBigPanelNoTitle renders the big panel without a title header line,
// giving one extra row of terminal content. A bottom info line shows the
// tab bar (if multiple tabs) with session info right-aligned.
func (m *tuiModel) renderBigPanelNoTitle(sess *tuiSession, w, h int, focused bool) string {
	sessInfo := fmt.Sprintf("[%d] %s", m.selected+1, sess.Name)
	return m.renderBigPanelFrame(sess, w, h, focused, sessInfo)
}

// renderBigPanelFrame is the shared panel frame used by the big panel and
// the floating window: bordered terminal content with a bottom info line
// (tab bar on the left, sessInfo right-aligned).
func (m *tuiModel) renderBigPanelFrame(sess *tuiSession, w, h int, focused bool, sessInfo string) string {
	agentState := sessionAgentState(sess)

	style := tuiNormalBorder.Width(w - 2).Height(h - 2)
	if focused {
		style = tuiFocusBorder.Width(w - 2).Height(h - 2)
	}
	// Agent state colors the border like the small panels: blocked (red),
	// working (orange) and done (green) all override the focus color.
	switch agentState {
	case "blocked":
		style = style.BorderForeground(agentBlockedColor)
	case "working":
		style = style.BorderForeground(agentWorkingColor)
	case "done":
		style = style.BorderForeground(agentDoneColor)
	}

	innerW := w - 2
	innerH := h - 2 // no title row
	if innerW < 1 || innerH < 1 {
		return style.Render("")
	}

	// Reserve one line for the bottom info bar (which includes tab bar if present).
	contentH := innerH - 1
	if contentH < 1 {
		contentH = 1
	}

	var lines []string
	if m.sel.session == sess.Name && (m.sel.active || m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY) {
		selFn := func(col, row int) bool {
			return m.selectionCovers(sess.Name, col, row)
		}
		lines = sess.RenderANSIWithSelection(innerW, contentH, selFn)
	} else {
		lines, _, _ = sess.RenderANSI(innerW, contentH)
	}

	content := strings.Join(lines, "\n")

	// Build the bottom info line: tab bar on left, session info on right.
	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245"))

	hasTabBar := len(sess.Tabs) > 1
	if hasTabBar {
		tabBar := renderTabBar(sess.Tabs, sess.ActiveTab, innerW, false)
		tabBarW := lipgloss.Width(tabBar)
		infoW := lipgloss.Width(sessInfo)
		pad := innerW - tabBarW - infoW
		if pad > 0 {
			bottomLine := tabBar + strings.Repeat(" ", pad) + infoStyle.Render(sessInfo)
			return style.Render(content + "\n" + bottomLine)
		}
		// Not enough space for info, just show tab bar
		return style.Render(content + "\n" + tabBar)
	}

	// No tab bar: right-align session info
	infoRendered := infoStyle.Render(sessInfo)
	infoW := lipgloss.Width(infoRendered)
	pad := innerW - infoW
	if pad < 0 {
		pad = 0
	}
	bottomLine := strings.Repeat(" ", pad) + infoRendered
	return style.Render(content + "\n" + bottomLine)
}

// renderPanelWithColor renders a panel with a highlighted title bar for side panels.
// If colorIdx >= 0, the title gets a light green background and content is dimmed.
// If showContent is false, only the title is rendered (useful for very small side panels).
func (m *tuiModel) renderPanelWithColor(sess *tuiSession, title string, w, h int, focused bool, colorIdx int, showContent bool) string {
	agentState := sessionAgentState(sess)

	style := tuiNormalBorder.Width(w - 2).Height(h - 2)
	if focused {
		style = tuiFocusBorder.Width(w - 2).Height(h - 2)
	}
	// Border color encodes the agent state like the small panels: blocked
	// (red), working (orange) and done (green) all override the focus color.
	switch agentState {
	case "blocked":
		style = style.BorderForeground(agentBlockedColor)
	case "working":
		style = style.BorderForeground(agentWorkingColor)
	case "done":
		style = style.BorderForeground(agentDoneColor)
	}

	innerW := w - 2
	innerH := h - 3
	if innerW < 1 || innerH < 1 {
		maxTitleW := w - 4
		if maxTitleW > 0 && lipgloss.Width(title) > maxTitleW {
			title = ansi.Truncate(title, maxTitleW, "…")
		}
		return style.Render(title)
	}

	isSidePanel := colorIdx >= 0

	// Build title
	var header string
	if isSidePanel {
		// Render [-] and [x] buttons on the right
		buttons := " [-][x]"
		buttonsW := lipgloss.Width(buttons)
		titleMaxW := innerW - buttonsW - 1
		if titleMaxW < 4 {
			titleMaxW = 4
		}
		displayTitle := title
		if label := agentStateLabel(agentState); label != "" {
			displayTitle += " " + label
		}
		if lipgloss.Width(displayTitle) > titleMaxW {
			displayTitle = ansi.Truncate(displayTitle, titleMaxW, "…")
		}

		titleVisualW := lipgloss.Width(displayTitle) + 1
		padW := innerW - titleVisualW - buttonsW
		if padW < 0 {
			padW = 0
		}
		pad := strings.Repeat(" ", padW)

		// A blocked agent turns the whole title bar red.
		titleFg, titleBg := lipgloss.Color("232"), sidePanelTitleColor
		if agentState == "blocked" {
			titleFg, titleBg = lipgloss.Color("231"), agentBlockedColor
		}
		titleStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(titleFg).
			Background(titleBg).
			Width(innerW)
		header = titleStyle.Render(" " + displayTitle + pad + buttons)
	} else {
		label := agentStateLabel(agentState)
		maxTitleW := w - 4
		if label != "" {
			maxTitleW -= lipgloss.Width(label) + 1
		}
		if maxTitleW > 0 && lipgloss.Width(title) > maxTitleW {
			title = ansi.Truncate(title, maxTitleW, "…")
		}
		header = lipgloss.NewStyle().Bold(focused).Render(title)
		if label != "" {
			labelColor := agentWorkingColor
			switch agentState {
			case "blocked":
				labelColor = agentBlockedColor
			case "done":
				labelColor = agentDoneColor
			}
			header += " " + lipgloss.NewStyle().Bold(true).Foreground(labelColor).Render(label)
		}
	}

	if !showContent {
		padding := strings.Repeat("\n", innerH/2)
		return style.Render(padding + header)
	}

	// Reserve space for tab bar if session has multiple tabs
	contentH := innerH
	hasTabBar := len(sess.Tabs) > 1
	if hasTabBar {
		contentH-- // 1 line for tab bar
	}
	if contentH < 1 {
		contentH = 1
		hasTabBar = false
	}

	var lines []string
	if isSidePanel && !focused {
		lines = sess.RenderDimmed(innerW, contentH)
	} else if m.sel.session == sess.Name && (m.sel.active || m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY) {
		selFn := func(col, row int) bool {
			return m.selectionCovers(sess.Name, col, row)
		}
		lines = sess.RenderANSIWithSelection(innerW, contentH, selFn)
	} else {
		lines, _, _ = sess.RenderANSI(innerW, contentH)
	}

	content := strings.Join(lines, "\n")

	if hasTabBar {
		tabBar := renderTabBar(sess.Tabs, sess.ActiveTab, innerW, isSidePanel)
		return style.Render(header + "\n" + content + "\n" + tabBar)
	}
	return style.Render(header + "\n" + content)
}

// effectiveAgentState folds the TUI-side done marker into a tab's agent
// state: a finished-but-unseen run reads as "done".
func effectiveAgentState(tab tuiTab) string {
	if tab.Done && (tab.AgentState == "idle" || tab.AgentState == "") {
		return "done"
	}
	return tab.AgentState
}

// agentBlinkSolid is the working-badge animation phase: the ● badge
// alternates solid/hollow while any agent is working, giving a Copilot-like
// activity pulse. Toggled by agentBlinkMsg ticks; true when idle.
var agentBlinkSolid = true

// agentBadge returns a one-cell state indicator for a tab, or "".
func agentBadge(state string) string {
	switch state {
	case "blocked":
		return "■"
	case "working":
		if agentBlinkSolid {
			return "●"
		}
		return "○"
	case "done":
		return "✓"
	}
	return ""
}

// sessionAgentState aggregates tab agent states into the most urgent one
// for session-level display (blocked > done > working > idle > none).
func sessionAgentState(sess *tuiSession) string {
	agg := ""
	for _, tab := range sess.Tabs {
		switch effectiveAgentState(tab) {
		case "blocked":
			return "blocked"
		case "done":
			agg = "done"
		case "working":
			if agg != "done" {
				agg = "working"
			}
		case "idle":
			if agg == "" {
				agg = "idle"
			}
		}
	}
	return agg
}

// panelTitle builds a panel title. Agent state is conveyed by the panel
// border, header label, and status bar rather than by the title itself.
func panelTitle(num int, sess *tuiSession) string {
	return fmt.Sprintf("[%d] %s", num, sess.Name)
}

// agentStateLabel returns a short "badge + word" label for a session-level
// agent state, or "" when there is nothing to show.
func agentStateLabel(state string) string {
	switch state {
	case "blocked":
		return "■ blocked"
	case "working":
		return agentBadge("working") + " working"
	case "done":
		return "✓ done"
	}
	return ""
}

// tabBarLabel builds the display label for a tab, including the agent
// state badge. Used by both renderTabBar and hitTestTabBar so rendered
// widths and hit-test widths stay in sync.
func tabBarLabel(i int, tab tuiTab) string {
	label := tab.Name
	if label == "" {
		label = fmt.Sprintf("%d", i+1)
	}
	if badge := agentBadge(effectiveAgentState(tab)); badge != "" {
		return fmt.Sprintf(" %d:%s %s ", i+1, label, badge)
	}
	return fmt.Sprintf(" %d:%s ", i+1, label)
}

// tabStyleKind decides which style class a tab gets. Extracted for testing
// since rendered color output depends on the terminal color profile.
// In muted mode (small side panels) agent state colors are the only
// accent: the active tab gets no highlight or emphasis at all.
func tabStyleKind(tab tuiTab, isActive, muted bool) string {
	state := effectiveAgentState(tab)
	switch {
	case !muted && isActive:
		return "active"
	case state == "blocked":
		return "blocked"
	case state == "done":
		return "done"
	case state == "working":
		return "working"
	default:
		return "inactive"
	}
}

// renderTabBar renders a tab bar at the bottom of a panel.
// In muted mode (small side panels) the active tab keeps no highlight
// color so agent state colors stand out as the only accent.
func renderTabBar(tabs []tuiTab, activeIdx, maxW int, muted bool) string {
	// Cool blue keeps the active highlight far from the warm agent
	// state hues (orange working / green done).
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("232")).
		Background(lipgloss.Color("#8caaee"))

	inactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("250")).
		Background(lipgloss.Color("238"))

	// Agent-state variants: blocked demands attention, working is a
	// softer hint.
	blockedStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("232")).
		Background(agentBlockedColor)

	workingStyle := lipgloss.NewStyle().
		Foreground(agentWorkingColor).
		Background(lipgloss.Color("238"))

	doneStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(agentDoneColor).
		Background(lipgloss.Color("238"))

	separatorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	var parts []string
	totalW := 0

	for i, tab := range tabs {
		tabLabel := tabBarLabel(i, tab)
		tabW := lipgloss.Width(tabLabel)

		// Check if adding this tab would exceed width
		if totalW+tabW+1 > maxW && i > 0 {
			remaining := len(tabs) - i
			more := fmt.Sprintf(" +%d ", remaining)
			parts = append(parts, inactiveStyle.Render(more))
			break
		}

		switch tabStyleKind(tab, i == activeIdx, muted) {
		case "active":
			parts = append(parts, activeStyle.Render(tabLabel))
		case "blocked":
			parts = append(parts, blockedStyle.Render(tabLabel))
		case "done":
			parts = append(parts, doneStyle.Render(tabLabel))
		case "working":
			parts = append(parts, workingStyle.Render(tabLabel))
		default:
			parts = append(parts, inactiveStyle.Render(tabLabel))
		}
		totalW += tabW

		if i < len(tabs)-1 {
			sep := separatorStyle.Render("│")
			parts = append(parts, sep)
			totalW++
		}
	}

	return strings.Join(parts, "")
}

// hitTestTabBar returns the tab index at the given X position within the tab bar,
// or -1 if the click doesn't land on a tab.
func hitTestTabBar(tabs []tuiTab, activeIdx, maxW, x int) int {
	totalW := 0

	for i, tab := range tabs {
		tabLabel := tabBarLabel(i, tab)
		tabW := lipgloss.Width(tabLabel)

		// Same overflow check as renderTabBar
		if totalW+tabW+1 > maxW && i > 0 {
			break
		}

		if x >= totalW && x < totalW+tabW {
			return i
		}
		totalW += tabW

		// Separator
		if i < len(tabs)-1 {
			totalW++ // "│" is 1 cell wide
		}
	}
	return -1
}

// overlayFloating renders a floating window on top of the base view.
func (m *tuiModel) overlayFloating(base string) string {
	ordered := m.orderedSessions()
	if m.floatingIdx < 0 || m.floatingIdx >= len(ordered) {
		return base
	}
	sess := ordered[m.floatingIdx]

	// Floating window is 80% of terminal size
	fw := m.width * 8 / 10
	fh := m.height * 8 / 10
	if fw < 40 {
		fw = min(40, m.width)
	}
	if fh < 10 {
		fh = min(10, m.height)
	}

	// Same frame as the big panel: no title bar, bottom info line with
	// tab bar on the left and session info (plus close hint) on the right.
	sessInfo := fmt.Sprintf("[%d] %s  (^A q close)", m.floatingIdx+1, sess.Name)
	floatPanel := m.renderBigPanelFrame(sess, fw, fh, true, sessInfo)

	// Center the floating panel over the base
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		floatPanel,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("0")))
}

// overlayConfirmKill renders a kill confirmation dialog.
func (m *tuiModel) overlayConfirmKill(base string) string {
	msg := fmt.Sprintf("Kill session \"%s\"?", m.confirmKillName)
	help := "[y/Enter] confirm  [n/Esc] cancel"

	content := lipgloss.NewStyle().Bold(true).Render(msg) + "\n\n" +
		lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render(help)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("196")).
		Padding(1, 3)

	box := boxStyle.Render(content)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("0")))
}

func (m *tuiModel) statusBar() string {
	if m.renaming {
		prompt := fmt.Sprintf(" Rename: %s█  (Enter confirm, Esc cancel)", m.renameText)
		return tuiStatusStyle.Width(m.width).Render(prompt)
	}
	if m.creating {
		prompt := fmt.Sprintf(" New session name: %s█  (Enter confirm, Esc cancel)", m.createText)
		return tuiStatusStyle.Width(m.width).Render(prompt)
	}
	if m.creatingTab {
		prompt := fmt.Sprintf(" New tab name: %s█  (Enter confirm, Esc cancel)", m.createTabText)
		return tuiStatusStyle.Width(m.width).Render(prompt)
	}

	sess := m.focusedSession()
	var name string
	if sess != nil {
		name = sess.Name
	}

	if m.prefix {
		help := m.prefixLabel + ": d detach  c new  t tab  x kill  X close-tab  r rename  R reload  n/p nav  s list  b big  f full  T tabs  w manager  Shift+←→ tabs"
		return tuiStatusStyle.Width(m.width).Render(" " + help)
	}

	left := fmt.Sprintf(" [%s] %d/%d", name, m.selected+1, len(m.visibleSessions()))
	modeName := "list"
	switch m.mode {
	case modeFullscreen:
		modeName = "fullscreen"
	case modeTabs:
		modeName = "tabs"
	case modeFocusBig:
		modeName = "big"
		if m.focusCursor >= 0 {
			modeName = "big*"
		}
	case modeList:
		if m.listFocus {
			modeName = "list*"
		}
	}
	right := fmt.Sprintf(" %s | %s prefix | Ctrl+click switch ", modeName, m.prefixLabel)

	// Agent state summary, rendered as its own colored block at the left
	// edge so its background isn't cancelled by the status bar style.
	seg := m.agentStatusSegment()
	segW := lipgloss.Width(seg)

	contentW := max(0, m.width-2-segW)
	mid := strings.Repeat(" ", max(0, contentW-lipgloss.Width(left)-lipgloss.Width(right)))

	bar := tuiStatusStyle.Width(max(0, m.width-segW)).Render(left + mid + right)
	return seg + bar
}

// agentStatusSegment summarizes agent states across all sessions (including
// hidden ones) for the status bar. Returns "" when no agent is active.
func (m *tuiModel) agentStatusSegment() string {
	blocked, working, done := 0, 0, 0
	for _, sess := range m.sessions {
		for _, tab := range sess.Tabs {
			switch effectiveAgentState(tab) {
			case "blocked":
				blocked++
			case "working":
				working++
			case "done":
				done++
			}
		}
	}

	var parts []string
	if blocked > 0 {
		style := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("232")).
			Background(agentBlockedColor)
		parts = append(parts, style.Render(fmt.Sprintf(" ■ %d blocked ", blocked)))
	}
	if done > 0 {
		style := lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("232")).
			Background(agentDoneColor)
		parts = append(parts, style.Render(fmt.Sprintf(" ✓ %d done ", done)))
	}
	if working > 0 {
		style := lipgloss.NewStyle().
			Foreground(agentWorkingColor).
			Background(lipgloss.Color("235"))
		parts = append(parts, style.Render(fmt.Sprintf(" %s %d working ", agentBadge("working"), working)))
	}
	return strings.Join(parts, "")
}

func (m *tuiModel) hitTest(x, y int) int {
	sessions := m.visibleSessions()
	if len(sessions) == 0 || m.mode == modeFullscreen || m.mode == modeTabs {
		return -1
	}

	statusH := 1
	availH := m.height - statusH

	if m.mode == modeList {
		_, sideW := m.listLayoutWidths()
		if x >= sideW && y < availH {
			return m.selected
		}
		idx, _ := m.listHitTest(x, y)
		return idx
	}

	if m.mode == modeFocusBig {
		if y >= availH {
			return -1
		}
		n := len(sessions)
		if n == 1 {
			return 0
		}
		mainW, _ := m.focusBigLayoutWidths()
		if x < mainW {
			return m.selected
		}
		slotH, visible := focusBigSideSlots(n-1, availH)
		slot := y / slotH
		if slot >= visible {
			// The last visible side panel absorbs the remaining height.
			slot = visible - 1
		}
		idx := 0
		for i := 0; i < n; i++ {
			if i == m.selected {
				continue
			}
			if idx == slot {
				return i
			}
			idx++
		}
		return -1
	}
	return -1
}

// imeCursorSeq returns an ANSI CUP sequence that moves the real terminal
// cursor to the focused session's cursor position.  Appending this to the
// View() output lets Windows Terminal (and other terminals) anchor the IME
// composition window at the correct cell instead of defaulting to the
// bottom-left corner of the screen.  Returns "" when the position cannot
// be determined (no session, cursor invisible, or an overlay is active).
func (m *tuiModel) imeCursorSeq() string {
	// Overlays and text-input prompts capture keyboard for non-PTY input —
	// no meaningful session cursor position to anchor the IME window to.
	if m.sessMgr.active || m.switcher.active ||
		m.confirmKill || m.renaming || m.creating || m.creatingTab {
		return ""
	}

	// Floating window: compute centered panel position and use that session.
	if m.floatingIdx >= 0 {
		ordered := m.orderedSessions()
		if m.floatingIdx >= len(ordered) {
			return ""
		}
		sess := ordered[m.floatingIdx]

		fw := m.width * 8 / 10
		fh := m.height * 8 / 10
		if fw < 40 {
			fw = min(40, m.width)
		}
		if fh < 10 {
			fh = min(10, m.height)
		}
		// lipgloss.Place centers the panel; compute top-left offset.
		panelTopRow := (m.height - fh) / 2
		panelLeftCol := (m.width - fw) / 2
		innerW := fw - 2
		innerH := fh - 3 // borders + bottom info line
		if innerW < 1 || innerH < 1 {
			return ""
		}
		_, curX, curY := sess.RenderANSI(innerW, innerH)
		if curX < 0 || curY < 0 {
			return ""
		}
		return fmt.Sprintf("\x1b[%d;%dH", panelTopRow+1+curY+1, panelLeftCol+1+curX+1)
	}

	sessions := m.visibleSessions()
	n := len(sessions)
	if n == 0 || m.selected >= n {
		return ""
	}
	sess := sessions[m.selected]

	status := m.statusBar()
	statusH := lipgloss.Height(status)
	availH := m.height - statusH
	if availH < 1 {
		return ""
	}

	switch m.mode {
	case modeFullscreen:
		_, curX, curY := sess.RenderANSI(m.width, m.height-1)
		if curX < 0 || curY < 0 {
			return ""
		}
		return fmt.Sprintf("\x1b[%d;%dH", curY+1, curX+1)

	case modeTabs:
		contentH := m.height - 2 // top session tab bar + bottom info line
		if contentH < 1 {
			return ""
		}
		_, curX, curY := sess.RenderANSI(m.width, contentH)
		if curX < 0 || curY < 0 {
			return ""
		}
		// Content starts on the second screen row (below the tab bar).
		return fmt.Sprintf("\x1b[%d;%dH", curY+2, curX+1)

	case modeList:
		mainW, sideW := m.listLayoutWidths()
		innerW := mainW - 2
		innerH := availH - 3 // borders + bottom info line
		if innerW < 1 || innerH < 1 {
			return ""
		}
		_, curX, curY := sess.RenderANSI(innerW, innerH)
		if curX < 0 || curY < 0 {
			return ""
		}
		// top border only (no title row): +1 for border
		absRow := 1 + curY
		absCol := sideW + 1 + curX
		return fmt.Sprintf("\x1b[%d;%dH", absRow+1, absCol+1)

	case modeFocusBig:
		mainW := m.width
		if n > 1 {
			mainW, _ = m.focusBigLayoutWidths()
		}
		innerW := mainW - 2
		innerH := availH - 3 // borders + bottom info line
		if innerW < 1 || innerH < 1 {
			return ""
		}
		_, curX, curY := sess.RenderANSI(innerW, innerH)
		if curX < 0 || curY < 0 {
			return ""
		}
		// Big panel at the left edge, top border only (no title row).
		absRow := 1 + curY
		absCol := 1 + curX
		return fmt.Sprintf("\x1b[%d;%dH", absRow+1, absCol+1)
	}
	return ""
}
