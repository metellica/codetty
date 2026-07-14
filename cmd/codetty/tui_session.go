package main

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"codetty/vt10x"

	"github.com/mattn/go-runewidth"
)

// vt10x Glyph.Mode flags (unexported in vt10x package)
const (
	glyphReverse   = 1 << 0
	glyphUnderline = 1 << 1
	glyphBold      = 1 << 2
	glyphItalic    = 1 << 4
)

// tuiSession represents one daemon session's display state in the TUI.
type tuiSession struct {
	Name      string
	Command   string
	Cols      uint16
	Rows      uint16
	Exited    bool
	ExitCode  int
	CreatedAt string
	Temporary bool // created via Ctrl+A m, killed on floating close

	// Tab support
	Tabs      []tuiTab // tabs within this session (native only)
	ActiveTab int      // index of the currently active tab

	term vt10x.Terminal
	mu   sync.Mutex

	// Scrollback view offset: 0 = live view, >0 = scrolled back into history.
	scrollOffset int

	// Per-source-row line cache for incremental rendering. lineCache is
	// indexed by terminal row; lineValid marks entries that are up to date.
	// The cache consumes the terminal's dirty-line flags (via ResetDirty),
	// so it must be the only dirty-flag consumer for this terminal.
	lineCache      []string
	lineValid      []bool
	cacheW, cacheH int
	// Cursor state baked into the cached lines, used to invalidate the rows
	// the cursor block was drawn on / moves to.
	cachedCurRow int
	cachedCurVis bool

	// Dimmed line cache for side panel rendering
	dimLines  []string
	dimCacheW int
	dimCacheH int
	dimStale  bool
}

// tuiTab represents a tab entry for display in the TUI.
type tuiTab struct {
	Name       string
	Command    string
	Agent      string // detected agent name (e.g. "copilot"), "" if none
	AgentState string // "idle", "working", "blocked", "" if unknown
	// Done marks a finished agent run (working → idle/exit) that the user
	// has not looked at yet. TUI-side only; cleared by ackAgentDone once
	// the tab is viewed as the focused panel's active tab.
	Done bool
}

func newTUISession(name, command string, cols, rows uint16) *tuiSession {
	term := vt10x.New(vt10x.WithSize(int(cols), int(rows)), vt10x.WithWriter(io.Discard))
	return &tuiSession{
		Name:    name,
		Command: command,
		Cols:    cols,
		Rows:    rows,
		term:    term,
	}
}

// WriteOutput feeds PTY output into the vt10x terminal.
func (s *tuiSession) WriteOutput(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Write(data)
	s.dimStale = true
}

// ScrollUp moves the scrollback view offset up (further into history).
// Returns true if the offset changed (i.e. there is more scrollback to show).
func (s *tuiSession) ScrollUp(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	sbLen := s.term.ScrollbackLen()
	s.term.Unlock()
	if sbLen == 0 {
		return false
	}
	old := s.scrollOffset
	s.scrollOffset += n
	if s.scrollOffset > sbLen {
		s.scrollOffset = sbLen
	}
	return s.scrollOffset != old
}

// ScrollDown moves the scrollback view offset down (toward live view).
// Returns true if the offset changed.
func (s *tuiSession) ScrollDown(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.scrollOffset
	s.scrollOffset -= n
	if s.scrollOffset < 0 {
		s.scrollOffset = 0
	}
	return s.scrollOffset != old
}

// IsScrolledBack returns true if the session is showing scrollback content.
func (s *tuiSession) IsScrolledBack() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scrollOffset > 0
}

// HasScrollback returns true if there are lines in the scrollback buffer.
func (s *tuiSession) HasScrollback() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return s.term.ScrollbackLen() > 0
}

// invalidateCache forces a full re-render on the next frame.
func (s *tuiSession) invalidateCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lineValid = nil
	s.lineCache = nil
	s.dimStale = true
}

