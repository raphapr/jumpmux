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
	theme        colorScheme
	defaultScope scopeMode
	nerdFont     bool
	preview      [tabCount]bool
	hasNerdFont  bool
}

type configFile struct {
	Theme        *string        `toml:"theme"`
	DefaultScope *string        `toml:"default_scope"`
	NerdFont     *bool          `toml:"nerdfont"`
	Preview      *previewConfig `toml:"preview"`
	Sessions     any            `toml:"sessions"`
}

type previewConfig struct {
	Agents    *bool `toml:"agents"`
	Worktrees *bool `toml:"worktrees"`
	Sessions  *bool `toml:"sessions"`
}

func loadConfig() (jumpmuxConfig, error) {
	config := jumpmuxConfig{
		preview: [tabCount]bool{true, true, true},
	}
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
			// Name the failing line. An unquoted theme or scope otherwise reads as a number error.
			var decodeErr *toml.DecodeError
			if errors.As(err, &decodeErr) {
				row, _ := decodeErr.Position()
				if lines := strings.Split(string(data), "\n"); row >= 1 && row <= len(lines) {
					key, _, _ := strings.Cut(lines[row-1], "=")
					if key = strings.TrimSpace(key); key == "theme" || key == "default_scope" {
						return config, fmt.Errorf("%s:%d: %s must be a quoted TOML string", path, row, key)
					}
				}
				return config, fmt.Errorf("%s:%d: %w", path, row, err)
			}
			return config, fmt.Errorf("%s: %w", path, err)
		}
		for _, detail := range unknown.Errors {
			key := detail.Key()
			if len(key) > 0 && key[0] == "worktree_backend" {
				return config, fmt.Errorf("%s: worktree_backend is no longer supported; remove this setting", path)
			}
			if len(key) == 0 {
				continue
			}
			if key[0] == "agents" || key[0] == "preview" {
				return config, fmt.Errorf("%s: %s uses unsupported %q", path, key[0], key[len(key)-1])
			}
			return config, fmt.Errorf("%s: uses unsupported %q", path, key[0])
		}
	}

	if file.Theme != nil {
		config.theme = colorSchemeFromSlug(*file.Theme)
		if config.theme.slug() != strings.ToLower(*file.Theme) {
			return config, fmt.Errorf("invalid theme %q", *file.Theme)
		}
	}
	if file.DefaultScope != nil {
		if *file.DefaultScope != scopeAll.label() && *file.DefaultScope != scopeSession.label() {
			return config, fmt.Errorf("invalid default_scope %q", *file.DefaultScope)
		}
		config.defaultScope = scopeModeFromLabel(*file.DefaultScope)
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

// configPath follows XDG on every OS, including macOS, where os.UserConfigDir
// would pick ~/Library/Application Support.
func configPath() (string, error) {
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" || !filepath.IsAbs(config) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "jumpmux", "config.toml"), nil
}

func saveConfigValue(key, value string) error {
	if _, err := loadConfig(); err != nil {
		return err
	}
	path, err := configPath()
	if err != nil {
		return err
	}
	// Write through a symlink so a dotfiles-managed config stays linked.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
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
