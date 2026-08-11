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

func loadWorktreeBackend() (worktreeBackend, error) {
	path, err := configPath()
	if err != nil {
		return backendAuto, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return backendAuto, nil
	}
	if err != nil {
		return backendAuto, err
	}
	backend := backendAuto
	topLevel := true
	for lineNumber, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if strings.HasPrefix(line, "[") {
			topLevel = false
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !topLevel || !ok || strings.TrimSpace(key) != "worktree_backend" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) < 2 || (value[0] != '"' && value[0] != '\'') || value[len(value)-1] != value[0] {
			return backendAuto, fmt.Errorf("%s:%d: worktree_backend must be a quoted TOML string", path, lineNumber+1)
		}
		backend = worktreeBackend(value[1 : len(value)-1])
	}
	if backend != backendAuto && backend != backendWT && backend != backendGit {
		return backendAuto, fmt.Errorf("invalid worktree_backend %q", backend)
	}
	return backend, nil
}

func configPath() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "jumpmux", "config.toml"), nil
}

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
	backend, err := resolvedWorktreeBackend(backend)
	if err != nil {
		return err
	}
	root, err := primaryWorktree(repo)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeActionTimeout)
	defer cancel()
	if output, err := runActionCommand(ctx, root, "git", "check-ref-format", "--branch", branch); err != nil {
		return actionError("invalid branch name", output, err)
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
	backend, err := resolvedWorktreeBackend(backend)
	if err != nil {
		return err
	}
	root, err := primaryWorktree(repo)
	if err != nil {
		return err
	}
	if samePath(root, path) {
		return errors.New("cannot remove the primary worktree")
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
