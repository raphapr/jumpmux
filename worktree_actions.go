package main

import (
	"cmp"
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

type branchSource int

const (
	branchNew branchSource = iota
	branchLocal
	branchRemote
	branchCheckedOut
	// branchRemoteName is a new name that starts with a remote, such as
	// origin/feature. Git would create an ambiguous local branch, so it is
	// refused; ref holds the name without the remote.
	branchRemoteName
)

// branchPlan is what adding a worktree for a branch name will do.
type branchPlan struct {
	source branchSource
	// ref is the default branch for branchNew, the remote-tracking branch for
	// branchRemote, and the worktree path for branchCheckedOut.
	ref string
}

// branchRefs is a snapshot of the branches and worktrees of one repository.
type branchRefs struct {
	local      map[string]bool
	remote     map[string][]string // branch name -> remote-tracking refs, such as origin/x
	checkedOut map[string]string   // branch name -> worktree path
	remotes    []string
	base       string
	root       string // primary worktree, where new worktree paths start
}

func loadBranchRefs(repo string) (branchRefs, error) {
	refs := branchRefs{local: map[string]bool{}, remote: map[string][]string{}, checkedOut: map[string]string{}, base: gitDefaultBranch(repo)}
	// Not listWorktrees: its rev-parse --show-toplevel fails in a bare
	// repository, which is the primary worktree of a bare layout.
	list, err := gitOutput(repo, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return refs, err
	}
	worktrees := parseWorktrees([]byte(list))
	if len(worktrees) > 0 {
		refs.root = worktrees[0].path
	}
	for _, wt := range worktrees {
		// A prunable worktree has no directory to open; Git reports the conflict.
		if wt.branch != "detached" && !wt.prunable {
			refs.checkedOut[wt.branch] = wt.path
		}
	}
	remotes, err := gitOutput(repo, "remote")
	if err != nil {
		return refs, err
	}
	refs.remotes = strings.Fields(remotes)
	output, err := gitOutput(repo, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return refs, err
	}
	for _, ref := range strings.Fields(output) {
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			refs.local[name] = true
			continue
		}
		// Remote names may contain slashes, so match the longest known remote.
		remote := ""
		for _, candidate := range refs.remotes {
			if strings.HasPrefix(ref, "refs/remotes/"+candidate+"/") && len(candidate) > len(remote) {
				remote = candidate
			}
		}
		if remote == "" {
			continue
		}
		if name := strings.TrimPrefix(ref, "refs/remotes/"+remote+"/"); name != "HEAD" {
			refs.remote[name] = append(refs.remote[name], remote+"/"+name)
		}
	}
	return refs, nil
}

func (refs branchRefs) plan(branch string) branchPlan {
	if path, ok := refs.checkedOut[branch]; ok {
		return branchPlan{source: branchCheckedOut, ref: path}
	}
	if refs.local[branch] {
		return branchPlan{source: branchLocal, ref: branch}
	}
	if remotes := refs.remote[branch]; len(remotes) > 0 {
		for _, ref := range remotes {
			if ref == "origin/"+branch {
				return branchPlan{source: branchRemote, ref: ref}
			}
		}
		return branchPlan{source: branchRemote, ref: remotes[0]}
	}
	for _, remote := range refs.remotes {
		if name, ok := strings.CutPrefix(branch, remote+"/"); ok && name != "" {
			return branchPlan{source: branchRemoteName, ref: name}
		}
	}
	return branchPlan{source: branchNew, ref: refs.base}
}

