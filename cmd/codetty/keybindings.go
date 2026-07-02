package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// parsePrefixKey converts a config prefix string (e.g. "ctrl+a", "C-a") into
// a bubbletea KeyType plus a human-readable label (e.g. "Ctrl+A"). Falls
// back to Ctrl+A for empty or unrecognized values.
func parsePrefixKey(s string) (tea.KeyType, string) {
	letter := normalizePrefixLetter(s)
	if letter == 0 {
		return tea.KeyCtrlA, "Ctrl+A"
	}
	// tea.KeyCtrlA..tea.KeyCtrlZ are contiguous KeyType values in
	// alphabetical order (see tuiKeyToBytes' default case, which relies on
	// the same assumption).
	kt := tea.KeyType(int(tea.KeyCtrlA) + int(letter-'a'))
	return kt, "Ctrl+" + strings.ToUpper(string(letter))
}

// normalizePrefixLetter extracts the single letter from a "ctrl+<letter>" or
// "C-<letter>" style key string. Returns 0 if s isn't a recognized
// ctrl+letter combination.
func normalizePrefixLetter(s string) byte {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.HasPrefix(s, "ctrl+"):
		s = s[len("ctrl+"):]
	case strings.HasPrefix(s, "c-"):
		s = s[len("c-"):]
	default:
		return 0
	}
	if len(s) == 1 && s[0] >= 'a' && s[0] <= 'z' {
		return s[0]
	}
	return 0
}
