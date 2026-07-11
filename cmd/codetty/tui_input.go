package main

import (
	"codetty/daemon"
	"codetty/terminal"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// handleConfirmKillKey processes input during kill confirmation.
func (m *tuiModel) handleConfirmKillKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEscape:
		m.confirmKill = false
		m.confirmKillName = ""
		return nil
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'y', 'Y':
				if m.conn != nil && m.confirmKillName != "" {
					m.conn.KillSession(m.confirmKillName, true)
				}
				m.confirmKill = false
				m.confirmKillName = ""
				return nil
			case 'n', 'N':
				m.confirmKill = false
				m.confirmKillName = ""
				return nil
			}
		}
	case tea.KeyEnter:
		if m.conn != nil && m.confirmKillName != "" {
			m.conn.KillSession(m.confirmKillName, true)
		}
		m.confirmKill = false
		m.confirmKillName = ""
		return nil
	}
	return nil
}

func (m *tuiModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Tmux picker overlay: capture all input
	if m.tmuxPicker.active {
		return m.handleTmuxPickerKey(msg)
	}
	// Session switcher overlay: capture all input
	if m.switcher.active {
		return m.handleSwitcherKey(msg)
	}
	// Rename mode: capture text input
	if m.renaming {
		return m.handleRenameKey(msg)
	}
	// Create type chooser mode
	if m.createChooser {
		return m.handleCreateChooserKey(msg)
	}
	// Create session mode: capture name input
	if m.creating {
		return m.handleCreateKey(msg)
	}

	// Floating window mode: <prefix> q closes, <prefix> still works, all else forwards
	if m.floatingIdx >= 0 {
		if msg.Type == m.prefixKey {
			if m.prefix {
				m.prefix = false
				m.forwardKeyTo(msg, m.floatingIdx)
				return nil
			}
			m.prefix = true
			return nil
		}
		if m.prefix {
			m.prefix = false
			if msg.Type == tea.KeyRunes && string(msg.Runes) == "q" {
				m.closeFloating()
				return nil
			}
			return m.handlePrefixKey(msg)
		}
		if msg.Type == tea.KeyShiftTab {
			m.closeFloatingFocusNext()
			return nil
		}
		// Tab switching in floating mode
		if msg.Type == tea.KeyShiftLeft || msg.Type == tea.KeyShiftRight {
			ordered := m.orderedSessions()
			if m.floatingIdx < len(ordered) {
				sess := ordered[m.floatingIdx]
				if len(sess.Tabs) > 1 {
					delta := 1
					if msg.Type == tea.KeyShiftLeft {
						delta = -1
					}
					m.switchTab(sess, delta)
					return nil
				}
			}
		}
		m.forwardKeyTo(msg, m.floatingIdx)
		return nil
	}

	if msg.Type == tea.KeyShiftTab {
		switch m.mode {
		case modeFocusBig:
			m.cycleSideHighlight()
		case modeList:
			// Cycle the sidebar: agents needing attention first (blocked,
			// then done), then the session entries; after the last one,
			// focus returns to the big panel.
			if positions := m.listShiftTabPositions(); len(positions) > 0 {
				if !m.listFocus {
					m.listFocus = true
					m.listCursor = positions[0].sessIdx
					m.listCursorTab = positions[0].tabIdx
				} else {
					pi := -1
					for idx, p := range positions {
						if p.sessIdx == m.listCursor && p.tabIdx == m.listCursorTab {
							pi = idx
							break
						}
					}
					if pi+1 < len(positions) {
						m.listCursor = positions[pi+1].sessIdx
						m.listCursorTab = positions[pi+1].tabIdx
					} else {
						m.listFocus = false
					}
				}
			}
		case modeFullscreen:
			// Open the session switcher with live previews instead of
			// blindly cycling — fullscreen hides all other sessions.
			m.openSwitcher()
		default:
			m.selectDelta(1)
		}
		return nil
	}

	// Enter on a highlighted panel opens floating window
	if msg.Type == tea.KeyEnter && m.floatingIdx < 0 {
		if m.mode == modeGrid {
			m.openFloating(m.selected)
			return nil
		}
		if m.mode == modeFocusBig && m.inputFocus >= 0 && m.inputFocus != m.selected {
			m.openFloating(m.inputFocus)
			return nil
		}
	}

	// Tab switching: Shift+Left/Right when focused session has multiple tabs
	if msg.Type == tea.KeyShiftLeft || msg.Type == tea.KeyShiftRight {
		sess := m.focusedSession()
		if sess != nil && len(sess.Tabs) > 1 {
			delta := 1
			if msg.Type == tea.KeyShiftLeft {
				delta = -1
			}
			m.switchTab(sess, delta)
			return nil
		}
	}

	if msg.Type == m.prefixKey {
		if m.prefix {
			m.prefix = false
			m.forwardKey(msg)
			return nil
		}
		m.prefix = true
		return nil
	}

	if m.prefix {
		m.prefix = false
		return m.handlePrefixKey(msg)
	}

	if m.mode == modeGrid {
		return m.handleGridKey(msg)
	}

	// List mode with sidebar focus: navigate the session list.
	if m.mode == modeList && m.listFocus {
		return m.handleListKey(msg)
	}

	// Focus-big/list mode: always forward to big panel
	m.forwardKey(msg)
	return nil
}