// validBranchName approximates git check-ref-format --branch, so the Add
// worktree panel can flag a bad name without running Git on each keystroke.
// addWorktree still asks Git.
func validBranchName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") {
		return false
	}
	if strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.Contains(name, "//") || strings.ContainsAny(name, " ~^:?*[\\\x7f") {
		return false
	}
	for _, r := range name {
		if r < 0x20 {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

// remoteNameError explains a branchRemoteName plan.
func remoteNameError(branch, name string) error {
	return fmt.Errorf("%s names a remote branch; type %s to use it", branch, name)
}

func (plan branchPlan) notice(branch string) string {
	switch plan.source {
	case branchCheckedOut:
		return "Opened existing worktree " + branch
	case branchLocal:
		return "Created worktree for existing branch " + branch
	case branchRemote:
		return "Created worktree " + branch + " tracking " + plan.ref
	}
	return "Created worktree " + branch
}

// addWorktree adds a worktree for branch. With reuse, it checks out an
// existing local or remote branch or returns the worktree that already has the
// branch checked out; otherwise, and for a new name, it creates the branch from
// the default branch. Agents pass reuse=false, so a name taken after they
// chose it fails instead of joining another agent's worktree.
func addWorktree(repo, branch string, reuse bool) (item, branchPlan, error) {
	if _, err := loadConfig(); err != nil {
		return item{}, branchPlan{}, err
	}
	root, err := primaryWorktree(repo)
	if err != nil {
		return item{}, branchPlan{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeActionTimeout)
	defer cancel()
	if output, err := runActionCommand(ctx, root, "git", "check-ref-format", "--branch", branch); err != nil {
		return item{}, branchPlan{}, actionError("invalid branch name", output, err)
	}
	plan := branchPlan{source: branchNew}
	if reuse {
		// Resolve now, not from the dashboard's snapshot, since refs may have changed.
		refs, err := loadBranchRefs(root)
		if err != nil {
			return item{}, branchPlan{}, err
		}
		plan = refs.plan(branch)
	}
	if plan.source == branchRemoteName {
		return item{}, branchPlan{}, remoteNameError(branch, plan.ref)
	}
	if plan.source == branchCheckedOut {
		return item{kind: "worktree", target: plan.ref, cwd: plan.ref, branch: branch, title: branch}, plan, nil
	}
	target := worktreePath(root, branch)
	args := []string{"worktree", "add", target, branch}
	switch plan.source {
	case branchRemote:
		args = []string{"worktree", "add", "--track", "-b", branch, target, plan.ref}
	case branchNew:
		base, err := requiredDefaultBranch(root)
		if err != nil {
			return item{}, branchPlan{}, err
		}
		plan.ref = base
		args = []string{"worktree", "add", "-b", branch, target, base}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return item{}, branchPlan{}, err
	}
	output, err := runActionCommand(ctx, root, "git", args...)
	if err != nil {
		return item{}, branchPlan{}, actionError("add worktree", output, err)
	}
	// The dashboard runs git status with optional locks off, so it never rewrites
	// a fresh worktree's stale index and every status rehashes all files (about
	// 3x slower). One refresh here fixes that; failure only costs speed.
	_, _ = runActionCommand(ctx, target, "git", "update-index", "-q", "--refresh")
	return item{kind: "worktree", target: target, cwd: target, branch: branch, title: branch}, plan, nil
}

// openAddedWorktree reuses the panes of a worktree that already existed and
// opens a new tmux shell for one just created.
func openAddedWorktree(cwd string, added item, plan branchPlan) error {
	if plan.source != branchCheckedOut {
		return openTmuxWorktree(added)
	}
	selected, err := requiredItem("worktrees", cwd, added.target)
	if err != nil {
		return err
	}
	return jump(selected)
}

func worktreePath(root, branch string) string {
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+"__worktrees", filepath.FromSlash(branch))
}

// worktreeChanges counts what git worktree remove would refuse to delete.
// Ignored files are not counted; Git deletes them without --force.
type worktreeChanges struct {
	changed, untracked int
}

func (c worktreeChanges) dirty() bool {
	return c.changed > 0 || c.untracked > 0
}

func (c worktreeChanges) String() string {
	files := func(count int, kind string) string {
		if count == 1 {
			return "1 " + kind + " file"
		}
		return fmt.Sprintf("%d %s files", count, kind)
	}
	switch {
	case c.changed > 0 && c.untracked > 0:
		return files(c.changed, "changed") + " and " + files(c.untracked, "untracked")
	case c.untracked > 0:
		return files(c.untracked, "untracked")
	}
	return files(c.changed, "changed")
}

// readWorktreeChanges takes the caller's deadline: listing every untracked file
// can outlast metadataTimeout in a large worktree.
func readWorktreeChanges(ctx context.Context, path string) (worktreeChanges, error) {
	// A missing directory has nothing to lose, and git worktree remove still
	// removes its record.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return worktreeChanges{}, nil
	}
	// Without its .git file, Git would read an enclosing repository instead and
	// a stash would take that repository's changes.
	top, err := gitOutputContext(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return worktreeChanges{}, err
	}
	if !samePath(strings.TrimSpace(top), path) {
		return worktreeChanges{}, fmt.Errorf("%s is not a Git worktree", compactHome(path))
	}
	output, err := gitOutputContext(ctx, path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return worktreeChanges{}, err
	}
	var changes worktreeChanges
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "?? "):
			changes.untracked++
		case len(line) > 3:
			changes.changed++
		}
	}
	return changes, nil
}