// resetTerminal clears all content and mode flags from the vt10x terminal so
// stale state from a previous tab does not bleed through before the new tab's
// snapshot arrives.
func (s *tuiSession) resetTerminal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Clear alt-screen, mouse, and all other mode flags so stale modes
	// from the previous tab (e.g. ModeAltScreen from nvim) don't persist.
	s.term.Lock()
	s.term.ClearMode(vt10x.ModeAltScreen | vt10x.ModeMouseMask | vt10x.ModeMouseSgr | vt10x.ModeAppCursor | vt10x.ModeBracketedPaste)
	// The scrollback belongs to the previous tab's content: discard it and
	// snap back to the live view.
	s.term.ClearScrollback()
	s.term.Unlock()
	s.scrollOffset = 0
	// ESC[2J clears the entire screen; ESC[H moves cursor to (0,0).
	s.term.Write([]byte("\x1b[2J\x1b[H"))
	s.lineValid = nil
	s.lineCache = nil
	s.dimStale = true
}

// RenderANSI renders the terminal screen at the given dimensions, reusing
// cached per-row strings for rows the terminal has not modified.
func (s *tuiSession) RenderANSI(w, h int) (lines []string, curX, curY int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()

	// When scrolled back into the scrollback buffer, use a dedicated path.
	if s.scrollOffset > 0 {
		return s.renderScrollback(w, h)
	}

	cols, rows := s.term.Size()

	// If dimensions changed, invalidate the whole cache.
	if s.cacheW != w || s.cacheH != h || len(s.lineCache) != rows {
		s.lineCache = make([]string, rows)
		s.lineValid = make([]bool, rows)
		s.cacheW = w
		s.cacheH = h
		s.cachedCurVis = false
	}

	// Invalidate rows the terminal reports as modified.
	for y := 0; y < rows; y++ {
		if s.term.Dirty(y) {
			s.lineValid[y] = false
		}
	}
	s.term.ResetDirty()

	// Invalidate the rows the cursor block leaves and enters. The cursor
	// row is also invalidated on every render to track column-only moves.
	cursor := s.term.Cursor()
	curVisible := s.term.CursorVisible()
	if s.cachedCurVis && s.cachedCurRow >= 0 && s.cachedCurRow < rows {
		s.lineValid[s.cachedCurRow] = false
	}
	if curVisible && cursor.Y >= 0 && cursor.Y < rows {
		s.lineValid[cursor.Y] = false
	}
	s.cachedCurRow = cursor.Y
	s.cachedCurVis = curVisible

	startRow := 0
	if contentRows := findLastVisibleRow(s.term, cols, rows) + 1; contentRows > h {
		startRow = contentRows - h
	}

	lines = make([]string, h)
	curX, curY = -1, -1
	for displayRow := 0; displayRow < h; displayRow++ {
		srcRow := startRow + displayRow
		if srcRow >= rows {
			lines[displayRow] = strings.Repeat(" ", w)
			continue
		}
		// The cursor row is always invalidated above, so whenever the cursor
		// is on a displayed row that row is rebuilt here and reports the
		// cursor's visual column.
		if !s.lineValid[srcRow] {
			cx := -1
			if curVisible && srcRow == cursor.Y {
				cx = cursor.X
			}
			rendered, visualCurX := renderRowANSI(s.term.Row(srcRow), w, cx)
			s.lineCache[srcRow] = rendered
			s.lineValid[srcRow] = true
			if visualCurX >= 0 {
				curX = visualCurX
				curY = displayRow
			}
		}
		lines[displayRow] = s.lineCache[srcRow]
	}
	return lines, curX, curY
}

// renderScrollback renders the combined scrollback + visible buffer at the
// current scrollOffset. Called with both mu and term locks held.
// No cursor is shown while scrolled back.
func (s *tuiSession) renderScrollback(w, h int) ([]string, int, int) {
	_, rows := s.term.Size()
	sbLen := s.term.ScrollbackLen()

	// Clamp scrollOffset so it doesn't exceed the scrollback size.
	if s.scrollOffset > sbLen {
		s.scrollOffset = sbLen
	}

	// Combined buffer: scrollback (0..sbLen-1) + visible (0..rows-1).
	// We want to show h rows ending at position (sbLen + rows - scrollOffset - 1).
	totalLines := sbLen + rows
	endIdx := totalLines - s.scrollOffset // one past the last line to show
	startIdx := endIdx - h
	if startIdx < 0 {
		startIdx = 0
	}

	// Consume dirty flags so they don't accumulate.
	s.term.ResetDirty()

	lines := make([]string, h)
	for displayRow := 0; displayRow < h; displayRow++ {
		idx := startIdx + displayRow
		if idx < 0 || idx >= totalLines {
			lines[displayRow] = strings.Repeat(" ", w)
			continue
		}
		var row []vt10x.Glyph
		if idx < sbLen {
			row = s.term.ScrollbackRow(idx)
		} else {
			row = s.term.Row(idx - sbLen)
		}
		rendered, _ := renderRowANSI(row, w, -1) // no cursor while scrolled
		lines[displayRow] = rendered
	}
	return lines, -1, -1
}

