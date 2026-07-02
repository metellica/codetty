// Package config loads codetty's user configuration file.
//
// The config file lives at ~/.config/codetty/config.toml. It is entirely
// optional: when absent (or when a value is left unset), codetty falls back
// to sensible defaults.
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// tmuxProbeTimeout bounds how long we wait on `tmux show-options`, so a
// wedged tmux client/server can't hang codetty startup or --help.
const tmuxProbeTimeout = 2 * time.Second

// Default prefix keys codetty can pick between when no config file sets one
// explicitly.
const (
	DefaultPrefixCtrlA = "ctrl+a"
	DefaultPrefixCtrlB = "ctrl+b"
)

// Config is the root of codetty's config.toml structure.
type Config struct {
	Keybindings KeybindingsConfig `toml:"keybindings"`
}

// KeybindingsConfig holds keybinding-related settings.
type KeybindingsConfig struct {
	// Prefix is the prefix key used to enter command mode, e.g. "ctrl+a".
	// Left empty, it is resolved by detectDefaultPrefix.
	Prefix string `toml:"prefix"`
}

// Path returns the path to codetty's user config file:
// ~/.config/codetty/config.toml
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", "codetty", "config.toml"), nil
}

// Load reads codetty's config file if present, filling in any unset values
// with defaults. It is not an error for the config file to not exist: a
// zero-value Config with defaults applied is returned instead. An error is
// only returned when the file exists but cannot be parsed, or when the
// user's home directory cannot be resolved to check for the file.
func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return withDefaults(&Config{}), nil
	}

	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return withDefaults(&cfg), nil
		}
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return withDefaults(&cfg), nil
}

// withDefaults fills in unset config fields with computed defaults.
func withDefaults(cfg *Config) *Config {
	if cfg.Keybindings.Prefix == "" {
		cfg.Keybindings.Prefix = detectDefaultPrefix()
	}
	return cfg
}

// detectDefaultPrefix determines codetty's default prefix key when no
// config file (or no explicit prefix) is set. codetty's own default is
// Ctrl+A. But if tmux is installed and its configured prefix is also
// Ctrl+A, codetty falls back to Ctrl+B instead, to avoid a keybinding
// conflict when codetty is run nested inside (or alongside) tmux.
func detectDefaultPrefix() string {
	if tmuxPrefixIsCtrlA() {
		return DefaultPrefixCtrlB
	}
	return DefaultPrefixCtrlA
}

// tmuxPrefixIsCtrlA reports whether tmux is installed and its globally
// configured prefix key is Ctrl+A. Returns false if tmux is not installed
// or the prefix cannot be determined (e.g. no server running).
func tmuxPrefixIsCtrlA() bool {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "show-options", "-g", "prefix").Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return false
	}
	return normalizeTmuxKey(fields[1]) == DefaultPrefixCtrlA
}

// normalizeTmuxKey converts a tmux key name (e.g. "C-a") into codetty's
// "ctrl+<letter>" notation.
func normalizeTmuxKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(key, "c-") {
		return "ctrl+" + key[2:]
	}
	return key
}
