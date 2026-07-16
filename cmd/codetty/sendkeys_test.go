package main

import "testing"

func TestTranslateKey(t *testing.T) {
	cases := map[string]string{
		"Enter":  "\r",
		"enter":  "\r",
		"Escape": "\x1b",
		"Tab":    "\t",
		"Space":  " ",
		"BSpace": "\x7f",
		"Up":     "\x1b[A",
		"PgDn":   "\x1b[6~",
		"F1":     "\x1bOP",
		"F12":    "\x1b[24~",
		"C-c":    "\x03",
		"C-C":    "\x03",
		"^c":     "\x03",
		"C-@":    "\x00",
		"C-[":    "\x1b",
		"M-x":    "\x1bx",
		"M-Up":   "\x1b\x1b[A",
		"hello":  "hello", // unknown → literal
		"C-":     "C-",    // malformed → literal
		"ls -l":  "ls -l", // plain text
	}
	for in, want := range cases {
		if got := translateKey(in); got != want {
			t.Errorf("translateKey(%q) = %q, want %q", in, got, want)
		}
	}
}