// SnapshotANSI returns a snapshot for panel rendering.
func (s *tuiSession) SnapshotANSI(w, h int) terminalSnap {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	lines, curX, curY := renderTerminalANSI(s.term, w, h)
	return terminalSnap{
		lines:         lines,
		cursorX:       curX,
		cursorY:       curY,
		cursorVisible: s.term.CursorVisible(),
	}
}

type terminalSnap struct {
	lines         []string
	cursorX       int
	cursorY       int
	cursorVisible bool
}

// Resize updates the vt10x terminal size.
func (s *tuiSession) Resize(cols, rows uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Cols = cols
	s.Rows = rows
	s.term.Resize(int(cols), int(rows))
	s.lineCache = nil
	s.dimLines = nil
}

// MouseEnabled returns true if the session's terminal has mouse reporting on.
func (s *tuiSession) MouseEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return s.term.Mode()&vt10x.ModeMouseMask != 0
}

// AltScreenActive returns true if the session is in the alternate screen buffer
// (e.g. less, vim, man, htop).
func (s *tuiSession) AltScreenActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return s.term.Mode()&vt10x.ModeAltScreen != 0
}

// BracketedPasteEnabled returns true if the session's terminal has bracketed
// paste mode (DEC 2004) on.
func (s *tuiSession) BracketedPasteEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	return s.term.Mode()&vt10x.ModeBracketedPaste != 0
}

// ResetMouseMode clears mouse reporting and bracketed paste mode flags
// from the terminal. Called on tab switch so stale flags from a previous
// tab (e.g. lazygit) don't cause SGR sequences or bracketed-paste markers
// to be forwarded to a plain shell.
func (s *tuiSession) ResetMouseMode() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.term.Lock()
	defer s.term.Unlock()
	s.term.ClearMode(vt10x.ModeMouseMask | vt10x.ModeMouseSgr | vt10x.ModeBracketedPaste)
}

// RenderDimmed renders terminal content in uniform dim gray, skipping per-cell color processing.
// The result is cached until new output arrives or the size changes, since
// side panels are re-rendered on every View() call.
func (s *tuiSession) RenderDimmed(w, h int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.dimCacheW != w || s.dimCacheH != h {
		s.dimLines = nil
		s.dimCacheW = w
		s.dimCacheH = h
	}
	if s.dimLines != nil && !s.dimStale {
		return s.dimLines
	}

	s.term.Lock()
	lines := renderTerminalDimmed(s.term, w, h)
	s.term.Unlock()
	s.dimLines = lines
	s.dimStale = false
	return lines
}

func renderTerminalDimmed(term vt10x.Terminal, w, h int) []string {
	cols, rows := term.Size()

	lastVisibleRow := findLastVisibleRow(term, cols, rows)
	startRow := 0
	contentRows := lastVisibleRow + 1
	if contentRows > h {
		startRow = contentRows - h
	}

	lines := make([]string, h)
	dimStart := "\x1b[38;5;242m"
	dimEnd := "\x1b[0m"

	var sb strings.Builder
	sb.Grow(w + 20)

	for displayRow := 0; displayRow < h; displayRow++ {
		srcRow := startRow + displayRow
		if srcRow >= rows {
			lines[displayRow] = strings.Repeat(" ", w)
			continue
		}

		row := term.Row(srcRow)
		sb.Reset()
		sb.WriteString(dimStart)
		visualCol := 0
		skipNext := false
		for x := 0; x < len(row) && visualCol < w; x++ {
			if skipNext {
				skipNext = false
				continue
			}
			ch := row[x].Char
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
			sb.WriteRune(ch)
			visualCol += cw
			// Only skip the following cell if it is an actual NUL continuation
			// cell. An application may overwrite a continuation cell with a new
			// (possibly wide) character, leaving two leads adjacent; in that
			// case the next cell holds a real glyph that must still be rendered.
			if cw > 1 && x+1 < len(row) && row[x+1].Char == 0 {
				skipNext = true
			}
		}
		for visualCol < w {
			sb.WriteByte(' ')
			visualCol++
		}
		sb.WriteString(dimEnd)
		lines[displayRow] = sb.String()
	}
	return lines
}