func (m *tuiModel) handleRenameKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		newName := m.renameText
		m.renaming = false
		m.renameText = ""
		if newName != "" {
			if sess := m.selectedSession(); sess != nil && m.conn != nil {
				m.conn.RenameSession(sess.Name, newName)
			}
		}
	case tea.KeyEscape:
		m.renaming = false
		m.renameText = ""
	case tea.KeyBackspace:
		if len(m.renameText) > 0 {
			m.renameText = m.renameText[:len(m.renameText)-1]
		}
	case tea.KeyRunes:
		m.renameText += string(msg.Runes)
	case tea.KeySpace:
		m.renameText += " "
	}
	return nil
}

func (m *tuiModel) handleCreateChooserKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type == tea.KeyEscape {
		m.createChooser = false
		return nil
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case 'c':
			m.createChooser = false
			m.creating = true
			m.createText = ""
			m.createTmux = false
		case 't':
			m.createChooser = false
			m.creating = true
			m.createText = ""
			m.createTmux = true
		}
	}
	return nil
}

func (m *tuiModel) handleCreateKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		name := m.createText
		m.creating = false
		m.createText = ""
		if m.conn != nil {
			cols, rows := m.focusedContentSize()
			if m.createTmux {
				m.conn.CreateTmuxSession(name, uint16(cols), uint16(rows))
			} else {
				m.conn.CreateSession(name, []string{terminal.DefaultShell()}, uint16(cols), uint16(rows))
			}
		}
		m.createTmux = false
	case tea.KeyEscape:
		m.creating = false
		m.createText = ""
		m.createTmux = false
	case tea.KeyBackspace:
		if len(m.createText) > 0 {
			m.createText = m.createText[:len(m.createText)-1]
		}
	case tea.KeyRunes:
		m.createText += string(msg.Runes)
	case tea.KeySpace:
		m.createText += " "
	}
	return nil
}