// dirtyWorktreeError means a plain removal found changes; callers add how to
// stash or discard them.
type dirtyWorktreeError struct {
	changes worktreeChanges
}

func (e dirtyWorktreeError) Error() string {
	return "worktree has " + e.changes.String()
}

type removeMode int

const (
	// removeClean refuses a worktree with changes, like git worktree remove.
	removeClean removeMode = iota
	// removeStash saves changes and untracked files in the repository's shared
	// stash, so any worktree can restore them.
	removeStash
	// removeDiscard deletes changes with git worktree remove --force.
	removeDiscard
)

func removeWorktree(repo, path string, mode removeMode) error {
	if _, err := loadConfig(); err != nil {
		return err
	}
	worktrees, current, err := listWorktrees(repo)
	if err != nil {
		return err
	}
	if len(worktrees) == 0 {
		return errors.New("repository has no worktrees")
	}
	root := worktrees[0].path
	if samePath(root, path) {
		return errors.New("cannot remove the primary worktree")
	}
	if samePath(current, path) {
		return errors.New("cannot remove the current worktree")
	}
	for _, wt := range worktrees {
		// Git refuses a locked worktree even with --force, so refuse before any stash.
		if wt.locked && samePath(wt.path, path) {
			return errors.New("worktree is locked; run git worktree unlock first")
		}
	}
	// Check panes first, so a blocked removal does not leave a needless stash.
	if err := validateWorktreeRemoval(path); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeActionTimeout)
	defer cancel()
	args := []string{"worktree", "remove", path}
	stashed := ""
	switch mode {
	case removeDiscard:
		args = []string{"worktree", "remove", "--force", path}
	case removeClean, removeStash:
		// Read changes now: the confirmation may be older than the latest edit.
		changes, err := readWorktreeChanges(ctx, path)
		if err != nil {
			return err
		}
		if !changes.dirty() {
			break
		}
		if mode == removeClean {
			return dirtyWorktreeError{changes}
		}
		name, _ := gitOutput(path, "branch", "--show-current")
		if name = strings.TrimSpace(name); name == "" {
			name = compactHome(path)
		}
		stashed = "jumpmux: removed worktree " + name
		if output, err := runActionCommand(ctx, path, "git", "stash", "push", "--include-untracked", "-m", stashed); err != nil {
			return actionError("stash changes", output, err)
		}
	}
	output, err := runActionCommand(ctx, root, "git", args...)
	if err != nil {
		if stashed != "" {
			return fmt.Errorf("changes are in the stash as %q, but %w", stashed, actionError("remove worktree", output, err))
		}
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

// worktreePanes lists tmux panes, in every session, whose directory is inside
// path or whose window jumpmux opened for it.
func worktreePanes(path string) ([]tmuxPane, error) {
	panes, err := listTmuxPanes()
	if err != nil {
		return nil, err
	}
	var open []tmuxPane
	for _, pane := range panes {
		if pathWithin(pane.Path, path) || (pane.Worktree != "" && samePath(pane.Worktree, path)) {
			open = append(open, pane)
		}
	}
	return open, nil
}

// paneLocations names panes by their tmux location, such as "dev:3.1 (editor)",
// or by pane ID when tmux gives no window index.
func paneLocations(panes []tmuxPane) []string {
	locations := make([]string, 0, len(panes))
	for _, pane := range panes {
		location := paneLocation(item{pane: pane.ID, muxSessionName: pane.SessionName, muxWindowIndex: pane.WindowIndex, paneIndex: pane.PaneIndex, muxWindowName: pane.WindowName})
		locations = append(locations, cmp.Or(location, safeText(pane.ID)))
	}
	return locations
}

// describePanes writes "tmux pane dev:3.1 (editor)" or
// "2 tmux panes: dev:3.1 (editor), dev:3.2 (pi)".
func describePanes(panes []tmuxPane) string {
	locations := paneLocations(panes)
	if len(locations) == 1 {
		return "tmux pane " + locations[0]
	}
	return fmt.Sprintf("%d tmux panes: %s", len(locations), strings.Join(locations, ", "))
}

func validateWorktreeRemoval(path string) error {
	panes, err := worktreePanes(path)
	if err != nil {
		return err
	}
	if len(panes) == 0 {
		return nil
	}
	pronoun := "it"
	if len(panes) > 1 {
		pronoun = "them"
	}
	return fmt.Errorf("worktree is open in %s; close %s before removing the worktree", describePanes(panes), pronoun)
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
