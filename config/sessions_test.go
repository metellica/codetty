package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSessions_BasicParsing(t *testing.T) {
	dir := t.TempDir()
	content := `
layout = "grid"
focus = "editor"

[template.dev]

  [[template.dev.window]]
  name = "server"
  command = "npm run dev"

  [[template.dev.window]]
  name = "shell"

[[session]]
name = "editor"
command = ["nvim", "."]

[[session]]
name = "dev-env"
type = "tmux"
tmux_session = "dev"
template = "dev"
dir = "./src"
`
	cfgPath := filepath.Join(dir, ".codetty.toml")
	os.WriteFile(cfgPath, []byte(content), 0o644)

	cfg, path, err := LoadSessions("", dir)
	if err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	if path != cfgPath {
		t.Fatalf("path = %q, want %q", path, cfgPath)
	}
	if cfg.Layout != "grid" {
		t.Fatalf("layout = %q, want grid", cfg.Layout)
	}
	if cfg.Focus != "editor" {
		t.Fatalf("focus = %q, want editor", cfg.Focus)
	}
	if len(cfg.Session) != 2 {
		t.Fatalf("got %d sessions, want 2", len(cfg.Session))
	}
	if len(cfg.Template) != 1 {
		t.Fatalf("got %d templates, want 1", len(cfg.Template))
	}
	tmpl := cfg.Template["dev"]
	if len(tmpl.Window) != 2 {
		t.Fatalf("template windows = %d, want 2", len(tmpl.Window))
	}
}

func TestResolve_NativeSession(t *testing.T) {
	cfg := &SessionsConfig{
		Session: []SessionDef{
			{Name: "shell", Command: []string{"bash"}},
		},
	}
	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d, want 1", len(results))
	}
	if results[0].Type != "native" {
		t.Fatalf("type = %q, want native", results[0].Type)
	}
	if len(results[0].Windows) != 0 {
		t.Fatalf("native session should have 0 windows, got %d", len(results[0].Windows))
	}
}

func TestResolve_TemplateWithOverrides(t *testing.T) {
	cfg := &SessionsConfig{
		Template: map[string]SessionTemplate{
			"base": {
				Window: []WindowDef{
					{Name: "server", Command: "npm run dev"},
					{Name: "watch", Command: "npm run watch"},
					{Name: "shell"},
				},
			},
		},
		Session: []SessionDef{
			{
				Name:          "dev",
				Type:          "tmux",
				TmuxSession:   "dev",
				Template:      "base",
				RemoveWindows: []string{"watch"},
				Window: []WindowDef{
					{Name: "server", Command: "npm start"},
					{Name: "db", Command: "docker compose up"},
				},
			},
		},
	}

	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}

	rs := results[0]
	if len(rs.Windows) != 3 {
		t.Fatalf("got %d windows, want 3 (server, shell, db); windows=%v", len(rs.Windows), rs.Windows)
	}

	// "server" should be overridden
	if rs.Windows[0].Name != "server" || rs.Windows[0].Command != "npm start" {
		t.Fatalf("window[0] = %+v, want server / npm start", rs.Windows[0])
	}
	// "watch" should be removed; "shell" remains
	if rs.Windows[1].Name != "shell" {
		t.Fatalf("window[1] = %+v, want shell", rs.Windows[1])
	}
	// "db" should be appended
	if rs.Windows[2].Name != "db" || rs.Windows[2].Command != "docker compose up" {
		t.Fatalf("window[2] = %+v, want db / docker compose up", rs.Windows[2])
	}
}

func TestResolve_TemplateFallbackLayout(t *testing.T) {
	cfg := &SessionsConfig{
		Template: map[string]SessionTemplate{
			"base": {Window: []WindowDef{{Name: "a"}}},
		},
		Session: []SessionDef{
			{Name: "s", Type: "tmux", TmuxSession: "s", Template: "base"},
		},
	}
	results, err := cfg.Resolve("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].Windows) != 1 {
		t.Fatalf("got %d windows, want 1", len(results[0].Windows))
	}
}