func (m *tuiModel) handlePrefixKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		switch msg.Runes[0] {
		case 'd':
			if m.conn != nil {
				m.conn.Detach()
			}
			return tea.Quit
		case 'x':
			if sess := m.selectedSession(); sess != nil && m.conn != nil {
				m.conn.KillSession(sess.Name, true)
			}
			return nil
		case 'X':
			// Close the active tab (if multiple tabs exist)
			if sess := m.focusedSession(); sess != nil && len(sess.Tabs) > 1 && m.conn != nil {
				m.conn.CloseTab(sess.Name, sess.ActiveTab)
			}
			return nil
		case 'c':
			if daemon.IsTmuxAvailable() {
				m.createChooser = true
			} else {
				m.creating = true
				m.createText = ""
				m.createTmux = false
			}
			return nil
		case 'n':
			m.selectDelta(1)
			return nil
		case 'p':
			m.selectDelta(-1)
			return nil
		case 'f':
			if m.floatingIdx >= 0 {
				// Floating window → fullscreen that session
				// Convert ordered index to visible index for m.selected
				ordered := m.orderedSessions()
				var sessName string
				if m.floatingIdx < len(ordered) {
					sessName = ordered[m.floatingIdx].Name
				}
				m.pushMode()
				m.floatingIdx = -1
				m.mode = modeFullscreen
				if sessName != "" {
					m.selected = m.visibleIndexByName(sessName)
				}
				m.resizeSessionPTYs()
			} else if m.mode == modeFullscreen {
				// Fullscreen → pop back
				sess := m.selectedSession()
				if sess != nil && sess.Temporary {
					// Temp session: return to floating (single pop)
					m.popMode()
				} else {
					// Normal session: pop back skipping floating states
					popped := false
					for m.popMode() {
						popped = true
						if m.floatingIdx < 0 {
							break
						}
					}
					if !popped || m.floatingIdx >= 0 {
						m.mode = modeFocusBig
						m.floatingIdx = -1
					}
				}
				m.resizeSessionPTYs()
			} else if m.mode == modeFocusBig || m.mode == modeList {
				// Big panel → fullscreen
				m.pushMode()
				m.mode = modeFullscreen
				m.listFocus = false
				m.resizeSessionPTYs()
			} else if m.mode == modeGrid {
				// Grid mode → open floating for selected (openFloating pushes)
				m.openFloating(m.selected)
			}
			return nil
		case 'g':
			if m.mode == modeGrid {
				m.mode = modeFocusBig
			} else {
				m.mode = modeGrid
			}
			m.listFocus = false
			m.resizeSessionPTYs()
			return nil
		case 'b':
			m.mode = modeFocusBig
			m.listFocus = false
			m.resizeSessionPTYs()
			return nil
		case 'a':
			m.mode = modeGrid
			m.listFocus = false
			m.resizeSessionPTYs()
			return nil
		case 's':
			m.mode = modeList
			m.listFocus = false
			m.resizeSessionPTYs()
			return nil
		case 'h':
			m.moveOrder(-1)
			return nil
		case 'l':
			m.moveOrder(1)
			return nil
		case 'r':
			if sess := m.selectedSession(); sess != nil {
				m.renaming = true
				m.renameText = sess.Name
			}
			return nil
		case 'R':
			m.reloadConfig()
			return nil
		case 't':
			m.openTmuxPicker()
			return nil
		case 'T':
			// Create a new tab in the focused session
			if sess := m.focusedSession(); sess != nil && sess.Type == "native" && m.conn != nil {
				m.conn.CreateTab(sess.Name, "", nil, "", nil)
			}
			return nil
		case 'w':
			m.openSessionManager()
			return nil
		case 'm':
			// Create a temporary floating shell (only in grid/focus-big modes)
			if m.floatingIdx < 0 && m.mode != modeFullscreen && m.conn != nil {
				_, _, cw, ch := m.floatingContentRect()
				name := fmt.Sprintf("tmp-%d", len(m.sessions)+1)
				m.floatingTarget = name
				m.conn.CreateSession(name, []string{terminal.DefaultShell()}, uint16(cw), uint16(ch))
			}
			return nil
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			idx := int(msg.Runes[0]-'0') - 1
			if idx < len(m.visibleSessions()) {
				m.selected = idx
				m.inputFocus = -1
				m.listCursor = idx
				m.listCursorTab = -1
				if m.mode != modeList {
					m.mode = modeFocusBig
				}
			}
			return nil
		}
	}
	return nil
}

