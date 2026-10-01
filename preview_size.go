package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	defaultPreviewSize = 50
	minPreviewSize     = 10
	maxPreviewSize     = 90
	previewSizeStep    = 10
)

type dashboardSettings struct {
	PreviewSize int `json:"preview_size"`
}

func loadPreviewSize() int {
	path, err := settingsPath()
	if err == nil {
		if data, readErr := os.ReadFile(path); readErr == nil {
			var settings dashboardSettings
			if json.Unmarshal(data, &settings) == nil && validPreviewSize(settings.PreviewSize) {
				return settings.PreviewSize
			}
		}
	}
	return defaultPreviewSize
}

func savePreviewSize(size int) error {
	data, err := json.Marshal(dashboardSettings{PreviewSize: size})
	if err != nil {
		return err
	}
	path, err := settingsPath()
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), 0o600)
}

func validPreviewSize(size int) bool {
	return size >= minPreviewSize && size <= maxPreviewSize
}

func settingsPath() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "jumpmux", "settings.json"), nil
}
