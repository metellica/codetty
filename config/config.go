// Package config loads codetty's user configuration file.
//
// The config file lives at ~/.config/codetty/config.toml. It is entirely
// optional: when absent (or when a value is left unset), codetty falls back
// to sensible defaults.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Default prefix key codetty uses when no config file sets one explicitly.
const DefaultPrefixCtrlA = "ctrl+a"

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

func detectDefaultPrefix() string {
	return DefaultPrefixCtrlA
}