func (m *tuiModel) handleGridKey(msg tea.KeyMsg) tea.Cmd {
	n := len(m.visibleSessions())
	cols := gridCols(n, m.width)
	switch msg.Type {
	case tea.KeyLeft:
		if m.selected%cols > 0 {
			m.selected--
		}
	case tea.KeyRight:
		if m.selected%cols < cols-1 && m.selected+1 < n {
			m.selected++
		}
	case tea.KeyUp:
		if m.selected-cols >= 0 {
			m.selected -= cols
		}
	case tea.KeyDown:
		if m.selected+cols < n {
			m.selected += cols
		}
	}
	return nil
}

func (m *tuiModel) forwardKey(msg tea.KeyMsg) {
	sess := m.focusedSession()
	if sess == nil || m.conn == nil {
		return
	}
	// Clear any active selection when typing
	m.sel = textSelection{}
	raw := tuiKeyToBytes(msg)
	if len(raw) > 0 {
		m.conn.SendInput(sess.Name, raw)
		m.ackAgentDoneSession(sess.Name)
	}
}

// forwardKeyTo sends a key to the session at the given index.
func (m *tuiModel) forwardKeyTo(msg tea.KeyMsg, idx int) {
	sessions := m.orderedSessions()
	if idx < 0 || idx >= len(sessions) || m.conn == nil {
		return
	}
	m.sel = textSelection{}
	raw := tuiKeyToBytes(msg)
	if len(raw) > 0 {
		m.conn.SendInput(sessions[idx].Name, raw)
		m.ackAgentDoneSession(sessions[idx].Name)
	}
}

func (m *tuiModel) selectDelta(delta int) {
	visible := m.visibleSessions()
	if len(visible) == 0 {
		return
	}
	m.selected = ((m.selected+delta)%len(visible) + len(visible)) % len(visible)
	m.inputFocus = -1
}

// cycleSideHighlight cycles inputFocus among all panels in focus-big mode.
// When inputFocus lands on m.selected, it means the big panel is highlighted.
func (m *tuiModel) cycleSideHighlight() {
	n := len(m.visibleSessions())
	if n <= 1 {
		return
	}
	cur := m.inputFocus
	if cur < 0 {
		cur = m.selected
	}
	next := (cur + 1) % n
	if next == m.selected {
		m.inputFocus = -1 // big panel highlighted
	} else {
		m.inputFocus = next
	}
}

// moveOrder swaps the selected session's position in the display order.
func (m *tuiModel) moveOrder(delta int) {
	visible := m.visibleSessions()
	n := len(visible)
	if n < 2 || m.selected < 0 || m.selected >= n {
		return
	}
	target := ((m.selected + delta) % n + n) % n
	// Use reorderByVisibleIdx to swap positions in m.order
	m.reorderByVisibleIdx(m.selected, target)
}

// reorderByVisibleIdx moves a session from one visible position to another in m.order.
func (m *tuiModel) reorderByVisibleIdx(fromVisible, toVisible int) {
	visible := m.visibleSessions()
	if fromVisible < 0 || fromVisible >= len(visible) || toVisible < 0 || toVisible >= len(visible) {
		return
	}
	fromName := visible[fromVisible].Name
	toName := visible[toVisible].Name

	// Find indices in m.order
	fromOrder := -1
	toOrder := -1
	for i, n := range m.order {
		if n == fromName {
			fromOrder = i
		}
		if n == toName {
			toOrder = i
		}
	}
	if fromOrder < 0 || toOrder < 0 {
		return
	}

	// Remove from original position and insert at target
	m.order = append(m.order[:fromOrder], m.order[fromOrder+1:]...)
	// Adjust toOrder if it was after fromOrder
	if toOrder > fromOrder {
		toOrder--
	}
	// Insert at toOrder position
	m.order = append(m.order[:toOrder+1], m.order[toOrder:]...)
	m.order[toOrder] = fromName

	// Adjust m.selected to follow the moved panel if needed
	if m.selected == fromVisible {
		m.selected = toVisible
	} else if fromVisible < m.selected && toVisible >= m.selected {
		m.selected--
	} else if fromVisible > m.selected && toVisible <= m.selected {
		m.selected++
	}
	m.inputFocus = -1
}

