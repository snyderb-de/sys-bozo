# Testing Strategy

`sys-bozo` is an installer-shaped project. Testing it on the developer's real machine is not enough, and in some cases it is exactly the wrong thing to do.

The test suite should be layered so most tests are fast and local, while install tests run against disposable homes, containers, or VMs.

## Test Tiers

### Tier 0: Pure Unit Tests

Runs on any machine with Go.

```sh
go test ./...
```

Tests:

- catalog parsing
- profile resolution
- tool exclusions
- host-specific includes
- command planning
- path safety rules
- SOPS template generation logic

These tests must not touch the real home directory, package managers, SSH config, Tailscale, SOPS keys, or system settings.

### Tier 1: Fake Home Integration Tests

Runs locally without a VM.

Implemented tests create temporary homes and repositories, inject command and
package-provider adapters, and verify file output without contacting real
managers. Future installer tests can extend the same boundary.

Example shape for future installer-specific integration tests:

```sh
SYS_BOZO_HOME="$(mktemp -d)" go test ./... -run Integration
```

Tests:

- `install docs` writes only expected files
- `shell-lite` creates XDG paths
- secrets init creates an age key path in the fake home
- dry-run makes no writes
- real-run never writes outside allowed roots

This is the safety net for "do not trash my actual machine."

## Current Package And Terminal Regression Cases

These cases are automated with fixture repositories, fake homes, harmless fake
commands, and injected package-manager responses. They must not use real
Homebrew state, Nix profiles, Home Manager generations, nix-darwin state, or
the developer's config files.

### Fake-repo package edit

```sh
go test ./internal/tui -run TestPackageWorkflowFakeRepoSmoke -count=1 -v
```

The test searches through a fake adapter, reviews a Nix insertion, applies it
only to a temporary `DOTFILES_REPO`, runs a fake rebuild command, and verifies
a fixture executable. It also proves the declaration is byte-for-byte
unchanged before confirmation.

### Fake-repo repository triage

```sh
go test ./internal/repostate -run 'Test(CommitOperationExcludesUnrelatedStagedPath|RepositoryActionsStayPathScopedInRealTempRepos|DeleteUntrackedSymlinkDoesNotFollowTarget)' -count=1 -v
go test ./internal/tui -run 'TestRepo(ActionPreparationIsReadOnlyUntilReview|ReviewStaleValidationRunsNothing|CommitUsesTerminalHandoffAfterValidation|HistoryExcludesPathsMessagesAndDiffs)' -count=1 -v
```

These tests initialize repositories under `t.TempDir`, disable system/global
Git config for fixture commands, and use fixture-only author data. They prove
that unrelated staged or modified paths survive, stale Review executes
nothing, untracked symlinks are removed without following targets, and no
path, diff, or commit message reaches history. They do not use real
credentials, signing configuration, or the developer's repositories.

### Provider partial failure

```sh
go test ./internal/packages -run 'TestSearchKeepsBrewWhenNixFails|TestSearchDiscardsFailedFormulaOutputAndKeepsCask|TestSearchDiscardsFailedCaskOutputAndKeepsFormula|TestSearchDiscardsAllBrewOutputWhenBothSearchesFail' -count=1 -v
```

Search failures are isolated by provider. Usable results survive a failed Nix,
Brew formula, or Brew cask query; output from a failed query is discarded and
the error remains available for the TUI warning.

### Stale hash and revert

```sh
go test ./internal/packages -run 'TestApplyRejectsStaleFileAndPreservesIt|TestApplyRejectsFileChangedWhilePreparingTemporaryFile|TestProposeRevertChecksPostHashAndRestoresExactBytes' -count=1 -v
go test ./internal/tui -run 'TestPackageRebuildFailureOffersHashGatedRevertReviewWithoutWriting|TestPackageRevertReviewRejectsStaleDeclaration|TestReversePackageRebuildRetrySkipsAppliedReverseEdit' -count=1 -v
```

Apply rejects a declaration changed after Review, including a change racing the
atomic temporary-file preparation. Revert is offered only after an applied edit
and failed rebuild, is checked against the exact post-edit hash, and receives
its own diff and confirmation. A stale revert does not write.

### 80x24 and `NO_COLOR`

```sh
NO_COLOR=1 PACKAGE_VISUAL_LOG=1 go test ./internal/tui -run 'TestPackageWorkflowViewsFit80x24AndPreserveNoColorSemantics|TestPackageReviewDiffViewportPreservesAndScrollsFullDiffAt80x24|TestTask5VisualSmokeFitsTargetTerminals|TestTask5NoColorHomeReviewAndResultHaveNoANSI' -count=1 -v
NO_COLOR=1 go test ./internal/tui -run 'Test(RepoWorkflowViewsFit80x24|RepoTriageUnavailableAndDiffRemainTruthfulAt80x24)' -count=1 -v
```

