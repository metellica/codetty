package main

import (
	"codetty/terminal"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *tuiModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Rename mode: capture text input
	if m.renaming {
		return m.handleRenameKey(msg)
	}
	// Create session mode: capture name input
	if m.creating {
		return m.handleCreateKey(msg)
	}

	// Floating window mode: Esc closes, Ctrl+A prefix still works, all else forwards
	if m.floatingIdx >= 0 {
		if msg.Type == tea.KeyEscape {
			m.closeFloating()
			return nil
		}
		if msg.Type == tea.KeyCtrlA {
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
			return m.handlePrefixKey(msg)
		}
		m.forwardKeyTo(msg, m.floatingIdx)
		return nil
	}

	if msg.Type == tea.KeyShiftTab {
		m.selectDelta(1)
		return nil
	}

	if msg.Type == tea.KeyCtrlA {
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

func (m *tuiModel) handleCreateKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		name := m.createText
		m.creating = false
		m.createText = ""
		if m.conn != nil {
			cols, rows := m.focusedContentSize()
			m.conn.CreateSession(name, []string{terminal.DefaultShell()}, uint16(cols), uint16(rows))
		}
	case tea.KeyEscape:
		m.creating = false
		m.createText = ""
	case tea.KeyBackspace:
		if len(m.createText) > 0 {
			m.createText = m.createText[:len(m.createText)-1]
		}
	case tea.KeyRunes:
		m.createText += string(msg.Runes)
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
				m.conn.KillSession(sess.Name)
			}
			return nil
		case 'c':
			m.creating = true
			m.createText = ""
			return nil
		case 'n':
			m.selectDelta(1)
			return nil
		case 'p':
			m.selectDelta(-1)
			return nil
		case 'f':
			if m.mode == modeFullscreen {
				m.mode = modeFocusBig
			} else {
				m.mode = modeFullscreen
			}
			m.resizeSessionPTYs()
			return nil
		case 'g':
			if m.mode == modeGrid {
				m.mode = modeFocusBig
			} else {
				m.mode = modeGrid
			}
			m.resizeSessionPTYs()
			return nil
		case 'b':
			m.mode = modeFocusBig
			m.resizeSessionPTYs()
			return nil
		case 'a':
			m.mode = modeGrid
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
		case 't':
			if m.conn != nil {
				m.conn.RefreshTmux()
			}
			return nil
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			idx := int(msg.Runes[0]-'0') - 1
			if idx < len(m.orderedSessions()) {
				m.selected = idx
				m.inputFocus = -1
				m.mode = modeFocusBig
			}
			return nil
		}
	}
	return nil
}

func (m *tuiModel) handleGridKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyLeft:
		m.selectDelta(-1)
	case tea.KeyRight:
		m.selectDelta(1)
	case tea.KeyUp:
		m.selectDelta(-1)
	case tea.KeyDown:
		m.selectDelta(1)
	default:
		m.forwardKey(msg)
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
	}
}

func (m *tuiModel) selectDelta(delta int) {
	ordered := m.orderedSessions()
	if len(ordered) == 0 {
		return
	}
	m.selected = ((m.selected+delta)%len(ordered) + len(ordered)) % len(ordered)
	m.inputFocus = -1
}

// moveOrder swaps the selected session's position in the display order.
func (m *tuiModel) moveOrder(delta int) {
	n := len(m.order)
	if n < 2 || m.selected < 0 || m.selected >= n {
		return
	}
	target := ((m.selected + delta) % n + n) % n
	m.order[m.selected], m.order[target] = m.order[target], m.order[m.selected]
	m.selected = target
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