func findLastVisibleRow(term vt10x.Terminal, cols, rows int) int {
	for y := rows - 1; y > 0; y-- {
		row := term.Row(y)
		for x := 0; x < cols && x < len(row); x++ {
			if c := row[x].Char; c != 0 && c != ' ' {
				return y
			}
		}
	}
	return 0
}

// glyphAttr is the render-relevant attribute set of a cell, used to detect
// runs of identically-styled cells so SGR sequences are emitted only at
// style-change boundaries instead of per cell.
type glyphAttr struct {
	fg, bg vt10x.Color
	mode   int16
	cursor bool
}

const glyphModeMask = glyphReverse | glyphUnderline | glyphBold | glyphItalic

func attrOf(g vt10x.Glyph, isCursor bool) glyphAttr {
	return glyphAttr{fg: g.FG, bg: g.BG, mode: g.Mode & glyphModeMask, cursor: isCursor}
}

var defaultGlyphAttr = glyphAttr{fg: vt10x.DefaultFG, bg: vt10x.DefaultBG}

// writeAttrSGR writes the SGR start sequence for a non-default attribute.
// Mirrors the per-glyph logic previously in glyphANSI.
func writeAttrSGR(sb *strings.Builder, a glyphAttr) {
	if a.cursor {
		// Force visible cursor: white background with black text.
		sb.WriteString("\x1b[30;107m")
	} else if a.mode&glyphReverse != 0 {
		// Reverse video: swap fg↔bg and emit directly as normal colors.
		// This avoids issues where \x1b[7m with explicit color 0 (black)
		// would produce invisible results.
		visualFG := a.bg // bg becomes visible foreground
		visualBG := a.fg // fg becomes visible background
		if visualFG != vt10x.DefaultBG && visualFG != vt10x.DefaultFG {
			sb.WriteString(colorToANSIFG(vt10x.Color(uint32(visualFG))))
		}
		if visualBG != vt10x.DefaultFG && visualBG != vt10x.DefaultBG {
			sb.WriteString(colorToANSIBG(vt10x.Color(uint32(visualBG))))
		} else {
			// fg was default → visual bg should be default fg color (white)
			sb.WriteString("\x1b[7m")
		}
	} else {
		if a.fg != vt10x.DefaultFG {
			sb.WriteString(colorToANSIFG(a.fg))
		}
		if a.bg != vt10x.DefaultBG {
			sb.WriteString(colorToANSIBG(a.bg))
		}
	}
	if a.mode&glyphBold != 0 {
		sb.WriteString("\x1b[1m")
	}
	if a.mode&glyphUnderline != 0 {
		sb.WriteString("\x1b[4m")
	}
	if a.mode&glyphItalic != 0 {
		sb.WriteString("\x1b[3m")
	}
}

// renderRowANSI renders a single terminal row to an ANSI string of exactly w
// visual columns. cursorX is the terminal-column of the cursor on this row,
// or -1 if the cursor is not on this row. Returns the rendered line and the
// visual column the cursor block was drawn at (-1 if not drawn).
func renderRowANSI(row []vt10x.Glyph, w int, cursorX int) (string, int) {
	var sb strings.Builder
	sb.Grow(w + 32)

	curVisualX := -1
	attr := defaultGlyphAttr
	visualCol := 0
	skipNext := false
	for x := 0; x < len(row) && visualCol < w; x++ {
		if skipNext {
			skipNext = false
			continue
		}
		g := row[x]
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
		nextIsCont := cw > 1 && x+1 < len(row) && row[x+1].Char == 0

		isCursor := x == cursorX || (nextIsCont && cursorX == x+1)
		if isCursor {
			curVisualX = visualCol
		}

		// Emit SGR only when the cell's style differs from the running one.
		a := attrOf(g, isCursor)
		if a != attr {
			if attr != defaultGlyphAttr {
				sb.WriteString("\x1b[0m")
			}
			if a != defaultGlyphAttr {
				writeAttrSGR(&sb, a)
			}
			attr = a
		}
		sb.WriteRune(ch)
		visualCol += cw
		if nextIsCont {
			skipNext = true
		}
	}
	if attr != defaultGlyphAttr {
		sb.WriteString("\x1b[0m")
	}
	for visualCol < w {
		sb.WriteByte(' ')
		visualCol++
	}
	return sb.String(), curVisualX
}

