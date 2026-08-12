package main

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestDashboardScope(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := saveScopeMode(scopeSession); err != nil {
		t.Fatal(err)
	}
	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !config.hasDefaultScope || config.defaultScope != scopeSession {
		t.Fatalf("saved scope = %#v", config)
	}

	model := newDashboardForLaunch("/repo", "dev", false)
	model.allAgents = []item{
		{kind: "session", target: "%1", muxSessionName: "dev"},
		{kind: "session", target: "%2", muxSessionName: "other"},
	}
	model.applyAgentScope()
	if len(model.agents) != 1 || model.agents[0].target != "%1" {
		t.Fatalf("session scope = %#v", model.agents)
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	got := updated.(dashboardModel)
	config, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got.scope != scopeAll || len(got.agents) != 2 || !config.hasDefaultScope || config.defaultScope != scopeAll {
		t.Fatalf("all scope = %s, agents = %#v", got.scope.label(), got.agents)
	}
}

func TestPerTabPreviewVisibility(t *testing.T) {
	model := newDashboard("/repo")
	model.width, model.height = 80, 20
	model.previewEnabled = [tabCount]bool{true, false, false}
	model.tab = tabAgents
	if model.previewHeight() == 0 {
		t.Fatal("enabled Agents preview is hidden")
	}
	for _, tab := range []int{tabWorktrees, tabSessions} {
		model.tab = tab
		if model.previewHeight() != 0 || model.tableHeight() != model.contentHeight() || strings.Contains(ansi.Strip(model.View()), "Preview") {
			t.Fatalf("disabled tab %d still renders a preview", tab)
		}
	}
}

func TestPreviewSize(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	model := newDashboard("/repo")
	model.width, model.height = 80, 33
	if model.previewSize != 50 || model.tableHeight() != 15 || model.previewHeight() != 15 {
		t.Fatalf("default split = %d%%, table=%d preview=%d", model.previewSize, model.tableHeight(), model.previewHeight())
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	model = updated.(dashboardModel)
	if model.previewSize != 60 || model.tableHeight() != 12 || model.previewHeight() != 18 || loadPreviewSize() != 60 {
		t.Fatalf("grown split = %d%%, table=%d preview=%d", model.previewSize, model.tableHeight(), model.previewHeight())
	}
	path, err := settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("preview size mode: info=%v err=%v", info, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "{\"preview_size\":60}\n" {
		t.Fatalf("settings file: %q err=%v", data, err)
	}

	loaded := newDashboardForLaunch("/repo", "", false)
	if loaded.previewSize != 60 {
		t.Fatalf("persisted preview size = %d", loaded.previewSize)
	}
	updated, _ = loaded.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	if got := updated.(dashboardModel).previewSize; got != 50 {
		t.Fatalf("shrunk preview size = %d", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	legacy, err := legacyPreviewSizePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(legacy, []byte("70\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadPreviewSize(); got != 70 {
		t.Fatalf("legacy preview size = %d", got)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "{\"preview_size\":70}\n" {
		t.Fatalf("migrated settings file: %q err=%v", data, err)
	}
}
