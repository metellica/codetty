package main

import (
	"errors"
	"testing"
)

type fakeTerminalProbe struct {
	terminals map[int]bool
	sizes     map[int]fakeTerminalSize
}

type fakeTerminalSize struct {
	cols int
	rows int
	err  error
}

func (f fakeTerminalProbe) IsTerminal(fd int) bool {
	return f.terminals[fd]
}

func (f fakeTerminalProbe) GetSize(fd int) (int, int, error) {
	size, ok := f.sizes[fd]
	if !ok {
		return 0, 0, errors.New("size unavailable")
	}
	return size.cols, size.rows, size.err
}

func TestDetectLocalTerminalConfigFallsBackToStdoutWhenStdinSizeFails(t *testing.T) {
	cfg := detectLocalTerminalConfig(
		fakeTerminalProbe{
			terminals: map[int]bool{1: true, 2: true},
			sizes: map[int]fakeTerminalSize{
				1: {err: errors.New("invalid stdin handle")},
				2: {cols: 120, rows: 80},
			},
		},
		1,
		[]int{1, 2},
	)

	if !cfg.inputIsTTY {
		t.Fatal("stdin should still be treated as the interactive terminal handle")
	}
	if cfg.cols != 120 || cfg.rows != 80 {
		t.Fatalf("terminal size = %dx%d, want 120x80", cfg.cols, cfg.rows)
	}
}

func TestDetectLocalTerminalConfigKeepsInputTTYSeparateFromDisplaySizeFallback(t *testing.T) {
	cfg := detectLocalTerminalConfig(
		fakeTerminalProbe{
			terminals: map[int]bool{2: true},
			sizes: map[int]fakeTerminalSize{
				2: {cols: 100, rows: 40},
			},
		},
		1,
		[]int{1, 2},
	)

	if cfg.inputIsTTY {
		t.Fatal("stdout fallback should not make stdin look interactive")
	}
	if cfg.cols != 100 || cfg.rows != 40 {
		t.Fatalf("terminal size = %dx%d, want 100x40", cfg.cols, cfg.rows)
	}
}

func TestDetectLocalTerminalConfigUsesDefaultsWhenNoTerminalSizeIsAvailable(t *testing.T) {
	cfg := detectLocalTerminalConfig(
		fakeTerminalProbe{
			terminals: map[int]bool{1: true, 2: true},
			sizes: map[int]fakeTerminalSize{
				1: {err: errors.New("invalid stdin handle")},
				2: {err: errors.New("invalid stdout handle")},
			},
		},
		1,
		[]int{1, 2},
	)

	if !cfg.inputIsTTY {
		t.Fatal("stdin terminal detection should still be reported")
	}
	if cfg.cols != 220 || cfg.rows != 50 {
		t.Fatalf("terminal size = %dx%d, want default 220x50", cfg.cols, cfg.rows)
	}
}
