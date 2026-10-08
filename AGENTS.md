# Repository Guidelines

## Project Structure & Module Organization

`sys-bozo` is a Go workstation control center for macOS and Linux.

- `cmd/sys-bozo/`: CLI entry point and command tests.
- `internal/tui/`: Bubble Tea models, updates, views, and Charm styling.
- `internal/plan/`, `runner/`, and `system/`: planning, execution, and host discovery.
- `internal/packages/`, `repostate/`, `fileedit/`, and `history/`: package workflows, Git safety, file edits, and run records.
- `catalog/`: YAML tool, profile, host, and secret-scaffolding definitions.
- `docs/`: static documentation, assets, design specifications, and implementation plans.
- `scripts/`: local launcher, terminal regression harnesses, and render previews.

Keep Go tests beside their implementation in `*_test.go`. Put disposable builds and previews in ignored `.tmp/`.

## Build, Test, and Development Commands

- `./scripts/sys-bozo`: run the local TUI through `go run`.
- `go build -o .tmp/sys-bozo ./cmd/sys-bozo`: build locally; create `.tmp/` first.
- `go test ./...`: run the Go test suite.
- `go vet ./...`: check common Go mistakes.
- `nix build --no-link`: verify the reproducible package without activating it.
- `git diff --check`: check patch whitespace.

For keyboard changes, compile and exercise the terminal fixture:

```sh
go test -c -o .tmp/keyboard-tests ./internal/tui
python3 scripts/keyboard-pty-smoke.py .tmp/keyboard-tests --color
```

See `TESTING.md` for isolated Herdr testing and visual fixtures.

## Coding Style & Naming Conventions

Use `gofmt` for Go formatting and tabs. Follow existing lowercase package names, exported `MixedCaps` identifiers, and descriptive `TestBehavior` test names. Keep platform-specific code in `_darwin.go` or `_linux.go` files.

Keep blocking host probes outside Bubble Tea `Update`; return asynchronous commands. Preserve global Ctrl-C handling and Escape navigation across overlays and small terminals.

## Testing Guidelines

Use Go's standard `testing` package, temporary repositories/homes, and injected providers. Test observable behavior and failure paths. Never use real maintenance, credentials, or system activation as routine test fixtures. For TUI changes, verify 80×24 layouts, `NO_COLOR`, resizing, and terminal handoff.

## Commit & Pull Request Guidelines

Follow existing Conventional Commit subjects: `fix(tui): ...`, `feat(packages): ...`, or `docs: ...`. Keep commits focused. PRs should explain the problem, resulting behavior, relevant issues, and verification commands. Include rendered examples for visual changes and distinguish local builds from installation or activation.

## Operator & Configuration Safety

Announce intended actions before executing them. Prefer Nix tooling and XDG configuration/state paths. Preserve unrelated work and reviewed-action safeguards. Never commit decrypted secrets or include sensitive input in logs.
