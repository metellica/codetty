package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"codetty/vt10x"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// textSelection tracks in-pane text selection state.
type textSelection struct {
	active  bool   // actively selecting (drag started)
	pending bool   // mouse pressed but not yet dragged
	session string // which session is being selected in
	// Coordinates in session-local terminal space (0-based col/row)
	startX, startY int
	endX, endY     int
	// Screen coordinates of the press (for forwarding to PTY)
	screenX, screenY int
}

// panelDrag tracks drag-to-reorder state for side panel titles.
type panelDrag struct {
	active     bool
	sourceIdx  int // visible index of panel being dragged
	sourceName string
	startY     int // screen Y where drag started
	targetIdx  int // current drop target visible index
}

// panelRegion describes where a session's content is rendered on screen.
type panelRegion struct {
	sessionIdx int
	// Screen coordinates of the content area (inside borders/title)
	contentX, contentY int
	contentW, contentH int
}

// selViewInterval caps selection-drag frame regeneration at ~30fps.
const selViewInterval = 33 * time.Millisecond

// selFlushMsg forces a fresh frame after a throttled selection drag,
// so the highlight always settles at the final mouse position.
type selFlushMsg struct{}

// selMotionCmd throttles frame regeneration while dragging a selection.
// Mouse-motion events can arrive faster than a full frame can be rendered;
// without throttling the message queue backs up and the highlight lags
// further and further behind the mouse. When a frame is skipped, a trailing
// tick is scheduled so the final position is always rendered.
func (m *tuiModel) selMotionCmd() tea.Cmd {
	now := time.Now()
	if now.Sub(m.selViewAt) >= selViewInterval {
		m.selViewAt = now
		return nil
	}
	m.selViewSkip = true
	if m.selFlushPending {
		return nil
	}
	m.selFlushPending = true
	return tea.Tick(selViewInterval, func(time.Time) tea.Msg { return selFlushMsg{} })
}

