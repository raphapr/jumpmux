package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	worktreeActionTimeout = 2 * time.Minute
	worktreeMergeTimeout  = 30 * time.Minute
)

func addWorktree(repo, branch string) (item, error) {
	if _, err := loadConfig(); err != nil {
		return item{}, err
	}
	root, err := primaryWorktree(repo)
	if err != nil {
		return item{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeActionTimeout)
	defer cancel()
	if output, err := runActionCommand(ctx, root, "git", "check-ref-format", "--branch", branch); err != nil {
		return item{}, actionError("invalid branch name", output, err)
	}
	base, err := requiredDefaultBranch(root)
	if err != nil {
		return item{}, err
	}
	target := filepath.Join(filepath.Dir(root), filepath.Base(root)+"__worktrees", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return item{}, err
	}
	output, err := runActionCommand(ctx, root, "git", "worktree", "add", "-b", branch, target, base)
	if err != nil {
		return item{}, actionError("add worktree", output, err)
	}
	return item{kind: "worktree", target: target, cwd: target, branch: branch, title: branch}, nil
}

func removeWorktree(repo, path string) error {
	if _, err := loadConfig(); err != nil {
		return err
	}
	root, err := primaryWorktree(repo)
	if err != nil {
		return err
	}
	if samePath(root, path) {
		return errors.New("cannot remove the primary worktree")
	}
	current, err := gitOutput(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if samePath(strings.TrimSpace(current), path) {
		return errors.New("cannot remove the current worktree")
	}
	if err := validateWorktreeRemoval(path); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeActionTimeout)
	defer cancel()
	output, err := runActionCommand(ctx, root, "git", "worktree", "remove", path)
	if err != nil {
		return actionError("remove worktree", output, err)
	}
	return nil
}

func updateWorktree(path, branch, operation string) error {
	if _, err := loadConfig(); err != nil {
		return err
	}
	if operation != "rebase" && operation != "merge" {
		return fmt.Errorf("unsupported worktree action %q", operation)
	}
	worktrees, _, err := listWorktrees(path)
	if err != nil {
		return err
	}
	if len(worktrees) == 0 || samePath(path, worktrees[0].path) {
		return errors.New("cannot rebase or merge the primary worktree")
	}
	found := false
	for _, worktree := range worktrees {
		if samePath(path, worktree.path) && worktree.branch == branch && !worktree.prunable {
			found = true
			break
		}
	}
	if !found {
		return errors.New("worktree changed; refresh and retry")
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeMergeTimeout)
	defer cancel()
	// The first record is the main worktree, or the repository itself in a bare layout.
	base, err := requiredDefaultBranch(worktrees[0].path)
	if err != nil {
		return err
	}
	dirs := []string{path}
	target := ""
	if operation == "merge" {
		// A fast-forward merge updates the worktree that has the default branch checked out.
		for _, worktree := range worktrees {
			if !worktree.bare && !worktree.prunable && worktree.branch == base {
				target = worktree.path
				break
			}
		}
		if target == "" {
			return fmt.Errorf("cannot merge: check out %s in a worktree first", base)
		}
		dirs = append(dirs, target)
	}
	for _, dir := range dirs {
		output, err := runActionCommand(ctx, dir, "git", "status", "--porcelain")
		if err != nil {
			return actionError("check worktree status", output, err)
		}
		if strings.TrimSpace(string(output)) != "" {
			return fmt.Errorf("commit or stash changes in %s first", compactHome(dir))
		}
	}
	if operation == "rebase" {
		output, err := runActionCommand(ctx, path, "git", "rebase", base)
		if err != nil {
			return actionError("rebase worktree", output, err)
		}
		return nil
	}
	output, err := runActionCommand(ctx, target, "git", "merge", "--ff-only", branch)
	if err != nil {
		return actionError("merge worktree", output, err)
	}
	return nil
}

func cleanupPrunableWorktree(repo string, selected item) error {
	if _, err := loadConfig(); err != nil {
		return err
	}
	if !selected.prunable || selected.locked || selected.current {
		return errors.New("this worktree cannot be cleaned up")
	}
	root, err := primaryWorktree(repo)
	if err != nil {
		return err
	}
	worktrees, _, err := listWorktrees(root)
	if err != nil {
		return err
	}
	stale := ""
	for _, worktree := range worktrees {
		if samePath(worktree.path, selected.cwd) && worktree.prunable && !worktree.locked {
			stale = worktree.path
			break
		}
	}
	if stale == "" {
		return errors.New("worktree changed; refresh and retry")
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeActionTimeout)
	defer cancel()
	// Unlike git worktree prune, remove drops only this record and leaves other stale records alone.
	output, err := runActionCommand(ctx, root, "git", "worktree", "remove", stale)
	if err != nil {
		return actionError("clean up worktree", output, err)
	}
	return nil
}

func validateWorktreeRemoval(path string) error {
	panes, err := listTmuxPanes()
	if err != nil {
		return err
	}
	for _, pane := range panes {
		if pathWithin(pane.Path, path) || (pane.Worktree != "" && samePath(pane.Worktree, path)) {
			return fmt.Errorf("close tmux pane %s before removing this worktree", pane.ID)
		}
	}
	return nil
}

func openPullRequest(repo string, number int) error {
	if number == 0 {
		return errors.New("no pull request found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := runActionCommand(ctx, repo, "gh", "pr", "view", fmt.Sprintf("%d", number), "--web")
	if err != nil {
		return actionError("open pull request", output, err)
	}
	return nil
}

func primaryWorktree(repo string) (string, error) {
	worktrees, _, err := listWorktrees(repo)
	if err != nil {
		return "", err
	}
	if len(worktrees) == 0 {
		return "", errors.New("repository has no worktrees")
	}
	return worktrees[0].path, nil
}

func runActionCommand(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	command := boundedCommand(ctx, name, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	// ErrWaitDelay means the command exited 0 but a child it started, such as a
	// browser or a hook's background job, still holds the output pipe.
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	return output, err
}

func actionError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %s", action, safeText(message))
}
