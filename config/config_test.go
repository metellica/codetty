package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeTmuxKey(t *testing.T) {
	cases := map[string]string{
		"C-a":   "ctrl+a",
		"c-b":   "ctrl+b",
		" C-A ": "ctrl+a",
		"c-Z":   "ctrl+z",
	}
	for in, want := range cases {
		if got := normalizeTmuxKey(in); got != want {
			t.Errorf("normalizeTmuxKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoad_NoFile_UsesDefault(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Keybindings.Prefix != DefaultPrefixCtrlA && cfg.Keybindings.Prefix != DefaultPrefixCtrlB {
		t.Fatalf("Keybindings.Prefix = %q, want ctrl+a or ctrl+b", cfg.Keybindings.Prefix)
	}
}

func TestLoad_FileSetsPrefix(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgDir := filepath.Join(dir, ".config", "codetty")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[keybindings]\nprefix = \"ctrl+b\"\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Keybindings.Prefix != "ctrl+b" {
		t.Fatalf("Keybindings.Prefix = %q, want ctrl+b", cfg.Keybindings.Prefix)
	}
}

func TestLoad_InvalidFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgDir := filepath.Join(dir, ".config", "codetty")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("not = valid = toml ["), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error for invalid TOML")
	}
}

func TestPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	path, err := Path()
	if err != nil {
		t.Fatalf("Path() error: %v", err)
	}
	want := filepath.Join(dir, ".config", "codetty", "config.toml")
	if path != want {
		t.Fatalf("Path() = %q, want %q", path, want)
	}
}
