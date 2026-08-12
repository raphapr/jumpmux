package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type jumpmuxConfig struct {
	worktreeBackend worktreeBackend
	theme           colorScheme
	defaultScope    scopeMode
	nerdFont        bool
	preview         [tabCount]bool
	hasTheme        bool
	hasDefaultScope bool
	hasNerdFont     bool
}

type configFile struct {
	WorktreeBackend *string        `toml:"worktree_backend"`
	Theme           *string        `toml:"theme"`
	DefaultScope    *string        `toml:"default_scope"`
	NerdFont        *bool          `toml:"nerdfont"`
	Preview         *previewConfig `toml:"preview"`
	Sessions        any            `toml:"sessions"`
}

type previewConfig struct {
	Agents    *bool `toml:"agents"`
	Worktrees *bool `toml:"worktrees"`
	Sessions  *bool `toml:"sessions"`
}

func loadConfig() (jumpmuxConfig, error) {
	config := jumpmuxConfig{worktreeBackend: backendAuto, preview: [tabCount]bool{true, true, true}}
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

	var file configFile
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		var unknown *toml.StrictMissingError
		if !errors.As(err, &unknown) {
			for _, key := range []string{"worktree_backend", "theme", "default_scope"} {
				if strings.Contains(string(data), key+" =") {
					return config, fmt.Errorf("%s: %s must be a quoted TOML string", path, key)
				}
			}
			return config, fmt.Errorf("%s: %w", path, err)
		}
		for _, detail := range unknown.Errors {
			key := detail.Key()
			if len(key) > 0 && key[0] == "preview" {
				return config, fmt.Errorf("%s: preview uses unsupported %q", path, key[len(key)-1])
			}
		}
	}

	if file.WorktreeBackend != nil {
		config.worktreeBackend = worktreeBackend(*file.WorktreeBackend)
		if config.worktreeBackend != backendAuto && config.worktreeBackend != backendWT && config.worktreeBackend != backendGit {
			return config, fmt.Errorf("invalid worktree_backend %q", *file.WorktreeBackend)
		}
	}
	if file.Theme != nil {
		config.theme = colorSchemeFromSlug(*file.Theme)
		if config.theme.slug() != strings.ToLower(*file.Theme) {
			return config, fmt.Errorf("invalid theme %q", *file.Theme)
		}
		config.hasTheme = true
	}
	if file.DefaultScope != nil {
		if *file.DefaultScope != scopeAll.label() && *file.DefaultScope != scopeSession.label() {
			return config, fmt.Errorf("invalid default_scope %q", *file.DefaultScope)
		}
		config.defaultScope, config.hasDefaultScope = scopeModeFromLabel(*file.DefaultScope), true
	}
	if file.NerdFont != nil {
		config.nerdFont, config.hasNerdFont = *file.NerdFont, true
	}
	if file.Preview != nil {
		for tab, enabled := range []*bool{file.Preview.Agents, file.Preview.Worktrees, file.Preview.Sessions} {
			if enabled != nil {
				config.preview[tab] = *enabled
			}
		}
	}
	return config, nil
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
