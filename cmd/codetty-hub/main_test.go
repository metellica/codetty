package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHubFlags_SecureRequiresCertKeyCA(t *testing.T) {
	_, err := parseHubFlags([]string{})
	if err == nil {
		t.Fatal("expected error when cert/key/ca are missing in secure mode")
	}
	if !strings.Contains(err.Error(), "init-ca") {
		t.Fatalf("error should mention 'init-ca', got: %v", err)
	}
	if !strings.Contains(err.Error(), "--insecure") {
		t.Fatalf("error should mention '--insecure', got: %v", err)
	}
}

func TestParseHubFlags_InsecurePassesWithoutCerts(t *testing.T) {
	cfg, err := parseHubFlags([]string{"--insecure"})
	if err != nil {
		t.Fatalf("unexpected error for --insecure: %v", err)
	}
	if !cfg.Insecure {
		t.Fatal("expected Insecure=true")
	}
}

func TestParseHubFlags_SecurePassesWithAllCerts(t *testing.T) {
	cfg, err := parseHubFlags([]string{
		"--cert", "server.crt",
		"--key", "server.key",
		"--ca", "ca.crt",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CertFile != "server.crt" {
		t.Fatalf("expected CertFile=server.crt, got %q", cfg.CertFile)
	}
}

func TestRunInitCA(t *testing.T) {
	dir := t.TempDir()
	if err := runInitCA([]string{"--out", dir}); err != nil {
		t.Fatalf("init-ca failed: %v", err)
	}
	for _, f := range []string{"ca.crt", "ca.key", "server.crt", "server.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("expected %s to exist: %v", f, err)
		}
	}
}

func TestRunIssueCert(t *testing.T) {
	dir := t.TempDir()
	if err := runInitCA([]string{"--out", dir}); err != nil {
		t.Fatalf("init-ca: %v", err)
	}
	if err := runIssueCert([]string{"--name", "alice", "--ca-dir", dir, "--out", dir}); err != nil {
		t.Fatalf("issue-cert: %v", err)
	}
	for _, f := range []string{"alice.crt", "alice.key"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("expected %s to exist: %v", f, err)
		}
	}
}

func TestRunListCerts(t *testing.T) {
	dir := t.TempDir()
	if err := runInitCA([]string{"--out", dir}); err != nil {
		t.Fatalf("init-ca: %v", err)
	}
	if err := runIssueCert([]string{"--name", "bob", "--ca-dir", dir, "--out", dir}); err != nil {
		t.Fatalf("issue-cert: %v", err)
	}
	out, err := runListCerts([]string{"--ca-dir", dir})
	if err != nil {
		t.Fatalf("list-certs: %v", err)
	}
	if !strings.Contains(out, "bob") {
		t.Fatalf("expected 'bob' in list output, got:\n%s", out)
	}
}

func TestRunRevokeCert(t *testing.T) {
	dir := t.TempDir()
	if err := runInitCA([]string{"--out", dir}); err != nil {
		t.Fatalf("init-ca: %v", err)
	}
	if err := runIssueCert([]string{"--name", "eve", "--ca-dir", dir, "--out", dir}); err != nil {
		t.Fatalf("issue-cert: %v", err)
	}
	if err := runRevokeCert([]string{"--name", "eve", "--ca-dir", dir}); err != nil {
		t.Fatalf("revoke-cert: %v", err)
	}
	// crl.pem should now exist
	if _, err := os.Stat(filepath.Join(dir, "crl.pem")); err != nil {
		t.Fatalf("expected crl.pem to exist: %v", err)
	}
	// list-certs should show eve as revoked
	out, err := runListCerts([]string{"--ca-dir", dir})
	if err != nil {
		t.Fatalf("list-certs after revoke: %v", err)
	}
	if !strings.Contains(out, "REVOKED") {
		t.Fatalf("expected 'REVOKED' in list output after revoke, got:\n%s", out)
	}
}
