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

const worktreeActionTimeout = 2 * time.Minute

type worktreeBackend string

const (
	backendAuto worktreeBackend = "auto"
	backendWT   worktreeBackend = "wt"
	backendGit  worktreeBackend = "git"
)

func resolvedWorktreeBackend(backend worktreeBackend) (worktreeBackend, error) {
	if backend != backendAuto {
		if backend == backendWT {
			if _, err := exec.LookPath("wt"); err != nil {
				return "", errors.New("worktree_backend is wt but wt is not installed")
			}
		}
		return backend, nil
	}
	if _, err := exec.LookPath("wt"); err == nil {
		return backendWT, nil
	}
	return backendGit, nil
}

func addWorktree(repo, branch string, backend worktreeBackend) error {
	root, err := primaryWorktree(repo)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeActionTimeout)
	defer cancel()
	if output, err := runActionCommand(ctx, root, "git", "check-ref-format", "--branch", branch); err != nil {
		return actionError("invalid branch name", output, err)
	}
	backend, err = actionWorktreeBackend(backend)
	if err != nil {
		return err
	}
	if backend == backendWT {
		output, err := runActionCommand(ctx, root, "wt", "-C", root, "switch", "--create", branch, "--no-cd", "--format=json")
		if err != nil {
			return actionError("add worktree", output, err)
		}
		return nil
	}

	target := filepath.Join(filepath.Dir(root), filepath.Base(root)+"__worktrees", filepath.FromSlash(branch))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	base := gitDefaultBranch(root)
	output, err := runActionCommand(ctx, root, "git", "worktree", "add", "-b", branch, target, base)
	if err != nil {
		return actionError("add worktree", output, err)
	}
	return nil
}

func removeWorktree(repo, path string, backend worktreeBackend) error {
	root, err := primaryWorktree(repo)
	if err != nil {
		return err
	}
	if samePath(root, path) {
		return errors.New("cannot remove the primary worktree")
	}
	if err := validateWorktreeRemoval(path); err != nil {
		return err
	}
	backend, err = actionWorktreeBackend(backend)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeActionTimeout)
	defer cancel()
	if backend == backendWT {
		output, err := runActionCommand(ctx, root, "wt", "-y", "-C", root, "remove", path, "--foreground")
		if err != nil {
			return actionError("remove worktree", output, err)
		}
		return nil
	}
	output, err := runActionCommand(ctx, root, "git", "worktree", "remove", path)
	if err != nil {
		return actionError("remove worktree", output, err)
	}
	return nil
}

func cleanupPrunableWorktree(repo string, selected item) error {
	if !selected.prunable || selected.locked || selected.current {
		return errors.New("the selected worktree is not safe to clean up")
	}
	root, err := primaryWorktree(repo)
	if err != nil {
		return err
	}
	worktrees, _, err := listWorktrees(root)
	if err != nil {
		return err
	}
	stillPrunable := false
	for _, worktree := range worktrees {
		if samePath(worktree.path, selected.cwd) && worktree.prunable && !worktree.locked {
			stillPrunable = true
			break
		}
	}
	if !stillPrunable {
		return errors.New("the selected worktree changed; refresh and try again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeActionTimeout)
	defer cancel()
	output, err := runActionCommand(ctx, root, "git", "worktree", "prune", "--expire", "now")
	if err != nil {
		return actionError("clean up worktrees", output, err)
	}
	return nil
}

func actionWorktreeBackend(backend worktreeBackend) (worktreeBackend, error) {
	if backend == backendAuto {
		configured, err := loadWorktreeBackend()
		if err != nil {
			return "", err
		}
		backend = configured
	}
	return resolvedWorktreeBackend(backend)
}

func validateWorktreeRemoval(path string) error {
	panes, err := listTmuxPanes()
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "executable file not found") {
			return nil
		}
		return err
	}
	for _, pane := range panes {
		if pathWithin(pane.Path, path) || (pane.Worktree != "" && samePath(pane.Worktree, path)) {
			return fmt.Errorf("cannot remove worktree open in tmux pane %s", pane.ID)
		}
	}
	return nil
}

func openPullRequest(repo string, number int) error {
	if number == 0 {
		return errors.New("selected row has no pull request")
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
	return command.CombinedOutput()
}

func actionError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %s", action, safeText(message))
}
