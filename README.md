# sys-bozo

Installable workstation profiles for macOS and Linux.

`sys-bozo` is the reusable engine and documentation layer. It should help a user buy a new MacBook, install Linux, or rebuild an existing machine, then choose how much management they want:

- docs only
- shell-lite without Nix, Homebrew, or Home Manager
- Nix CLI tools only
- Home Manager user profile
- Homebrew GUI apps only
- nix-darwin full macOS host
- Linux Home Manager profile

The project must be able to create SOPS/age secret-management scaffolding for a user, but it must never ship or know the user's actual secrets.

## Current State

The control-center TUI, action planner/runner, and guided package-add flow are
implemented. Profile installation and catalog generation are still roadmap
work.

- Static docs live in `docs/`; the install/profile model is in
  `docs/install.html`.
- `sys-bozo doctor` reports host and manager facts.
- `sys-bozo plan` previews profile, update, package, move, tarball, and config
  plans without applying them.
- `sys-bozo run <action>` runs the same maintenance actions exposed by the TUI.
- Running `sys-bozo` with no arguments opens the TUI.

Open the docs locally:

```sh
open docs/index.html
```

Run the development build from the repository:

```sh
./scripts/sys-bozo
```

## Guided Control Center

The Home screen has three entries: `1` Weekly Maintenance, `2` Add Package,
and `3` Inspect System. Use the arrow keys or `j`/`k` to move, `Enter` to open,
`Escape` to go back, and `q` to quit.

For maintenance, open Weekly Maintenance, use `Space` to select one or more
available actions, and press `Enter` to review. The Review screen shows the
exact command queue. Press `Enter` again to confirm or `Escape` to return
without running. The safety rule is simple: review every mutating plan before
the program executes it.

Commands that need a password, prompt, or other native input use an
interactive terminal handoff. The TUI gives the child process the terminal,
then restores the Result screen when it exits. Interactive input is not copied
into sys-bozo's captured output or history.

### Add Package

From Home, press `2` or select Add Package:

1. Enter a query and press `Enter` to search Nix and Homebrew. A failed
   provider is shown as a warning while results from the other provider remain
   usable; Nix is the default when available.
2. Use the arrow keys or `j`/`k` to choose a result, then press `Enter`.
3. Choose shared, platform, or host scope. Unsupported provider/scope pairs
   fail without writing. For supported flat lists, choose the destination
   section; `Misc` is selected when present. Ambiguous supported files use
   `$EDITOR` on a temporary copy instead of guessing.
4. Review the exact file, complete diff, apply command, and verification. Use
   `j`/`k` or `PgUp`/`PgDn` to inspect a long diff. Nothing changes until the
   final `Enter` confirmation.

Confirmation atomically edits the declarative Nix or Homebrew config, applies
the matching Home Manager or nix-darwin action, and verifies the selected
provider. A changed file hash aborts instead of overwriting newer work. If the
apply action fails after the edit, the declaration stays visible and `v` opens
a separately reviewed, hash-gated revert; sys-bozo never silently rolls it
back. Verification failure is reported as failure and also leaves the
declaration in place.

## Boundary

`sys-bozo` owns:

- installer CLI
- tool catalog
- profile definitions
- optional module model
- docs site
- generic macOS/Linux support
- SOPS setup scaffolding

Personal dotfiles repos own:

- actual host choices
- private aliases
- encrypted secrets
- local policy
- machine-specific overrides

## Near-Term Target

Continue from the current prototype toward profile installation:

```sh
sys-bozo doctor
sys-bozo plan
sys-bozo install docs
sys-bozo
```

That prototype must run without Nix, Homebrew, or Home Manager.

The default `sys-bozo` command is the control-center TUI. Maintenance and
package changes use reviewed plans; profile installation, package moves, and
tarball execution remain planned work.

## Tests

Go is the project test harness from the start:

```sh
go test ./...
go vet ./...
```

Tests cover project smoke checks, planning and execution, terminal handoff,
the TUI, package search/edit/apply/verify/revert behavior, and fake-home safety.
Catalog/profile resolution and full installer coverage remain future work.

See [TESTING.md](TESTING.md) for the full test strategy, including fake-home tests, Linux containers, and macOS runner/VM tests.