// handleMouse processes mouse events for selection and panel switching.
func (m *tuiModel) handleMouse(msg tea.MouseMsg) tea.Cmd {
	if m.mode == modeFullscreen {
		return m.handleFullscreenMouse(msg)
	}

	// If floating window is open, forward mouse events to the floating session
	if m.floatingIdx >= 0 {
		return m.handleFloatingMouse(msg)
	}

	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			// List mode sidebar: clicking an entry switches the big
			// panel and auto-activates the priority tab.
			if m.mode == modeList {
				if _, sideW := m.listLayoutWidths(); msg.X < sideW {
					if idx, _ := m.listHitTest(msg.X, msg.Y); idx >= 0 {
						m.selected = idx
						m.listCursor = idx
						m.listCursorTab = -1
						m.inputFocus = -1
						sessions := m.visibleSessions()
						if idx < len(sessions) {
							sess := sessions[idx]
							targetTab := sessionPriorityTab(sess)
							if targetTab >= 0 && targetTab != sess.ActiveTab {
								m.requestTabSwitch(sess.Name, targetTab)
							}
						}
					}
					m.sel = textSelection{}
					return nil
				}
			}

			// Ctrl+Click: promote panel to big (switch m.selected)
			if msg.Ctrl {
				if idx := m.hitTest(msg.X, msg.Y); idx >= 0 {
					m.selected = idx
					m.inputFocus = -1
				}
				m.sel = textSelection{}
				return nil
			}

			// Plain click on panels
			regions := m.panelRegions()
			sessions := m.visibleSessions()

			// Check for tab bar clicks first. Small panels (side panels
			// and grid cells) are read-only monitors: only the big
			// panel's tab bar is interactive here; small panels are
			// operated through their floating window.
			for _, r := range regions {
				if r.sessionIdx >= len(sessions) {
					continue
				}
				if (m.mode != modeFocusBig && m.mode != modeList) || r.sessionIdx != m.selected {
					continue
				}
				sess := sessions[r.sessionIdx]
				if len(sess.Tabs) <= 1 {
					continue
				}
				// Tab bar is on the last line of the content area
				tabBarY := r.contentY + r.contentH - 1
				if msg.Y == tabBarY && msg.X >= r.contentX && msg.X < r.contentX+r.contentW {
					localX := msg.X - r.contentX
					if tabIdx := hitTestTabBar(sess.Tabs, sess.ActiveTab, r.contentW, localX); tabIdx >= 0 {
						m.requestTabSwitch(sess.Name, tabIdx)
					}
					// The tab bar row belongs to the TUI, not the app:
					// consume the click either way.
					m.sel = textSelection{}
					return nil
				}
			}

			for _, r := range regions {
				localX := msg.X - r.contentX
				localY := msg.Y - r.contentY
				if localX >= 0 && localX < r.contentW && localY >= 0 && localY < r.contentH {
					if m.mode == modeGrid || r.sessionIdx != m.selected {
						// Grid mode or side panel: open floating window
						m.openFloating(r.sessionIdx)
						m.sel = textSelection{}
						return nil
					}
					// Clicking the big panel. If the app handles its own
					// mouse (tmux, vim, copilot, ...), forward the real
					// press so in-app clicks and drags work; otherwise
					// start a text selection.
					sess := sessions[r.sessionIdx]
					if sess.MouseEnabled() {
						m.forwardMouseToSession(sess.Name, msg, localX, localY)
						m.mouseFwdDown = true
						m.sel = textSelection{}
						return nil
					}
					m.sel = textSelection{
						pending: true,
						session: sess.Name,
						startX:  localX,
						startY:  localY,
						endX:    localX,
						endY:    localY,
						screenX: msg.X,
						screenY: msg.Y,
					}
					return nil
				}
			}

			// Check for button clicks on side panel title bars
			if idx, action := m.hitTestTitleButton(msg.X, msg.Y); idx >= 0 && action != "" {
				visible := m.visibleSessions()
				if idx < len(visible) {
					sess := visible[idx]
					switch action {
					case "hide":
						m.hideSession(sess.Name)
						return tea.ClearScreen
					case "kill":
						m.confirmKill = true
						m.confirmKillName = sess.Name
						return nil
					}
				}
			}

			// Check for title bar drag initiation (not on buttons)
			if idx := m.hitTestTitleBar(msg.X, msg.Y); idx >= 0 {
				visible := m.visibleSessions()
				if idx < len(visible) && (m.mode == modeGrid || idx != m.selected) {
					m.drag = panelDrag{
						active:     true,
						sourceIdx:  idx,
						sourceName: visible[idx].Name,
						startY:     msg.Y,
						targetIdx:  idx,
					}
					m.sel = textSelection{}
					return nil
				}
			}

			// Click on border/title area (fallback: open floating)
			if idx := m.hitTest(msg.X, msg.Y); idx >= 0 {
				if m.mode == modeGrid || idx != m.selected {
					m.openFloating(idx)
				}
			}
			m.sel = textSelection{}
		}
		// Forward scroll wheel to PTY (with local coordinate translation).
		// Only the big panel receives wheel input; small panels (side
		// panels, grid cells) are read-only monitors.
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			// List mode sidebar: scroll the session list.
			if m.mode == modeList {
				if _, sideW := m.listLayoutWidths(); msg.X < sideW {
					if msg.Button == tea.MouseButtonWheelUp {
						m.listScroll--
					} else {
						m.listScroll++
					}
					if m.listScroll < 0 {
						m.listScroll = 0
					}
					return nil
				}
			}
			regions := m.panelRegions()
			sessions := m.visibleSessions()
			for _, r := range regions {
				localX := msg.X - r.contentX
				localY := msg.Y - r.contentY
				if localX >= 0 && localX < r.contentW && localY >= 0 && localY < r.contentH {
					if (m.mode == modeFocusBig || m.mode == modeList) && r.sessionIdx == m.selected {
						sess := sessions[r.sessionIdx]
						if sess.MouseEnabled() {
							m.forwardMouseToSession(sess.Name, msg, localX, localY)
						}
					}
					break
				}
			}
		}

	case tea.MouseActionMotion:
		// Panel drag-to-reorder
		if m.drag.active {
			if idx := m.hitTestTitleBar(msg.X, msg.Y); idx >= 0 {
				m.drag.targetIdx = idx
			} else if idx := m.hitTest(msg.X, msg.Y); idx >= 0 {
				m.drag.targetIdx = idx
			}
			return nil
		}

		if m.sel.pending {
			// First motion after press: start real selection
			regions := m.panelRegions()
			for _, r := range regions {
				sess := m.visibleSessions()[r.sessionIdx]
				if sess.Name != m.sel.session {
					continue
				}
				localX := msg.X - r.contentX
				localY := msg.Y - r.contentY
				if localX < 0 {
					localX = 0
				}
				if localX >= r.contentW {
					localX = r.contentW - 1
				}
				if localY < 0 {
					localY = 0
				}
				if localY >= r.contentH {
					localY = r.contentH - 1
				}
				// Only promote to active if actually moved
				if localX != m.sel.startX || localY != m.sel.startY {
					m.sel.pending = false
					m.sel.active = true
					m.sel.endX = localX
					m.sel.endY = localY
				}
				return nil
			}
		} else if m.sel.active {
			regions := m.panelRegions()
			for _, r := range regions {
				sess := m.visibleSessions()[r.sessionIdx]
				if sess.Name != m.sel.session {
					continue
				}
				localX := msg.X - r.contentX
				localY := msg.Y - r.contentY
				if localX < 0 {
					localX = 0
				}
				if localX >= r.contentW {
					localX = r.contentW - 1
				}
				if localY < 0 {
					localY = 0
				}
				if localY >= r.contentH {
					localY = r.contentH - 1
				}
				m.sel.endX = localX
				m.sel.endY = localY
				return m.selMotionCmd()
			}
		} else if m.mode == modeFocusBig || m.mode == modeList {
			// Forward motion to big panel only if mouse reporting is enabled.
			if sess := m.selectedSession(); sess != nil && sess.MouseEnabled() {
				regions := m.panelRegions()
				for _, r := range regions {
					if r.sessionIdx != m.selected {
						continue
					}
					localX := msg.X - r.contentX
					localY := msg.Y - r.contentY
					if localX >= 0 && localX < r.contentW && localY >= 0 && localY < r.contentH {
						m.forwardMouseToSession(sess.Name, msg, localX, localY)
					}
					break
				}
			}
		}

	case tea.MouseActionRelease:
		// Panel drag-to-reorder release
		if m.drag.active {
			source := m.drag.sourceIdx
			target := m.drag.targetIdx
			m.drag = panelDrag{}
			if source != target {
				m.reorderByVisibleIdx(source, target)
			}
			return tea.ClearScreen
		}

		if m.sel.active {
			m.sel.active = false
			if m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY {
				text := m.extractSelectedText()
				if text != "" {
					return m.copyToClipboard(text)
				}
			}
			m.sel = textSelection{}
		} else if m.sel.pending {
			// Click without drag on big panel: forward to PTY only if the
			// session has mouse reporting enabled (tmux, vim, etc.).
			m.sel.pending = false
			if m.mode == modeFocusBig || m.mode == modeList {
				if sess := m.selectedSession(); sess != nil && sess.MouseEnabled() {
					m.forwardClickToPTY(m.sel.startX, m.sel.startY)
				}
			}
			m.sel = textSelection{}
		} else if m.mouseFwdDown && (m.mode == modeFocusBig || m.mode == modeList) {
			// The press was forwarded directly to a mouse-enabled app, so
			// forward the matching release too.
			m.mouseFwdDown = false
			if sess := m.selectedSession(); sess != nil && sess.MouseEnabled() {
				for _, r := range m.panelRegions() {
					if r.sessionIdx != m.selected {
						continue
					}
					localX := clamp(msg.X-r.contentX, 0, r.contentW-1)
					localY := clamp(msg.Y-r.contentY, 0, r.contentH-1)
					m.forwardMouseToSession(sess.Name, msg, localX, localY)
					break
				}
			}
		}
	}
	return nil
}

