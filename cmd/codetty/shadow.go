package main

import "codetty/daemon"

// localShadowID returns this client's owner ID (hostname + uid + daemon
// instance). Every request and subscription declares it so the daemon can
// resolve session names inside this host's owner namespace: remote daemons
// keep our sessions isolated from their own and from other hosts', while a
// daemon on this same host normalizes the ID to its local namespace.
//
// main.go exports the instance name via CODETTY_INSTANCE before anything
// runs, so this matches the ID the daemon computes for itself.
func localShadowID() string {
	return daemon.LocalOwnerID()
}
