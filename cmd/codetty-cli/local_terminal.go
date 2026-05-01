package main

import (
	"os"

	xterm "golang.org/x/term"
)

const (
	defaultTerminalCols uint16 = 220
	defaultTerminalRows uint16 = 50
)

type terminalProbe interface {
	IsTerminal(fd int) bool
	GetSize(fd int) (int, int, error)
}

type xtermTerminalProbe struct{}

func (xtermTerminalProbe) IsTerminal(fd int) bool {
	return xterm.IsTerminal(fd)
}

func (xtermTerminalProbe) GetSize(fd int) (int, int, error) {
	return xterm.GetSize(fd)
}

type localTerminalConfig struct {
	inputFD    int
	inputIsTTY bool
	cols       uint16
	rows       uint16
}

func currentLocalTerminalConfig() localTerminalConfig {
	inputFD := int(os.Stdin.Fd())
	return detectLocalTerminalConfig(
		xtermTerminalProbe{},
		inputFD,
		[]int{inputFD, int(os.Stdout.Fd()), int(os.Stderr.Fd())},
	)
}

func detectLocalTerminalConfig(probe terminalProbe, inputFD int, sizeFDs []int) localTerminalConfig {
	cfg := localTerminalConfig{
		inputFD:    inputFD,
		inputIsTTY: probe.IsTerminal(inputFD),
		cols:       defaultTerminalCols,
		rows:       defaultTerminalRows,
	}

	seen := make(map[int]struct{}, len(sizeFDs))
	for _, fd := range sizeFDs {
		if _, ok := seen[fd]; ok {
			continue
		}
		seen[fd] = struct{}{}

		if !probe.IsTerminal(fd) {
			continue
		}

		cols, rows, err := probe.GetSize(fd)
		if err != nil || cols <= 0 || rows <= 0 {
			continue
		}

		cfg.cols = uint16(cols)
		cfg.rows = uint16(rows)
		return cfg
	}

	return cfg
}