// handleFullscreenMouse handles mouse in fullscreen mode (entire screen is one session).
func (m *tuiModel) handleFullscreenMouse(msg tea.MouseMsg) tea.Cmd {
	sess := m.selectedSession()
	if sess == nil {
		return nil
	}
	mouseOn := sess.MouseEnabled()

	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			// Check tab bar click in fullscreen (tab bar is on the last line)
			if len(sess.Tabs) > 1 && msg.Y == m.height-1 {
				if tabIdx := hitTestTabBar(sess.Tabs, sess.ActiveTab, m.width, msg.X); tabIdx >= 0 {
					m.requestTabSwitch(sess.Name, tabIdx)
					m.sel = textSelection{}
					return nil
				}
			}

			// Mouse-enabled app: forward the real press so drags and
			// press/release pairs reach the app; selection is bypassed.
			if mouseOn {
				m.forwardMouseToPTY(msg)
				m.mouseFwdDown = true
				m.sel = textSelection{}
				return nil
			}

			m.sel = textSelection{
				pending: true,
				session: sess.Name,
				startX:  msg.X,
				startY:  msg.Y,
				endX:    msg.X,
				endY:    msg.Y,
				screenX: msg.X,
				screenY: msg.Y,
			}
			return nil
		}
		// Forward scroll and other buttons directly
		if mouseOn {
			m.forwardMouseToPTY(msg)
		}
	case tea.MouseActionMotion:
		if m.sel.pending {
			if msg.X != m.sel.startX || msg.Y != m.sel.startY {
				m.sel.pending = false
				m.sel.active = true
				m.sel.endX = msg.X
				m.sel.endY = msg.Y
			}
		} else if m.sel.active {
			x, y := msg.X, msg.Y
			if x < 0 {
				x = 0
			}
			if x >= m.width {
				x = m.width - 1
			}
			if y < 0 {
				y = 0
			}
			if y >= m.height {
				y = m.height - 1
			}
			m.sel.endX = x
			m.sel.endY = y
			return m.selMotionCmd()
		} else if mouseOn {
			m.forwardMouseToPTY(msg)
		}
	case tea.MouseActionRelease:
		if m.sel.active {
			m.sel.active = false
			if m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY {
				text := m.extractSelectedText()
				if text != "" {
					return m.copyToClipboard(text)
				}
			}
			m.sel = textSelection{}
		} else if m.sel.pending {
			m.sel.pending = false
			// Don't forward clicks on the tab bar to PTY
			if mouseOn && !(len(sess.Tabs) > 1 && m.sel.startY == m.height-1) {
				m.forwardClickToPTY(m.sel.startX, m.sel.startY)
			}
			m.sel = textSelection{}
		} else {
			m.mouseFwdDown = false
			// Don't forward release events on the tab bar line
			if !(len(sess.Tabs) > 1 && msg.Y == m.height-1) {
				m.forwardMouseToPTY(msg)
			}
		}
	}
	return nil
}

