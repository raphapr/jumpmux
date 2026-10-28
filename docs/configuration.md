# Configuration

jumpmux reads `$XDG_CONFIG_HOME/jumpmux/config.toml`, which defaults to `~/.config/jumpmux/config.toml` on Linux and macOS:

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

[agent]
command = ["pi", "--name", "{branch}", "@{prompt_file}"]
name_command = ["pi", "-p", "--no-session", "--no-tools", "--no-extensions", "--no-skills", "--no-context-files", "--no-prompt-templates", "--no-mcp", "--offline"]
```

- `theme` accepts any scheme listed in [Themes and fonts](dashboard.md#themes-and-fonts).
- `default_scope` accepts `all` or `session`.
- `nerdfont` accepts `true` or `false` and defaults to `true`.
- `[preview]` controls the preview panel independently for `agents`, `worktrees`, and `sessions`. Each defaults to `true`.
- `sessions.exclude` accepts regular expressions. Use `^name$` for an exact match.
- `sessions.discovery_command` runs the first array value as an executable and passes the rest as arguments. The command must print one absolute, existing directory per line. jumpmux expands a leading `~` in the executable path and runs the command without a shell. If the command fails, configured and live sessions still load and the error appears in the footer.
- Each `sessions.entries` item needs a unique name and an existing path. Paths support environment variables and a leading `~`. Unknown session keys cause an error.
- `[agent]` configures [`agent new`](cli.md#start-an-agent-from-a-prompt). The values above are the defaults.
  - `agent.command` starts the agent. It accepts `{prompt}`, `{prompt_file}`, `{branch}`, and `{session}` placeholders; `{session}` is the project's tmux session name. Without a prompt, arguments that contain `{prompt}` or `{prompt_file}` are dropped. If no argument references the prompt, jumpmux appends the prompt text as the last argument.
  - `agent.name_command` chooses the branch name. jumpmux appends one argument with naming instructions and the prompt, then reads the last output line. It must be `<type>/<kebab-slug>`, at most 40 characters, where `<type>` is a commit type such as `feat` or `fix`. To use a smaller model, add `"--model", "anthropic/claude-haiku-4-5"` or another model from `pi --list-models`.
  - Both commands run without a shell. A leading `~` in the executable is expanded. An empty array, an unknown placeholder, or an unbalanced brace in `agent.command` is an error.
- tmux 3.6 and older replace `.` and `:` in session names with `_`, so jumpmux does the same for configured and discovered names on every tmux version. `jumpmux session open my.project` still finds `my_project`.

jumpmux uses native Git for worktree actions. It creates branches under `<repo>__worktrees/` from the local default branch and keeps branches after worktree removal. Existing `worktree_backend` settings are no longer supported; remove that key from the configuration file.

The `t` and `s` keys save `theme` and `default_scope`. jumpmux validates the configuration before mutations and asks for confirmation before removal. It rejects the primary worktree and worktrees used by tmux panes. Locked and prunable worktrees have separate indicators. You can remove an unlocked stale record from the action menu; other stale records stay.

Earlier macOS releases read `~/Library/Application Support/jumpmux/config.toml`. If you have that file, move it to `~/.config/jumpmux/config.toml`.
