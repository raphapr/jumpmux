# Dashboard

## Keys

| Key                                       | Action                                                                 |
| ----------------------------------------- | ---------------------------------------------------------------------- |
| Mouse click / double-click                | Select / focus an agent, worktree, or tmux session                     |
| Mouse wheel                               | Scroll the table, focused panel, or Help                               |
| `j`/`k`, arrows                           | Move in Agents/Worktrees, browse themes, or scroll a diff/help panel   |
| `g`/`Home`, `G`/`End`                     | First/last row in Agents and Worktrees; top/bottom in diff or Help     |
| `Ctrl+j` / `Ctrl+k` / `Ctrl+n` / `Ctrl+p` | Move the selection in Sessions                                         |
| `Home` / `End`                            | First/last Session row                                                 |
| `1`–`9`                                   | Open that numbered row in Agents or Worktrees                          |
| `Tab` / `Shift+Tab`                       | Next / previous tab                                                    |
| `Enter`                                   | Focus the selected agent/worktree or switch/create a tmux session      |
| `/`                                       | Filter Agents or Worktrees                                             |
| Type, except `O` / `P` / initial `?`      | Search Sessions immediately                                            |
| `Ctrl+f`                                  | Cycle Sessions through All, Live, Inactive, Configured, and Discovered |
| `Ctrl+g`                                  | Toggle Sessions between grouped and most-recently-attached order       |
| `Shift+Left` / `Shift+Right`              | Focus the left or right preview/diff panel                             |
| `Space`                                   | Open actions; press the shown key or `Enter` to run one                |
| `G` / `End` in a paused live preview      | Resume bottom-follow                                                   |
| `s`                                       | Toggle agent scope between all and current tmux session                |
| `t`                                       | Open the theme picker                                                  |
| `Enter` in the theme picker               | Apply the selected theme                                               |
| `Esc` in the theme picker                 | Restore the previous theme and close                                   |
| Type in the theme picker                  | Filter theme names                                                     |
| `Enter` while filtering                   | Accept an Agents/Worktrees filter or open a Session                    |
| `Esc` while filtering                     | Clear and close the filter                                             |
| Agents: `o`, `d`, `p`, `m`                | Open, diff, open PR when available, or mark an unseen completion seen  |
| Worktrees: `a`, `o`, `d`, `p`             | Add, open, diff, or open PR when available                             |
| Worktrees: `b`, `m`, `x`, `r`             | Rebase, merge, clean up, or remove; `Enter` confirms                   |
| Sessions: `O`, `Ctrl+r`, `P`              | Open, remove (`Enter` confirms), or switch to the previous session     |
| `PgUp` / `PgDn`                           | Page diff/Help, or the Session preview                                 |
| `Ctrl+u` / `Ctrl+d`                       | Page the focused preview, diff panel, or Help                          |
| `h` / `l`, left/right                     | Pan long preview or diff lines                                         |
| `Ctrl+v`                                  | Toggle the current tab's runtime preview without saving configuration  |
| `+` / `-`                                 | Resize Agents/Worktrees previews; search Sessions                      |
| `?`                                       | Open Help; enter `?` when Sessions search is active                    |
| `Esc`                                     | Close a view, cancel an action, clear a filter, or quit the dashboard  |
| `q`                                       | Quit the dashboard or Help; close the diff view                        |
| `Ctrl+C`                                  | Quit from any state                                                    |

## Dashboard behavior

### Agents

The Pi extension writes agent status records. Before displaying a record, jumpmux checks its pane with `tmux list-panes`. Pi UI prompts show as a question state until the prompt closes. Set `JUMPMUX_STATE_DIR` to change the status directory. `jumpmux setup` honors `PI_CODING_AGENT_DIR`.

`jumpmux agent list` shows live Pi agents, and `jumpmux agent open <session-id|pane-id>` focuses one. Session IDs that identify more than one pane are ambiguous; use a pane ID.

A completion is seen when its pane is focused or explicitly marked seen from the dashboard. On an unseen completed agent, press `m` directly or choose Mark seen from `Space`. Acknowledgement clears its tmux completion marker.

`jumpmux worktree add <branch> [--detach] [--json]` creates a normal worktree. Without `--detach` or `--json`, it opens a new tmux shell in the worktree. `--detach` prints its path, and `--json` returns the worktree resource.

