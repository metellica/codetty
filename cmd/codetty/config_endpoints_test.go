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
		{Name: "agent-a", Tabs: []config.ResolvedTab{{Remote: "gpu-a"}}},
		{Name: "agent-b", Tabs: []config.ResolvedTab{{Remote: "gpu-b"}}},
		{Name: "local", Tabs: []config.ResolvedTab{{}}},
	}

	endpoints, plans, err := buildConfigEndpoints(cfg, sessions, "/tmp/codetty.sock")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 {
		t.Fatalf("endpoints = %d, want local + one shared remote", len(endpoints))
	}
	if len(plans) != 3 {
		t.Fatalf("plans = %d, want 3", len(plans))
	}
	remoteA := plans[0].Containers[0].EndpointID
	remoteB := plans[1].Containers[0].EndpointID
	if remoteA != remoteB {
		t.Fatalf("remote endpoints differ: %q != %q", remoteA, remoteB)
	}
	if got := plans[2].Containers[0].EndpointID; got != "local" {
		t.Fatalf("local endpoint = %q, want local", got)
	}
}

func TestBuildConfigEndpointsSplitsMixedSessionByTarget(t *testing.T) {
	cfg := &config.SessionsConfig{
		Remote: map[string]config.RemoteDef{"gpu": {Host: "gpu01"}},
	}
	sessions := []config.ResolvedSession{{
		Name: "dev",
		Tabs: []config.ResolvedTab{
			{Name: "agent", Remote: "gpu"},
			{Name: "shell"},
			{Name: "logs", Remote: "gpu"},
		},
	}}

	endpoints, plans, err := buildConfigEndpoints(cfg, sessions, "/tmp/codetty.sock")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := endpoints["local"]; !ok {
		t.Fatal("local endpoint must always be present")
	}
	containers := plans[0].Containers
	if len(containers) != 2 {
		t.Fatalf("containers = %d, want local + remote", len(containers))
	}
	if containers[0].EndpointID != "local" || containers[0].Container != "dev" {
		t.Fatalf("containers[0] = %+v, want local container first", containers[0])
	}
	if len(containers[0].Tabs) != 1 || containers[0].Tabs[0].Name != "shell" {
		t.Fatalf("local tabs = %+v", containers[0].Tabs)
	}
	wantShadow := shadowSessionName("dev", localShadowID())
	if containers[1].Container != wantShadow {
		t.Fatalf("remote container = %q, want %q", containers[1].Container, wantShadow)
	}
	if len(containers[1].Tabs) != 2 ||
		containers[1].Tabs[0].Name != "agent" ||
		containers[1].Tabs[1].Name != "logs" {
		t.Fatalf("remote tabs = %+v", containers[1].Tabs)
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
