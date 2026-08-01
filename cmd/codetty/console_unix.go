//go:build !windows

package main

// platformInit is a no-op on Unix.
func platformInit() func() { return func() {} }
