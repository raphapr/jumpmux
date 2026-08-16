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
jumpmux worktree add feature/example --detach --json
jumpmux worktree list
jumpmux --list             # print available contexts
jumpmux setup              # install or update the Pi extension
jumpmux --version
jumpmux --help
```

Worktree `remove`, `cleanup`, `rebase`, and `merge`, plus Session `remove`, ask for confirmation. For non-interactive use, append `--yes` as the final argument.
