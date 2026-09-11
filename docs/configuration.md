# Configuration

jumpmux reads `~/.config/jumpmux/config.toml`:

```toml
theme = "default"
default_scope = "all"
nerdfont = true

[preview]
agents = true
worktrees = true
sessions = true

[sessions]
exclude = []
discovery_command = ["ghq", "list", "-p"]

[[sessions.entries]]
name = "api"
path = "~/src/api"
```

- `theme` accepts any scheme listed in [Themes and fonts](dashboard.md#themes-and-fonts).
- `default_scope` accepts `all` or `session`.
- `nerdfont` accepts `true` or `false` and defaults to `true`.
- `[preview]` controls the preview panel independently for `agents`, `worktrees`, and `sessions`. Each defaults to `true`.
- `sessions.exclude` accepts regular expressions. Use `^name$` for an exact match.
- `sessions.discovery_command` runs the first array value as an executable and passes the rest as arguments. The command must print one absolute, existing directory per line. jumpmux expands a leading `~` in the executable path and runs the command without a shell.
- Each `sessions.entries` item needs a unique name and an existing path. Paths support environment variables and a leading `~`. Unknown session keys cause an error.

jumpmux uses native Git for worktree actions. It creates branches under `<repo>__worktrees/` from the local default branch and keeps branches after worktree removal. Existing `worktree_backend` settings are no longer supported; remove that key from the configuration file.

The `t` and `s` keys save `theme` and `default_scope`. jumpmux validates the configuration before mutations and asks for confirmation before removal. It rejects the primary worktree and worktrees used by tmux panes. Locked and prunable worktrees have separate indicators. You can prune an unlocked stale record from the action menu.
