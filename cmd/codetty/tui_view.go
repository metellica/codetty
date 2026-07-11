package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	tuiMinPanelW = 14
	tuiMinPanelH = 3
	// Side panels shorter than this threshold show only the title, no content.
	tuiSidePanelContentThreshold = 6
)

// sidePanelTitleColor is a light green used for all side panel title bars.
var sidePanelTitleColor = lipgloss.Color("78") // light green

// Agent state accent colors: blocked demands attention, working is a hint,
// done marks a finished run awaiting review.
// Using muted pastel tones for low-contrast modern aesthetics.
var (
	agentBlockedColor = lipgloss.Color("#e78284") // soft rose
	agentWorkingColor = lipgloss.Color("#e5c890") // warm amber
	agentDoneColor    = lipgloss.Color("#a6d189") // soft sage
)

// fullscreenView renders only the selected session filling the entire terminal.
func (m *tuiModel) fullscreenView() string {
	sess := m.selectedSession()
	if sess == nil {
		m.mode = modeFocusBig
		return m.focusBigView()
	}

	hasTabBar := len(sess.Tabs) > 1
	contentH := m.height
	if hasTabBar {
		contentH--
	}

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

	if hasTabBar {
		tabBar := renderTabBar(sess.Tabs, sess.ActiveTab, m.width, false)
		return content + "\n" + tabBar
	}
	return content
}

func (m *tuiModel) focusBigView() string {
	sessions := m.visibleSessions()
	n := len(sessions)
	if n == 0 || m.selected >= n {
		return m.gridView()
	}

	status := m.statusBar()
	statusH := lipgloss.Height(status)
	availableH := m.height - statusH
	if availableH < 1 {
		return status
	}

	if n == 1 {
		sess := sessions[m.selected]
		title := panelTitle(1, sess)
		panel := m.renderPanel(sess, title, m.width, availableH, true)
		return lipgloss.JoinVertical(lipgloss.Left, panel, status)
	}

	if m.width >= 120 {
		return lipgloss.JoinVertical(lipgloss.Left, m.focusBigWide(sessions, availableH), status)
	}
	return lipgloss.JoinVertical(lipgloss.Left, m.focusBigNarrow(sessions, availableH), status)
}

func (m *tuiModel) focusBigWide(sessions []*tuiSession, availH int) string {
	primary := sessions[m.selected]
	mainW := m.width * 7 / 10
	sideW := m.width - mainW
	if sideW < tuiMinPanelW {
		sideW = tuiMinPanelW
		mainW = m.width - sideW
	}

	mainTitle := fmt.Sprintf("▶ [%d] %s", m.selected+1, primary.Name)
	mainPanel := m.renderPanel(primary, mainTitle, mainW, availH, true)

	secondaryCount := len(sessions) - 1
	if secondaryCount == 0 {
		return mainPanel
	}
	slotH := availH / secondaryCount
	if slotH < tuiMinPanelH {
		slotH = tuiMinPanelH
	}
	visible := availH / slotH
	if visible > secondaryCount {
		visible = secondaryCount
	}

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
		title := panelTitle(i+1, sess)
		highlighted := m.inputFocus == i
		if highlighted {
			title = "▶ " + title
		}
		if m.drag.active && i == m.drag.targetIdx && i != m.drag.sourceIdx {
			title = "⬇ " + title
		}
		showContent := h >= tuiSidePanelContentThreshold
		sides = append(sides, m.renderPanelWithColor(sess, title, sideW, h, highlighted, i, showContent))
		idx++
	}

	side := lipgloss.JoinVertical(lipgloss.Left, sides...)
	return lipgloss.JoinHorizontal(lipgloss.Top, mainPanel, side)
}

