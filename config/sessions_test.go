package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSessions_BasicParsing(t *testing.T) {
	dir := t.TempDir()
	content := `
layout = "list"
focus = "editor"

[template.dev]
  [[template.dev.tab]]
  name = "server"
  command = ["npm", "run", "dev"]

[[session]]
name = "editor"
command = ["nvim", "."]

[[session]]
name = "dev-env"
template = "dev"
dir = "./src"

  [[session.tab]]
  name = "shell"
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
	if cfg.Layout != "list" {
		t.Fatalf("layout = %q, want list", cfg.Layout)
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
	if len(tmpl.Tab) != 1 {
		t.Fatalf("template tabs = %d, want 1", len(tmpl.Tab))
	}
}

func TestLoadSessions_RemoteParsing(t *testing.T) {
	dir := t.TempDir()
	content := `
[remote.gpu]
host = "gpu01"
instance = "work"
deploy = "auto"

[[session]]
name = "agent"
remote = "gpu"
command = ["copilot"]
dir = "/workspace/project"
`
	cfgPath := filepath.Join(dir, ".codetty.toml")
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := LoadSessions("", dir)
	if err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	if got := cfg.Remote["gpu"]; got.Host != "gpu01" || got.Instance != "work" || got.Deploy != "auto" {
		t.Fatalf("remote gpu = %+v", got)
	}
	if cfg.Session[0].Remote != "gpu" {
		t.Fatalf("session remote = %q, want gpu", cfg.Session[0].Remote)
	}
}

func TestResolve_NativeSession(t *testing.T) {
	cfg := &SessionsConfig{
		Session: []SessionDef{{Name: "shell", Command: []string{"bash"}}},
	}
	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d, want 1", len(results))
	}
	if results[0].Dir != "/project" {
		t.Fatalf("dir = %q, want /project", results[0].Dir)
	}
	if len(results[0].Tabs) != 0 {
		t.Fatalf("session should have 0 tabs, got %d", len(results[0].Tabs))
	}
}

func TestResolve_TemplateTabs(t *testing.T) {
	cfg := &SessionsConfig{
		Template: map[string]SessionTemplate{
			"base": {
				Tab: []TabDef{
					{Name: "editor", Command: []string{"nvim", "."}},
					{Name: "shell"},
				},
			},
		},
		Session: []SessionDef{{
			Name:     "dev",
			Command:  []string{"bash"},
			Template: "base",
			Tab: []TabDef{
				{Name: "editor", Command: []string{"vim", "."}},
				{Name: "server", Command: []string{"npm", "run", "dev"}},
			},
		}},
	}

	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}
	rs := results[0]
	if len(rs.Tabs) != 3 {
		t.Fatalf("got %d tabs, want 3", len(rs.Tabs))
	}
	if rs.Tabs[0].Name != "editor" || rs.Tabs[0].Command[0] != "vim" {
		t.Fatalf("tab[0] = %+v, want overridden editor tab", rs.Tabs[0])
	}
	if rs.Tabs[1].Name != "shell" || rs.Tabs[1].Command[0] != "bash" {
		t.Fatalf("tab[1] = %+v, want shell inheriting session command", rs.Tabs[1])
	}
	if rs.Tabs[2].Name != "server" {
		t.Fatalf("tab[2] = %+v, want appended server tab", rs.Tabs[2])
	}
}

func TestResolve_DirResolution(t *testing.T) {
	cfg := &SessionsConfig{
		Session: []SessionDef{{
			Name:    "dev",
			Command: []string{"bash"},
			Dir:     "./frontend",
			Tab: []TabDef{
				{Name: "server"},
				{Name: "backend", Dir: "./backend"},
				{Name: "abs", Dir: "/var/log"},
			},
		}},
	}

	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}

	tabs := results[0].Tabs
	if tabs[0].Dir != "/project/frontend" {
		t.Fatalf("tab[0].Dir = %q, want /project/frontend", tabs[0].Dir)
	}
	if tabs[1].Dir != "/project/backend" {
		t.Fatalf("tab[1].Dir = %q, want /project/backend", tabs[1].Dir)
	}
	if tabs[2].Dir != "/var/log" {
		t.Fatalf("tab[2].Dir = %q, want /var/log", tabs[2].Dir)
	}
}

func TestResolve_NativeSessionDir(t *testing.T) {
	cfg := &SessionsConfig{Session: []SessionDef{{Name: "s", Dir: "./subdir"}}}
	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Dir != "/project/subdir" {
		t.Fatalf("dir = %q, want /project/subdir", results[0].Dir)
	}
}

func TestResolve_RemotePathsStayRemote(t *testing.T) {
	cfg := &SessionsConfig{
		Remote: map[string]RemoteDef{"gpu": {Host: "gpu01"}},
		Session: []SessionDef{{
			Name:    "dev",
			Remote:  "gpu",
			Dir:     "workspace/project",
			Command: []string{"bash"},
			Tab: []TabDef{
				{Name: "agent", Command: []string{"copilot"}},
				{Name: "logs", Dir: "/var/log"},
			},
		}},
	}
	results, err := cfg.Resolve("/local/config")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Remote != "gpu" || results[0].Dir != "workspace/project" {
		t.Fatalf("session = %+v", results[0])
	}
	if results[0].Tabs[0].Remote != "gpu" || results[0].Tabs[0].Dir != "workspace/project" {
		t.Fatalf("tab[0] = %+v", results[0].Tabs[0])
	}
	if results[0].Tabs[1].Dir != "/var/log" {
		t.Fatalf("tab[1].Dir = %q, want /var/log", results[0].Tabs[1].Dir)
	}
}

func TestResolve_TabRemoteSelectsSessionTarget(t *testing.T) {
	cfg := &SessionsConfig{
		Remote: map[string]RemoteDef{"gpu": {Host: "gpu01"}},
		Session: []SessionDef{{
			Name: "agent",
			Tab: []TabDef{{
				Name:    "copilot",
				Remote:  "gpu",
				Command: []string{"copilot"},
				Dir:     "/workspace/project",
			}},
		}},
	}
	results, err := cfg.Resolve("/local/config")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Remote != "gpu" || results[0].Tabs[0].Remote != "gpu" {
		t.Fatalf("resolved target = %+v", results[0])
	}
}

func TestResolve_RejectsMixedExecutionTargets(t *testing.T) {
	cfg := &SessionsConfig{
		Remote: map[string]RemoteDef{"gpu": {Host: "gpu01"}},
		Session: []SessionDef{{
			Name: "mixed",
			Tab: []TabDef{
				{Name: "local"},
				{Name: "remote", Remote: "gpu"},
			},
		}},
	}
	if _, err := cfg.Resolve("/project"); err == nil {
		t.Fatal("expected mixed execution target error")
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := &SessionsConfig{
		Layout: "list",
		Template: map[string]SessionTemplate{
			"dev": {Tab: []TabDef{{Name: "editor"}, {Name: "shell"}}},
		},
		Session: []SessionDef{
			{Name: "shell"},
			{Name: "dev", Template: "dev", Tab: []TabDef{{Name: "server"}}},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidate_LegacyLayoutsAccepted(t *testing.T) {
	// Removed layouts from old configs must not fail validation; the TUI
	// falls back to list mode.
	for _, layout := range []string{"focus-big", "grid"} {
		cfg := &SessionsConfig{Layout: layout}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("layout %q: unexpected error: %v", layout, err)
		}
	}
}

func TestValidate_Errors(t *testing.T) {
	tests := []struct {
		name string
		cfg  SessionsConfig
	}{
		{"invalid layout", SessionsConfig{Layout: "invalid"}},
		{"empty session name", SessionsConfig{Session: []SessionDef{{Name: ""}}}},
		{"duplicate session name", SessionsConfig{Session: []SessionDef{{Name: "a"}, {Name: "a"}}}},
		{"missing template", SessionsConfig{Session: []SessionDef{{Name: "a", Template: "missing"}}}},
		{"missing remote", SessionsConfig{Session: []SessionDef{{Name: "a", Remote: "missing"}}}},
		{"remote without host", SessionsConfig{Remote: map[string]RemoteDef{"gpu": {}}}},
		{"invalid deploy", SessionsConfig{Remote: map[string]RemoteDef{"gpu": {Host: "gpu01", Deploy: "sometimes"}}}},
		{"template empty tab name", SessionsConfig{Template: map[string]SessionTemplate{"t": {Tab: []TabDef{{Name: ""}}}}}},
		{"template duplicate tab name", SessionsConfig{Template: map[string]SessionTemplate{"t": {Tab: []TabDef{{Name: "a"}, {Name: "a"}}}}}},
		{"tab local and remote", SessionsConfig{
			Remote:  map[string]RemoteDef{"gpu": {Host: "gpu01"}},
			Session: []SessionDef{{Name: "a", Tab: []TabDef{{Name: "t", Local: true, Remote: "gpu"}}}},
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
	cfg := &SessionsConfig{Session: []SessionDef{{Name: "native"}}}
	results, err := cfg.Resolve("/project")
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Dir != "/project" {
		t.Fatalf("Dir = %q, want /project", results[0].Dir)
	}
}

func TestResolve_NativeSessionTabs(t *testing.T) {
	cfg := &SessionsConfig{
		Session: []SessionDef{{
			Name:    "dev",
			Command: []string{"bash"},
			Dir:     "./project",
			Tab: []TabDef{
				{Name: "editor", Command: []string{"nvim", "."}},
				{Name: "server", Command: []string{"npm", "run", "dev"}, Dir: "./frontend"},
				{Name: "shell"},
			},
		}},
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
	if rs.Tabs[0].Name != "editor" {
		t.Fatalf("tab[0].Name = %q, want editor", rs.Tabs[0].Name)
	}
	if rs.Tabs[0].Dir != "/home/user/project" {
		t.Fatalf("tab[0].Dir = %q, want /home/user/project", rs.Tabs[0].Dir)
	}
	if rs.Tabs[1].Dir != "/home/user/frontend" {
		t.Fatalf("tab[1].Dir = %q, want /home/user/frontend", rs.Tabs[1].Dir)
	}
	if rs.Tabs[1].Command[0] != "npm" {
		t.Fatalf("tab[1].Command = %v, want [npm run dev]", rs.Tabs[1].Command)
	}
	if rs.Tabs[2].Command[0] != "bash" {
		t.Fatalf("tab[2].Command = %v, want [bash]", rs.Tabs[2].Command)
	}
}

func TestValidate_TabErrors(t *testing.T) {
	tests := []struct {
		name string
		cfg  SessionsConfig
	}{
		{"tab empty name", SessionsConfig{Session: []SessionDef{{Name: "a", Tab: []TabDef{{Name: ""}}}}}},
		{"tab duplicate name", SessionsConfig{Session: []SessionDef{{Name: "a", Tab: []TabDef{{Name: "t1"}, {Name: "t1"}}}}}},
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
