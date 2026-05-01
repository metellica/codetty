package hub

import (
	"strings"
	"testing"
	"time"
)

func TestClientSession_WriteOutputAndScreen(t *testing.T) {
	sess := NewClientSession("test1", "test@host", 80, 24, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("Hello, World!\r\n"))
	sess.WriteOutput([]byte("Line two"))

	// vt10x processes writes asynchronously via internal pipe
	time.Sleep(50 * time.Millisecond)

	screen := sess.Screen()
	if !strings.Contains(screen, "Hello, World!") {
		t.Fatalf("screen should contain 'Hello, World!', got:\n%s", screen)
	}
	if !strings.Contains(screen, "Line two") {
		t.Fatalf("screen should contain 'Line two', got:\n%s", screen)
	}
}

func TestClientSession_ScreenSize(t *testing.T) {
	sess := NewClientSession("test2", "test@host", 120, 40, nil)
	defer sess.Close()

	if sess.Cols != 120 || sess.Rows != 40 {
		t.Fatalf("expected 120x40, got %dx%d", sess.Cols, sess.Rows)
	}
}

func TestClientSession_RenderANSIIncludesCursorWhenTuiIsTallerThanPanel(t *testing.T) {
	sess := NewClientSession("copilot", "copilot@test", 80, 40, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("\033[2J\033[H"))
	sess.WriteOutput([]byte("GitHub Copilot\r\n"))
	sess.WriteOutput([]byte("\033[38;1H~/logsim-mount/tmp"))
	sess.WriteOutput([]byte("\033[39;H────────────────────"))
	sess.WriteOutput([]byte("\033[40;H❯ focus-test"))

	time.Sleep(50 * time.Millisecond)

	lines, _, curY := sess.RenderANSI(78, 22)
	rendered := strings.Join(lines, "\n")
	if curY < 0 {
		t.Fatalf("expected cursor row to be visible in rendered panel, got curY=%d\n%s", curY, rendered)
	}
	if !strings.Contains(rendered, "focus-test") {
		t.Fatalf("expected rendered panel to include bottom input text, got:\n%s", rendered)
	}
}

func TestClientSession_RenderANSIUsesHiddenCursorPositionForViewport(t *testing.T) {
	sess := NewClientSession("copilot", "copilot@test", 80, 40, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("\033[2J\033[H\033[?25l"))
	sess.WriteOutput([]byte("GitHub Copilot\r\n"))
	sess.WriteOutput([]byte("\033[38;1H~/logsim-mount/tmp"))
	sess.WriteOutput([]byte("\033[39;H────────────────────"))
	sess.WriteOutput([]byte("\033[40;H❯ focus-test"))

	time.Sleep(50 * time.Millisecond)

	lines, _, curY := sess.RenderANSI(78, 22)
	rendered := strings.Join(lines, "\n")
	if curY >= 0 {
		t.Fatalf("expected hidden cursor not to be drawn, got curY=%d\n%s", curY, rendered)
	}
	if !strings.Contains(rendered, "focus-test") {
		t.Fatalf("expected hidden cursor position to keep input text visible, got:\n%s", rendered)
	}
}

func TestClientSession_RenderANSIUsesBottomContentWhenCursorReturnsHome(t *testing.T) {
	sess := NewClientSession("copilot", "copilot@test", 80, 40, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("\033[2J\033[H\033[?25l"))
	sess.WriteOutput([]byte("GitHub Copilot\r\n"))
	sess.WriteOutput([]byte("\033[38;1H~/logsim-mount/tmp"))
	sess.WriteOutput([]byte("\033[39;H────────────────────"))
	sess.WriteOutput([]byte("\033[40;H❯ focus-test"))
	sess.WriteOutput([]byte("\033[H"))

	time.Sleep(50 * time.Millisecond)

	lines, _, curY := sess.RenderANSI(78, 22)
	rendered := strings.Join(lines, "\n")
	if curY >= 0 {
		t.Fatalf("expected hidden cursor not to be drawn after returning home, got curY=%d\n%s", curY, rendered)
	}
	if !strings.Contains(rendered, "focus-test") {
		t.Fatalf("expected viewport to stay anchored on bottom content even after cursor moved home, got:\n%s", rendered)
	}
}