// forwardMouseToPTY sends a mouse event as SGR escape sequence to the focused session PTY.
func (m *tuiModel) forwardMouseToPTY(msg tea.MouseMsg) {
	sess := m.selectedSession()
	if sess == nil || m.conn == nil {
		return
	}

	var btn int
	switch msg.Button {
	case tea.MouseButtonLeft:
		btn = 0
	case tea.MouseButtonMiddle:
		btn = 1
	case tea.MouseButtonRight:
		btn = 2
	case tea.MouseButtonWheelUp:
		btn = 64
	case tea.MouseButtonWheelDown:
		btn = 65
	default:
		btn = 3 // release
	}

	if msg.Shift {
		btn |= 4
	}
	if msg.Alt {
		btn |= 8
	}
	if msg.Ctrl {
		btn |= 16
	}
	if msg.Action == tea.MouseActionMotion {
		btn |= 32
	}

	x, y := msg.X+1, msg.Y+1 // SGR uses 1-based coordinates
	var suffix byte = 'M'
	if msg.Action == tea.MouseActionRelease {
		suffix = 'm'
	}
	seq := fmt.Sprintf("\x1b[<%d;%d;%d%c", btn, x, y, suffix)
	m.conn.SendInput(sess.Name, []byte(seq))
	m.ackAgentDoneSession(sess.Name)
}

// forwardClickToPTY sends a click (press+release) to PTY at the given local coords.
func (m *tuiModel) forwardClickToPTY(localX, localY int) {
	sess := m.selectedSession()
	if sess == nil || m.conn == nil {
		return
	}
	x, y := localX+1, localY+1 // SGR 1-based
	press := fmt.Sprintf("\x1b[<0;%d;%dM", x, y)
	release := fmt.Sprintf("\x1b[<0;%d;%dm", x, y)
	m.conn.SendInput(sess.Name, []byte(press+release))
	m.ackAgentDoneSession(sess.Name)
}

// floatingContentRect returns the content area (x, y, w, h) of the floating window.
func (m *tuiModel) floatingContentRect() (int, int, int, int) {
	fw := m.width * 8 / 10
	fh := m.height * 8 / 10
	if fw < 40 {
		fw = min(40, m.width)
	}
	if fh < 10 {
		fh = min(10, m.height)
	}
	// Panel has 1-cell border on each side, 1 title line at top
	contentW := fw - 2
	contentH := fh - 3
	// Centered: offset = (total - panel) / 2, then +1 for border
	x := (m.width-fw)/2 + 1
	y := (m.height-fh)/2 + 2 // +1 border + 1 title
	return x, y, contentW, contentH
}

