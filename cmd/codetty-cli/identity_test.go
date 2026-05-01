package main

import (
	"flag"
	"path/filepath"
	"strings"
	"testing"
)

func TestImplicitClientIdentityIsUniquePerProcess(t *testing.T) {
	id1, title1 := buildClientIdentity("", false, "/root/logsim-mount/tmp", "host-a", 111, "claude")
	id2, title2 := buildClientIdentity("", false, "/root/logsim-mount/tmp", "host-a", 222, "claude")

	if id1 == id2 {
		t.Fatalf("implicit IDs should differ for same directory with different PIDs: %q", id1)
	}
	if id1 != "tmp-host-a-111" {
		t.Fatalf("id1 = %q, want %q", id1, "tmp-host-a-111")
	}
	if id2 != "tmp-host-a-222" {
		t.Fatalf("id2 = %q, want %q", id2, "tmp-host-a-222")
	}
	if title1 != "claude@tmp#111" {
		t.Fatalf("title1 = %q, want %q", title1, "claude@tmp#111")
	}
	if title2 != "claude@tmp#222" {
		t.Fatalf("title2 = %q, want %q", title2, "claude@tmp#222")
	}
}

func TestExplicitClientIdentityPreservesIDAndTitleShape(t *testing.T) {
	id, title := buildClientIdentity("shared", true, "/root/logsim-mount/tmp", "host-a", 111, "claude")

	if id != "shared" {
		t.Fatalf("explicit id = %q, want %q", id, "shared")
	}
	if title != "claude@shared" {
		t.Fatalf("explicit title = %q, want %q", title, "claude@shared")
	}
}

func TestFlagWasProvidedDetectsExplicitEmptyID(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("id", "", "client identifier")
	if err := fs.Parse([]string{"--id=", "--", "cat"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	if !flagWasProvided(fs, "id") {
		t.Fatal("empty --id= should count as an explicit id")
	}
}

func TestFlagWasProvidedIgnoresOmittedID(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.String("id", "", "client identifier")
	if err := fs.Parse([]string{"--", "cat"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	if flagWasProvided(fs, "id") {
		t.Fatal("omitted --id should not count as explicit")
	}
}

func TestImplicitClientIdentityFallsBackToUnknownHostname(t *testing.T) {
	id, title := buildClientIdentity("", false, filepath.Join("/", "tmp"), "", 333, "cat")

	if !strings.Contains(id, "tmp-unknown-333") {
		t.Fatalf("implicit id should include unknown hostname fallback and pid, got %q", id)
	}
	if title != "cat@tmp#333" {
		t.Fatalf("title = %q, want %q", title, "cat@tmp#333")
	}
}

// TestParseClientFlagsTracksExplicitEmptyID verifies that an explicit empty
// --id= flag sets IDProvided=true, while an omitted --id leaves it false.
func TestParseClientFlagsTracksExplicitEmptyID(t *testing.T) {
	cfg, _, err := parseClientFlags([]string{"--id=", "--insecure", "--", "bash"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.IDProvided {
		t.Fatal("explicit empty --id= should set IDProvided=true")
	}
	if cfg.ClientID != "" {
		t.Errorf("ClientID = %q, want empty string", cfg.ClientID)
	}

	cfg2, _, err := parseClientFlags([]string{"--insecure", "--", "bash"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg2.IDProvided {
		t.Fatal("omitted --id should leave IDProvided=false")
	}
}
