# jumpmux

jumpmux is a terminal dashboard for moving between Git worktrees and live agents in tmux. It is inspired by [Workmux](https://github.com/raine/workmux), but takes a more opinionated approach.

## Tabs

- **Agents:** Open Pi panes with Project, Worktree, Git, PR, Status, Time, and Title columns.
- **Worktrees:** Worktrees from the current repository with Project, Worktree, Git, PR, Mux, Age, and Agent columns.

Selecting an agent focuses its tmux pane. Selecting a worktree focuses its agent pane or its jumpmux tmux window. The first jump creates a window in that worktree. Later jumps reuse it.

## Requirements

- Go 1.24+ to install from source
- Git
- tmux
- [Pi](https://pi.dev)
- [Nerd Font](https://www.nerdfonts.com/) (optional)
- [Worktrunk](https://worktrunk.dev/) (optional)
- [GitHub CLI](https://cli.github.com/) (optional)

## Install

```console
go install github.com/raphapr/jumpmux@latest
jumpmux setup
```

`jumpmux setup` installs `jumpmux-status.ts` in Pi's global extensions directory. Restart Pi or run `/reload` in each open Pi session.

The extension reports the current pane's agent state.

## Keys

| Key                          | Action                                                                |
| ---------------------------- | --------------------------------------------------------------------- |
| Mouse click / double-click   | Select / focus an agent or worktree                                   |
| Mouse wheel                  | Scroll the table, focused panel, or Help                              |
| `j`/`k`, arrows              | Move the selection, browse themes, or scroll the focused diff panel   |
| `1`–`9`                      | Open that numbered row                                                |
| `Tab`                        | Switch tabs                                                           |
| `Enter`                      | Focus the selected agent or worktree                                  |
| `/`                          | Filter the active tab                                                 |
| `Shift+Left` / `Shift+Right` | Focus the left or right preview/diff panel                            |
| `s`                          | Toggle agent scope between all and current tmux session               |
| `t`                          | Open the theme picker                                                 |
| `Enter` in the theme picker  | Apply the selected theme                                              |
| `Esc` in the theme picker    | Restore the previous theme and close                                  |
| Type in the theme picker     | Filter theme names                                                    |
| `Enter` while filtering      | Accept the filter                                                     |
| `Esc` while filtering        | Clear and close the filter                                            |
| `d`                          | Open the selected context's Git WIP diff                              |
| `a`                          | Add a worktree from the Worktrees tab                                 |
| `r`                          | Remove a worktree after confirmation                                  |
| `o`                          | Open the selected PR in a browser                                     |
| `Ctrl+u` / `Ctrl+d`          | Page the focused preview, diff panel, or Help                         |
| `h` / `l`, left/right        | Pan long preview or diff lines                                        |
| `+` / `-`                    | Change preview height by 10%                                          |
| `?`                          | Open Help                                                             |
| `Esc`                        | Close a view, cancel an action, clear a filter, or quit the dashboard |
| `q`                          | Quit the dashboard or Help; close the diff view                       |
| `Ctrl+C`                     | Quit from any state                                                   |

## Dashboard behavior

### Layout

The dashboard starts with an even table and preview split. `+` and `-` change the preview from 10% to 90%. jumpmux stores the value as `preview_size` in `$XDG_STATE_HOME/jumpmux/settings.json`, or `~/.local/state/jumpmux/settings.json` when `XDG_STATE_HOME` is unset.

The Worktrees preview splits into status and Git log panels on terminals at least 60 columns wide. The diff view splits into diff and file panels when space permits. `Shift+Left` and `Shift+Right` choose which panel receives scroll input.

### Agents

The Pi extension writes agent status records. jumpmux checks each record against `tmux list-panes` before displaying it. Set `JUMPMUX_STATE_DIR` to override the status directory. `jumpmux setup` honors `PI_CODING_AGENT_DIR`.

The preview captures the last 200 lines from the selected pane every 500 ms. It keeps SGR colors and removes Pi's prompt and footer using tmux cursor geometry. New output keeps the preview at the bottom until you scroll away.

Working agents show a Braille spinner. A status older than one hour shows `󰔛`. The Time column uses the success color below five minutes, warning below one hour, and a dim accent after one hour. The dashboard redraws time and animation every 250 ms.

### Git

Worktree rows use the current repository. Agent rows read Git state from each agent's working directory.

Git cells show:

- A loading spinner
- The non-default base branch
- Committed and uncommitted line counts
- Rebase or conflict state
- Upstream ahead and behind counts

jumpmux reads the default branch from `wt list --format=json` schema 2 when `wt` is present. Git metadata provides the fallback.

The dashboard loads `git_status_cache.json` from the user cache directory before the first render. It refreshes Git data in the background and saves new values on exit.

### Pull requests

PR cells use `#number state-icon check-icon`. Check states use `󰄴` for success, `󰅙` for failure, and a spinner for pending checks.

jumpmux queries each agent repository with `gh pr list`. It matches fork PRs against the branch's upstream repository and prefers an open PR over old merged or closed PRs. The dashboard loads `pr_status_cache.json` before the first render and keeps cached data when GitHub is unavailable.

### Themes and fonts

Press `t` to open the theme picker and browse these color schemes:

`default`, `emberforge`, `glacier-signal`, `obsidian-pop`, `slate-garden`, `phosphor-arcade`, `lasergrid`, `mossfire`, `night-sorbet`, `graphite-code`, `festival-circuit`, `teal-drift`, `catppuccin-latte`, `catppuccin-frappe`, `catppuccin-macchiato`, `catppuccin-mocha`

Built-in themes follow the terminal profile. Catppuccin flavours use their fixed palette. jumpmux saves the selected scheme in the user configuration directory.

Set `nerdfont = false` in the configuration to replace Nerd Font Git, PR, check, and stale icons with text symbols. `JUMPMUX_PLAIN=1` overrides the setting for one launch.

## Usage

```console
jumpmux            # open the dashboard with the saved scope
jumpmux --session  # start with agents from the current tmux session
jumpmux --list     # print available contexts
jumpmux setup      # install or update the Pi extension
jumpmux --version
jumpmux --help
```

Run the full package during development:

```console
go run .
```

## Development

The Go command stays at the module root, which keeps `go install github.com/raphapr/jumpmux@latest` working. Source and test files use feature names. The Pi extension lives in `extension/`.

## Configuration

jumpmux reads `~/.config/jumpmux/config.toml`:

```toml
worktree_backend = "auto"
theme = "default"
default_scope = "all"
nerdfont = true
```

- `worktree_backend` accepts `auto`, `wt`, or `git`.
- `theme` accepts any scheme listed above.
- `default_scope` accepts `all` or `session`. `--session` overrides it for that launch.
- `nerdfont` accepts `true` or `false` and defaults to `true`.

`auto` uses Worktrunk when `wt` exists on `PATH`; otherwise it uses `git worktree`. `wt` uses `wt switch --create` and `wt remove`. `git` creates branches from the detected default branch under `<repo>__worktrees/` and keeps the branch after removal.

The `t` and `s` keys update `theme` and `default_scope`. jumpmux validates the configuration before each mutation flow. Removal requires confirmation. jumpmux rejects the primary worktree and any worktree used by a tmux pane.

## Scope

jumpmux reads agent, Git, and PR state. It switches tmux panes, creates reusable tmux windows, adds or removes worktrees, and opens PRs in a browser.

It does not stage patches, run a daemon, provide a command palette, or support a non-tmux backend.
