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

	if m.mode == modeTabs {
		return m.handleTabsMouse(msg)
	}

	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			// List mode sidebar: clicking an entry switches the big
			// panel and auto-activates the priority tab.
			if m.mode == modeList {
				if _, sideW := m.listLayoutWidths(); msg.X < sideW {
					if idx, _ := m.listHitTest(msg.X, msg.Y); idx >= 0 {
						m.promoteToBig(idx)
						m.listCursor = idx
						m.listCursorTab = -1
					}
					m.sel = textSelection{}
					return nil
				}
			}

			// Focus-big side panels never receive session input: a plain
			// click opens the session in a floating window for quick,
			// temporary work; Ctrl+click promotes it to the big panel.
			if m.mode == modeFocusBig {
				sessions := m.visibleSessions()
				if len(sessions) > 1 {
					if mainW, _ := m.focusBigLayoutWidths(); msg.X >= mainW {
						if idx := m.hitTest(msg.X, msg.Y); idx >= 0 && idx != m.selected {
							m.focusCursor = -1
							if msg.Ctrl {
								m.promoteToBig(idx)
							} else {
								m.openFloating(idx)
							}
						}
						m.sel = textSelection{}
						return nil
					}
				}
				// Click landed on the big panel: focus returns there.
				m.focusCursor = -1
			}

			// Ctrl+Click: promote panel to big (switch m.selected)
			if msg.Ctrl {
				if idx := m.hitTest(msg.X, msg.Y); idx >= 0 {
					m.selected = idx
				}
				m.sel = textSelection{}
				return nil
			}

			// Plain click on panels
			regions := m.panelRegions()
			sessions := m.visibleSessions()

			// Check for tab bar clicks first.
			for _, r := range regions {
				if r.sessionIdx >= len(sessions) {
					continue
				}
				if r.sessionIdx != m.selected {
					continue
				}
				sess := sessions[r.sessionIdx]
				if len(sess.Tabs) <= 1 {
					continue
				}
				// Tab bar is on the info line below the content area
				tabBarY := r.contentY + r.contentH
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
					// Always start a pending local selection (same as the
					// floating window): a drag selects+copies locally, a
					// plain click is forwarded to the app on release.
					sess := sessions[r.sessionIdx]
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

			// Click on border/title area (fallback: open floating)
			m.sel = textSelection{}
		}
		// Right click: paste the last copied selection into the big
		// panel. Middle click: forward to mouse-enabled apps.
		if msg.Button == tea.MouseButtonRight || msg.Button == tea.MouseButtonMiddle {
			if sess := m.selectedSession(); sess != nil {
				for _, r := range m.panelRegions() {
					if r.sessionIdx != m.selected {
						continue
					}
					localX := msg.X - r.contentX
					localY := msg.Y - r.contentY
					if localX >= 0 && localX < r.contentW && localY >= 0 && localY < r.contentH {
						if msg.Button == tea.MouseButtonRight {
							m.pasteToSession(sess)
						} else if sess.MouseEnabled() {
							m.forwardMouseToSession(sess.Name, msg, localX, localY)
							m.mouseFwdDown = true
						}
					}
					break
				}
			}
			return nil
		}
		// Forward scroll wheel to PTY (with local coordinate translation).
		// Only the selected panel receives wheel input.
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
					if r.sessionIdx == m.selected {
						sess := sessions[r.sessionIdx]
						if sess.MouseEnabled() {
							m.forwardMouseToSession(sess.Name, msg, localX, localY)
						} else {
							m.handleSessionScroll(sess, msg.Button == tea.MouseButtonWheelUp)
						}
					}
					break
				}
			}
		}

	case tea.MouseActionMotion:
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
		} else if m.mode == modeList || m.mode == modeFocusBig {
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
			// session has mouse reporting enabled (vim, etc.).
			m.sel.pending = false
			if m.mode == modeList || m.mode == modeFocusBig {
				if sess := m.selectedSession(); sess != nil && sess.MouseEnabled() {
					m.forwardClickToPTY(m.sel.startX, m.sel.startY)
				}
			}
			m.sel = textSelection{}
		} else if m.mouseFwdDown && (m.mode == modeList || m.mode == modeFocusBig) {
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
			// Bottom info line (tab bar + session info) — not forwarded to PTY.
			if msg.Y == m.height-1 {
				if len(sess.Tabs) > 1 {
					if tabIdx := hitTestTabBar(sess.Tabs, sess.ActiveTab, m.width, msg.X); tabIdx >= 0 {
						m.requestTabSwitch(sess.Name, tabIdx)
					}
				}
				m.sel = textSelection{}
				return nil
			}

			// Always start a pending local selection (same as the
			// floating window): a drag selects+copies locally, a
			// plain click is forwarded to the app on release.
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
		// Right click: paste the last copied selection (not on the info line).
		if msg.Button == tea.MouseButtonRight {
			if msg.Y != m.height-1 {
				m.pasteToSession(sess)
			}
			return nil
		}
		// Forward scroll and other buttons (middle) directly
		if mouseOn {
			m.forwardMouseToPTY(msg)
			if msg.Button != tea.MouseButtonWheelUp && msg.Button != tea.MouseButtonWheelDown {
				m.mouseFwdDown = true
			}
		} else if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			m.handleSessionScroll(sess, msg.Button == tea.MouseButtonWheelUp)
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
			// Don't forward clicks on the bottom info line to PTY
			if mouseOn && m.sel.startY != m.height-1 {
				m.forwardClickToPTY(m.sel.startX, m.sel.startY)
			}
			m.sel = textSelection{}
		} else if m.mouseFwdDown {
			m.mouseFwdDown = false
			// Only forward the release if the current tab still has mouse
			// reporting enabled; after a tab switch the new session may be
			// a plain shell that would echo the SGR sequence as garbage.
			if mouseOn && msg.Y != m.height-1 {
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
	// Panel has a 1-cell border on each side; the bottom info line (tab
	// bar + session info) sits between the content and the bottom border.
	contentW := fw - 2
	contentH := fh - 3
	// Centered: offset = (total - panel) / 2, then +1 for border
	x := (m.width-fw)/2 + 1
	y := (m.height-fh)/2 + 1
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
			if inside {
				if mouseOn {
					m.forwardMouseToSession(sess.Name, msg, localX, localY)
				} else {
					m.handleSessionScroll(sess, msg.Button == tea.MouseButtonWheelUp)
				}
			}
			return nil
		}
		if msg.Button == tea.MouseButtonLeft {
			// Tab bar lives on the info line below the content area
			// (same as the big panel); consume clicks on that row.
			if len(sess.Tabs) > 1 && localY == ch && localX >= 0 && localX < cw {
				if tabIdx := hitTestTabBar(sess.Tabs, sess.ActiveTab, cw, localX); tabIdx >= 0 {
					m.requestTabSwitch(sess.Name, tabIdx)
				}
				m.sel = textSelection{}
				return nil
			}
			if inside {
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
		} else if inside && msg.Button == tea.MouseButtonRight {
			// Right click: paste the last copied selection.
			m.pasteToSession(sess)
		} else if inside && mouseOn {
			// Middle click: forward to the app.
			m.forwardMouseToSession(sess.Name, msg, localX, localY)
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
		} else if inside && mouseOn && msg.Button != tea.MouseButtonRight {
			// Right presses are consumed as paste; don't leak the release.
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

// scrollLinesPerTick is the number of lines to scroll per wheel tick.
const scrollLinesPerTick = 3

// sendScrollAsArrows converts a scroll wheel event into repeated up/down arrow
// key sequences for the given session. Only called when the session is in the
// alternate screen buffer without mouse reporting (xterm alternateScroll behavior).
func (m *tuiModel) sendScrollAsArrows(sessionName string, up bool) {
	if m.conn == nil {
		return
	}
	var seq string
	if up {
		seq = "\x1b[A"
	} else {
		seq = "\x1b[B"
	}
	m.conn.SendInput(sessionName, []byte(strings.Repeat(seq, scrollLinesPerTick)))
}

// handleSessionScroll handles a scroll wheel event for a session that does NOT
// have mouse reporting enabled. Alt-screen apps (less/man/vim without mouse)
// get arrow keys (xterm alternateScroll behavior); the primary screen scrolls
// through the local scrollback buffer.
func (m *tuiModel) handleSessionScroll(sess *tuiSession, up bool) {
	if sess.AltScreenActive() {
		m.sendScrollAsArrows(sess.Name, up)
		return
	}
	if up {
		sess.ScrollUp(scrollLinesPerTick)
	} else {
		sess.ScrollDown(scrollLinesPerTick)
	}
}

// panelRegions computes the on-screen content rectangles for all visible sessions.
func (m *tuiModel) panelRegions() []panelRegion {
	if len(m.visibleSessions()) == 0 {
		return nil
	}

	statusH := 1
	availH := m.height - statusH
	if m.mode == modeFocusBig {
		return m.focusBigPanelRegions(availH)
	}
	return m.listPanelRegions(availH)
}

// focusBigPanelRegions returns the single big-panel region in focus-big
// mode; side panels are switch-only and never forward mouse events.
func (m *tuiModel) focusBigPanelRegions(availH int) []panelRegion {
	mainW := m.width
	if len(m.visibleSessions()) > 1 {
		mainW, _ = m.focusBigLayoutWidths()
	}
	contentW := mainW - 2
	contentH := availH - 3 // borders + bottom info line
	if contentW < 1 {
		contentW = 1
	}
	if contentH < 1 {
		contentH = 1
	}
	return []panelRegion{{
		sessionIdx: m.selected,
		contentX:   1, // big panel on the left; skip left border
		contentY:   1, // top border only (no title row)
		contentW:   contentW,
		contentH:   contentH,
	}}
}

// listPanelRegions returns the single big-panel region in list mode; the
// sidebar is hit-tested separately and never forwards mouse events to a PTY.
func (m *tuiModel) listPanelRegions(availH int) []panelRegion {
	mainW, sideW := m.listLayoutWidths()
	contentW := mainW - 2
	contentH := availH - 3 // borders + bottom info line
	if contentW < 1 {
		contentW = 1
	}
	if contentH < 1 {
		contentH = 1
	}
	return []panelRegion{{
		sessionIdx: m.selected,
		contentX:   sideW + 1, // sidebar on left + main panel left border
		contentY:   1,         // top border only (no title row)
		contentW:   contentW,
		contentH:   contentH,
	}}
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

// copyToClipboard uses OSC 52 to copy text to the system clipboard, and
// remembers the text locally so right-click can paste it back.
func (m *tuiModel) copyToClipboard(text string) tea.Cmd {
	m.clipText = text
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	osc52 := fmt.Sprintf("\x1b]52;c;%s\x1b\\", encoded)
	return func() tea.Msg {
		os.Stdout.Write([]byte(osc52))
		return nil
	}
}

// pasteToSession sends the last locally copied text to a session's PTY,
// wrapped in bracketed-paste markers when the app has DEC 2004 enabled.
func (m *tuiModel) pasteToSession(sess *tuiSession) {
	if m.clipText == "" || m.conn == nil || sess == nil {
		return
	}
	text := m.clipText
	if sess.BracketedPasteEnabled() {
		text = "\x1b[200~" + text + "\x1b[201~"
	}
	m.conn.SendInput(sess.Name, []byte(text))
	m.ackAgentDoneSession(sess.Name)
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