// handleFloatingMouse handles mouse events when a floating window is open.
// Supports text selection (drag to select, release to copy) when mouse reporting
// is off. When mouse reporting is on, forwards events to the session PTY.
// Click-outside always closes the floating window.
func (m *tuiModel) handleFloatingMouse(msg tea.MouseMsg) tea.Cmd {
	sessions := m.orderedSessions()
	if m.floatingIdx < 0 || m.floatingIdx >= len(sessions) {
		return nil
	}
	sess := sessions[m.floatingIdx]
	mouseOn := sess.MouseEnabled()

	cx, cy, cw, ch := m.floatingContentRect()
	localX := msg.X - cx
	localY := msg.Y - cy
	inside := localX >= 0 && localX < cw && localY >= 0 && localY < ch

	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if inside && mouseOn {
				m.forwardMouseToSession(sess.Name, msg, localX, localY)
			}
			return nil
		}
		if msg.Button == tea.MouseButtonLeft {
			if inside {
				// Check for tab bar click in floating window (last line of content)
				if len(sess.Tabs) > 1 && localY == ch-1 {
					if tabIdx := hitTestTabBar(sess.Tabs, sess.ActiveTab, cw, localX); tabIdx >= 0 {
						m.requestTabSwitch(sess.Name, tabIdx)
						m.sel = textSelection{}
						return nil
					}
				}
				// Always start pending selection; on release we decide
				// whether to copy text or forward click to PTY.
				m.sel = textSelection{
					pending: true,
					session: sess.Name,
					startX:  localX,
					startY:  localY,
					endX:    localX,
					endY:    localY,
					screenX: msg.X,
					screenY: msg.Y,
				}
			} else {
				m.closeFloating()
			}
		}
	case tea.MouseActionMotion:
		if m.sel.pending && m.sel.session == sess.Name {
			lx := clamp(localX, 0, cw-1)
			ly := clamp(localY, 0, ch-1)
			if lx != m.sel.startX || ly != m.sel.startY {
				m.sel.pending = false
				m.sel.active = true
				m.sel.endX = lx
				m.sel.endY = ly
			}
		} else if m.sel.active && m.sel.session == sess.Name {
			m.sel.endX = clamp(localX, 0, cw-1)
			m.sel.endY = clamp(localY, 0, ch-1)
			return m.selMotionCmd()
		} else if inside && mouseOn {
			m.forwardMouseToSession(sess.Name, msg, localX, localY)
		}
	case tea.MouseActionRelease:
		if m.sel.active && m.sel.session == sess.Name {
			m.sel.active = false
			if m.sel.startX != m.sel.endX || m.sel.startY != m.sel.endY {
				text := m.extractSelectedText()
				if text != "" {
					m.sel = textSelection{}
					return m.copyToClipboard(text)
				}
			}
			m.sel = textSelection{}
		} else if m.sel.pending && m.sel.session == sess.Name {
			m.sel.pending = false
			if mouseOn {
				// Forward click to the floating session's PTY
				x, y := m.sel.startX+1, m.sel.startY+1
				press := fmt.Sprintf("\x1b[<0;%d;%dM", x, y)
				release := fmt.Sprintf("\x1b[<0;%d;%dm", x, y)
				m.conn.SendInput(sess.Name, []byte(press+release))
				m.ackAgentDoneSession(sess.Name)
			}
			m.sel = textSelection{}
		} else if inside && mouseOn {
			m.forwardMouseToSession(sess.Name, msg, localX, localY)
		}
	}
	return nil
}