func (m *tuiModel) focusBigNarrow(sessions []*tuiSession, availH int) string {
	primary := sessions[m.selected]
	mainH := availH * 7 / 10
	sideH := availH - mainH

	mainTitle := fmt.Sprintf("▶ [%d] %s", m.selected+1, primary.Name)
	mainPanel := m.renderPanel(primary, mainTitle, m.width, mainH, true)

	var bottomPanel string
	for i, sess := range sessions {
		if i != m.selected {
			title := panelTitle(i+1, sess)
			highlighted := m.inputFocus == i
			if highlighted {
				title = "▶ " + title
			}
			if m.drag.active && i == m.drag.targetIdx && i != m.drag.sourceIdx {
				title = "⬇ " + title
			}
			showContent := sideH >= tuiSidePanelContentThreshold
			bottomPanel = m.renderPanelWithColor(sess, title, m.width, sideH, highlighted, i, showContent)
			break
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, mainPanel, bottomPanel)
}

func (m *tuiModel) gridView() string {
	sessions := m.visibleSessions()
	n := len(sessions)
	if n == 0 {
		return m.statusBar()
	}

	status := m.statusBar()
	statusH := lipgloss.Height(status)
	availH := m.height - statusH

	cols := gridCols(n, m.width)
	rows := (n + cols - 1) / cols
	panelW := m.width / cols
	panelH := availH / rows
	if panelH < tuiMinPanelH {
		panelH = tuiMinPanelH
	}

	var rowViews []string
	for r := 0; r < rows; r++ {
		var colViews []string
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx >= n {
				colViews = append(colViews, strings.Repeat(" ", panelW))
				continue
			}
			sess := sessions[idx]
			title := panelTitle(idx+1, sess)
			focused := idx == m.selected
			if focused {
				title = "▶ " + title
			}
			if m.drag.active && idx == m.drag.targetIdx && idx != m.drag.sourceIdx {
				title = "⬇ " + title
			}
			// All grid panels use colored title; only non-selected panels dim content
			colViews = append(colViews, m.renderPanelWithColor(sess, title, panelW, panelH, focused, idx, panelH >= tuiSidePanelContentThreshold))
		}
		rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
	}

	grid := lipgloss.JoinVertical(lipgloss.Left, rowViews...)
	return lipgloss.JoinVertical(lipgloss.Left, grid, status)
}

