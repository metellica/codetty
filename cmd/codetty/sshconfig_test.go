package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSSHConfigFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "config.d")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "config"), []byte(`
# comment
Host gpu2
  HostName 10.0.0.2
  User root

Host jenkins build-*  ci?  bastion
  User admin

Host *
  ServerAliveInterval 30

Include config.d/*.conf
Include missing-dir/*.conf
`), 0o644)
	os.WriteFile(filepath.Join(sub, "extra.conf"), []byte(`
Host extra gpu2
`), 0o644)

	var hosts []string
	seen := make(map[string]bool)
	parseSSHConfigFile(filepath.Join(dir, "config"), dir, 0, seen, &hosts)

	want := []string{"gpu2", "jenkins", "bastion", "extra"}
	if len(hosts) != len(want) {
		t.Fatalf("hosts = %v, want %v", hosts, want)
	}
	for i, h := range want {
		if hosts[i] != h {
			t.Fatalf("hosts[%d] = %q, want %q (all: %v)", i, hosts[i], h, hosts)
		}
	}
}

func TestParseSSHConfigFile_IncludeCycle(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config"), []byte("Include config\nHost one\n"), 0o644)

	var hosts []string
	seen := make(map[string]bool)
	parseSSHConfigFile(filepath.Join(dir, "config"), dir, 0, seen, &hosts)
	if len(hosts) != 1 || hosts[0] != "one" {
		t.Fatalf("hosts = %v, want [one]", hosts)
	}
}
