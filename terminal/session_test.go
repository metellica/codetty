package terminal

import (
	"strings"
	"testing"
)

func termValue(env []string) string {
	v := ""
	for _, kv := range env {
		if s, ok := strings.CutPrefix(kv, "TERM="); ok {
			v = s
		}
	}
	return v
}

func TestPtyEnviron_FixesDumbTerm(t *testing.T) {
	t.Setenv("TERM", "dumb")
	if got := termValue(ptyEnviron(nil)); got != "xterm-256color" {
		t.Fatalf("TERM = %q, want xterm-256color", got)
	}
}

func TestPtyEnviron_KeepsRealTerm(t *testing.T) {
	t.Setenv("TERM", "screen-256color")
	if got := termValue(ptyEnviron(nil)); got != "screen-256color" {
		t.Fatalf("TERM = %q, want screen-256color", got)
	}
}

func TestPtyEnviron_ExtraOverrides(t *testing.T) {
	t.Setenv("TERM", "dumb")
	env := ptyEnviron([]string{"TERM=vt100", "FOO=bar"})
	if got := termValue(env); got != "vt100" {
		t.Fatalf("TERM = %q, want caller override vt100", got)
	}
}