func (m *tuiModel) renderPanel(sess *tuiSession, title string, w, h int, focused bool) string {
	return m.renderPanelWithColor(sess, title, w, h, focused, -1, true)
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
	// Border color encodes the most urgent agent state: blocked (red)
	// overrides even the focus color; working (orange) and done (green)
	// only replace the unfocused border.
	switch agentState {
	case "blocked":
		style = style.BorderForeground(agentBlockedColor)
	case "working":
		if !focused {
			style = style.BorderForeground(agentWorkingColor)
		}
	case "done":
		if !focused {
			style = style.BorderForeground(agentDoneColor)
		}
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
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("232")).
		Background(lipgloss.Color("#efb58e"))

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

	title := fmt.Sprintf("▶ [%d] %s  (^A q close)", m.floatingIdx+1, sess.Name)
	floatPanel := m.renderPanel(sess, title, fw, fh, true)

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
	if m.createChooser {
		prompt := " New session: [c]odetty  [t]mux  (Esc cancel)"
		return tuiStatusStyle.Width(m.width).Render(prompt)
	}
	if m.creating {
		kind := "session"
		if m.createTmux {
			kind = "tmux session"
		}
		prompt := fmt.Sprintf(" New %s name: %s█  (Enter confirm, Esc cancel)", kind, m.createText)
		return tuiStatusStyle.Width(m.width).Render(prompt)
	}

	sess := m.focusedSession()
	var name string
	if sess != nil {
		name = sess.Name
	}

	if m.prefix {
		help := m.prefixLabel + ": d detach  c new  T tab  x kill  X close-tab  r rename  n/p nav  s list  b big  g grid  f full  Shift+←→ tabs"
		return tuiStatusStyle.Width(m.width).Render(" " + help)
	}

	left := fmt.Sprintf(" [%s] %d/%d", name, m.selected+1, len(m.visibleSessions()))
	modeName := "focus"
	switch m.mode {
	case modeGrid:
		modeName = "grid"
	case modeFullscreen:
		modeName = "fullscreen"
	case modeList:
		modeName = "list"
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
	n := len(sessions)
	if n == 0 || m.mode == modeFullscreen {
		return -1
	}

	statusH := 1
	availH := m.height - statusH

	if m.mode == modeList {
		mainW, _ := m.listLayoutWidths()
		if x < mainW && y < availH {
			return m.selected
		}
		idx, _ := m.listHitTest(x, y)
		return idx
	}

	if m.mode == modeGrid {
		cols := gridCols(n, m.width)
		panelW := m.width / cols
		rows := (n + cols - 1) / cols
		panelH := availH / rows
		if panelH < tuiMinPanelH {
			panelH = tuiMinPanelH
		}
		col := x / panelW
		row := y / panelH
		idx := row*cols + col
		if idx >= 0 && idx < n {
			return idx
		}
		return -1
	}

	// Focus-big mode
	if n == 1 {
		if y < availH {
			return 0
		}
		return -1
	}

	if m.width >= 120 {
		mainW := m.width * 7 / 10
		if x < mainW {
			return m.selected
		}
		// Sidebar panels
		sideW := m.width - mainW
		if sideW < tuiMinPanelW {
			return m.selected
		}
		secondaryCount := n - 1
		slotH := availH / secondaryCount
		if slotH < tuiMinPanelH {
			slotH = tuiMinPanelH
		}
		visible := availH / slotH
		if visible > secondaryCount {
			visible = secondaryCount
		}
		slot := y / slotH
		if slot >= visible {
			return -1
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
	} else {
		// Narrow: top is main, bottom is secondary
		mainH := availH * 7 / 10
		if y < mainH {
			return m.selected
		}
		for i := 0; i < n; i++ {
			if i != m.selected {
				return i
			}
		}
	}
	return -1
}

func gridCols(n, width int) int {
	if n <= 1 {
		return 1
	}
	return 2
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
	if m.tmuxPicker.active || m.sessMgr.active || m.switcher.active ||
		m.confirmKill || m.renaming || m.creating || m.createChooser {
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
		innerH := fh - 3
		if innerW < 1 || innerH < 1 {
			return ""
		}
		_, curX, curY := sess.RenderANSI(innerW, innerH)
		if curX < 0 || curY < 0 {
			return ""
		}
		return fmt.Sprintf("\x1b[%d;%dH", panelTopRow+2+curY+1, panelLeftCol+1+curX+1)
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

	// absPos computes absolute (row, col) given content-space cursor and panel top-left.
	// Panels rendered by renderPanelWithColor have: 1 col left border, 1 row top border +
	// 1 row title header = 2 rows before the first content line.
	mkSeq := func(panelTopRow, panelLeftCol, innerW, innerH int) string {
		if innerW < 1 || innerH < 1 {
			return ""
		}
		_, curX, curY := sess.RenderANSI(innerW, innerH)
		if curX < 0 || curY < 0 {
			return ""
		}
		absRow := panelTopRow + 2 + curY
		absCol := panelLeftCol + 1 + curX
		// ANSI CUP is 1-indexed.
		return fmt.Sprintf("\x1b[%d;%dH", absRow+1, absCol+1)
	}

	switch m.mode {
	case modeFullscreen:
		_, curX, curY := sess.RenderANSI(m.width, m.height)
		if curX < 0 || curY < 0 {
			return ""
		}
		return fmt.Sprintf("\x1b[%d;%dH", curY+1, curX+1)

	case modeGrid:
		cols := gridCols(n, m.width)
		rows := (n + cols - 1) / cols
		panelW := m.width / cols
		panelH := availH / rows
		if panelH < tuiMinPanelH {
			panelH = tuiMinPanelH
		}
		pRow := m.selected / cols
		pCol := m.selected % cols
		return mkSeq(pRow*panelH, pCol*panelW, panelW-2, panelH-3)

	case modeList:
		mainW, _ := m.listLayoutWidths()
		return mkSeq(0, 0, mainW-2, availH-3)

	default: // modeFocusBig
		// The selected session is always the big/main panel at the top-left.
		if n == 1 {
			return mkSeq(0, 0, m.width-2, availH-3)
		}
		if m.width >= 120 {
			mainW := m.width * 7 / 10
			sideW := m.width - mainW
			if sideW < tuiMinPanelW {
				sideW = tuiMinPanelW
				mainW = m.width - sideW
			}
			return mkSeq(0, 0, mainW-2, availH-3)
		}
		// Narrow layout: main panel takes top 70% of height.
		mainH := availH * 7 / 10
		return mkSeq(0, 0, m.width-2, mainH-3)
	}
}
