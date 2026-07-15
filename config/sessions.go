package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// SessionsConfig is the root of a session layout config file (.codetty.toml).
type SessionsConfig struct {
	Layout   string                     `toml:"layout"`   // "list", "focus-big", "fullscreen", "tabs"
	Focus    string                     `toml:"focus"`    // session name to focus initially
	Remote   map[string]RemoteDef       `toml:"remote"`   // managed remote CodeTTY targets
	Template map[string]SessionTemplate `toml:"template"` // reusable session templates
	Session  []SessionDef               `toml:"session"`  // session definitions
}

// RemoteDef defines a managed remote CodeTTY target reached through OpenSSH.
type RemoteDef struct {
	Host     string `toml:"host"`
	Instance string `toml:"instance"`
	Deploy   string `toml:"deploy"` // "", "auto", or "never"
	Binary   string `toml:"binary"` // remote binary path when deploy = "never"
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
	Remote  string            `toml:"remote"`
	Local   bool              `toml:"local"`
}

// SessionDef defines a single session in the config file.
type SessionDef struct {
	Name     string            `toml:"name"`
	Command  []string          `toml:"command"`
	Dir      string            `toml:"dir"`
	Env      map[string]string `toml:"env"`
	Hidden   bool              `toml:"hidden"`
	Remote   string            `toml:"remote"`
	Tab      []TabDef          `toml:"tab"`
	Template string            `toml:"template"`
}

// ResolvedTab is a resolved tab definition.
type ResolvedTab struct {
	Name    string
	Command []string
	Dir     string
	Env     map[string]string
	Remote  string
}

// ResolvedSession is a session definition with template applied.
type ResolvedSession struct {
	Name    string
	Command []string
	Dir     string
	Env     map[string]string
	Hidden  bool
	Remote  string
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
	case "", "list", "focus-big", "fullscreen", "tabs":
	case "grid":
		// Removed layout, accepted for backwards compatibility with old
		// configs; the TUI falls back to list mode.
	default:
		return fmt.Errorf("invalid layout %q (valid: list, focus-big, fullscreen, tabs)", cfg.Layout)
	}

	for name, remote := range cfg.Remote {
		if name == "" {
			return fmt.Errorf("remote with empty name")
		}
		if remote.Host == "" {
			return fmt.Errorf("remote %q: host is required", name)
		}
		switch remote.Deploy {
		case "", "auto", "never":
		default:
			return fmt.Errorf("remote %q: invalid deploy %q (valid: auto, never)", name, remote.Deploy)
		}
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
		if err := cfg.validateRemoteRef(fmt.Sprintf("session %q", sd.Name), sd.Remote); err != nil {
			return err
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
			if tab.Local && tab.Remote != "" {
				return fmt.Errorf("session %q tab %q: local and remote are mutually exclusive", sd.Name, tab.Name)
			}
			if err := cfg.validateRemoteRef(
				fmt.Sprintf("session %q tab %q", sd.Name, tab.Name),
				tab.Remote,
			); err != nil {
				return err
			}
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
			if tab.Local && tab.Remote != "" {
				return fmt.Errorf("template %q tab %q: local and remote are mutually exclusive", name, tab.Name)
			}
			if err := cfg.validateRemoteRef(
				fmt.Sprintf("template %q tab %q", name, tab.Name),
				tab.Remote,
			); err != nil {
				return err
			}
		}
	}

	return nil
}

func (cfg *SessionsConfig) validateRemoteRef(owner, name string) error {
	if name == "" {
		return nil
	}
	if _, ok := cfg.Remote[name]; !ok {
		return fmt.Errorf("%s: remote %q not found", owner, name)
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
		Env:     sd.Env,
		Hidden:  sd.Hidden,
		Remote:  sd.Remote,
	}
	if rs.Remote == "" {
		rs.Dir = resolveDir(sd.Dir, configDir)
	} else {
		rs.Dir = sd.Dir
	}
	if rs.Dir == "" && rs.Remote == "" {
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
				if it.Remote != "" {
					baseTabs[i].Remote = it.Remote
				}
				if it.Local {
					baseTabs[i].Local = true
					baseTabs[i].Remote = ""
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
			Remote:  tab.Remote,
		}
		if rt.Remote == "" && !tab.Local {
			rt.Remote = rs.Remote
		}
		if tab.Dir != "" {
			if rt.Remote == "" {
				rt.Dir = resolveDir(tab.Dir, configDir)
			} else {
				rt.Dir = tab.Dir
			}
		} else if rt.Remote == rs.Remote {
			rt.Dir = rs.Dir
		} else if rt.Remote == "" {
			rt.Dir = configDir
		}
		if len(rt.Command) == 0 {
			rt.Command = rs.Command
		}
		if rt.Env == nil && rs.Env != nil {
			rt.Env = rs.Env
		}
		rs.Tabs = append(rs.Tabs, rt)
	}

	if len(rs.Tabs) > 0 {
		target := rs.Tabs[0].Remote
		for _, tab := range rs.Tabs[1:] {
			if tab.Remote != target {
				return ResolvedSession{}, fmt.Errorf(
					"tabs %q and %q use different execution targets; split local and remote tabs into separate sessions",
					rs.Tabs[0].Name,
					tab.Name,
				)
			}
		}
		rs.Remote = target
		rs.Command = rs.Tabs[0].Command
		rs.Dir = rs.Tabs[0].Dir
		rs.Env = rs.Tabs[0].Env
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
