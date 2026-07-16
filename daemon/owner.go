package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strconv"
)

// Owner namespaces: every session belongs to an owner, identified by a
// short stable hash of the client host (hostname + uid + daemon instance).
// Clients declare their owner ID in the protocol hello; the daemon resolves
// all session names inside the requester's namespace and only delivers
// events and output for sessions the subscriber's scope covers. The owner
// ID of the daemon's own host normalizes to "" (the local namespace), so
// sessions created by local clients and sessions predating owner tracking
// share one namespace.

// LocalOwnerID returns the owner ID of this process's host: a short stable
// hash of hostname + uid + daemon instance (CODETTY_INSTANCE). The same
// value is computed by the TUI, the CLI and the daemon of one instance.
func LocalOwnerID() string {
	host, _ := os.Hostname()
	seed := host + "\x00" + strconv.Itoa(os.Getuid()) + "\x00" + os.Getenv("CODETTY_INSTANCE")
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:4])
}

// ownerKey builds the session map key for a session name within an owner
// namespace.
func ownerKey(owner, name string) string {
	return owner + "\x00" + name
}

// normalizeOwner maps the daemon host's own owner ID to "" (the local
// namespace). Caller-facing: every owner value entering the server goes
// through this, so hostOwner never appears as a map key.
func (s *Server) normalizeOwner(owner string) string {
	if owner == s.hostOwner {
		return ""
	}
	return owner
}
