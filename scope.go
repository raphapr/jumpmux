package main

import (
	"os"
	"path/filepath"
	"strings"
)

type scopeMode uint8

const (
	scopeAll scopeMode = iota
	scopeSession
)

func (scope scopeMode) toggle() scopeMode {
	if scope == scopeSession {
		return scopeAll
	}
	return scopeSession
}

func (scope scopeMode) label() string {
	if scope == scopeSession {
		return "session"
	}
	return "all"
}

func scopeModeFromLabel(value string) scopeMode {
	if value == scopeSession.label() {
		return scopeSession
	}
	return scopeAll
}

func loadLegacyScopeMode() scopeMode {
	path, err := scopeStatePath()
	if err != nil {
		return scopeAll
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return scopeAll
	}
	return scopeModeFromLabel(strings.TrimSpace(string(data)))
}

func saveScopeMode(scope scopeMode) error {
	return saveConfigValue("default_scope", scope.label())
}

func scopeStatePath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "jumpmux", "dashboard_scope"), nil
}
