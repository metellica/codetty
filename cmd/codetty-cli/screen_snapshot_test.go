package main

import (
	"strings"
	"sync"
	"testing"

	"github.com/hinshun/vt10x"
)

func TestScreenMirrorSnapshotRestoresFullScreenFromBlank(t *testing.T) {
	mirror := NewScreenMirror(40, 8)
	mirror.Write([]byte("\033[2J\033[H"))
	mirror.Write([]byte("GitHub Copilot"))
	mirror.Write([]byte("\033[8;1H❯ ready"))

	restored := vt10x.New(vt10x.WithSize(40, 8))
	restored.Write(mirror.Snapshot())

	screen := restored.String()
	if !strings.Contains(screen, "GitHub Copilot") {
		t.Fatalf("snapshot should restore header, got:\n%s", screen)
	}
	if !strings.Contains(screen, "❯ ready") {
		t.Fatalf("snapshot should restore prompt, got:\n%s", screen)
	}
}

func TestScreenMirrorSnapshotReturnsNilBeforeOutput(t *testing.T) {
	mirror := NewScreenMirror(40, 8)

	if got := mirror.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot before output should be empty, got %d bytes", len(got))
	}
}

func TestScreenMirrorSnapshotRestoresTerminalModes(t *testing.T) {
	mirror := NewScreenMirror(40, 8)
	mirror.Write([]byte("\033[?1049h\033[?1h\033[?7l\033[?1006h"))
	mirror.Write([]byte("alternate screen"))

	restored := vt10x.New(vt10x.WithSize(40, 8))
	restored.Write(mirror.Snapshot())

	mode := restored.Mode()
	if mode&vt10x.ModeAltScreen == 0 {
		t.Fatalf("snapshot should restore alt-screen mode, mode=%#x", mode)
	}
	if mode&vt10x.ModeAppCursor == 0 {
		t.Fatalf("snapshot should restore app-cursor mode, mode=%#x", mode)
	}
	if mode&vt10x.ModeWrap != 0 {
		t.Fatalf("snapshot should restore wrap mode disabled, mode=%#x", mode)
	}
	if mode&vt10x.ModeMouseSgr == 0 {
		t.Fatalf("snapshot should restore SGR mouse mode, mode=%#x", mode)
	}
}

func TestScreenMirrorSnapshotCanReplayRepeatedlyIntoFreshTerminals(t *testing.T) {
	mirror := NewScreenMirror(40, 8)
	mirror.Write([]byte("\033[2J\033[HGitHub Copilot"))
	mirror.Write([]byte("\033[8;1H❯ ready"))

	snapshot := mirror.Snapshot()
	for i := 0; i < 2; i++ {
		restored := vt10x.New(vt10x.WithSize(40, 8))
		restored.Write(snapshot)
		if screen := restored.String(); !strings.Contains(screen, "GitHub Copilot") || !strings.Contains(screen, "❯ ready") {
			t.Fatalf("snapshot replay %d should restore full screen, got:\n%s", i+1, screen)
		}
	}
}

func TestSnapshotReplayDrainsQueuedOutputIncludedInSnapshot(t *testing.T) {
	mirror := NewScreenMirror(40, 8)
	outputCh := make(chan []byte, 4)
	replayMu := &sync.Mutex{}

	replayMu.Lock()
	chunk := []byte("queued output")
	mirror.Write(chunk)
	outputCh <- chunk
	replayMu.Unlock()

	snapshot := snapshotReplay(outputCh, mirror, replayMu)
	if len(outputCh) != 0 {
		t.Fatalf("snapshotReplay should drain queued output, got %d queued chunks", len(outputCh))
	}

	restored := vt10x.New(vt10x.WithSize(40, 8))
	restored.Write(snapshot)
	if screen := restored.String(); !strings.Contains(screen, "queued output") {
		t.Fatalf("snapshot should include drained output, got:\n%s", screen)
	}
}