func TestResolve_DirResolution(t *testing.T) {
	cfg := &SessionsConfig{
		Session: []SessionDef{
			{
				Name:        "dev",
				Type:        "tmux",
				TmuxSession: "dev",
				Dir:         "./frontend",
				Window: []WindowDef{
					{Name: "server"},                    // inherits session dir
					{Name: "backend", Dir: "./backend"}, // relative to config dir
					{Name: "abs", Dir: "/tmp/logs"},      // absolute
				},
			},
		},
	}

	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}

	windows := results[0].Windows
	if windows[0].Dir != "/project/frontend" {
		t.Fatalf("window[0].Dir = %q, want /project/frontend", windows[0].Dir)
	}
	if windows[1].Dir != "/project/backend" {
		t.Fatalf("window[1].Dir = %q, want /project/backend", windows[1].Dir)
	}
	if windows[2].Dir != "/tmp/logs" {
		t.Fatalf("window[2].Dir = %q, want /tmp/logs", windows[2].Dir)
	}
}

func TestResolve_NativeSessionDir(t *testing.T) {
	cfg := &SessionsConfig{
		Session: []SessionDef{
			{Name: "s", Dir: "./subdir"},
		},
	}
	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Dir != "/project/subdir" {
		t.Fatalf("dir = %q, want /project/subdir", results[0].Dir)
	}
}