func renderTerminalANSI(term vt10x.Terminal, w, h int) ([]string, int, int) {
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
	curX, curY := -1, -1

	for displayRow := 0; displayRow < h; displayRow++ {
		srcRow := startRow + displayRow
		if srcRow >= rows {
			lines[displayRow] = strings.Repeat(" ", w)
			continue
		}
		cx := -1
		if curVisible && srcRow == cursor.Y {
			cx = cursor.X
		}
		rendered, visualCurX := renderRowANSI(term.Row(srcRow), w, cx)
		lines[displayRow] = rendered
		if visualCurX >= 0 {
			curX = visualCurX
			curY = displayRow
		}
	}
	return lines, curX, curY
}

func glyphANSI(g vt10x.Glyph, isCursor bool) (string, string) {
	var start strings.Builder
	needReset := false

	fg := g.FG
	bg := g.BG

	if isCursor {
		// Force visible cursor: white background with black text.
		start.WriteString("\x1b[30;107m") // black fg, bright white bg
		needReset = true
	} else {
		if g.Mode&glyphReverse != 0 {
			// Reverse video: swap fg↔bg and emit directly as normal colors.
			// This avoids issues where \x1b[7m with explicit color 0 (black)
			// would produce invisible results.
			visualFG := bg // bg becomes visible foreground
			visualBG := fg // fg becomes visible background
			if visualFG != vt10x.DefaultBG && visualFG != vt10x.DefaultFG {
				start.WriteString(colorToANSIFG(vt10x.Color(uint32(visualFG))))
			}
			if visualBG != vt10x.DefaultFG && visualBG != vt10x.DefaultBG {
				start.WriteString(colorToANSIBG(vt10x.Color(uint32(visualBG))))
			} else {
				// fg was default → visual bg should be default fg color (white)
				start.WriteString("\x1b[7m")
			}
			needReset = true
		} else {
			if fg != vt10x.DefaultFG {
				start.WriteString(colorToANSIFG(fg))
				needReset = true
			}
			if bg != vt10x.DefaultBG {
				start.WriteString(colorToANSIBG(bg))
				needReset = true
			}
		}
	}
	if g.Mode&glyphBold != 0 {
		start.WriteString("\x1b[1m")
		needReset = true
	}
	if g.Mode&glyphUnderline != 0 {
		start.WriteString("\x1b[4m")
		needReset = true
	}
	if g.Mode&glyphItalic != 0 {
		start.WriteString("\x1b[3m")
		needReset = true
	}

	if needReset {
		return start.String(), "\x1b[0m"
	}
	return "", ""
}

var (
	ansiFG [256]string
	ansiBG [256]string
)

func init() {
	for i := 0; i < 16; i++ {
		if i < 8 {
			ansiFG[i] = fmt.Sprintf("\x1b[%dm", 30+i)
			ansiBG[i] = fmt.Sprintf("\x1b[%dm", 40+i)
		} else {
			ansiFG[i] = fmt.Sprintf("\x1b[%dm", 90+i-8)
			ansiBG[i] = fmt.Sprintf("\x1b[%dm", 100+i-8)
		}
	}
	for i := 16; i < 256; i++ {
		ansiFG[i] = fmt.Sprintf("\x1b[38;5;%dm", i)
		ansiBG[i] = fmt.Sprintf("\x1b[48;5;%dm", i)
	}
}

func colorToANSIFG(c vt10x.Color) string {
	v := uint32(c)
	if v < 256 {
		return ansiFG[v]
	}
	r := (v >> 16) & 0xFF
	g := (v >> 8) & 0xFF
	b := v & 0xFF
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
}

func colorToANSIBG(c vt10x.Color) string {
	v := uint32(c)
	if v < 256 {
		return ansiBG[v]
	}
	r := (v >> 16) & 0xFF
	g := (v >> 8) & 0xFF
	b := v & 0xFF
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}
