package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// SessionsConfig is the root of a session layout config file (.codetty.toml).
type SessionsConfig struct {
	Layout   string                     `toml:"layout"`   // "list", "fullscreen"
	Focus    string                     `toml:"focus"`    // session name to focus initially
	Template map[string]SessionTemplate `toml:"template"` // reusable session templates
	Session  []SessionDef               `toml:"session"`  // session definitions
}

// SessionTemplate defines a reusable set of tabs.
type SessionTemplate struct {
	Tab []TabDef `toml:"tab"`
}

// TabDef defines a tab within a session.
type TabDef struct {
	Name    string            `toml:"name"`
	Command []string          `toml:"command"`
	Dir     string            `toml:"dir"`
	Env     map[string]string `toml:"env"`
}

// SessionDef defines a single session in the config file.
type SessionDef struct {
	Name     string            `toml:"name"`
	Command  []string          `toml:"command"`
	Dir      string            `toml:"dir"`
	Env      map[string]string `toml:"env"`
	Hidden   bool              `toml:"hidden"`
	Tab      []TabDef          `toml:"tab"`
	Template string            `toml:"template"`
}

// ResolvedTab is a resolved tab definition.
type ResolvedTab struct {
	Name    string
	Command []string
	Dir     string
	Env     map[string]string
}

// ResolvedSession is a session definition with template applied.
type ResolvedSession struct {
	Name    string
	Command []string
	Dir     string
	Env     map[string]string
	Hidden  bool
	Tabs    []ResolvedTab
}

// LoadSessions loads a session config file.
// Search order: explicit path > .codetty.toml in searchDir > ~/.config/codetty/sessions.toml
// The returned path is always absolute, so callers can safely derive the
// config directory for relative-path resolution regardless of process cwd.
func LoadSessions(path string, searchDir string) (*SessionsConfig, string, error) {
	if path != "" {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		cfg, err := loadSessionsFile(path)
		return cfg, path, err
	}

	candidate := filepath.Join(searchDir, ".codetty.toml")
	if _, err := os.Stat(candidate); err == nil {
		cfg, err := loadSessionsFile(candidate)
		return cfg, candidate, err
	}

	home, err := os.UserHomeDir()
	if err == nil {
		candidate = filepath.Join(home, ".config", "codetty", "sessions.toml")
		if _, err := os.Stat(candidate); err == nil {
			cfg, err := loadSessionsFile(candidate)
			return cfg, candidate, err
		}
	}

	return nil, "", fmt.Errorf("no session config found (tried .codetty.toml and ~/.config/codetty/sessions.toml)")
}

func loadSessionsFile(path string) (*SessionsConfig, error) {
	var cfg SessionsConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// Validate checks the config for structural errors.
func (cfg *SessionsConfig) Validate() error {
	switch cfg.Layout {
	case "", "list", "fullscreen":
	case "focus-big", "grid":
		// Removed layouts, accepted for backwards compatibility with old
		// configs; the TUI falls back to list mode.
	default:
		return fmt.Errorf("invalid layout %q (valid: list, fullscreen)", cfg.Layout)
	}

	seen := make(map[string]bool)
	for _, sd := range cfg.Session {
		if sd.Name == "" {
			return fmt.Errorf("session with empty name")
		}
		if seen[sd.Name] {
			return fmt.Errorf("duplicate session name: %q", sd.Name)
		}
		seen[sd.Name] = true

		if sd.Template != "" {
			if _, ok := cfg.Template[sd.Template]; !ok {
				return fmt.Errorf("session %q: template %q not found", sd.Name, sd.Template)
			}
		}

		tabNames := make(map[string]bool)
		for _, tab := range sd.Tab {
			if tab.Name == "" {
				return fmt.Errorf("session %q: tab with empty name", sd.Name)
			}
			if tabNames[tab.Name] {
				return fmt.Errorf("session %q: duplicate tab name %q", sd.Name, tab.Name)
			}
			tabNames[tab.Name] = true
		}
	}

	for name, tmpl := range cfg.Template {
		tabNames := make(map[string]bool)
		for _, tab := range tmpl.Tab {
			if tab.Name == "" {
				return fmt.Errorf("template %q: tab with empty name", name)
			}
			if tabNames[tab.Name] {
				return fmt.Errorf("template %q: duplicate tab name %q", name, tab.Name)
			}
			tabNames[tab.Name] = true
		}
	}

	return nil
}

// Resolve resolves all sessions, applying templates and computing final tab lists.
// configDir is the directory containing the config file (used to resolve relative paths).
func (cfg *SessionsConfig) Resolve(configDir string) ([]ResolvedSession, error) {
	var results []ResolvedSession
	for _, sd := range cfg.Session {
		rs, err := cfg.resolveSession(sd, configDir)
		if err != nil {
			return nil, fmt.Errorf("session %q: %w", sd.Name, err)
		}
		results = append(results, rs)
	}
	return results, nil
}

func (cfg *SessionsConfig) resolveSession(sd SessionDef, configDir string) (ResolvedSession, error) {
	rs := ResolvedSession{
		Name:    sd.Name,
		Command: sd.Command,
		Dir:     resolveDir(sd.Dir, configDir),
		Env:     sd.Env,
		Hidden:  sd.Hidden,
	}
	if rs.Dir == "" {
		rs.Dir = configDir
	}

	var baseTabs []TabDef
	if sd.Template != "" {
		tmpl := cfg.Template[sd.Template]
		baseTabs = make([]TabDef, len(tmpl.Tab))
		copy(baseTabs, tmpl.Tab)
	}

	for _, it := range sd.Tab {
		found := false
		for i, bt := range baseTabs {
			if bt.Name == it.Name {
				if len(it.Command) > 0 {
					baseTabs[i].Command = it.Command
				}
				if it.Dir != "" {
					baseTabs[i].Dir = it.Dir
				}
				if it.Env != nil {
					baseTabs[i].Env = it.Env
				}
				found = true
				break
			}
		}
		if !found {
			baseTabs = append(baseTabs, it)
		}
	}

	for _, tab := range baseTabs {
		rt := ResolvedTab{
			Name:    tab.Name,
			Command: tab.Command,
			Env:     tab.Env,
		}
		if tab.Dir != "" {
			rt.Dir = resolveDir(tab.Dir, configDir)
		} else {
			rt.Dir = rs.Dir
		}
		if len(rt.Command) == 0 {
			rt.Command = rs.Command
		}
		if rt.Env == nil && rs.Env != nil {
			rt.Env = rs.Env
		}
		rs.Tabs = append(rs.Tabs, rt)
	}

	return rs, nil
}

func resolveDir(dir, configDir string) string {
	if dir == "" {
		return ""
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(configDir, dir)
}
