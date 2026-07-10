# Interactive Monolith TUI Design

Date: 2026-07-10

## Summary

Redesign `sys-bozo` as a plan-first guided workstation control center and fix interactive commands that currently fight Bubble Tea for terminal input. The new interface uses the Monolith layout system—large editorial hierarchy, generous spacing, and thin rules—with the Afterburner palette of graphite, bone, amber, cyan, green, and rust.

The same release adds a declarative package workflow: search Nix and Homebrew, choose provider and scope, preview the exact config diff, apply the appropriate rebuild, and verify the installed package.

## Problem

### Interactive command hang

`runner.StartWork` launches commands with stdout and stderr connected to an `io.Pipe`, but leaves stdin unset. Bubble Tea still owns the terminal in raw mode. Homebrew casks backed by macOS `.pkg` installers can invoke `sudo` and write a password prompt directly to the controlling terminal. The prompt appears over the alternate-screen TUI, while keystrokes remain under Bubble Tea control. Correct passwords can therefore be rejected or never reach the installer correctly.

The iLok License Manager cask exercises this path because its artifact is `License Support.pkg`.

### TUI structure

The current TUI exposes five small tabs and runs an action immediately on Enter. Its 1,227-line `internal/tui/model.go` mixes state transitions, process execution, rendering, configuration editing, audit behavior, and styles. It works, but it does not match the project rule: plan first, apply second.

### Package addition

`plan.PackageSearch` currently describes a future operation. It does not search providers, modify the dotfiles source of truth, apply the selected declaration, or verify the result.

## Goals

- Make terminal password and interactive installer input reliable.
- Ensure password bytes never enter sys-bozo state, logs, or history.
- Require review and confirmation before every mutating action.
- Preserve inline streaming output for commands that do not need terminal control.
- Make the TUI visually distinctive and useful at normal terminal sizes.
- Add packages declaratively through known Nix and Homebrew config lists.
- Preview exact file changes before writing.
- Apply and verify package changes through the correct host workflow.
- Keep non-interactive CLI action parity.
- Preserve XDG state paths and the existing secrets boundary.

## Non-goals

- Building an embedded terminal emulator or general ANSI parser.
- Capturing interactive command output while it owns the terminal.
- Supporting arbitrary Nix expression rewrites.
- Installing undeclared packages as one-off state.
- Automatically choosing host-specific targets when the repository shape is ambiguous.
- Recording terminal input or full command output in history.

## Product Principles

1. **Plan first.** Every mutating action reaches Review before execution.
2. **Shortcuts preselect.** `hms`, `nds`, and similar shortcuts open Select with one checked action; they never bypass Review.
3. **Native terminal for native prompts.** Interactive tools temporarily own stdin, stdout, and stderr.
4. **Declarative packages.** Package addition changes tracked configuration before applying system state.
5. **Never guess at config.** Unsupported or ambiguous edits fall back to `$EDITOR` at the correct file.
6. **Stop on failure.** Mutating steps are not retried automatically.

## Information Architecture

The default screen is a numbered launch surface rather than a tab strip.

1. Weekly Maintenance
2. Add Package
3. Inspect System
   - Config
   - Audit
   - Doctor
   - History

Numeric shortcuts open these destinations. Arrow keys and `j`/`k` move. Enter advances. Escape moves back without mutation. Context-specific controls stay in a persistent footer.

### Guided action flow

All maintenance actions follow one state sequence:

```text
Home -> Select -> Review -> Running -> Result
                            |
                            +-> Native terminal handoff -> Running/Result
```

- **Home:** host health, repo state, update count, last run, and primary actions.
- **Select:** checklist of available work. A direct shortcut starts here with one task selected.
- **Review:** immutable ordered steps, exact command labels, target host, dirty repo warning, and interactive handoff markers.
- **Running:** numbered progress, current operation, elapsed time, and inline output for streamed steps.
- **Native terminal handoff:** TUI suspends while an interactive process owns the terminal, then restores after exit.
- **Result:** success or failure, per-step state and duration, history status, log access, retry/back options.

## Visual System

### Direction

Use Monolith composition with Afterburner colors:

- oversized editorial headings;
- large vertical intervals between conceptual groups;
- thin horizontal rules instead of nested rounded cards;
- numbered operations and right-aligned status words;
- dense details only beneath a strong primary state;
- restrained animation: spinner, progress value, and one active-row treatment.

