# jumpmux

jumpmux is a terminal dashboard for moving between Git worktrees, live agents, and tmux sessions. It is inspired by [Workmux](https://github.com/raine/workmux), but takes a more opinionated approach.

## Tabs

- **Agents:** Open Pi panes with Project, Worktree, Git, PR, Status, Time, and Title columns.
- **Worktrees:** Worktrees from the current repository with Project, Worktree, Git, PR, Mux, Age, and Agent columns.
- **Sessions:** Configured locations, discovered repositories, and live tmux sessions with Session, Path, Windows, and Last-attached columns.

Selecting an agent focuses its tmux pane. Selecting a worktree focuses its agent pane or its jumpmux tmux window. The first jump creates a window in that worktree. Later jumps reuse it. Inside tmux, selecting a live session switches the current client; selecting an inactive configured session creates it detached at its configured path, then switches to it. Outside tmux, selecting either attaches to the named session, creating a configured session when needed.

## Requirements

- Go 1.24+ to install from source
- Git
- tmux
- Session locations are optional. Configure them in `~/.config/jumpmux/config.toml`.
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
| Type                                      | Search Sessions immediately                                            |
| `Ctrl+f`                                  | Cycle Sessions through All, Live, Inactive, Configured, and Discovered |
| `Ctrl+r`                                  | Toggle Sessions between grouped and most-recently-attached order       |
| `Shift+Left` / `Shift+Right`              | Focus the left or right preview/diff panel                             |
| `Space`                                   | Open valid actions for the selected row                                |
| `G` / `End` in a paused live preview      | Resume bottom-follow                                                   |
| `s`                                       | Toggle agent scope between all and current tmux session                |
| `t`                                       | Open the theme picker                                                  |
| `Enter` in the theme picker               | Apply the selected theme                                               |
| `Esc` in the theme picker                 | Restore the previous theme and close                                   |
| Type in the theme picker                  | Filter theme names                                                     |
| `Enter` while filtering                   | Accept an Agents/Worktrees filter or open a Session                    |
| `Esc` while filtering                     | Clear and close the filter                                             |
| `d`                                       | Open the selected Git diff from Agents or Worktrees                    |
| `a`                                       | Add a worktree from the Worktrees tab                                  |
| `r`                                       | Remove a worktree after confirmation                                   |
| `Ctrl+d`                                  | Remove a live tmux session after confirmation (Sessions only)          |
| `o`                                       | Open the selected PR from Agents or Worktrees                          |
| `PgUp` / `PgDn`                           | Page diff/Help, or the Session preview                                 |
| `Ctrl+u` / `Ctrl+d`                       | Page the focused preview, diff panel, or Help (except Sessions)        |
| `h` / `l`, left/right                     | Pan long preview or diff lines                                         |
| `Ctrl+v`                                  | Toggle the current tab's runtime preview without saving configuration  |
| `+` / `-`                                 | Change preview height by 10%                                           |
| `?`                                       | Open Help                                                              |
| `Esc`                                     | Close a view, cancel an action, clear a filter, or quit the dashboard  |
| `q`                                       | Quit the dashboard or Help; close the diff view                        |
| `Ctrl+C`                                  | Quit from any state                                                    |

## Dashboard behavior

### Layout

Below 60 columns, jumpmux starts table-only. `Ctrl+v` can force a preview for this launch without changing `[preview]` configuration. At 140 columns and wider, the table and preview sit side by side. Other sizes use the vertical split. `+` and `-` change the vertical preview from 10% to 90%. jumpmux stores that value as `preview_size` in `$XDG_STATE_HOME/jumpmux/settings.json`, or `~/.local/state/jumpmux/settings.json` when `XDG_STATE_HOME` is unset.

The Worktrees preview splits into status and Git log panels on terminals at least 60 columns wide. The diff view splits into diff and file panels when space permits. Click a preview or diff panel, or use `Shift+Left` and `Shift+Right`, to choose which panel receives scroll input. The header shows non-default modes such as SEARCH, PREVIEW, CONFIRM, THEME, or WORKING beside refresh status. Normal browsing has no mode label.

### Agents

The Pi extension writes agent status records. jumpmux checks each record against `tmux list-panes` before displaying it. Set `JUMPMUX_STATE_DIR` to override the status directory. `jumpmux setup` honors `PI_CODING_AGENT_DIR`.

The preview captures the last 200 lines from the selected pane every 500 ms while the terminal is focused. It keeps SGR colors and removes Pi's prompt and footer using tmux cursor geometry. `FOLLOW` means new output stays at the bottom. Scrolling changes this to `PAUSED line/total`; `G` or `End` resumes following. Focus returns trigger an immediate data and selected-preview refresh; background terminals keep only the lightweight clock.

Working agents show a Braille spinner. A status older than one hour shows `󰔛`. The Time column uses the success color below five minutes, warning below one hour, and a dim accent after one hour. The dashboard redraws time and animation every 250 ms. Set `JUMPMUX_REDUCED_MOTION=1` for a static loader and a one-second clock tick; data and preview refreshes continue.

### Sessions

Session locations live in `$XDG_CONFIG_HOME/jumpmux/config.toml`, falling back to `~/.config/jumpmux/config.toml`. Entries have a unique name and an existing path. Paths expand environment variables and a leading `~`.

```toml
[sessions]
exclude = ["^scratchpad$"]
discover = ["~/.bin/jumpmux-sessions"]

[[sessions.entries]]
name = "api"
path = "~/src/api"
```

`sessions.exclude` uses regular expressions; use `^name$` for an exact match. `sessions.discover` runs an executable with the remaining array values as arguments. It must print one absolute, existing directory per line. The executable path expands a leading `~`; jumpmux does not invoke a shell. Other keys under `sessions` and session behavior settings are rejected.

Press `Ctrl+f` to cycle the Sessions table through All, Live, Inactive, Configured, and Discovered. The filter lasts until jumpmux exits.

The Sessions table uses `` for live-only sessions, `` for configured entries, and `` for discovered repositories. Plain mode uses `L`, `C`, and `R`. A separate marker identifies the current client, another attached client, or a detached live session; plain mode says `SELF`, `ATT`, or `LIVE`. Live-session previews capture the active pane's current screen immediately, including alternate-screen applications such as `btop`, and refresh every 500 ms while focused. Inactive configured entries show a creation hint. The table merges configured, discovered, and live entries by exact name. Its `Last` column comes from tmux's last-attached time. Without a filter, it sorts live sessions first, configured entries second, and discovered directories last, alphabetically within each group. `Ctrl+r` switches to most-recently-attached order for this launch only. Active fuzzy searches sort by relevance. Configured paths win for merged entries; live-only entries use the active pane path. `Space` offers copy actions for paths, branch or session names, and available PR URLs, plus live-session rename and removal after the applicable flow; configured entries remain unchanged in the file. `Ctrl+d` only removes a live tmux session after confirmation.

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

PR cells use `#number state-icon check-icon`. Check states use `󰄴` for success, `󰅙` for failure, and a spinner for pending checks. Fresh PR data includes up to three failed check names in agent and worktree previews.

jumpmux queries each agent repository with `gh pr list`. It matches fork PRs against the branch's upstream repository and prefers an open PR over old merged or closed PRs. The dashboard loads `pr_status_cache.json` before the first render and keeps cached data when GitHub is unavailable.

### Themes and fonts

Press `t` to open the theme picker and browse these color schemes:

`default`, `emberforge`, `glacier-signal`, `obsidian-pop`, `slate-garden`, `phosphor-arcade`, `lasergrid`, `mossfire`, `night-sorbet`, `graphite-code`, `festival-circuit`, `teal-drift`, `catppuccin-latte`, `catppuccin-frappe`, `catppuccin-macchiato`, `catppuccin-mocha`

Built-in themes follow the terminal profile. Catppuccin flavours use their fixed palette. jumpmux saves the selected scheme in the user configuration directory.

Set `nerdfont = false` in the configuration to replace Nerd Font Git, PR, check, and stale icons with text symbols. `JUMPMUX_PLAIN=1` overrides the setting for one launch.

## Usage

```console
jumpmux                 # open the dashboard with the saved scope
jumpmux --session       # start with agents from the current tmux session
jumpmux -t sessions     # open the Sessions tab (`--tab` also works)
jumpmux sessions list   # print sessions (`--json` for scripts)
jumpmux sessions open dev
jumpmux sessions last   # switch to the previous tmux session
jumpmux agents list
jumpmux worktrees list
jumpmux --list          # print available contexts
jumpmux setup           # install or update the Pi extension
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
- `default_scope` accepts `all` or `session`. `--session` overrides it for that launch.
- `nerdfont` accepts `true` or `false` and defaults to `true`.
- `[preview]` controls the preview panel independently for `agents`, `worktrees`, and `sessions`. Each defaults to `true`.

`auto` uses Worktrunk when `wt` exists on `PATH`; otherwise it uses `git worktree`. `wt` uses `wt switch --create` and `wt remove`. `git` creates branches from the detected default branch under `<repo>__worktrees/` and keeps the branch after removal.

The `t` and `s` keys update `theme` and `default_scope`. jumpmux validates the configuration before each mutation flow. Removal requires confirmation. jumpmux rejects the primary worktree and any worktree used by a tmux pane. Locked and prunable Git worktrees show explicit indicators. The action menu offers confirmed Git-native cleanup only for an unlocked prunable record.

## Scope

jumpmux reads agent, Git, PR, configured session locations, and tmux session state. It switches tmux panes and sessions, creates reusable tmux windows and configured tmux sessions, adds or removes worktrees, and opens PRs in a browser.

It does not stage patches, run a daemon, provide a command palette, or support a non-tmux backend.
