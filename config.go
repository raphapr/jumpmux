package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type jumpmuxConfig struct {
	worktreeBackend worktreeBackend
	theme           colorScheme
	defaultScope    scopeMode
	nerdFont        bool
	hasTheme        bool
	hasDefaultScope bool
	hasNerdFont     bool
}

func loadConfig() (jumpmuxConfig, error) {
	config := jumpmuxConfig{worktreeBackend: backendAuto}
	path, err := configPath()
	if err != nil {
		return config, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, err
	}

	topLevel := true
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if strings.HasPrefix(line, "[") {
			topLevel = false
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !topLevel || !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key != "worktree_backend" && key != "theme" && key != "default_scope" && key != "nerdfont" {
			continue
		}
		if key == "nerdfont" {
			switch value {
			case "true":
				config.nerdFont = true
			case "false":
				config.nerdFont = false
			default:
				return config, fmt.Errorf("%s:%d: nerdfont must be true or false", path, lineNumber+1)
			}
			config.hasNerdFont = true
			continue
		}
		value, ok = tomlString(value)
		if !ok {
			return config, fmt.Errorf("%s:%d: %s must be a quoted TOML string", path, lineNumber+1, key)
		}
		switch key {
		case "worktree_backend":
			config.worktreeBackend = worktreeBackend(value)
			if config.worktreeBackend != backendAuto && config.worktreeBackend != backendWT && config.worktreeBackend != backendGit {
				return config, fmt.Errorf("invalid worktree_backend %q", value)
			}
		case "theme":
			config.theme = colorSchemeFromSlug(value)
			if config.theme.slug() != strings.ToLower(value) {
				return config, fmt.Errorf("invalid theme %q", value)
			}
			config.hasTheme = true
		case "default_scope":
			if value != scopeAll.label() && value != scopeSession.label() {
				return config, fmt.Errorf("invalid default_scope %q", value)
			}
			config.defaultScope, config.hasDefaultScope = scopeModeFromLabel(value), true
		}
	}
	return config, nil
}

func tomlString(value string) (string, bool) {
	if len(value) < 2 || (value[0] != '"' && value[0] != '\'') || value[len(value)-1] != value[0] {
		return "", false
	}
	return value[1 : len(value)-1], true
}

func configPath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "jumpmux", "config.toml"), nil
}

func loadWorktreeBackend() (worktreeBackend, error) {
	config, err := loadConfig()
	return config.worktreeBackend, err
}

func saveConfigValue(key, value string) error {
	if _, err := loadConfig(); err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}

	topLevel, insertAt := true, len(lines)
	for index, line := range lines {
		trimmed := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if strings.HasPrefix(trimmed, "[") {
			if topLevel {
				insertAt = index
			}
			topLevel = false
			continue
		}
		name, _, ok := strings.Cut(trimmed, "=")
		if topLevel && ok && strings.TrimSpace(name) == key {
			lines[index] = fmt.Sprintf("%s = %q", key, value)
			return atomicWrite(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
		}
	}
	lines = append(lines[:insertAt], append([]string{fmt.Sprintf("%s = %q", key, value)}, lines[insertAt:]...)...)
	return atomicWrite(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