func TestResolve_InlineWindowsNoTemplate(t *testing.T) {
	cfg := &SessionsConfig{
		Session: []SessionDef{
			{
				Name:        "mon",
				Type:        "tmux",
				TmuxSession: "mon",
				Window: []WindowDef{
					{Name: "logs", Command: "tail -f /var/log/app.log"},
					{Name: "htop", Command: "htop"},
				},
			},
		},
	}

	results, err := cfg.Resolve("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].Windows) != 2 {
		t.Fatalf("got %d windows, want 2", len(results[0].Windows))
	}
	if results[0].Windows[0].Name != "logs" {
		t.Fatalf("window[0].Name = %q, want logs", results[0].Windows[0].Name)
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := &SessionsConfig{
		Layout: "grid",
		Template: map[string]SessionTemplate{
			"dev": {Window: []WindowDef{{Name: "a"}, {Name: "b"}}},
		},
		Session: []SessionDef{
			{Name: "shell"},
			{Name: "tmux-dev", Type: "tmux", TmuxSession: "dev", Template: "dev"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name string
		cfg  SessionsConfig
	}{
		{"invalid layout", SessionsConfig{Layout: "invalid"}},
		{"empty session name", SessionsConfig{Session: []SessionDef{{Name: ""}}}},
		{"duplicate session name", SessionsConfig{Session: []SessionDef{
			{Name: "a"}, {Name: "a"},
		}}},
		{"invalid type", SessionsConfig{Session: []SessionDef{
			{Name: "a", Type: "invalid"},
		}}},
		{"tmux without tmux_session", SessionsConfig{Session: []SessionDef{
			{Name: "a", Type: "tmux"},
		}}},
		{"missing template", SessionsConfig{Session: []SessionDef{
			{Name: "a", Type: "tmux", TmuxSession: "x", Template: "missing"},
		}}},
		{"template empty window name", SessionsConfig{
			Template: map[string]SessionTemplate{
				"t": {Window: []WindowDef{{Name: ""}}},
			},
		}},
		{"template duplicate window name", SessionsConfig{
			Template: map[string]SessionTemplate{
				"t": {Window: []WindowDef{{Name: "a"}, {Name: "a"}}},
			},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadSessions_Fallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	_, _, err := LoadSessions("", dir)
	if err == nil {
		t.Fatal("expected error when no config file found")
	}
}

func TestLoadSessions_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	content := "[[session]]\nname = \"test\"\n"
	path := filepath.Join(dir, "custom.toml")
	os.WriteFile(path, []byte(content), 0o644)

	cfg, gotPath, err := LoadSessions(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != path {
		t.Fatalf("path = %q, want %q", gotPath, path)
	}
	if len(cfg.Session) != 1 || cfg.Session[0].Name != "test" {
		t.Fatalf("unexpected session: %+v", cfg.Session)
	}
}

func TestLoadSessions_HomeConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	cfgDir := filepath.Join(dir, ".config", "codetty")
	os.MkdirAll(cfgDir, 0o755)
	content := "[[session]]\nname = \"home-session\"\n"
	os.WriteFile(filepath.Join(cfgDir, "sessions.toml"), []byte(content), 0o644)

	// searchDir has no .codetty.toml, should fall back to home config
	searchDir := t.TempDir()
	cfg, _, err := LoadSessions("", searchDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Session) != 1 || cfg.Session[0].Name != "home-session" {
		t.Fatalf("unexpected: %+v", cfg.Session)
	}
}

func TestLoadSessions_RelativeExplicitPathReturnsAbsolute(t *testing.T) {
	dir := t.TempDir()
	content := "[[session]]\nname = \"t\"\n"
	os.WriteFile(filepath.Join(dir, "conf.toml"), []byte(content), 0o644)

	t.Chdir(dir)
	_, gotPath, err := LoadSessions("conf.toml", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(gotPath) {
		t.Fatalf("path %q is not absolute", gotPath)
	}
}

func TestResolve_OmittedDirDefaultsToConfigDir(t *testing.T) {
	cfg := &SessionsConfig{
		Session: []SessionDef{
			{Name: "native"},
			{Name: "mux", Type: "tmux", TmuxSession: "mux", Window: []WindowDef{{Name: "w"}}},
		},
	}
	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Dir != "/project" {
		t.Fatalf("native Dir = %q, want /project", results[0].Dir)
	}
	if results[1].Windows[0].Dir != "/project" {
		t.Fatalf("window Dir = %q, want /project", results[1].Windows[0].Dir)
	}
}

func TestResolve_NativeSessionTabs(t *testing.T) {
	cfg := &SessionsConfig{
		Session: []SessionDef{
			{
				Name:    "dev",
				Command: []string{"bash"},
				Dir:     "./project",
				Tab: []TabDef{
					{Name: "editor", Command: []string{"nvim", "."}},
					{Name: "server", Command: []string{"npm", "run", "dev"}, Dir: "./frontend"},
					{Name: "shell"},
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	results, err := cfg.Resolve("/home/user")
	if err != nil {
		t.Fatal(err)
	}
	rs := results[0]
	if len(rs.Tabs) != 3 {
		t.Fatalf("got %d tabs, want 3", len(rs.Tabs))
	}
	// First tab inherits nothing special for dir, gets session dir
	if rs.Tabs[0].Name != "editor" {
		t.Fatalf("tab[0].Name = %q, want editor", rs.Tabs[0].Name)
	}
	if rs.Tabs[0].Dir != "/home/user/project" {
		t.Fatalf("tab[0].Dir = %q, want /home/user/project", rs.Tabs[0].Dir)
	}
	// Second tab has explicit dir
	if rs.Tabs[1].Dir != "/home/user/frontend" {
		t.Fatalf("tab[1].Dir = %q, want /home/user/frontend", rs.Tabs[1].Dir)
	}
	if rs.Tabs[1].Command[0] != "npm" {
		t.Fatalf("tab[1].Command = %v, want [npm run dev]", rs.Tabs[1].Command)
	}
	// Third tab with no command inherits session command
	if rs.Tabs[2].Command[0] != "bash" {
		t.Fatalf("tab[2].Command = %v, want [bash]", rs.Tabs[2].Command)
	}
}

func TestValidate_TabErrors(t *testing.T) {
	tests := []struct {
		name string
		cfg  SessionsConfig
	}{
		{"tab on tmux session", SessionsConfig{Session: []SessionDef{
			{Name: "a", Type: "tmux", TmuxSession: "x", Tab: []TabDef{{Name: "t1"}}},
		}}},
		{"tab empty name", SessionsConfig{Session: []SessionDef{
			{Name: "a", Tab: []TabDef{{Name: ""}}},
		}}},
		{"tab duplicate name", SessionsConfig{Session: []SessionDef{
			{Name: "a", Tab: []TabDef{{Name: "t1"}, {Name: "t1"}}},
		}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestLoadSessions_WithTabs(t *testing.T) {
	dir := t.TempDir()
	content := `
[[session]]
name = "dev"
command = ["bash"]

  [[session.tab]]
  name = "editor"
  command = ["nvim"]

  [[session.tab]]
  name = "server"
  command = ["npm", "start"]
  dir = "./src"
`
	cfgPath := filepath.Join(dir, ".codetty.toml")
	os.WriteFile(cfgPath, []byte(content), 0o644)

	cfg, _, err := LoadSessions("", dir)
	if err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	if len(cfg.Session) != 1 {
		t.Fatalf("got %d sessions, want 1", len(cfg.Session))
	}
	if len(cfg.Session[0].Tab) != 2 {
		t.Fatalf("got %d tabs, want 2", len(cfg.Session[0].Tab))
	}
	if cfg.Session[0].Tab[0].Name != "editor" {
		t.Fatalf("tab[0].Name = %q, want editor", cfg.Session[0].Tab[0].Name)
	}
	if cfg.Session[0].Tab[1].Dir != "./src" {
		t.Fatalf("tab[1].Dir = %q, want ./src", cfg.Session[0].Tab[1].Dir)
	}
}