func TestClientSession_RenderANSIAnchorsOnLastNonBlankRowWithoutExtraBlankPadding(t *testing.T) {
	sess := NewClientSession("copilot", "copilot@test", 80, 40, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("\033[2J\033[H\033[?25l"))
	sess.WriteOutput([]byte("\033[32;1Htop-of-viewport"))
	sess.WriteOutput([]byte("\033[36;1H❯ focus-test"))
	sess.WriteOutput([]byte("\033[H"))

	time.Sleep(50 * time.Millisecond)

	lines, _, _ := sess.RenderANSI(78, 5)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "top-of-viewport") {
		t.Fatalf("expected viewport to anchor on the last non-blank row without adding an extra blank row, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "focus-test") {
		t.Fatalf("expected bottom prompt text to remain visible, got:\n%s", rendered)
	}
}

func TestClientSession_RenderANSIUsesStyledSpaceRowsForViewportAnchoring(t *testing.T) {
	sess := NewClientSession("copilot", "copilot@test", 80, 40, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("\033[2J\033[H\033[?25l"))
	sess.WriteOutput([]byte("GitHub Copilot"))
	sess.WriteOutput([]byte("\033[34;1HAllow once"))
	sess.WriteOutput([]byte("\033[36;1H\033[7m                    \033[0m"))
	sess.WriteOutput([]byte("\033[H"))

	time.Sleep(50 * time.Millisecond)

	lines, _, _ := sess.RenderANSI(20, 5)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "Allow once") {
		t.Fatalf("expected permission text to stay visible, got:\n%s", rendered)
	}
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "\033[") {
		t.Fatalf("expected reverse-video space row to count as visible bottom content, got last line %q in:\n%s", lastLine, rendered)
	}
}

func TestClientSession_RenderANSIIgnoresForegroundOnlySpaceRowsForViewportAnchoring(t *testing.T) {
	sess := NewClientSession("copilot", "copilot@test", 80, 40, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("\033[2J\033[H\033[?25l"))
	sess.WriteOutput([]byte("GitHub Copilot"))
	sess.WriteOutput([]byte("\033[34;1HAllow once"))
	sess.WriteOutput([]byte("\033[36;1H\033[31m                    \033[0m"))
	sess.WriteOutput([]byte("\033[H"))

	time.Sleep(50 * time.Millisecond)

	lines, _, _ := sess.RenderANSI(20, 5)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "Allow once") {
		t.Fatalf("expected permission text to stay visible, got:\n%s", rendered)
	}
	lastLine := lines[len(lines)-1]
	if strings.Contains(lastLine, "\033[") {
		t.Fatalf("expected foreground-only spaces to stay visually blank for viewport anchoring, got last line %q in:\n%s", lastLine, rendered)
	}
}

func TestClientSession_RenderANSIUsesBackgroundOnlySpaceRowsForViewportAnchoring(t *testing.T) {
	sess := NewClientSession("copilot", "copilot@test", 80, 40, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("\033[2J\033[H\033[?25l"))
	sess.WriteOutput([]byte("GitHub Copilot"))
	sess.WriteOutput([]byte("\033[34;1HAllow once"))
	sess.WriteOutput([]byte("\033[36;1H\033[41m                    \033[0m"))
	sess.WriteOutput([]byte("\033[H"))

	time.Sleep(50 * time.Millisecond)

	lines, _, _ := sess.RenderANSI(20, 5)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "Allow once") {
		t.Fatalf("expected permission text to stay visible, got:\n%s", rendered)
	}
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "\033[") {
		t.Fatalf("expected background-only spaces to count as visible bottom content, got last line %q in:\n%s", lastLine, rendered)
	}
}

func TestClientSession_RenderANSIUsesUnderlinedSpaceRowsForViewportAnchoring(t *testing.T) {
	sess := NewClientSession("copilot", "copilot@test", 80, 40, nil)
	defer sess.Close()

	sess.WriteOutput([]byte("\033[2J\033[H\033[?25l"))
	sess.WriteOutput([]byte("GitHub Copilot"))
	sess.WriteOutput([]byte("\033[34;1HAllow once"))
	sess.WriteOutput([]byte("\033[36;1H\033[4m                    \033[0m"))
	sess.WriteOutput([]byte("\033[H"))

	time.Sleep(50 * time.Millisecond)

	lines, _, _ := sess.RenderANSI(20, 5)
	rendered := strings.Join(lines, "\n")
	if !strings.Contains(rendered, "Allow once") {
		t.Fatalf("expected permission text to stay visible, got:\n%s", rendered)
	}
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "\033[") {
		t.Fatalf("expected underlined spaces to count as visible bottom content, got last line %q in:\n%s", lastLine, rendered)
	}
}