The preview captures up to 200 lines from the selected pane every 500 ms while the terminal has focus. It includes the tmux session name, preserves SGR colors, and strips Pi's prompt and footer. Scrolling up shows `PAUSED line/total`. Press `G` or `End` to follow new output again. When focus returns, jumpmux refreshes the data and selected preview.

Working agents show a Braille spinner. Statuses older than one hour show `󰔛`. The Time column changes color after five minutes and one hour. Set `JUMPMUX_REDUCED_MOTION=1` to replace animation with a static loader and reduce clock updates to once per second.

### Sessions

Configure session locations and discovery commands in `$XDG_CONFIG_HOME/jumpmux/config.toml` or `~/.config/jumpmux/config.toml`. See [Configuration](configuration.md) for the schema and examples.

Press `Ctrl+f` to cycle through All, Live, Inactive, Configured, and Discovered. The filter resets when jumpmux exits.

Session icons:

- ``: live
- ``: configured and inactive
- ``: discovered
- Plain mode: `L`, `C`, and `R`

Live previews capture the active pane every 500 ms, including alternate-screen programs such as `btop`. Inactive configured entries show a creation hint. Sessions do not yet aggregate agent attention; acknowledged completion is shown in the Agents and Worktrees tabs.

jumpmux merges rows with the same name. Configured paths take precedence; live-only rows use the active pane path. Rows appear in three groups: live, configured, and discovered. Each group sorts by name. Press `Ctrl+g` to sort by the `Last` column until jumpmux exits. Searches sort by fuzzy match score.

Press `Space` for actions available on the selected row. Press the shown key from the table or menu, or select an action and press `Enter`. The Sessions `P` action switches to the previous session even when no row is selected. Opening a selected row, adding a worktree, or switching to the previous session exits the dashboard; failures leave it open. Worktree actions include add, open, diff, PR, rebase, merge, cleanup, and removal. Worktree rebase and merge use the configured worktree backend against its local default branch: Worktrunk when selected or available in `auto`, otherwise native Git. Merge requires clean worktrees and keeps the worktree. Worktrunk squashes by default; press `s` on the merge confirmation to preserve commits. Native Git always preserves commits. Removing a live session does not delete its configured entry.

### Git

Worktree rows use the current repository. Agent rows read Git state from each agent's working directory. Agent Git cells compactly show loading, dirty line counts, rebase or conflict state, and upstream ahead and behind counts. Worktree Git cells also show the non-default base branch and distinguish committed from uncommitted line counts.

When `wt` is available, jumpmux reads the default branch from `wt list --format=json` schema 2. Otherwise, it uses Git metadata.

jumpmux loads `git_status_cache.json` before the first render, refreshes Git data in the background, and saves the cache on exit.

### Pull requests

Worktree PR cells show `#number state-icon check-icon`. Agent PR cells omit the open-state icon to reduce noise. Checks use `󰄴` for success, `󰅙` for failure, and a spinner while pending. Agent and worktree previews list up to three failed checks.

jumpmux calls `gh pr list` for each agent repository. It matches fork PRs against the branch's upstream repository and prefers an open PR. It loads `pr_status_cache.json` before the first render and keeps cached data when GitHub is unavailable.

### Themes and fonts

Press `t` to open the theme picker and browse these color schemes:

`default`, `emberforge`, `glacier-signal`, `obsidian-pop`, `slate-garden`, `phosphor-arcade`, `lasergrid`, `mossfire`, `night-sorbet`, `graphite-code`, `festival-circuit`, `teal-drift`, `catppuccin-latte`, `catppuccin-frappe`, `catppuccin-macchiato`, `catppuccin-mocha`

Built-in themes adapt to the terminal profile. Catppuccin themes use fixed palettes. jumpmux saves the selected theme in its configuration file.

Dashboard agent status uses `` for working, `` for an interactive question, `` for acknowledged completion, and ` ` for completion that still needs attention, with `W`, `?`, `D`, and `D U` in plain mode. Question status uses warning styling and takes precedence over working and done tmux window markers. Status cells use icons rather than completion labels. Set `nerdfont = false` to replace Nerd Font Git, PR, check, agent, and stale icons with text symbols. `JUMPMUX_PLAIN=1` overrides the setting for one launch.
