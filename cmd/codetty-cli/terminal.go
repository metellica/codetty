package main

import (
	"errors"
	"io"
)

var errNoCommand = errors.New("no command specified")

type terminalSession interface {
	io.ReadWriteCloser
	Resize(cols, rows uint16) error
	Wait() error
}

func startTerminal(args []string, cols, rows uint16) (terminalSession, error) {
	if len(args) == 0 {
		return nil, errNoCommand
	}
	return startPlatformTerminal(args, cols, rows)
}
