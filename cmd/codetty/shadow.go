package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
)

// Shadow sessions are the remote half of a composite session. A session is
// always a local concept; when some of its tabs run on a remote daemon they
// live inside a hidden container session there, named
// "<display>@@<localID>". The TUI merges those parts back into the single
// display session, so the shadow name never appears in the UI.
const shadowSep = "@@"

// shadowSessionName returns the remote container session name for a display
// session owned by this TUI host.
func shadowSessionName(display, localID string) string {
	return display + shadowSep + localID
}

// parseShadowSessionName splits a remote session name into the display name
// and owner ID. Returns ok=false for names that are not shadow sessions.
func parseShadowSessionName(name string) (display, localID string, ok bool) {
	i := strings.LastIndex(name, shadowSep)
	if i <= 0 || i+len(shadowSep) >= len(name) {
		return "", "", false
	}
	return name[:i], name[i+len(shadowSep):], true
}

// localShadowID returns a short stable identifier for this TUI owner
// (hostname + uid + daemon instance). It marks shadow sessions on remote
// daemons so they survive TUI restarts and never collide with shadows
// created by other hosts or users.
func localShadowID() string {
	host, _ := os.Hostname()
	seed := host + "\x00" + strconv.Itoa(os.Getuid()) + "\x00" + instanceName
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:4])
}
