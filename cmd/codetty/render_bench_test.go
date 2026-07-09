package main

import (
	"fmt"
	"io"
	"testing"

	"codetty/vt10x"
)

func mkColoredTerm(cols, rows int) vt10x.Terminal {
	term := vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(io.Discard))
	for i := 0; i < rows*3; i++ {
		fmt.Fprintf(term, "\x1b[3%dm line %d some colored text abcdefghijklmnop \x1b[1mqrstuvwxyz\x1b[0m more tail text here\r\n", i%8, i)
	}
	return term
}

func BenchmarkRenderANSI_Full(b *testing.B) {
	term := mkColoredTerm(200, 50)
	term.Lock()
	defer term.Unlock()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		renderTerminalANSI(term, 200, 50)
	}
}

// Simulates scroll: one new output line per frame, cached render path.
func BenchmarkRenderANSI_CachedScroll(b *testing.B) {
	s := newTUISession("bench", "sh", 200, 50)
	s.WriteOutput([]byte("\x1b[2J\x1b[H"))
	for i := 0; i < 60; i++ {
		s.WriteOutput([]byte(fmt.Sprintf("\x1b[3%dm line %d colored text\x1b[0m tail\r\n", i%8, i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.WriteOutput([]byte(fmt.Sprintf("\x1b[3%dm scroll line %d\x1b[0m more\r\n", i%8, i)))
		s.RenderANSI(200, 50)
	}
}

// Simulates typing: one char per frame, cached render path.
func BenchmarkRenderANSI_CachedTyping(b *testing.B) {
	s := newTUISession("bench", "sh", 200, 50)
	for i := 0; i < 49; i++ {
		s.WriteOutput([]byte(fmt.Sprintf("\x1b[3%dm line %d colored text\x1b[0m tail\r\n", i%8, i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.WriteOutput([]byte{byte('a' + i%26)})
		s.RenderANSI(200, 50)
	}
}
