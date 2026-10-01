package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDashboardConfig(t *testing.T) {
	defer func() {
		nerdFontEnabled = true
		applyColorScheme(schemeDefault)
	}()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("# jumpmux\ntheme = \"teal-drift\"\ndefault_scope = 'session'\nnerdfont = false\n[preview]\nagents = true\nworktrees = false\nsessions = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.theme != schemeTealDrift || config.defaultScope != scopeSession || !config.hasNerdFont || config.nerdFont || config.preview != [tabCount]bool{true, false, false} {
		t.Fatalf("config = %#v", config)
	}
	model := newDashboardForLaunch("/repo", "")
	if model.scheme != schemeTealDrift || model.scope != scopeSession || nerdFontEnabled || dashboardIcon(gitDiffIcon, "*") != "*" || model.previewEnabled != [tabCount]bool{true, false, false} {
		t.Fatalf("dashboard preferences = theme %q, scope %q, nerd font %t", model.scheme.slug(), model.scope.label(), nerdFontEnabled)
	}

	if err := saveColorScheme(schemeEmberforge); err != nil {
		t.Fatal(err)
	}
	if err := saveScopeMode(scopeAll); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# jumpmux\ntheme = \"emberforge\"\ndefault_scope = \"all\"\nnerdfont = false\n[preview]\nagents = true\nworktrees = false\nsessions = false\n"
	if string(data) != want {
		t.Fatalf("saved config = %q, want %q", data, want)
	}
}

func TestDashboardConfigRejectsInvalidPreferences(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []string{"theme = \"unknown\"\n", "default_scope = \"project\"\n", "nerdfont = maybe\n", "[agents]\nquestion_tools = [\"ask_user_question\"]\n", "worktree_backened = \"git\"\n", "[preview]\nsessions = maybe\n", "[preview]\nunknown = true\n"} {
		if err := atomicWrite(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(); err == nil {
			t.Fatalf("invalid config was accepted: %q", config)
		}
	}
}

func TestLegacyWorktreeBackendIsRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"auto", "wt", "git"} {
		data := []byte("worktree_backend = \"" + value + "\"\n")
		if err := atomicWrite(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "remove this setting") {
			t.Fatalf("legacy %s config error = %v", value, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(data) {
			t.Fatalf("legacy %s config changed: %q, %v", value, got, err)
		}
	}
	valid := []byte("# worktree_backend is retired\nsessions = \"worktree_backend\"\n")
	if err := atomicWrite(path, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(); err != nil {
		t.Fatalf("unrelated legacy text rejected: %v", err)
	}
}

func TestConfigPathFollowsXDGOnEveryOS(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for xdg, want := range map[string]string{
		"":              filepath.Join(home, ".config", "jumpmux", "config.toml"),
		"relative/path": filepath.Join(home, ".config", "jumpmux", "config.toml"),
		"/xdg/config":   filepath.Join("/xdg/config", "jumpmux", "config.toml"),
	} {
		t.Setenv("XDG_CONFIG_HOME", xdg)
		if got, err := configPath(); err != nil || got != want {
			t.Fatalf("config path with XDG_CONFIG_HOME=%q = %q, %v; want %q", xdg, got, err, want)
		}
	}
}

func TestSaveConfigWritesThroughSymlink(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dotfile := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(dotfile, []byte("theme = \"default\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dotfile, link); err != nil {
		t.Fatal(err)
	}
	if err := saveScopeMode(scopeSession); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("saving replaced the config symlink: %v", err)
	}
	if data, err := os.ReadFile(dotfile); err != nil || string(data) != "theme = \"default\"\ndefault_scope = \"session\"\n" {
		t.Fatalf("symlink target = %q, %v", data, err)
	}
}

func TestConfigDecodeErrorsNameTheFailingLine(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	for config, want := range map[string]string{
		"theme = default\n": "config.toml:1: theme must be a quoted TOML string",
		// A valid theme must not take the blame for another key's error.
		"theme = \"default\"\nnerdfont = \"yes\"\n": "config.toml:2: toml: cannot decode TOML string",
	} {
		if err := atomicWrite(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("config %q error = %v, want %q", config, err, want)
		}
	}
}
