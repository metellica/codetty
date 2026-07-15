package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codetty/config"
)

func TestBuildConfigEndpointsSharesRemoteHostAndInstance(t *testing.T) {
	cfg := &config.SessionsConfig{
		Remote: map[string]config.RemoteDef{
			"gpu-a": {Host: "gpu01", Instance: "work"},
			"gpu-b": {Host: "gpu01", Instance: "work"},
		},
	}
	sessions := []config.ResolvedSession{
		{Name: "agent-a", Remote: "gpu-a"},
		{Name: "agent-b", Remote: "gpu-b"},
		{Name: "local"},
	}

	endpoints, routes, err := buildConfigEndpoints(cfg, sessions, "/tmp/codetty.sock")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("endpoints = %d, want local + one shared remote", len(endpoints))
	}
	if routes["agent-a"] != routes["agent-b"] {
		t.Fatalf("remote routes differ: %q != %q", routes["agent-a"], routes["agent-b"])
	}
	if routes["local"] != "local" {
		t.Fatalf("local route = %q, want local", routes["local"])
	}
}

func TestNewSSHDaemonEndpointDefaults(t *testing.T) {
	endpoint, err := newSSHDaemonEndpoint("gpu", config.RemoteDef{Host: "user@gpu01"})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.instance != "default" || endpoint.deploy != "auto" {
		t.Fatalf("endpoint defaults = instance %q deploy %q", endpoint.instance, endpoint.deploy)
	}
	command := endpoint.bridgeCommand()
	for _, want := range []string{".local/lib/codetty/codetty", "-i 'default' __bridge"} {
		if !strings.Contains(command, want) {
			t.Fatalf("bridge command %q does not contain %q", command, want)
		}
	}
}

func TestNewSSHDaemonEndpointRejectsOptionLikeHost(t *testing.T) {
	if _, err := newSSHDaemonEndpoint("bad", config.RemoteDef{Host: "-oProxyCommand=bad"}); err == nil {
		t.Fatal("expected invalid host error")
	}
}

func TestShellQuote(t *testing.T) {
	if got, want := shellQuote("a'b"), `'a'"'"'b'`; got != want {
		t.Fatalf("shellQuote = %q, want %q", got, want)
	}
}

func TestCheckMachineIgnoresSSHStderr(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "ssh")
	remoteOS := runtime.GOOS
	if remoteOS == "darwin" {
		remoteOS = "Darwin"
	} else {
		remoteOS = "Linux"
	}
	remoteArch := runtime.GOARCH
	if remoteArch == "amd64" {
		remoteArch = "x86_64"
	} else if remoteArch == "arm64" {
		remoteArch = "aarch64"
	}
	content := fmt.Sprintf("#!/bin/sh\necho 'Warning: test notice' >&2\nprintf '%%s\\n%%s\\n' %s %s\n", remoteOS, remoteArch)
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	endpoint, err := newSSHDaemonEndpoint("gpu", config.RemoteDef{Host: "gpu01"})
	if err != nil {
		t.Fatal(err)
	}
	if err := endpoint.checkMachine(); err != nil {
		t.Fatalf("checkMachine: %v", err)
	}
}
