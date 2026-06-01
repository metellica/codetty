package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRunWithAutowrapDisabled_Ordering(t *testing.T) {
	var buf bytes.Buffer
	ran := false
	err := runWithAutowrapDisabled(&buf, func() error {
		// Autowrap must already be disabled before fn runs.
		if got := buf.String(); got != ansi.ResetAutoWrapMode {
			t.Fatalf("expected autowrap disabled before fn, buffer = %q", got)
		}
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ran {
		t.Fatal("fn was not run")
	}
	want := ansi.ResetAutoWrapMode + ansi.SetAutoWrapMode
	if got := buf.String(); got != want {
		t.Fatalf("sequence mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestRunWithAutowrapDisabled_RestoresOnError(t *testing.T) {
	var buf bytes.Buffer
	sentinel := errors.New("boom")
	err := runWithAutowrapDisabled(&buf, func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	want := ansi.ResetAutoWrapMode + ansi.SetAutoWrapMode
	if got := buf.String(); got != want {
		t.Fatalf("autowrap not restored on error:\n got %q\nwant %q", got, want)
	}
}

func TestRunWithAutowrapDisabled_RestoresOnPanic(t *testing.T) {
	var buf bytes.Buffer
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic to propagate")
			}
		}()
		_ = runWithAutowrapDisabled(&buf, func() error {
			panic("boom")
		})
	}()
	want := ansi.ResetAutoWrapMode + ansi.SetAutoWrapMode
	if got := buf.String(); got != want {
		t.Fatalf("autowrap not restored on panic:\n got %q\nwant %q", got, want)
	}
}
