package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// sessionTabItems converts visible sessions into synthetic tuiTab values so
// the top session tab bar reuses renderTabBar/hitTestTabBar. Each session
// tab carries the session's aggregate agent state, giving it the exact same
// active-highlight and agent-state colors as the tab bar in focus-big mode.
func sessionTabItems(sessions []*tuiSession) []tuiTab {
	items := make([]tuiTab, len(sessions))
	for i, sess := range sessions {
		name := sess.Name
		if sess.Exited {
			name += " (exited)"
		}
		items[i] = tuiTab{Name: name, AgentState: sessionAgentState(sess)}
	}
	return items
}

// tabsView renders tab mode: the session tab bar on the top row, the
// selected session's content below it (full width, no borders), and the
// fullscreen-style bottom info line (per-session tab bar + session info).
func (m *tuiModel) tabsView() string {
	sessions := m.visibleSessions()
	if len(sessions) == 0 {
		m.mode = modeList
		return m.listView()
	}
	if m.selected >= len(sessions) {
		m.selected = len(sessions) - 1
	}
	sess := sessions[m.selected]

	topBar := renderTabBar(sessionTabItems(sessions), m.selected, m.width, false)

	contentH := m.height - 2 // top session tab bar + bottom info line
	if contentH < 1 {
		contentH = 1
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

	return topBar + "\n" + content + "\n" + m.bottomInfoLine(sess, m.width)
}

// handleTabsMouse handles mouse in tab mode: the top row is the session
// tab bar, the bottom row the info line (per-session tab bar), and
// everything between is the selected session's content, offset one row
// down from the screen.
func (m *tuiModel) handleTabsMouse(msg tea.MouseMsg) tea.Cmd {
	sessions := m.visibleSessions()
	sess := m.selectedSession()
	if sess == nil {
		return nil
	}
	mouseOn := sess.MouseEnabled()
	contentH := m.height - 2
	if contentH < 1 {
		contentH = 1
	}
	localY := msg.Y - 1
	inContent := msg.Y > 0 && msg.Y < m.height-1

	switch msg.Action {
	case tea.MouseActionPress:
		if msg.Button == tea.MouseButtonLeft {
			// Top session tab bar: click switches the displayed session
			// (activating its priority tab, like the list-mode sidebar).
			if msg.Y == 0 {
				if idx := hitTestTabBar(sessionTabItems(sessions), m.selected, m.width, msg.X); idx >= 0 && idx != m.selected {
					m.promoteToBig(idx)
				}
				m.sel = textSelection{}
				return nil
			}
			// Bottom info line: per-session tab bar clicks, never the PTY.
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
				startY:  localY,
				endX:    msg.X,
				endY:    localY,
				screenX: msg.X,
				screenY: msg.Y,
			}
			return nil
		}
		// Forward scroll and other buttons directly (content area only —
		// the top tab bar and bottom info line are TUI chrome).
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if !inContent {
				return nil
			}
			if mouseOn {
				m.forwardMouseToSession(sess.Name, msg, msg.X, clamp(localY, 0, contentH-1))
			} else {
				m.handleSessionScroll(sess, msg.Button == tea.MouseButtonWheelUp)
			}
			return nil
		}
		// Right click: paste the last copied selection into the content
		// area. Middle click: forward to mouse-enabled apps.
		if msg.Button == tea.MouseButtonRight {
			if inContent {
				m.pasteToSession(sess)
			}
			return nil
		}
		if mouseOn && inContent {
			m.forwardMouseToSession(sess.Name, msg, msg.X, localY)
			m.mouseFwdDown = true
		}

	case tea.MouseActionMotion:
		if m.sel.pending {
			cx := clamp(msg.X, 0, m.width-1)
			cy := clamp(localY, 0, contentH-1)
			if cx != m.sel.startX || cy != m.sel.startY {
				m.sel.pending = false
				m.sel.active = true
				m.sel.endX = cx
				m.sel.endY = cy
			}
		} else if m.sel.active {
			m.sel.endX = clamp(msg.X, 0, m.width-1)
			m.sel.endY = clamp(localY, 0, contentH-1)
			return m.selMotionCmd()
		} else if mouseOn && inContent {
			m.forwardMouseToSession(sess.Name, msg, msg.X, localY)
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
			// Click without drag: forward to the PTY only when the app
			// has mouse reporting enabled (coords are already local).
			m.sel.pending = false
			if mouseOn {
				m.forwardClickToPTY(m.sel.startX, m.sel.startY)
			}
			m.sel = textSelection{}
		} else if m.mouseFwdDown {
			m.mouseFwdDown = false
			if mouseOn && inContent {
				m.forwardMouseToSession(sess.Name, msg, msg.X, clamp(localY, 0, contentH-1))
			}
		}
	}
	return nil
}
