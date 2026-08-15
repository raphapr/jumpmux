package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type contextItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Icon          string `json:"icon"`
	Kind          string `json:"kind"`
	Status        string `json:"status,omitempty"`
	Attention     string `json:"attention,omitempty"`
	Current       bool   `json:"current,omitempty"`
	Pane          string `json:"pane,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	Branch        string `json:"branch,omitempty"`
	PRNumber      int    `json:"pr_number,omitempty"`
	PRURL         string `json:"pr_url,omitempty"`
	TmuxSessionID string `json:"tmux_session_id,omitempty"`
	TmuxWindowID  string `json:"tmux_window_id,omitempty"`
	Live          bool   `json:"live,omitempty"`
}

func contextCommand(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "agent":
		return true, runAgentCommand(args[1:])
	case "session":
		return true, runSessionCommand(args[1:])
	case "worktree":
		cwd, err := os.Getwd()
		if err != nil {
			return true, err
		}
		return true, runWorktreeCommand(cwd, args[1:])
	default:
		return false, nil
	}
}

func contextJSON(items []item) []contextItem {
	result := make([]contextItem, 0, len(items))
	for _, item := range items {
		entry := contextItem{ID: item.target, Name: item.title, Path: item.cwd}
		switch item.kind {
		case "session":
			entry.Kind, entry.Status = "agent", item.status
			entry.Current, entry.Pane = item.current, item.pane
			entry.TmuxSessionID, entry.TmuxWindowID = item.muxSessionID, item.muxWindowID
			entry.SessionID = item.agentSessionID
			switch item.status {
			case "done":
				entry.Icon = dashboardIcon(doneIcon, "D")
				entry.Attention = "seen"
				if !item.seen {
					entry.Icon += " " + dashboardIcon(unseenAgentIcon, "U")
					entry.Attention = "unseen"
				}
			case "question":
				entry.Icon = dashboardIcon(questionIcon, "?")
				entry.Attention = "question"
			default:
				entry.Icon = dashboardIcon(workingIcon, "A")
			}
		case "worktree":
			entry.Kind, entry.Icon = "worktree", dashboardIcon("", "W")
			entry.Current, entry.Pane = item.current, item.pane
			entry.TmuxSessionID, entry.TmuxWindowID = item.muxSessionID, item.muxWindowID
			entry.Branch, entry.PRNumber, entry.PRURL = item.branch, item.prNumber, item.prURL
			entry.Status, entry.SessionID = item.status, item.agentSessionID
			switch item.status {
			case "question":
				entry.Attention = "question"
			case "done":
				entry.Attention = "seen"
				if !item.seen {
					entry.Attention = "unseen"
				}
			}
		case "tmux-session":
			entry.Kind, entry.Current, entry.Pane = "session", item.current, item.pane
			entry.Icon, _ = sessionIcon(item)
			entry.TmuxSessionID, entry.TmuxWindowID = item.muxSessionID, item.muxWindowID
			entry.Live = item.muxSessionID != ""
		}
		result = append(result, entry)
	}
	return result
}

func cliItems(kind, cwd string) ([]item, error) {
	switch kind {
	case "agents":
		return listLiveAgents()
	case "sessions":
		return listSessions(true)
	case "worktrees":
		items, err := listWorktreeItems(cwd)
		if err != nil {
			return nil, err
		}
		agents, err := listLiveAgents()
		if err != nil {
			return nil, err
		}
		attachAgentsToWorktrees(items, agents)
		if err := attachTmuxWorktrees(items); err != nil {
			return nil, err
		}
		return items, nil
	}
	return nil, fmt.Errorf("unknown context %q", kind)
}

func listContext(kind, cwd string, args []string) error {
	if len(args) > 1 || (len(args) == 1 && args[0] != "--json") {
		return fmt.Errorf("usage: jumpmux %s list [--json]", strings.TrimSuffix(kind, "s"))
	}
	items, err := cliItems(kind, cwd)
	if err != nil {
		return err
	}
	config, err := loadConfig()
	if err != nil {
		return err
	}
	nerdFontEnabled = !config.hasNerdFont || config.nerdFont
	if len(args) == 1 && kind == "worktrees" {
		items = worktreePRDetails(cwd, items)
	}
	entries := contextJSON(items)
	if len(args) == 1 {
		return json.NewEncoder(os.Stdout).Encode(entries)
	}
	for _, entry := range entries {
		fmt.Printf("%s %-24s %s\n", entry.Icon, safeText(entry.Name), safeText(compactHome(entry.Path)))
	}
	return nil
}

func findItem(items []item, id string) (item, bool) {
	for _, item := range items {
		if item.target == id || (item.kind == "session" && item.agentSessionID != "" && item.agentSessionID == id) {
			return item, true
		}
	}
	return item{}, false
}

func requiredAgent(id string) (item, error) {
	agents, err := listLiveAgents()
	if err != nil {
		return item{}, err
	}
	var matches []item
	for _, agent := range agents {
		if agent.pane == id || agent.target == id || agent.agentSessionID == id {
			matches = append(matches, agent)
		}
	}
	if len(matches) == 0 {
		return item{}, fmt.Errorf("agent %q not found", id)
	}
	if len(matches) > 1 && !validTmuxPaneID(id) {
		return item{}, fmt.Errorf("agent ID %q is ambiguous; use a pane ID", id)
	}
	return matches[0], nil
}

func requiredItem(kind, cwd, id string) (item, error) {
	items, err := cliItems(kind, cwd)
	if err != nil {
		return item{}, err
	}
	selected, ok := findItem(items, id)
	if !ok {
		return item{}, fmt.Errorf("%s %q not found", kind, id)
	}
	return selected, nil
}

func requiredWorktree(cwd, id string) (item, error) {
	items, err := listWorktreeItems(cwd)
	if err != nil {
		return item{}, err
	}
	selected, ok := findItem(items, id)
	if !ok {
		return item{}, fmt.Errorf("worktree %q not found", id)
	}
	return selected, nil
}

func runAgentCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jumpmux agent <list|open>")
	}
	switch args[0] {
	case "list":
		return listContext("agents", "", args[1:])
	case "open":
		if len(args) != 2 {
			return errors.New("usage: jumpmux agent open <session-id|pane-id>")
		}
		selected, err := requiredAgent(args[1])
		if err != nil {
			return err
		}
		return jump(selected)
	default:
		return fmt.Errorf("unknown agent command %q", args[0])
	}
}

type worktreeAddOptions struct {
	branch string
	detach bool
	json   bool
}

func parseWorktreeAdd(args []string) (worktreeAddOptions, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return worktreeAddOptions{}, errors.New("usage: jumpmux worktree add <branch> [--detach] [--json]")
	}
	options := worktreeAddOptions{branch: args[0]}
	for _, arg := range args[1:] {
		switch arg {
		case "--detach":
			options.detach = true
		case "--json":
			options.json = true
		default:
			return worktreeAddOptions{}, fmt.Errorf("unknown add option %q", arg)
		}
	}
	return options, nil
}

func runWorktreeAdd(cwd string, args []string) error {
	options, err := parseWorktreeAdd(args)
	if err != nil {
		return err
	}
	backend, err := actionWorktreeBackend(backendAuto)
	if err != nil {
		return err
	}
	if !options.detach && !options.json {
		if err := requireTmuxWorktreeWindow(); err != nil {
			return err
		}
	}
	created, err := addWorktree(cwd, options.branch, backend)
	if err != nil {
		return err
	}
	if options.json {
		return json.NewEncoder(os.Stdout).Encode(contextJSON([]item{created})[0])
	}
	if options.detach {
		fmt.Println(created.cwd)
		return nil
	}
	return openTmuxWorktree(created)
}

func runWorktreeCommand(cwd string, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jumpmux worktree <list|add|open|diff|pr|remove|cleanup|rebase|merge>")
	}
	switch args[0] {
	case "list":
		return listContext("worktrees", cwd, args[1:])
	case "add":
		return runWorktreeAdd(cwd, args[1:])
	case "open":
		if len(args) != 2 {
			return errors.New("usage: jumpmux worktree open <id>")
		}
		selected, err := requiredWorktree(cwd, args[1])
		if err != nil {
			return err
		}
		return jump(selected)
	case "diff":
		if len(args) != 2 {
			return errors.New("usage: jumpmux worktree diff <id>")
		}
		selected, err := requiredWorktree(cwd, args[1])
		if err != nil {
			return err
		}
		output, err := gitOutput(selected.cwd, "diff", "--no-ext-diff", "--no-color")
		if err != nil {
			return err
		}
		fmt.Print(output)
		return nil
	case "pr":
		if len(args) != 2 {
			return errors.New("usage: jumpmux worktree pr <id>")
		}
		items, err := listWorktreeItems(cwd)
		if err != nil {
			return err
		}
		items = worktreePRDetails(cwd, items)
		selected, ok := findItem(items, args[1])
		if !ok {
			return fmt.Errorf("worktree %q not found", args[1])
		}
		return openPullRequest(selected.cwd, selected.prNumber)
	case "remove", "cleanup", "rebase", "merge":
		return runDestructiveWorktreeCommand(cwd, args)
	default:
		return fmt.Errorf("unknown worktree command %q", args[0])
	}
}

func runDestructiveWorktreeCommand(cwd string, args []string) error {
	clean, yes, err := parseYes(args[1:])
	if err != nil {
		return err
	}
	if len(clean) != 1 {
		return fmt.Errorf("usage: jumpmux worktree %s <id> [--yes]", args[0])
	}
	if _, err := requiredWorktree(cwd, clean[0]); err != nil {
		return err
	}
	if !yes {
		if err := confirmOperation("worktree " + args[0]); err != nil {
			return err
		}
	}
	// Revalidate immediately before mutating after a potentially long confirmation.
	selected, err := requiredWorktree(cwd, clean[0])
	if err != nil {
		return err
	}
	switch args[0] {
	case "remove":
		backend, err := actionWorktreeBackend(backendAuto)
		if err != nil {
			return err
		}
		return removeWorktree(cwd, selected.cwd, backend)
	case "cleanup":
		return cleanupPrunableWorktree(cwd, selected)
	case "rebase", "merge":
		backend, err := actionWorktreeBackend(backendAuto)
		if err != nil {
			return err
		}
		return updateWorktree(selected.cwd, selected.branch, args[0], false, backend)
	}
	return nil
}

func runSessionCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: jumpmux session <list|open|last|rename|remove>")
	}
	switch args[0] {
	case "list":
		return listContext("sessions", "", args[1:])
	case "open":
		if len(args) != 2 {
			return errors.New("usage: jumpmux session open <id>")
		}
		selected, err := requiredItem("sessions", "", args[1])
		if err != nil {
			return err
		}
		return jump(selected)
	case "last":
		if len(args) != 1 {
			return errors.New("usage: jumpmux session last")
		}
		return switchToLastTmuxSession()
	case "rename":
		if len(args) != 3 {
			return errors.New("usage: jumpmux session rename <id> <name>")
		}
		selected, err := requiredItem("sessions", "", args[1])
		if err != nil {
			return err
		}
		return renameTmuxSession(selected, args[2])
	case "remove":
		clean, yes, err := parseYes(args[1:])
		if err != nil {
			return err
		}
		if len(clean) != 1 {
			return errors.New("usage: jumpmux session remove <id> [--yes]")
		}
		if _, err := requiredItem("sessions", "", clean[0]); err != nil {
			return err
		}
		if !yes {
			if err := confirmOperation("session removal"); err != nil {
				return err
			}
		}
		selected, err := requiredItem("sessions", "", clean[0])
		if err != nil {
			return err
		}
		return removeTmuxSession(selected)
	default:
		return fmt.Errorf("unknown session command %q", args[0])
	}
}

func confirmOperation(action string) error {
	info, err := os.Stdin.Stat()
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%s requires confirmation; rerun with --yes", action)
	}
	return confirmWith(action, os.Stdin, os.Stdout)
}

func parseYes(args []string) ([]string, bool, error) {
	if len(args) > 0 && args[len(args)-1] == "--yes" {
		return args[:len(args)-1], true, nil
	}
	for _, arg := range args {
		if arg == "--yes" {
			return nil, false, errors.New("--yes must be last")
		}
	}
	return args, false, nil
}

func confirmWith(action string, input io.Reader, output io.Writer) error {
	if _, err := fmt.Fprintf(output, "%s? [y/N] ", action); err != nil {
		return err
	}
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(answer), "y") || strings.EqualFold(strings.TrimSpace(answer), "yes") {
		return nil
	}
	return errors.New("cancelled")
}
