# Command line

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
jumpmux agent new "fix the flaky cache test"
jumpmux worktree add feature/example --detach --json
jumpmux worktree list
jumpmux --list             # print available contexts
jumpmux setup              # install or update the Pi extension
jumpmux --version
jumpmux --help
```

Worktree `remove`, `cleanup`, `rebase`, and `merge`, plus Session `remove`, ask for confirmation. For non-interactive use, append `--yes` as the final argument.

## Start an agent from a prompt

```console
jumpmux agent new <prompt> [--branch <name>] [--repo <path>] [--background]
```

`agent new` turns a prompt into a running agent in four steps:

1. It runs `agent.name_command` to choose a branch name such as `fix/flaky-cache-test`. `--branch` skips this step.
2. It creates the branch and worktree from the local default branch, like `worktree add`.
3. It finds the project's tmux session: a live session started at the repository root, else the configured or discovered session for that root, else a session named after the repository. It creates that session at the root if it is not running.
4. It opens a window named after the branch in that session, starts `agent.command` there with the prompt, and switches to the window. Outside tmux, it attaches instead.

- `--repo` uses another repository instead of the current one.
- `--background` creates everything without switching and prints the agent's pane ID, which `jumpmux agent open` accepts.
- If the naming command fails, takes longer than 10 seconds, or returns an invalid name, jumpmux warns and uses `agent/<words-from-prompt>`.
- If the branch or worktree path already exists, jumpmux appends `-2`, `-3`, and so on to both.
- If a step fails after the worktree exists, jumpmux keeps the worktree and reports the error.

The agent window runs `jumpmux agent-exec <id>` through your tmux `default-shell` (or `$SHELL`) as an interactive shell, so your PATH and tool activations apply. When the agent exits, the pane keeps an interactive shell in the worktree. `agent-exec` reads a private launch file, deletes it, and replaces itself with the agent, so the command works in any shell. A launch ID works once and expires after an hour. Prompt files stay in `$XDG_STATE_HOME/jumpmux/prompts/` (default `~/.local/state/jumpmux/prompts/`) for 7 days.
