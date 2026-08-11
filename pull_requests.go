package main

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

type pullRequest struct {
	Number      int    `json:"number"`
	State       string `json:"state"`
	Draft       bool   `json:"isDraft"`
	HeadRefName string `json:"headRefName"`
}

var prCache struct {
	sync.Mutex
	repo    string
	expires time.Time
	values  map[string]pullRequest
}

func listPullRequests(repo string) map[string]pullRequest {
	prCache.Lock()
	defer prCache.Unlock()
	if prCache.repo == repo && time.Now().Before(prCache.expires) {
		return prCache.values
	}

	values := map[string]pullRequest{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// ponytail: cap PR lookup; add pagination if repositories with 100+ PRs miss active branches.
	cmd := boundedCommand(ctx, "gh", "pr", "list", "--state", "all", "--limit", "100", "--json", "number,state,isDraft,headRefName")
	cmd.Dir = repo
	if output, err := cmd.Output(); err == nil {
		values = parsePullRequests(output)
	}
	prCache.repo = repo
	prCache.expires = time.Now().Add(time.Minute)
	prCache.values = values
	return values
}

func parsePullRequests(output []byte) map[string]pullRequest {
	var listed []pullRequest
	if json.Unmarshal(output, &listed) != nil {
		return map[string]pullRequest{}
	}
	result := make(map[string]pullRequest, len(listed))
	for _, pr := range listed {
		if pr.HeadRefName != "" {
			if _, exists := result[pr.HeadRefName]; !exists {
				result[pr.HeadRefName] = pr
			}
		}
	}
	return result
}
