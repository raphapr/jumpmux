# jumpmux

A small terminal dashboard for jumping between Git worktrees and live Pi agents in tmux.

```console
jumpmux
```

The dashboard mirrors Dashboard's visual layout: a two-line tab header, resource table, 40/60 table-preview split, bordered previews, contextual footer, and split diff/file view. It highlights the active tmux agent or worktree even when jumpmux is launched from a popup.

- **Agents** — open Pi panes with Project, Worktree, Git, PR, Status, Time, and Title columns
- **Worktrees** — current-repository worktrees with Project, Worktree, Git, PR, Mux, Age, and Agent columns

Selecting an agent focuses its existing tmux pane. Selecting a worktree focuses its live-agent or jumpmux-managed tmux window; the first jump creates a new window in that worktree and later jumps reuse it.

## Requirements

- Go 1.24+ to install from source
- Git (for worktree and diff data)
- [Worktrunk](https://worktrunk.dev/) (optional, for default-branch detection)
- [GitHub CLI](https://cli.github.com/) (optional, for PR data)
- A Nerd Font for Dashboard-compatible Git and PR icons
- tmux (for live-agent discovery, worktree windows, and switching)
- [Pi](https://pi.dev)

## Install

```console
go install github.com/raphapr/jumpmux@latest
jumpmux setup
```

`jumpmux setup` installs `jumpmux-status.ts` under Pi's global extensions directory. Restart Pi or run `/reload` in existing sessions. The extension reports events for the current tmux pane and adds Dashboard's default `🤖` while Pi is working or `✅` when it finishes to the tmux window label. The done icon clears when that pane receives focus.

## Keys

| Key                             | Action                                   |
| ------------------------------- | ---------------------------------------- |
| Mouse click / double-click      | Select / focus an agent or worktree      |
| Mouse wheel                     | Scroll the table, preview, or diff       |
| `j`/`k`, arrows                 | Move selection or scroll the diff        |
| `1`–`9`                         | Open that numbered row                   |
| `Tab`                           | Switch Agents and Worktrees              |
| `Enter`                         | Focus the selected agent or worktree     |
| `/`                             | Filter the active tab (case-insensitive) |
| `F`                             | Toggle agent scope: all or session       |
| `T`                             | Cycle the dashboard color scheme         |
| `Enter` / `Esc` while filtering | Accept / clear and leave the filter      |
| `d`                             | Show the selected context's Git WIP diff |
| `Ctrl+u` / `Ctrl+d`             | Scroll preview or diff                   |
| `h` / `l`, left/right           | Pan long preview or diff lines           |
| `?`                             | Show all keyboard and mouse controls     |
| `r`                             | Refresh data                             |
| `Esc`                           | Clear an active filter, otherwise quit   |
| `q`, `Ctrl+C`                   | Quit outside filter and diff views       |

The dashboard loads live agents and the base worktree list first. Git status, PR data, and tmux metadata then fill in independently, so neither tab waits for slower GitHub work. Refreshes are coalesced, so a slow repository cannot accumulate overlapping Git and tmux scans. Agent scope defaults to `all`; press `F` to toggle `all`/`session`, or launch with `--session` (`-s`). Press `T` to cycle Dashboard's 12 color schemes: `default`, `emberforge`, `glacier-signal`, `obsidian-pop`, `slate-garden`, `phosphor-arcade`, `lasergrid`, `mossfire`, `night-sorbet`, `graphite-code`, `festival-circuit`, and `teal-drift`. Schemes automatically use dark or light colors based on the terminal and persist in the user configuration directory. Data refreshes about every two seconds. Working agents show Dashboard's animated Braille spinner; agents with no status update for over an hour show its stale marker `󰔛`. Elapsed time and animation redraw every 250 ms. The diff view includes tracked changes and untracked file names, supports vertical scrolling and horizontal panning, caps captured diff output at 2 MiB, and works for clean or unborn repositories.

## Usage

```console
jumpmux            # open the dashboard with the saved scope
jumpmux --session  # start with agents from the current tmux session
jumpmux --list     # print available contexts
jumpmux setup      # install/update the Pi status extension
jumpmux --version
jumpmux --help
```

For local development, run the whole package with `go run .`, not `go run main.go`.

Worktrees come from the current Git repository. Both tabs use Dashboard's Git format: a loading spinner, non-default base branch, committed and uncommitted line counts, rebase/conflict icons, and upstream ahead/behind counts. When `wt` is available, jumpmux uses Worktrunk's default branch; otherwise it falls back to Git's default-branch metadata. PR numbers use Dashboard's draft, open, merged, and closed icons. PR data comes from `gh pr list`, refreshes once per minute, and displays `-` when `gh` or GitHub data is unavailable. Live agents come from extension status records reconciled against `tmux list-panes`. Status records default to the user cache directory and can be overridden with `JUMPMUX_STATE_DIR`. The setup path honors `PI_CODING_AGENT_DIR`.

## Scope

jumpmux reads worktree, PR, and live-agent state, switches tmux panes, and creates reusable tmux windows for selected worktrees. It has no worktree mutation, patch staging, PR actions, command palette, daemon, or non-tmux backend.
