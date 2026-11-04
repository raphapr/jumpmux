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
| Agents: `o`, `d`, `p`, `m`, `r`           | Open, diff, open PR, mark an unseen completion seen, or remove         |
| Agents and Worktrees: `A`                 | Start an agent from a prompt: name the branch, edit it, then create    |
| Worktrees: `a`, `o`, `d`, `p`             | Add, open, diff, or open PR when available                             |
| Worktrees: `b`, `m`, `x`, `r`             | Rebase, merge, clean up, or remove; `Enter`, `s`, or `F` confirms      |
| Sessions: `O`, `Ctrl+r`, `P`              | Open, remove (`Enter` confirms), or switch to the previous session     |
| `PgUp` / `PgDn`                           | Page diff/Help, or the Session preview                                 |
| `Ctrl+u` / `Ctrl+d`                       | Page the focused preview, diff panel, or Help                          |
| `h` / `l`, left/right                     | Pan long preview or diff lines                                         |
| `v`, or `Ctrl+t` on any tab               | Toggle the preview without saving it; on Sessions `v` types in search  |
| `+` / `-`                                 | Resize Agents/Worktrees previews; search Sessions                      |
| `?`                                       | Open Help, current tab first; enter `?` in active Sessions search      |
| `Esc`                                     | Close a view, cancel an action, clear a filter, or quit the dashboard  |
| `q`                                       | Quit Agents/Worktrees or Help; close a diff; search in Sessions        |
| `Ctrl+C`                                  | Quit from any state, including Sessions search                         |

## Dashboard behavior

### Agents

The Pi extension writes agent status records. Before displaying a record, jumpmux checks its pane with `tmux list-panes`. Pi UI prompts show as a question state until the prompt closes. Set `JUMPMUX_STATE_DIR` to change the status directory. `jumpmux setup` honors `PI_CODING_AGENT_DIR`.

`jumpmux agent list` shows live Pi agents, and `jumpmux agent open <session-id|pane-id>` focuses one. Session IDs that identify more than one pane are ambiguous; use a pane ID.

To start an agent on a new task, press `A` on the Agents or Worktrees tab and type a one-line prompt. jumpmux asks `agent.name_command` for a branch name and fills the branch input with it; edit it if needed and press `Enter`. jumpmux then creates the worktree, opens a window for it in the project's tmux session, starts the agent with the prompt, and switches to that window. `Esc` cancels while naming runs. If naming fails, the input reads `naming failed, branch:` and holds a name built from the prompt. The CLI equivalent is [`jumpmux agent new`](cli.md#start-an-agent-from-a-prompt).

A completion is seen when its pane is focused or explicitly marked seen from the dashboard. On an unseen completed agent, press `m` directly or choose Mark seen from `Space`. Acknowledgement clears its tmux completion marker.

To close an agent, press `r` or choose Remove agent from `Space`, then press `Enter`. jumpmux first checks that the pane still runs the same Pi session, then closes the pane. Panes split next to Pi survive, and tmux closes the window only when Pi was its last pane. The worktree and branch stay.

Agent status also appears in the tmux window list. jumpmux adds a marker to the global `window-status-format` and `window-status-current-format`, or to a window's own format when that window sets one. Reloading the tmux configuration drops the marker until the next agent update. While a completion is unseen, jumpmux also sets global focus hooks at index `987654` and removes them once every completion is seen.

`jumpmux worktree add <branch> [--detach] [--json]` adds a normal worktree. A new name creates the branch from the local default branch. An existing local branch is checked out, and a branch that exists only on a remote gets a local branch that tracks it, preferring `origin`. A branch already checked out in another worktree opens that worktree instead. A name that starts with a remote, such as `origin/feature`, is refused; type `feature` to use the remote branch. Without `--detach` or `--json`, it opens a tmux shell in a new worktree, or switches to a pane already open in an existing one. `--detach` prints its path, and `--json` returns the worktree resource with `source` set to `new`, `local`, `remote`, or `existing-worktree`.

In the dashboard, press `a` and type a branch name. As you type, the Add worktree panel shows the branch, the worktree path, and what `Enter` will do: `New branch from main`, `Uses the existing local branch`, `Tracks origin/<branch>`, or `Already checked out here`. In the last case the path is the existing worktree and `Enter` opens it. When `Enter` would fail, the panel says why instead: an invalid name, a name that starts with a remote, or a missing local default branch.

When agents in the same worktree share a title, the Title column appends the pane location as `window.pane (window-name)`, for example `π - repo · 2.1 (editor)`. When the dashboard runs inside tmux, agents in sessions other than the one jumpmux was opened from get a `@session:window` tag, for example `π - labs @labs:4`, so a tag means `Enter` switches sessions. Pressing `s` hides the tagged rows. The filter matches the tag, so `/@labs` and `/labs:4` work. Colliding titles in another session combine both, as in `@labs:4.2 (helm)`. Renaming the session jumpmux runs in does not tag its own agents. On narrow terminals the title is shortened so the location stays visible; a long session name is shortened with `…` and keeps its window, and a tag that does not fit beside a readable title is hidden. In the Worktrees tab, the Agent column shows `+N` for extra agents and the preview lists each agent with its pane location.

