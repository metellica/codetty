package main

import "codetty/terminal"

// terminalSession is a type alias for the shared terminal.Session interface.
type terminalSession = terminal.Session

func startTerminal(args []string, cols, rows uint16) (terminalSession, error) {
	return terminal.Start(args, cols, rows)
}
