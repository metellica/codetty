package main

import (
	"sync"

	"codetty/terminal"
)

// ScreenMirror is a type alias for the shared terminal.ScreenMirror.
type ScreenMirror = terminal.ScreenMirror

// NewScreenMirror delegates to terminal.NewScreenMirror.
var NewScreenMirror = terminal.NewScreenMirror

func snapshotReplay(outputCh <-chan []byte, screen *ScreenMirror, replayMu *sync.Mutex) []byte {
	replayMu.Lock()
	defer replayMu.Unlock()

	drainOutput(outputCh)
	return screen.Snapshot()
}

func drainOutput(outputCh <-chan []byte) {
	for {
		select {
		case <-outputCh:
		default:
			return
		}
	}
}
