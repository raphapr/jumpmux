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

func loadScopeMode() scopeMode {
	path, err := scopeStatePath()
	if err != nil {
		return scopeAll
	}
	data, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(data)) == "session" {
		return scopeSession
	}
	return scopeAll
}

func saveScopeMode(scope scopeMode) error {
	path, err := scopeStatePath()
	if err != nil {
		return err
	}
	return atomicWrite(path, []byte(scope.label()+"\n"), 0o600)
}

func scopeStatePath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "jumpmux", "dashboard_scope"), nil
}