// forwardMouseToSession sends a mouse event as SGR sequence to a named session.
func (m *tuiModel) forwardMouseToSession(name string, msg tea.MouseMsg, localX, localY int) {
	if m.conn == nil {
		return
	}
	var btn int
	switch msg.Button {
	case tea.MouseButtonLeft:
		btn = 0
	case tea.MouseButtonMiddle:
		btn = 1
	case tea.MouseButtonRight:
		btn = 2
	case tea.MouseButtonWheelUp:
		btn = 64
	case tea.MouseButtonWheelDown:
		btn = 65
	default:
		btn = 3
	}
	if msg.Shift {
		btn |= 4
	}
	if msg.Alt {
		btn |= 8
	}
	if msg.Ctrl {
		btn |= 16
	}
	if msg.Action == tea.MouseActionMotion {
		btn |= 32
	}
	x, y := localX+1, localY+1
	var suffix byte = 'M'
	if msg.Action == tea.MouseActionRelease {
		suffix = 'm'
	}
	seq := fmt.Sprintf("\x1b[<%d;%d;%d%c", btn, x, y, suffix)
	m.conn.SendInput(name, []byte(seq))
	m.ackAgentDoneSession(name)
}

// titleBarRegion describes a side panel's title bar location.
type titleBarRegion struct {
	sessionIdx int // visible index
	x, y       int // top-left screen coords of title bar
	w          int // width of title bar
}

// sidePanelTitleRegions returns screen regions for all side panel title bars.
func (m *tuiModel) sidePanelTitleRegions() []titleBarRegion {
	regions := m.panelRegions()
	var titles []titleBarRegion
	for _, r := range regions {
		if r.sessionIdx == m.selected && m.mode != modeGrid {
			continue // skip big panel
		}
		// Title bar is one row above content area
		titles = append(titles, titleBarRegion{
			sessionIdx: r.sessionIdx,
			x:          r.contentX,
			y:          r.contentY - 1,
			w:          r.contentW,
		})
	}
	return titles
}

// hitTestTitleButton checks if (x,y) hits a [-] or [x] button on a title bar.
// Returns: sessionIdx, "hide"|"kill"|""
func (m *tuiModel) hitTestTitleButton(x, y int) (int, string) {
	for _, tb := range m.sidePanelTitleRegions() {
		if y != tb.y {
			continue
		}
		localX := x - tb.x
		if localX < 0 || localX >= tb.w {
			continue
		}
		// Buttons are right-aligned: " [-][x]" = 7 chars
		// [x] occupies rightmost positions: w-3, w-2, w-1
		// [-] occupies: w-6, w-5, w-4
		btnOffset := tb.w - 1 - localX // 0 = rightmost char
		if btnOffset >= 0 && btnOffset <= 2 {
			// [x] button region
			return tb.sessionIdx, "kill"
		}
		if btnOffset >= 3 && btnOffset <= 5 {
			// [-] button region
			return tb.sessionIdx, "hide"
		}
		return tb.sessionIdx, ""
	}
	return -1, ""
}

// hitTestTitleBar checks if (x,y) is on any side panel title bar (not on buttons).
func (m *tuiModel) hitTestTitleBar(x, y int) int {
	for _, tb := range m.sidePanelTitleRegions() {
		if y == tb.y && x >= tb.x && x < tb.x+tb.w {
			return tb.sessionIdx
		}
	}
	return -1
}

// panelRegions computes the on-screen content rectangles for all visible sessions.
func (m *tuiModel) panelRegions() []panelRegion {
	sessions := m.visibleSessions()
	n := len(sessions)
	if n == 0 {
		return nil
	}

	statusH := 1
	availH := m.height - statusH

	if m.mode == modeGrid {
		return m.gridPanelRegions(n, availH)
	}
	if m.mode == modeList {
		return m.listPanelRegions(availH)
	}
	return m.focusBigPanelRegions(sessions, n, availH)
}

// listPanelRegions returns the single big-panel region in list mode; the
// sidebar is hit-tested separately and never forwards mouse events to a PTY.
func (m *tuiModel) listPanelRegions(availH int) []panelRegion {
	mainW, sideW := m.listLayoutWidths()
	contentW := mainW - 2
	contentH := availH - 3
	if contentW < 1 {
		contentW = 1
	}
	if contentH < 1 {
		contentH = 1
	}
	return []panelRegion{{
		sessionIdx: m.selected,
		contentX:   sideW + 1, // sidebar on left + main panel left border
		contentY:   2,
		contentW:   contentW,
		contentH:   contentH,
	}}
}

