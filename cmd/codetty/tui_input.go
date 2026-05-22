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
			if m.conn != nil {
				m.conn.CreateSession("", []string{terminal.DefaultShell()}, uint16(m.width), uint16(m.height))
			}
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
			return nil
		case 'g':
			if m.mode == modeGrid {
				m.mode = modeFocusBig
			} else {
				m.mode = modeGrid
			}
			return nil
		case 'b':
			m.mode = modeFocusBig
			return nil
		case 'a':
			m.mode = modeGrid
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
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			idx := int(msg.Runes[0]-'0') - 1
			if idx < len(m.orderedSessions()) {
				m.selected = idx
				m.mode = modeFocusBig
			}
			return nil
		}
	}
	return nil
}

func (m *tuiModel) handleGridKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEnter:
		m.mode = modeFocusBig
		return nil
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
	sess := m.selectedSession()
	if sess == nil || m.conn == nil {
		return
	}
	raw := tuiKeyToBytes(msg)
	if len(raw) > 0 {
		m.conn.SendInput(sess.Name, raw)
	}
}

func (m *tuiModel) selectDelta(delta int) {
	ordered := m.orderedSessions()
	if len(ordered) == 0 {
		return
	}
	m.selected = ((m.selected+delta)%len(ordered) + len(ordered)) % len(ordered)
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
		return []byte{0x1b, '[', 'A'}
	case tea.KeyDown:
		return []byte{0x1b, '[', 'B'}
	case tea.KeyRight:
		return []byte{0x1b, '[', 'C'}
	case tea.KeyLeft:
		return []byte{0x1b, '[', 'D'}
	case tea.KeyHome:
		return []byte{0x1b, '[', 'H'}
	case tea.KeyEnd:
		return []byte{0x1b, '[', 'F'}
	case tea.KeyDelete:
		return []byte{0x1b, '[', '3', '~'}
	case tea.KeyPgUp:
		return []byte{0x1b, '[', '5', '~'}
	case tea.KeyPgDown:
		return []byte{0x1b, '[', '6', '~'}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	default:
		if msg.Type >= tea.KeyCtrlA && msg.Type <= tea.KeyCtrlZ {
			return []byte{byte(msg.Type)}
		}
		return nil
	}
}