The preview captures up to 200 lines from the selected pane every 500 ms while the terminal has focus. It starts with the pane location, such as `Pane dev:2.1 (editor)`, preserves SGR colors, and strips Pi's prompt and footer. Scrolling up shows `PAUSED line/total`. Press `G` or `End` to follow new output again. When focus returns, jumpmux refreshes the data and selected preview.

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

jumpmux merges rows with the same name, and drops a discovered directory that a configured entry already covers. Configured paths take precedence; live-only rows use the active pane path. Rows appear in three groups: live, configured, and discovered. Each group sorts by name. Press `Ctrl+g` to sort by the `Last` column until jumpmux exits. Searches sort by fuzzy match score.

Press `Space` for actions available on the selected row. Press the shown key from the table or menu, or select an action and press `Enter`. The Sessions `P` action switches to the previous session even when no row is selected. Opening a selected row, adding a worktree, or switching to the previous session exits the dashboard; failures leave it open. Worktree actions include add, open, diff, PR, rebase, merge, cleanup, and removal. Worktree rebase and merge use native Git against the local default branch. Merge is fast-forward-only and runs in the worktree that has the default branch checked out. It requires both worktrees to be clean, preserves commits, and keeps the worktree. In a bare repository layout, the bare directory is not listed. Existing `worktree_backend` settings are unsupported and must be removed. Removing a live session does not delete its configured entry.

Before removing a worktree, jumpmux checks tmux and reads `git status`. If a tmux pane in any session has its directory inside the worktree, or sits in a window jumpmux opened for it, the confirmation lists each pane as `session:window.pane (window-name)` and only `Esc` works. Close those panes yourself; jumpmux does not close them, because that would kill what runs in them. The CLI refuses the removal with the same list, even with `--force`. A clean worktree is removed with `Enter`, and the branch stays. If the worktree has changed or untracked files, the confirmation shows how many, and `Enter` does nothing:

- `s` saves the changes and untracked files with `git stash push --include-untracked`, then removes the worktree. The stash is shared by every worktree in the repository, so `git stash list` and `git stash pop` restore the changes anywhere.
- `F` removes the worktree with `git worktree remove --force` and deletes the changes. This cannot be undone.

Ignored files are not stashed; Git deletes them with the worktree. If the worktree changes after you confirm a clean removal, jumpmux stops and asks you to press `r` again. Inside the confirmation, `r` checks the worktree again, for example after you close a blocking pane. A worktree whose directory is gone has nothing to lose, so `Enter` removes its record. A locked worktree is refused; run `git worktree unlock` first.

### Git

Worktree rows use the current repository. Agent rows read Git state from each agent's working directory. Agent Git cells compactly show loading, dirty line counts, rebase or conflict state, and upstream ahead and behind counts. Worktree Git cells also show the non-default base branch and distinguish committed from uncommitted line counts.

jumpmux reads the default branch from local Git metadata: `origin/HEAD`, then local `main`, then local `master`. Base-dependent actions fail clearly when no usable local default branch exists; jumpmux does not fetch automatically.

jumpmux loads `git_status_cache.json` before the first render, refreshes Git data in the background, and saves the cache on exit.

Background refreshes run only while the dashboard is on screen: the terminal has focus and the dashboard's tmux window is the active window of an attached session. For terminal focus tracking inside tmux, set `set -g focus-events on`. Each tab refreshes only the Git data it shows. The Worktrees tab refreshes the selected row and rows with agents every 5 seconds and every other row every 30 seconds. The Agents tab refreshes worktrees that host an agent, and the Sessions tab refreshes no Git data.

### Pull requests

Worktree PR cells show `#number state-icon check-icon`. Agent PR cells omit the open-state icon to reduce noise. Checks use `󰄴` for success, `󰅙` for failure, and a spinner while pending. Agent and worktree previews list up to three failed checks.

jumpmux calls `gh pr list` for each agent repository. It matches fork PRs against the branch's upstream repository and prefers an open PR. It loads `pr_status_cache.json` before the first render and keeps cached data when GitHub is unavailable.

### Themes and fonts

Press `t` to open the theme picker and browse these color schemes:

`default`, `emberforge`, `glacier-signal`, `obsidian-pop`, `slate-garden`, `phosphor-arcade`, `lasergrid`, `mossfire`, `night-sorbet`, `graphite-code`, `festival-circuit`, `teal-drift`, `catppuccin-latte`, `catppuccin-frappe`, `catppuccin-macchiato`, `catppuccin-mocha`

Built-in themes adapt to the terminal profile. Catppuccin themes use fixed palettes. jumpmux saves the selected theme in its configuration file.

Dashboard agent status uses `` for working, `` for an interactive question, `` for acknowledged completion, and ` ` for completion that still needs attention, with `W`, `?`, `D`, and `D U` in plain mode. Question status uses warning styling and takes precedence over working and done tmux window markers. Status cells use icons rather than completion labels. Set `nerdfont = false` to replace Nerd Font Git, PR, check, agent, and stale icons with text symbols. `JUMPMUX_PLAIN=1` overrides the setting for one launch.