func (m *tuiModel) gridPanelRegions(n, availH int) []panelRegion {
	cols := gridCols(n, m.width)
	rows := (n + cols - 1) / cols
	panelW := m.width / cols
	panelH := availH / rows
	if panelH < tuiMinPanelH {
		panelH = tuiMinPanelH
	}

	// Panel content: border=1 each side, title line=1
	contentW := panelW - 2
	contentH := panelH - 3
	if contentW < 1 {
		contentW = 1
	}
	if contentH < 1 {
		contentH = 1
	}

	regions := make([]panelRegion, 0, n)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx >= n {
				break
			}
			regions = append(regions, panelRegion{
				sessionIdx: idx,
				contentX:   c*panelW + 1,
				contentY:   r*panelH + 2, // border top + title
				contentW:   contentW,
				contentH:   contentH,
			})
		}
	}
	return regions
}

func (m *tuiModel) focusBigPanelRegions(sessions []*tuiSession, n, availH int) []panelRegion {
	if n == 1 {
		contentW := m.width - 2
		contentH := availH - 3
		if contentW < 1 {
			contentW = 1
		}
		if contentH < 1 {
			contentH = 1
		}
		return []panelRegion{{
			sessionIdx: m.selected,
			contentX:   1,
			contentY:   2,
			contentW:   contentW,
			contentH:   contentH,
		}}
	}

	if m.width >= 120 {
		return m.focusBigWidePanelRegions(n, availH)
	}
	return m.focusBigNarrowPanelRegions(n, availH)
}

func (m *tuiModel) focusBigWidePanelRegions(n, availH int) []panelRegion {
	mainW := m.width * 7 / 10
	sideW := m.width - mainW
	if sideW < tuiMinPanelW {
		sideW = tuiMinPanelW
		mainW = m.width - sideW
	}

	mainContentW := mainW - 2
	mainContentH := availH - 3
	if mainContentW < 1 {
		mainContentW = 1
	}
	if mainContentH < 1 {
		mainContentH = 1
	}

	regions := []panelRegion{{
		sessionIdx: m.selected,
		contentX:   1,
		contentY:   2,
		contentW:   mainContentW,
		contentH:   mainContentH,
	}}

	secondaryCount := n - 1
	if secondaryCount == 0 {
		return regions
	}
	slotH := availH / secondaryCount
	if slotH < tuiMinPanelH {
		slotH = tuiMinPanelH
	}
	visible := availH / slotH
	if visible > secondaryCount {
		visible = secondaryCount
	}

	sideContentW := sideW - 2
	if sideContentW < 1 {
		sideContentW = 1
	}

	idx := 0
	for i := 0; i < n; i++ {
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
		contentH := h - 3
		if contentH < 1 {
			contentH = 1
		}
		regions = append(regions, panelRegion{
			sessionIdx: i,
			contentX:   mainW + 1,
			contentY:   idx*slotH + 2,
			contentW:   sideContentW,
			contentH:   contentH,
		})
		idx++
	}
	return regions
}

func (m *tuiModel) focusBigNarrowPanelRegions(n, availH int) []panelRegion {
	mainH := availH * 7 / 10
	sideH := availH - mainH

	mainContentW := m.width - 2
	mainContentH := mainH - 3
	if mainContentW < 1 {
		mainContentW = 1
	}
	if mainContentH < 1 {
		mainContentH = 1
	}

	regions := []panelRegion{{
		sessionIdx: m.selected,
		contentX:   1,
		contentY:   2,
		contentW:   mainContentW,
		contentH:   mainContentH,
	}}

	sideContentW := m.width - 2
	sideContentH := sideH - 3
	if sideContentW < 1 {
		sideContentW = 1
	}
	if sideContentH < 1 {
		sideContentH = 1
	}

	for i := 0; i < n; i++ {
		if i != m.selected {
			regions = append(regions, panelRegion{
				sessionIdx: i,
				contentX:   1,
				contentY:   mainH + 2,
				contentW:   sideContentW,
				contentH:   sideContentH,
			})
			break
		}
	}
	return regions
}

