package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// SessionsConfig is the root of a session layout config file (.codetty.toml).
type SessionsConfig struct {
	Layout   string                     `toml:"layout"`   // "focus-big", "grid", "fullscreen"
	Focus    string                     `toml:"focus"`    // session name to focus initially
	Template map[string]SessionTemplate `toml:"template"` // reusable pane templates
	Session  []SessionDef               `toml:"session"`  // session definitions
}

// SessionTemplate defines a reusable set of windows for tmux sessions.
type SessionTemplate struct {
	Window []WindowDef `toml:"window"`
}

// TabDef defines a tab within a native session.
type TabDef struct {
	Name    string            `toml:"name"`
	Command []string          `toml:"command"` // command to run in this tab
	Dir     string            `toml:"dir"`     // working directory override
	Env     map[string]string `toml:"env"`     // extra environment variables
}

// SessionDef defines a single session in the config file.
type SessionDef struct {
	Name           string            `toml:"name"`
	Type           string            `toml:"type"`             // "native" or "tmux" (default: "native")
	Command        []string          `toml:"command"`          // native session command
	Dir            string            `toml:"dir"`              // working directory
	Env            map[string]string `toml:"env"`              // extra environment variables
	Hidden         bool              `toml:"hidden"`           // hidden on startup
	Tab            []TabDef          `toml:"tab"`              // tabs within a native session
	Template       string            `toml:"template"`         // template name (tmux only)
	TmuxSession    string            `toml:"tmux_session"`     // tmux session name
	TmuxAttachOnly bool              `toml:"tmux_attach_only"` // only attach, don't create windows
	RemoveWindows  []string          `toml:"remove_windows"`   // window names to remove from template
	Window         []WindowDef       `toml:"window"`           // inline windows or overrides
}

// WindowDef defines a single window within a tmux session.
type WindowDef struct {
	Name    string `toml:"name"`
	Command string `toml:"command"` // shell command string
	Dir     string `toml:"dir"`     // override working directory
}

// ResolvedTab is a resolved tab definition for a native session.
type ResolvedTab struct {
	Name    string
	Command []string
	Dir     string
	Env     map[string]string
}

// ResolvedSession is a session definition with template applied.
type ResolvedSession struct {
	Name           string
	Type           string // "native" or "tmux"
	Command        []string
	Dir            string
	Env            map[string]string
	Hidden         bool
	Tabs           []ResolvedTab // tabs within a native session (empty means single-tab)
	TmuxSession    string
	TmuxAttachOnly bool
	Windows        []WindowDef
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
	if cfg.Layout != "" && cfg.Layout != "focus-big" && cfg.Layout != "grid" && cfg.Layout != "fullscreen" {
		return fmt.Errorf("invalid layout %q (valid: focus-big, grid, fullscreen)", cfg.Layout)
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

		if sd.Type != "" && sd.Type != "native" && sd.Type != "tmux" {
			return fmt.Errorf("session %q: invalid type %q (valid: native, tmux)", sd.Name, sd.Type)
		}
		if sd.Type == "tmux" && sd.TmuxSession == "" {
			return fmt.Errorf("session %q: tmux type requires tmux_session", sd.Name)
		}
		if sd.Template != "" {
			if _, ok := cfg.Template[sd.Template]; !ok {
				return fmt.Errorf("session %q: template %q not found", sd.Name, sd.Template)
			}
		}

		// Validate tabs (native sessions only).
		if len(sd.Tab) > 0 {
			if sd.Type == "tmux" {
				return fmt.Errorf("session %q: tabs are only supported for native sessions", sd.Name)
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
	}

	for name, tmpl := range cfg.Template {
		winNames := make(map[string]bool)
		for _, w := range tmpl.Window {
			if w.Name == "" {
				return fmt.Errorf("template %q: window with empty name", name)
			}
			if winNames[w.Name] {
				return fmt.Errorf("template %q: duplicate window name %q", name, w.Name)
			}
			winNames[w.Name] = true
		}
	}

	return nil
}

// Resolve resolves all sessions, applying templates and computing final window lists.
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
		Name:           sd.Name,
		Type:           sd.Type,
		Command:        sd.Command,
		Dir:            resolveDir(sd.Dir, configDir),
		Env:            sd.Env,
		Hidden:         sd.Hidden,
		TmuxSession:    sd.TmuxSession,
		TmuxAttachOnly: sd.TmuxAttachOnly,
	}
	// An omitted dir defaults to the config file's directory, so a
	// project-local .codetty.toml opens sessions in the project.
	if rs.Dir == "" {
		rs.Dir = configDir
	}
	if rs.Type == "" {
		rs.Type = "native"
	}

	if rs.Type == "native" {
		// Resolve tabs for native sessions.
		for _, tab := range sd.Tab {
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
				rt.Command = rs.Command // inherit session command
			}
			if rt.Env == nil && rs.Env != nil {
				rt.Env = rs.Env // inherit session env
			}
			rs.Tabs = append(rs.Tabs, rt)
		}
		return rs, nil
	}

	// Resolve tmux windows from template + inline overrides.
	var baseWindows []WindowDef

	if sd.Template != "" {
		tmpl := cfg.Template[sd.Template]
		baseWindows = make([]WindowDef, len(tmpl.Window))
		copy(baseWindows, tmpl.Window)
	}

	// Remove windows listed in remove_windows.
	if len(sd.RemoveWindows) > 0 {
		removeSet := make(map[string]bool)
		for _, name := range sd.RemoveWindows {
			removeSet[name] = true
		}
		filtered := make([]WindowDef, 0, len(baseWindows))
		for _, w := range baseWindows {
			if !removeSet[w.Name] {
				filtered = append(filtered, w)
			}
		}
		baseWindows = filtered
	}

	// Apply inline windows: same name → override fields; new name → append.
	for _, iw := range sd.Window {
		found := false
		for i, bw := range baseWindows {
			if bw.Name == iw.Name {
				if iw.Command != "" {
					baseWindows[i].Command = iw.Command
				}
				if iw.Dir != "" {
					baseWindows[i].Dir = iw.Dir
				}
				found = true
				break
			}
		}
		if !found {
			baseWindows = append(baseWindows, iw)
		}
	}

	// Resolve window dirs: explicit dir relative to config dir, else inherit session dir.
	for i := range baseWindows {
		if baseWindows[i].Dir != "" {
			baseWindows[i].Dir = resolveDir(baseWindows[i].Dir, configDir)
		} else if rs.Dir != "" {
			baseWindows[i].Dir = rs.Dir
		}
	}

	rs.Windows = baseWindows

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
