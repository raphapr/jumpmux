# jumpmux

A small terminal dashboard for jumping between Git worktrees and live Pi agents in tmux.

```console
jumpmux
```

The dashboard uses a two-line tab header, adjustable table-preview split, bordered previews, contextual footer, and split diff/file view. It highlights the active tmux agent or worktree even when jumpmux is launched from a popup.

- **Agents** — open Pi panes with Project, Worktree, Git, PR, Status, Time, and Title columns
- **Worktrees** — current-repository worktrees with Project, Worktree, Git, PR, Mux, Age, and Agent columns

Selecting an agent focuses its existing tmux pane. Selecting a worktree focuses its live-agent or jumpmux-managed tmux window; the first jump creates a new window in that worktree and later jumps reuse it.

## Requirements

- Go 1.24+ to install from source
- Git (for worktree and diff data)
- [Worktrunk](https://worktrunk.dev/) (optional, for default-branch detection)
- [GitHub CLI](https://cli.github.com/) (optional, for PR data)
- A Nerd Font for detailed icons (optional; set `JUMPMUX_PLAIN=1` for portable text symbols)
- tmux (for live-agent discovery, worktree windows, and switching)
- [Pi](https://pi.dev)

## Install

```console
go install github.com/raphapr/jumpmux@latest
jumpmux setup
```

`jumpmux setup` installs `jumpmux-status.ts` under Pi's global extensions directory. Restart Pi or run `/reload` in existing sessions. The extension reports events for the current tmux pane and adds `🤖` while Pi is working or `✅` when it finishes to the tmux window label. The done icon clears when that pane receives focus.

## Keys

| Key                             | Action                                                                            |
| ------------------------------- | --------------------------------------------------------------------------------- |
| Mouse click / double-click      | Select / focus an agent or worktree                                               |
| Mouse wheel                     | Scroll the table, focused preview/diff panel, or help                             |
| `j`/`k`, arrows                 | Move selection; scroll the focused diff panel or help                             |
| `1`–`9`                         | Open that numbered row                                                            |
| `Tab`                           | Switch Agents and Worktrees                                                       |
| `Enter`                         | Focus the selected agent or worktree                                              |
| `/`                             | Filter the active tab (case-insensitive); supports normal text editing and paste  |
| `Shift+Left` / `Shift+Right`    | Focus the left or right split preview/diff panel                                  |
| `s`                             | Toggle agent scope: all or session                                                |
| `t`                             | Cycle the dashboard color scheme                                                  |
| `Enter` / `Esc` while filtering | Accept / clear and leave the filter                                               |
| `d`                             | Show the selected context's Git WIP diff                                          |
| `a`                             | Add a worktree from the Worktrees tab                                             |
| `r`                             | Remove a worktree after confirmation                                              |
| `o`                             | Open the selected PR in the browser                                               |
| `Ctrl+u` / `Ctrl+d`             | Page preview, focused diff panel, or help                                         |
| `h` / `l`, left/right           | Pan long preview or diff lines                                                    |
| `+` / `-`                       | Grow / shrink preview by 10%                                                      |
| `?`                             | Show all keyboard and mouse controls                                              |
| `Esc`                           | Clear a filter, close a view, cancel an action, or quit from the normal dashboard |
| `q`                             | Quit from the normal dashboard or Help; close the diff view                       |
| `Ctrl+C`                        | Quit from any dashboard state                                                     |

Set `JUMPMUX_PLAIN=1` when Nerd Font glyphs are unavailable; Git, PR, check, and stale indicators then use portable text symbols. The dashboard defaults to an even 50/50 table and preview split. `+`/`-` adjusts the preview in 10% steps from 10% to 90%, and persists the selected size as `preview_size` in `$XDG_STATE_HOME/jumpmux/settings.json` (default `~/.local/state/jumpmux/settings.json`). The dashboard loads live agents and the base worktree list first. Git status, PR data, and tmux metadata then fill in independently, so neither tab waits for slower GitHub work. The selected agent preview captures the last 200 lines of its tmux pane every 500 ms, safely preserves SGR colors, uses tmux cursor geometry to omit Pi's prompt and footer regardless of theme or status-bar content, follows new output while at the bottom, and preserves manual scrolling. Refreshes are coalesced, so a slow repository cannot accumulate overlapping Git and tmux scans. Agent scope defaults to `all`; press `s` to toggle `all`/`session`, or launch with `--session` (`-s`). Press `t` to cycle the 12 built-in color schemes: `default`, `emberforge`, `glacier-signal`, `obsidian-pop`, `slate-garden`, `phosphor-arcade`, `lasergrid`, `mossfire`, `night-sorbet`, `graphite-code`, `festival-circuit`, and `teal-drift`. Schemes automatically use dark or light colors based on the terminal and persist in the user configuration directory. Data refreshes about every two seconds. Working agents show an animated Braille spinner; agents with no status update for over an hour show the stale marker `󰔛`. Elapsed time and animation redraw every 250 ms. The Time column uses success below five minutes, warning below one hour, and dimmed accent afterward; inactive clock units are dimmed. The diff view includes tracked changes and untracked file names, supports vertical scrolling and horizontal panning, caps captured diff output at 2 MiB, and works for clean or unborn repositories.

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

Worktrees come from the current Git repository. Agent rows load Git status from each agent's own working directory, including agents in other repositories. Jumpmux loads persisted Git status from `git_status_cache.json` in the user cache directory for immediate first-frame rendering, refreshes it in the background, and saves fresh values on exit. Both tabs show a loading spinner, non-default base branch, committed and uncommitted line counts, rebase/conflict icons, and upstream ahead/behind counts. When `wt` is available, jumpmux uses Worktrunk's default branch; otherwise it falls back to Git's default-branch metadata. PR cells use the `#number state-icon check-icon` format. Checks show `󰄴` for success, `󰅙` for failure, and an animated spinner while pending; `󰅙` is the Nerd Font Material Design close-circle icon used for failed GitHub checks. PR data is resolved independently for every live agent repository, regardless of the tmux session where jumpmux starts. Cached values load immediately from `pr_status_cache.json`, refresh through `gh pr list` once per minute, and remain visible if a refresh is temporarily unavailable. Live agents come from extension status records reconciled against `tmux list-panes`. Status records default to the user cache directory and can be overridden with `JUMPMUX_STATE_DIR`. The setup path honors `PI_CODING_AGENT_DIR`.

## Configuration

Worktree mutations use `~/.config/jumpmux/config.toml`:

```toml
worktree_backend = "auto"
```

Supported values are `auto`, `wt`, and `git`. `auto` uses Worktrunk when `wt` is installed and otherwise uses native `git worktree`. Native Git creates new branches from the detected default branch under the sibling `<repo>__worktrees/` directory. Native removal keeps the branch; Worktrunk removal follows `wt remove` semantics. Both backends reload and validate this file before starting each mutation flow, require confirmation before removal, and refuse to remove the primary worktree or a worktree used by a tmux pane.

## Scope

jumpmux reads live-agent, Git, and PR state; switches tmux panes; creates reusable tmux windows; adds and removes worktrees; and opens PRs in the browser. It has no patch staging, command palette, daemon, or non-tmux backend.