The package and maintenance screens must fit an 80-column by 24-row terminal.
Long package diffs remain scrollable rather than being discarded. With
`NO_COLOR`, semantic labels remain present and output contains no ANSI styling.
Repository FILES, DIFF, delete confirmation, Review, Running, and Result screens
are held to the same boundary.

### Interactive terminal handoff

```sh
go test ./internal/runner ./cmd/sys-bozo ./internal/tui -run 'TestRunInteractiveUsesProvidedStdio|TestRunWorkItemDispatchesInteractiveMode|TestAdvanceQueueUsesTerminalHandoffForInteractiveWork|TestInteractiveHandoffReturnAdvancesToSuccessResult|TestInteractiveFailureStopsQueueAndRestoresDoneState|TestTerminalHandoffCancellationStoresCancelledResult' -count=1 -v
```

These tests prove interactive work receives native stdin/stdout/stderr, avoids
the captured-output scanner, and restores truthful success, failure, or
cancellation state. An optional real-PTY restoration smoke test uses only
`/usr/bin/printf` and a temporary home:

```sh
test_bin="$(mktemp -u "${TMPDIR:-/tmp}/sys-bozo-tui-test.XXXXXX")"
go test -c -o "$test_bin" ./internal/tui
/usr/bin/script -q /dev/null env SYS_BOZO_PTY_SMOKE=1 "$test_bin" \
  -test.run '^TestPTYTerminalHandoffSmoke$' -test.v
rm -f "$test_bin"
```

The test binary must run directly under the pseudo-terminal; `go test` captures
its child stdio and therefore cannot be the command wrapped by `script`. The
test intentionally skips in ordinary non-PTY automation.

### Live package discovery pipeline

```sh
go test -race ./internal/packages ./internal/tui -run 'Test(StartSearch|PackagePipeline|CompletedProvider|PackageTabs)' -count=1 -v
# Run the compiled-binary PTY command from the section above.
```

Provider tests use deterministic fakes. The PTY restoration smoke test invokes
no package manager; it hands the terminal only to `/usr/bin/printf`.

### Tier 2: Linux Container Tests

Runs Linux install flows in Docker, Podman, or a CI container.

Useful for:

- shell-lite on clean Linux
- no-Nix Linux
- Nix-present Linux
- Home Manager-present Linux
- missing dependency behavior

Container tests can validate Linux behavior cheaply. They cannot validate macOS behavior.

### Tier 3: macOS Runner Tests

Runs on a disposable macOS environment, such as:

- GitHub Actions macOS runners
- a local macOS VM
- a dedicated test Mac

Useful for:

- macOS path conventions
- Homebrew detection
- nix-darwin planning
- LaunchServices / app path checks
- macOS-specific shell behavior

These tests should prefer `plan` and fake-home modes. Anything that changes real system defaults must be explicitly isolated.

### Tier 4: Full VM Smoke Tests

Runs a complete install path in a disposable VM.

Useful for:

- "new MacBook" simulation
- "fresh Linux desktop" simulation
- end-to-end bootstrap documentation
- testing failures and re-runs

This is slower and should not be required for every edit.

## What Docker Can And Cannot Do

Docker is good for Linux tests.

Docker is not a macOS test environment. It cannot prove that nix-darwin, Homebrew casks, macOS defaults, or app bundles behave correctly.

For macOS, use macOS runners, a macOS VM, or a spare test machine.

## Safety Rules

Installer tests must support dependency injection for:

- home directory
- config directory
- data directory
- cache directory
- repo directory
- command runner
- OS facts
- package-manager availability

No test should need to mutate:

- the real `$HOME`
- `/etc`
- `/Applications`
- real SSH config
- real SOPS keys
- real Tailscale state
- real Homebrew state
- real Nix profiles

## Test Design Rule

The installer should be built around a planner.

The planner returns a list of intended actions:

- create directory
- write file
- create symlink
- install package
- install cask
- decrypt secret
- run command

Tests inspect the plan first. Execution is a separate layer. This is how sys-bozo stays testable without turning the developer's machine into a lab accident.

## First Test Milestones

1. Keep current smoke tests green.
2. Keep plan, execution, terminal-handoff, TUI, and package workflow tests green.
3. Add catalog parser tests.
4. Add profile resolver tests.
5. Add exclusion tests.
6. Add fake-home install tests.
7. Add Linux container tests.
8. Add macOS runner smoke tests.
