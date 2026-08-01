//go:build !windows

package main

import tea "github.com/charmbracelet/bubbletea"

// vtStdinReader is Windows-only; on Unix the default Bubble Tea input path
// already reads raw VT bytes.
type vtStdinReader struct{}

func newVTStdinReader() *vtStdinReader                 { return nil }
func (r *vtStdinReader) Read(p []byte) (int, error)    { return 0, nil }
func (r *vtStdinReader) forwardResizes(p *tea.Program) {}
func (r *vtStdinReader) restore()                      {}
