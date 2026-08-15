# jumpmux

jumpmux is a terminal dashboard for Git worktrees, Pi agents, and tmux sessions.

It is inspired by [Workmux](https://github.com/raine/workmux), but takes a more opinionated approach.

## Tabs

- **Agents:** Open Pi panes with Project, Worktree, Git, PR, Status, Time, and Title columns.
- **Worktrees:** Worktrees from the current repository with Project, Worktree, Git, PR, Mux, Age, and Agent columns.
- **Sessions:** Configured locations, discovered repositories, and live tmux sessions with Session, Path, Windows, and Last-attached columns.

## Requirements

- Go 1.24+ to install from source
- git
- tmux
- [Pi](https://pi.dev)
- Optional: [Nerd Font](https://www.nerdfonts.com/), [Worktrunk](https://worktrunk.dev/), [GitHub CLI](https://cli.github.com/)

## Install

Download the Linux or macOS archive for your amd64 or arm64 system from [GitHub Releases](https://github.com/raphapr/jumpmux/releases), extract `jumpmux` into your `PATH`, then run:

```console
jumpmux setup
```

To install from source instead:

```console
go install github.com/raphapr/jumpmux@latest
jumpmux setup
```

`jumpmux setup` installs or updates `jumpmux-status.ts` in Pi's global extensions directory. Run it again after upgrading jumpmux, then restart Pi or run `/reload` in each open Pi session.

The extension reports each pane's lifecycle state for the dashboard.

## Keys

| Key                                       | Action                                                                       |
| ----------------------------------------- | ---------------------------------------------------------------------------- |
| Mouse click / double-click                | Select / focus an agent, worktree, or tmux session                           |
| Mouse wheel                               | Scroll the table, focused panel, or Help                                     |
| `j`/`k`, arrows                           | Move in Agents/Worktrees, browse themes, or scroll a diff/help panel         |
| `g`/`Home`, `G`/`End`                     | First/last row in Agents and Worktrees; top/bottom in diff or Help           |
| `Ctrl+j` / `Ctrl+k` / `Ctrl+n` / `Ctrl+p` | Move the selection in Sessions                                               |
| `Home` / `End`                            | First/last Session row                                                       |
| `1`–`9`                                   | Open that numbered row in Agents or Worktrees                                |
| `Tab` / `Shift+Tab`                       | Next / previous tab                                                          |
| `Enter`                                   | Focus the selected agent/worktree or switch/create a tmux session            |
| `/`                                       | Filter Agents or Worktrees                                                   |
| Type, except `O` / `Y` / `R` / `D` / `P`  | Search Sessions immediately                                                  |
| `Ctrl+f`                                  | Cycle Sessions through All, Live, Inactive, Configured, and Discovered       |
| `Ctrl+r`                                  | Toggle Sessions between grouped and most-recently-attached order             |
| `Shift+Left` / `Shift+Right`              | Focus the left or right preview/diff panel                                   |
| `Space`                                   | Open actions; press the shown key or `Enter` to run one                      |
| `G` / `End` in a paused live preview      | Resume bottom-follow                                                         |
| `s`                                       | Toggle agent scope between all and current tmux session                      |
| `t`                                       | Open the theme picker                                                        |
| `Enter` in the theme picker               | Apply the selected theme                                                     |
| `Esc` in the theme picker                 | Restore the previous theme and close                                         |
| Type in the theme picker                  | Filter theme names                                                           |
| `Enter` while filtering                   | Accept an Agents/Worktrees filter or open a Session                          |
| `Esc` while filtering                     | Clear and close the filter                                                   |
| Agents: `o`, `d`, `p`, `y`, `m`           | Open, diff, open PR, copy PR URL, or mark an unseen completion seen          |
| Worktrees: `a`, `o`, `d`, `p`, `y`        | Add, open, diff, open PR, or copy PR URL                                     |
| Worktrees: `b`, `m`, `x`, `r`             | Rebase, merge, clean up, or remove when applicable                           |
| Sessions: `O`, `Y`, `R`, `D`, `P`         | Open, copy, rename, remove (`D` confirms), or switch to the previous session |
| `PgUp` / `PgDn`                           | Page diff/Help, or the Session preview                                       |
| `Ctrl+u` / `Ctrl+d`                       | Page the focused preview, diff panel, or Help                                |
| `h` / `l`, left/right                     | Pan long preview or diff lines                                               |
| `Ctrl+v`                                  | Toggle the current tab's runtime preview without saving configuration        |
| `+` / `-`                                 | Change preview height by 10%                                                 |
| `?`                                       | Open Help                                                                    |
| `Esc`                                     | Close a view, cancel an action, clear a filter, or quit the dashboard        |
| `q`                                       | Quit the dashboard or Help; close the diff view                              |
| `Ctrl+C`                                  | Quit from any state                                                          |

## Dashboard behavior

### Agents

The Pi extension writes agent status records. Before displaying a record, jumpmux checks its pane with `tmux list-panes`. It reports configured `agents.question_tools`, including `ask_user_question` by default, as a question state until their result arrives. Set `JUMPMUX_STATE_DIR` to change the status directory. `jumpmux setup` honors `PI_CODING_AGENT_DIR`.

`jumpmux agent list` shows live Pi agents, and `jumpmux agent open <session-id|pane-id>` focuses one. Session IDs that identify more than one pane are ambiguous; use a pane ID.

A completion is seen when its pane is focused or explicitly marked seen from the dashboard. On an unseen completed agent, press `m` directly or choose Mark seen from `Space`. Acknowledgement clears its tmux completion marker.

`jumpmux worktree add <branch> [--detach] [--json]` creates a normal worktree. Without `--detach` or `--json`, it opens a new tmux shell in the worktree. `--detach` prints its path, and `--json` returns the worktree resource.

The preview captures up to 200 lines from the selected pane every 500 ms while the terminal has focus. It includes the tmux session name, preserves SGR colors, and strips Pi's prompt and footer. Scrolling up shows `PAUSED line/total`. Press `G` or `End` to follow new output again. When focus returns, jumpmux refreshes the data and selected preview.

Working agents show a Braille spinner. Statuses older than one hour show `󰔛`. The Time column changes color after five minutes and one hour. Set `JUMPMUX_REDUCED_MOTION=1` to replace animation with a static loader and reduce clock updates to once per second.

### Sessions

Configure session locations in `$XDG_CONFIG_HOME/jumpmux/config.toml` or `~/.config/jumpmux/config.toml`. Each entry needs a unique name and an existing path. Paths support environment variables and a leading `~`.

```toml
[sessions]
exclude = ["^scratchpad$"]
discover = ["~/.bin/jumpmux-sessions"]

[[sessions.entries]]
name = "api"
path = "~/src/api"
```

`sessions.exclude` accepts regular expressions. Use `^name$` for an exact match. `sessions.discover` runs the first array value as an executable and passes the rest as arguments. The command must print one absolute, existing directory per line. jumpmux expands a leading `~` in the executable path and runs the command without a shell. Unknown session keys cause an error.

Press `Ctrl+f` to cycle through All, Live, Inactive, Configured, and Discovered. The filter resets when jumpmux exits.

Session icons:

- ``: live
- ``: configured and inactive
- ``: discovered
- Plain mode: `L`, `C`, and `R`

Live previews capture the active pane every 500 ms, including alternate-screen programs such as `btop`. Inactive configured entries show a creation hint. Sessions do not yet aggregate agent attention; acknowledged completion is shown in the Agents and Worktrees tabs.

jumpmux merges rows with the same name. Configured paths take precedence; live-only rows use the active pane path. Rows appear in three groups: live, configured, and discovered. Each group sorts by name. Press `Ctrl+r` to sort by the `Last` column until jumpmux exits. Searches sort by fuzzy match score.

Press `Space` for actions available on the selected row. Press the shown key from the table or menu, or select an action and press `Enter`. The Sessions `P` action switches to the previous session even when no row is selected. Worktree actions include add, open, diff, PR, copy, rebase, merge, cleanup, and removal. Worktree rebase and merge use the configured worktree backend against its local default branch: Worktrunk when selected or available in `auto`, otherwise native Git. Merge keeps the worktree. Removing a live session does not delete its configured entry.

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

## Usage

CLI resource commands use singular nouns. `--tab` values use plural tab names.

```console
jumpmux                    # open the dashboard with the saved scope
jumpmux -t sessions        # open the Sessions tab (`--tab` also works)
jumpmux session list       # print sessions (`--json` for scripts)
jumpmux session open dev
jumpmux session last       # switch to the previous tmux session
jumpmux agent list
jumpmux agent open <session-id|pane-id>
jumpmux worktree add feature/example --detach --json
jumpmux worktree list
jumpmux --list             # print available contexts
jumpmux setup              # install or update the Pi extension
jumpmux --version
jumpmux --help
```

Worktree `remove`, `cleanup`, `rebase`, and `merge`, plus Session `remove`, ask for confirmation. For non-interactive use, append `--yes` as the final argument.

Run the full package during development:

```console
go run .
```

## Development

The Go command stays at the module root so `go install github.com/raphapr/jumpmux@latest` works. Source and test filenames follow their features. The Pi extension lives in `extension/`.

Prepare a release from a clean working tree:

```console
make release VERSION=0.0.1
git push origin v0.0.1
```

The Make target tests the project and creates an annotated `v0.0.1` tag on the current commit. Pushing that tag starts the release workflow. The tag is the only release version source; local and source builds report `dev`.

## Configuration

jumpmux reads `~/.config/jumpmux/config.toml`:

```toml
worktree_backend = "auto"
theme = "default"
default_scope = "all"
nerdfont = true

[agents]
question_tools = ["ask_user_question"]

[preview]
agents = true
worktrees = true
sessions = true

[sessions]
exclude = []
discover = ["ghq", "list", "-p"]

[[sessions.entries]]
name = "api"
path = "~/src/api"
```

- `worktree_backend` accepts `auto`, `wt`, or `git`.
- `theme` accepts any scheme listed above.
- `default_scope` accepts `all` or `session`.
- `nerdfont` accepts `true` or `false` and defaults to `true`.
- `agents.question_tools` contains tool-name suffixes that show question attention while running. It defaults to `["ask_user_question"]`; use `[]` to disable it. Run `jumpmux setup` and reload Pi after changing it.
- `[preview]` controls the preview panel independently for `agents`, `worktrees`, and `sessions`. Each defaults to `true`.

`auto` uses Worktrunk when `wt` is on `PATH`, then falls back to `git worktree`. The Worktrunk backend calls `wt switch --create` and `wt remove`. The Git backend creates branches under `<repo>__worktrees/` from the default branch and keeps branches after worktree removal.

The `t` and `s` keys save `theme` and `default_scope`. jumpmux validates the configuration before mutations and asks for confirmation before removal. It rejects the primary worktree and worktrees used by tmux panes. Locked and prunable worktrees have separate indicators. You can prune an unlocked stale record from the action menu.

## Scope

jumpmux reads agent, Git, PR, session, and tmux state. It switches panes and sessions, creates tmux windows and configured sessions, manages worktrees, and opens PRs.

jumpmux does not stage changes, run a daemon, provide a command palette, or support another multiplexer.