// extractSelectedText reads the selected text from the vt10x terminal buffer.
func (m *tuiModel) extractSelectedText() string {
	sess := m.sessionByName(m.sel.session)
	if sess == nil {
		return ""
	}

	startY, startX := m.sel.startY, m.sel.startX
	endY, endX := m.sel.endY, m.sel.endX

	// Normalize: start should be before end
	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.term.Lock()
	defer sess.term.Unlock()

	cols, rows := sess.term.Size()
	_ = rows

	var lines []string
	for row := startY; row <= endY; row++ {
		if row >= rows {
			break
		}
		sx := 0
		ex := cols - 1
		if row == startY {
			sx = startX
		}
		if row == endY {
			ex = endX
		}
		if sx >= cols {
			sx = cols - 1
		}
		if ex >= cols {
			ex = cols - 1
		}

		var sb strings.Builder
		for x := sx; x <= ex; x++ {
			g := sess.term.Cell(x, row)
			ch := g.Char
			if ch == 0 {
				// Skip NUL continuation cells placed after wide characters
				continue
			}
			sb.WriteRune(ch)
		}
		line := strings.TrimRight(sb.String(), " ")
		lines = append(lines, line)
	}

	// Trim trailing empty lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// copyToClipboard uses OSC 52 to copy text to the system clipboard.
func (m *tuiModel) copyToClipboard(text string) tea.Cmd {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	osc52 := fmt.Sprintf("\x1b]52;c;%s\x1b\\", encoded)
	return func() tea.Msg {
		os.Stdout.Write([]byte(osc52))
		return nil
	}
}

// selectionCovers checks if a cell at (col, row) in session-local coords is selected.
func (m *tuiModel) selectionCovers(sessionName string, col, row int) bool {
	if m.sel.session != sessionName {
		return false
	}
	if !m.sel.active && (m.sel.startX == m.sel.endX && m.sel.startY == m.sel.endY) {
		return false
	}

	startY, startX := m.sel.startY, m.sel.startX
	endY, endX := m.sel.endY, m.sel.endX
	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}

	if row < startY || row > endY {
		return false
	}
	if row == startY && row == endY {
		return col >= startX && col <= endX
	}
	if row == startY {
		return col >= startX
	}
	if row == endY {
		return col <= endX
	}
	return true
}

// RenderANSIWithSelection renders terminal content with selection highlighting.
func (s *tuiSession) RenderANSIWithSelection(w, h int, selFn func(col, row int) bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return renderTerminalANSIWithSelection(s.term, w, h, selFn)
}

func renderTerminalANSIWithSelection(term vt10x.Terminal, w, h int, selFn func(col, row int) bool) []string {
	cols, rows := term.Size()
	cursor := term.Cursor()
	curVisible := term.CursorVisible()

	lastVisibleRow := findLastVisibleRow(term, cols, rows)

	startRow := 0
	contentRows := lastVisibleRow + 1
	if contentRows > h {
		startRow = contentRows - h
	}

	lines := make([]string, h)
	var sb strings.Builder
	sb.Grow(w * 4)

	for displayRow := 0; displayRow < h; displayRow++ {
		srcRow := startRow + displayRow
		if srcRow >= rows {
			lines[displayRow] = strings.Repeat(" ", w)
			continue
		}

		sb.Reset()
		visualCol := 0
		skipNext := false
		for x := 0; x < cols && visualCol < w; x++ {
			if skipNext {
				skipNext = false
				continue
			}
			g := term.Cell(x, srcRow)
			ch := g.Char
			if ch == 0 {
				ch = ' '
			}
			cw := runewidth.RuneWidth(ch)
			if cw == 0 {
				ch = ' '
				cw = 1
			}
			if visualCol+cw > w {
				break
			}

			// A wide glyph owns the following NUL continuation cell. Detect that
			// up front so we can both skip the continuation cell and treat the
			// cursor as resting on the wide glyph when it sits on that cell.
			nextIsCont := cw > 1 && x+1 < cols && term.Cell(x+1, srcRow).Char == 0

			isCursor := curVisible && srcRow == cursor.Y &&
				(x == cursor.X || (nextIsCont && cursor.X == x+1))
			isSelected := selFn != nil && selFn(visualCol, displayRow)

			if isSelected {
				sb.WriteString("\x1b[7m")
				sb.WriteRune(ch)
				sb.WriteString("\x1b[0m")
			} else {
				ansiStart, ansiEnd := glyphANSI(g, isCursor)
				sb.WriteString(ansiStart)
				sb.WriteRune(ch)
				sb.WriteString(ansiEnd)
			}
			visualCol += cw
			if nextIsCont {
				skipNext = true
			}
		}
		for visualCol < w {
			if selFn != nil && selFn(visualCol, displayRow) {
				sb.WriteString("\x1b[7m \x1b[0m")
			} else {
				sb.WriteByte(' ')
			}
			visualCol++
		}
		lines[displayRow] = sb.String()
	}
	return lines
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
