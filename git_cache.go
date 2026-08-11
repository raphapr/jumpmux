package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type cachedGitStatus struct {
	Branch           string `json:"branch,omitempty"`
	BaseBranch       string `json:"base_branch,omitempty"`
	Dirty            bool   `json:"dirty,omitempty"`
	CommittedAdded   int    `json:"committed_added,omitempty"`
	CommittedRemoved int    `json:"committed_removed,omitempty"`
	Added            int    `json:"added,omitempty"`
	Removed          int    `json:"removed,omitempty"`
	Untracked        int    `json:"untracked,omitempty"`
	Ahead            int    `json:"ahead,omitempty"`
	Behind           int    `json:"behind,omitempty"`
	HasConflict      bool   `json:"has_conflict,omitempty"`
	IsRebasing       bool   `json:"is_rebasing,omitempty"`
}

func gitStatusCachePath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "jumpmux", "git_status_cache.json"), nil
}

func loadGitStatusCache() map[string]item {
	path, err := gitStatusCachePath()
	if err != nil {
		return map[string]item{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]item{}
	}
	var cached map[string]cachedGitStatus
	if json.Unmarshal(data, &cached) != nil {
		return map[string]item{}
	}
	items := make(map[string]item, len(cached))
	for path, status := range cached {
		items[path] = status.item(path)
	}
	return items
}

func saveGitStatusCache(items map[string]item) error {
	cached := make(map[string]cachedGitStatus, len(items))
	for path, item := range items {
		if item.gitLoaded {
			cached[path] = cachedGitStatusFrom(item)
		}
	}
	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}
	path, err := gitStatusCachePath()
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), 0o600)
}

func cachedGitStatusFrom(item item) cachedGitStatus {
	return cachedGitStatus{
		Branch: item.branch, BaseBranch: item.baseBranch, Dirty: item.dirty,
		CommittedAdded: item.committedAdded, CommittedRemoved: item.committedRemoved,
		Added: item.added, Removed: item.removed, Untracked: item.untracked,
		Ahead: item.ahead, Behind: item.behind, HasConflict: item.hasConflict,
		IsRebasing: item.isRebasing,
	}
}

func (status cachedGitStatus) item(path string) item {
	return item{
		kind: "worktree", target: path, cwd: path, gitLoaded: true,
		branch: status.Branch, baseBranch: status.BaseBranch, dirty: status.Dirty,
		committedAdded: status.CommittedAdded, committedRemoved: status.CommittedRemoved,
		added: status.Added, removed: status.Removed, untracked: status.Untracked,
		ahead: status.Ahead, behind: status.Behind, hasConflict: status.HasConflict,
		isRebasing: status.IsRebasing,
	}
}
