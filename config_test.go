package main

import (
	"os"
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
	if err := atomicWrite(path, []byte("# jumpmux\nworktree_backend = \"git\"\ntheme = \"teal-drift\"\ndefault_scope = 'session'\nnerdfont = false\n[future]\nvalue = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.worktreeBackend != backendGit || !config.hasTheme || config.theme != schemeTealDrift || !config.hasDefaultScope || config.defaultScope != scopeSession || !config.hasNerdFont || config.nerdFont {
		t.Fatalf("config = %#v", config)
	}
	model := newDashboardForLaunch("/repo", "", false)
	if model.scheme != schemeTealDrift || model.scope != scopeSession || nerdFontEnabled || dashboardIcon(gitDiffIcon, "*") != "*" {
		t.Fatalf("dashboard preferences = theme %q, scope %q, nerd font %t", model.scheme.slug(), model.scope.label(), nerdFontEnabled)
	}

	if err := saveColorScheme(schemeEmberforge); err != nil {
		t.Fatal(err)
	}
	if err := saveScopeMode(scopeAll); err != nil {
		t.Fatal(err)
	}
	if forced := newDashboardForLaunch("/repo", "", true); forced.scope != scopeSession {
		t.Fatalf("forced scope = %q", forced.scope.label())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# jumpmux\nworktree_backend = \"git\"\ntheme = \"emberforge\"\ndefault_scope = \"all\"\nnerdfont = false\n[future]\nvalue = true\n"
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
	for _, config := range []string{"theme = \"unknown\"\n", "default_scope = \"project\"\n", "nerdfont = maybe\n"} {
		if err := atomicWrite(path, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConfig(); err == nil {
			t.Fatalf("invalid config was accepted: %q", config)
		}
	}
}

func TestLegacyDashboardPreferences(t *testing.T) {
	defer applyColorScheme(schemeDefault)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	themePath, err := colorSchemePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(themePath, []byte("glacier-signal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scopePath, err := scopeStatePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(scopePath, []byte("session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := newDashboardForLaunch("/repo", "", false)
	if model.scheme != schemeGlacierSignal || model.scope != scopeSession {
		t.Fatalf("legacy preferences = theme %q, scope %q", model.scheme.slug(), model.scope.label())
	}
}
