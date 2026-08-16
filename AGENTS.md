# AGENTS.md

## Project

jumpmux is a Go 1.24 Bubble Tea dashboard for Git worktrees, live Pi agents, and tmux sessions.

- Keep the executable in the root `main` package.
- Run the complete package with `go run .`, not `go run main.go`.
- Prefer existing helpers and native Git/tmux commands over new abstractions or dependencies.
- Keep terminal output ANSI-width safe and usable without color or Nerd Font glyphs.

## Code Map

- `main.go`: entry point, shared item data, Git discovery, and process helpers.
- `cli.go`: resource command parsing, output, and confirmed CLI actions.
- `dashboard.go`: Bubble Tea state, updates, actions, preview loading, and navigation.
- `dashboard_view.go`: dashboard layout and rendering.
- `dashboard_format.go`: cells, status formatting, ANSI handling, and icons.
- `config.go`, `scope.go`, `theme.go`, `preview_size.go`: persisted user settings.
- `tmux_sessions.go`: session configuration, discovery, activation, rename, and removal.
- `live_agents.go`: Pi lifecycle state and tmux status integration.
- `worktree_actions.go`: confirmed worktree mutations and safety checks.
- `pull_requests.go`, `git_cache.go`: GitHub status lookup and persisted Git/PR caches.
- `extension/jumpmux-status.ts`: embedded Pi lifecycle extension.
- `docs/`: dashboard, CLI, configuration, and development documentation.

## Change Rules

- Make the smallest focused change. Do not add speculative package structure.
- Preserve current selection and reject stale asynchronous responses.
- Revalidate tmux sessions and worktrees immediately before destructive actions.
- Keep Sessions search-first behavior and its plain-text icon fallbacks.
- Do not communicate state through color or Nerd Font glyphs alone.
- Add focused regression coverage for non-trivial branches, parsers, and actions.
- If `extension/jumpmux-status.ts` changes, run `go install .` and `jumpmux setup`; active Pi sessions need `/reload`.

## Validation

Run checks for the changed surface. Before reporting completion, run at least:

```console
gofmt -w <changed-go-files>
go test ./...
go vet ./...
git diff --check
```

For dashboard, concurrency, tmux, Git, dependency, or release-facing changes, also run:

```console
go test -race ./...
golangci-lint run
NO_COLOR=1 go test -count=1 ./...
JUMPMUX_PLAIN=1 go test -count=1 ./...
JUMPMUX_REDUCED_MOTION=1 go test -count=1 ./...
go build ./...
```

Use commit subjects in the form `<type>: <lowercase imperative description>`. Never push unless explicitly requested.