// tuiKeyToBytes converts a tea.KeyMsg to raw terminal bytes.
func tuiKeyToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeyCtrlC:
		return []byte{0x03}
	case tea.KeyCtrlD:
		return []byte{0x04}
	case tea.KeyCtrlZ:
		return []byte{0x1a}
	case tea.KeyCtrlL:
		return []byte{0x0c}
	case tea.KeyCtrlA:
		return []byte{0x01}
	case tea.KeyUp:
		if msg.Alt {
			return []byte{0x1b, '[', '1', ';', '3', 'A'}
		}
		return []byte{0x1b, '[', 'A'}
	case tea.KeyDown:
		if msg.Alt {
			return []byte{0x1b, '[', '1', ';', '3', 'B'}
		}
		return []byte{0x1b, '[', 'B'}
	case tea.KeyRight:
		if msg.Alt {
			return []byte{0x1b, '[', '1', ';', '3', 'C'}
		}
		return []byte{0x1b, '[', 'C'}
	case tea.KeyLeft:
		if msg.Alt {
			return []byte{0x1b, '[', '1', ';', '3', 'D'}
		}
		return []byte{0x1b, '[', 'D'}
	case tea.KeyShiftUp:
		return []byte{0x1b, '[', '1', ';', '2', 'A'}
	case tea.KeyShiftDown:
		return []byte{0x1b, '[', '1', ';', '2', 'B'}
	case tea.KeyShiftRight:
		return []byte{0x1b, '[', '1', ';', '2', 'C'}
	case tea.KeyShiftLeft:
		return []byte{0x1b, '[', '1', ';', '2', 'D'}
	case tea.KeyCtrlUp:
		return []byte{0x1b, '[', '1', ';', '5', 'A'}
	case tea.KeyCtrlDown:
		return []byte{0x1b, '[', '1', ';', '5', 'B'}
	case tea.KeyCtrlRight:
		return []byte{0x1b, '[', '1', ';', '5', 'C'}
	case tea.KeyCtrlLeft:
		return []byte{0x1b, '[', '1', ';', '5', 'D'}
	case tea.KeyCtrlShiftUp:
		return []byte{0x1b, '[', '1', ';', '6', 'A'}
	case tea.KeyCtrlShiftDown:
		return []byte{0x1b, '[', '1', ';', '6', 'B'}
	case tea.KeyCtrlShiftRight:
		return []byte{0x1b, '[', '1', ';', '6', 'C'}
	case tea.KeyCtrlShiftLeft:
		return []byte{0x1b, '[', '1', ';', '6', 'D'}
	case tea.KeyHome:
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		return []byte{0x1b, '[', 'F'}
	case tea.KeyShiftHome:
		return []byte{0x1b, '[', '1', ';', '2', 'H'}
	case tea.KeyShiftEnd:
		return []byte{0x1b, '[', '1', ';', '2', 'F'}
	case tea.KeyDelete:
		return []byte{0x1b, '[', '3', '~'}
	case tea.KeyPgUp:
		return []byte{0x1b, '[', '5', '~'}
	case tea.KeyPgDown:
		return []byte{0x1b, '[', '6', '~'}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyRunes:
		if msg.Alt && len(msg.Runes) == 1 {
			// Alt+key: ESC prefix + character
			return append([]byte{0x1b}, []byte(string(msg.Runes))...)
		}
		return []byte(string(msg.Runes))
	default:
		if msg.Type >= tea.KeyCtrlA && msg.Type <= tea.KeyCtrlZ {
			return []byte{byte(msg.Type)}
		}
		return nil
	}
}