### Palette roles

- Graphite `#0a0d10`: terminal field.
- Bone `#dae4ea` / bright bone `#f4f7f8`: text and major headings.
- Muted steel `#60717c`: secondary labels and inactive content.
- Amber `#ffcb6b`: attention, counts, and notable data.
- Cyan `#66d9ef`: selection, active operation, and progress.
- Green `#7ee787`: verified success and healthy state.
- Rust `#ff8f70`: danger, failure, dirty state, and terminal handoff.
- Rule `#27343c`: separators and low-emphasis structure.

Colors communicate roles; they are not decoration. Text labels and symbols duplicate every color-only signal.

### Terminal behavior

- Primary layout targets 100 columns and remains useful at 80x24.
- Large headings collapse to smaller single-line forms on narrow terminals.
- Detail panes collapse below summaries rather than forcing horizontal scrolling.
- Unicode symbols have ASCII fallbacks.
- `NO_COLOR` disables color without removing hierarchy.
- Styling honors terminal background and truecolor capability where detectable.

## Execution Architecture

### Work metadata

Add an explicit execution mode to runner steps and work items:

```go
type ExecutionMode int

const (
    ExecutionStreamed ExecutionMode = iota
    ExecutionInteractive
)
```

`brew upgrade` is interactive because cask package installers may request authorization. Other steps that can require native input, including direct `sudo` operations and editors, are also marked interactive. The default remains streamed.

### Streamed steps

Streamed steps retain the existing pipe-based execution and line rendering. They never receive terminal input. Bubble Tea remains active.

### Interactive steps

Interactive work uses `tea.ExecProcess`:

1. Build `exec.Cmd` from the already-reviewed work item.
2. Let Bubble Tea restore the normal terminal.
3. Attach native stdin, stdout, and stderr.
4. Run the child process.
5. Receive its exit result in the Bubble Tea callback.
6. Restore the alternate-screen TUI and advance or stop the reviewed plan.

No password input or interactive output is copied into the TUI log. The TUI displays a pre-handoff warning and a post-handoff result.

### CLI parity

`sys-bozo run <action>` uses the same execution mode. Streamed work prints normally. Interactive work attaches directly to the current terminal. The CLI does not use the pipe runner for interactive work.

### Run plan immutability

Select builds an ordered reviewed plan from task metadata. Review shows that exact plan. Confirm runs the same work item values; commands are not regenerated from mutable state after confirmation.

## TUI Module Boundaries

Keep one Go package while splitting the current large model into focused files:

- `model.go`: state types, constructors, and screen identifiers.
- `update.go`: top-level Bubble Tea message handling and navigation.
- `execution.go`: reviewed plan transitions, streamed commands, and terminal handoff.
- `package_flow.go`: package workflow state and messages.
- `view_home.go`, `view_plan.go`, `view_package.go`, `view_inspect.go`: screen rendering.
- `styles.go`: palette, typography roles, responsive helpers, and `NO_COLOR` behavior.

`internal/runner` remains responsible for task definitions, queue construction, and process setup. `internal/packages` owns package search, declaration edits, and verification.

## Package Addition Flow

```text
Search -> Choose Provider -> Choose Scope/Section -> Review Diff
       -> Apply Edit -> Run Rebuild -> Verify -> Result
```

### Search

- Query Nix and Homebrew through separate adapters.
- Run provider searches concurrently.
- Show source-specific errors without discarding the other source's results.
- Select Nix by default when both providers have a result.
- Distinguish Brew formulae from casks.

### Placement

The user chooses:

- provider;
- scope: shared, platform, or this host when supported;
- an existing section comment within the target list, with `Misc` preselected.

Initial known mappings are:

- Shared Nix package: `home/modules/packages.nix`, `home.packages`.
- macOS Nix package: `home/darwin/default.nix`, `home.packages`.
- Linux Nix package: `home/linux/default.nix`, `home.packages`.
- Shared Brew formula: `homebrew.nix`, `brews`.
- Shared Brew cask: `homebrew.nix`, `casks`.

Host-specific placement is automatic only when sys-bozo can identify one unique supported list in that host file. Otherwise it opens `$EDITOR` with the selected package/provider context and resumes at diff review after the editor exits.

### Safe edit model

The editor is a pure transformation:

```text
original bytes + target list + section + normalized item
    -> proposed bytes + unified diff
```

It must:

- locate exactly one known list assignment;
- preserve existing comments, whitespace, ordering, and unrelated bytes;
- insert a normalized item within the selected section;
- detect already-declared items;
- refuse multiple or malformed target lists;
- produce a diff without writing during Review.

Review records a hash of the original bytes. Apply aborts if the file changed after Review. The final write uses a same-directory temporary file and atomic rename while preserving file permissions.

### Apply

- Nix user-package edits select `hms`.
- Brew declaration edits select `nds` so nix-darwin's Homebrew activation owns installation.
- Package Review is the standard Review state. It shows one immutable combined plan—config edit, rebuild, and verification—and one confirmation runs that plan. There is no second hidden review or confirmation.

### Verification

- CLI packages: confirm expected executable resolves, then run a safe version probe when candidate metadata provides one.
- Brew formulae without a known executable: verify Homebrew receipt.
- Brew casks: verify Homebrew receipt and expected application artifact when metadata provides it.
- Verification failure is a failed result even when rebuild succeeded; it does not silently remove the declaration.

## Failure Handling

- Any failed or cancelled step stops the remaining plan.
- Result shows the failed step, exit status, completed steps, elapsed time, and safe next actions.
- Retry requires another confirmation and begins only at a step whose retry semantics are explicit.
- No mutating step retries automatically.
- `Ctrl+C` during native handoff is delivered to the child; TUI restores after child exit.
- A panic or callback error during restoration falls back to the normal terminal and prints a concise recovery message.
- Search adapter failures remain isolated by provider.
- A stale file hash returns to Review with a clear “file changed” message.
- If a rebuild fails after a package edit, keep the edit visible and offer an exact revert only while the file still matches the post-edit hash.
- Revert itself receives a diff preview and confirmation.

## History and Secrets

History may store:

- action and step identifiers;
- timestamps and durations;
- success, failure, or cancellation;
- target host;
- declaration path for package edits.

History must not store:

- terminal input;
- passwords or tokens;
- environment secret values;
- full interactive output;
- decrypted SOPS content.

Existing history remains under `~/.local/state/sys-bozo/`.

## Testing Strategy

Implementation follows test-driven development.

### Interactive regression

- A failing test first proves an interactive work item selects terminal handoff rather than `StartWork`.
- Command construction tests prove interactive CLI work receives native stdin, stdout, and stderr.
- Bubble Tea transition tests prove success, failure, and cancellation restore the correct Result state.
- A manual pseudo-terminal smoke command confirms an entered value reaches a child process and is not copied into TUI state or logs.

### TUI tests

- State transition tests cover Home, Select, Review, Running, handoff return, and Result.
- Shortcuts prove they preselect rather than execute.
- Render tests cover 80-, 100-, and 140-column layouts.
- `NO_COLOR` output remains legible.
- Failure and terminal-handoff warnings remain visible without color.

### Package tests

- Fixture tests cover each known Nix and Brew list.
- Placement preserves comments, formatting, and unrelated bytes.
- Duplicate items create no change.
- Missing, duplicate, or malformed target lists fail closed.
- Stale hashes prevent writes.
- Atomic writes preserve permissions.
- Exact revert succeeds only against the expected post-edit hash.
- Fake adapters cover Nix-only, Brew-only, both, neither, and one-provider-failed searches.
- Fake apply and verification runners touch no real package manager or user profile.

### Project verification

- `go test ./...`
- `go vet ./...`
- build the binary through the project's Nix development environment or flake package
- manual interactive handoff smoke test
- manual 80x24 and normal-size visual checks

## Success Criteria

- iLok/Homebrew authorization accepts correct terminal input and returns cleanly to sys-bozo.
- No terminal prompt is drawn over an active alternate-screen TUI.
- Every mutating action displays a reviewed plan and requires confirmation.
- Home, package search, running, failure, and result screens follow the approved Monolith/Afterburner visual contract.
- A user can add a supported shared Nix package or Brew formula/cask without manually editing config.
- Exact changes are visible before write, and stale files cannot be overwritten.
- Package application and verification report truthful results.
- Existing CLI actions, audit, doctor, config editing, and history remain reachable.
- Test suite, vet, Nix build, and manual handoff smoke checks pass.
