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

### Mac mini updates

On `bags-Mac-mini` (including its local DNS suffix), Home opens **Updates**.
Press `A` to select the recommended options, use arrows or `j`/`k` to read each
description, and use `Space` to adjust the selection. Press `Enter` to review;
selection and readiness checks never execute an updater.

The recommended sequence is:

1. Update Nix version pins in the dotfiles `flake.lock`.
2. Refresh Homebrew metadata.
3. Build and apply the Mini's nix-darwin configuration, including Home Manager
   and the dotfiles' interactive Homebrew review.
4. Run Topgrade with native terminal input and the user's existing configuration.
5. Query the system profile and check for missing Homebrew formula dependencies.

Topgrade is recommended when available. Bozo adds exclusions for Nix, Home
Manager, Homebrew, system updates, restart checks, and remote hosts; it does not
rewrite Topgrade configuration or force `--yes`. The Mini's workstation rebuild
already owns the Homebrew pass. Selecting it replaces **Upgrade Homebrew only**,
so a later upgrade cannot reverse choices declined during activation.

Standalone Homebrew upgrades exclude DisplayLink. **Upgrade DisplayLink** and
**Remove unused dependencies** require their own selections. The workstation
rebuild can still offer DisplayLink and undeclared-package removals in the
dotfiles' existing native prompts; bozo does not change that activation script.
`Tab` switches between Updates and Recovery, clearing the prior selection.
Rollback does not revert the lock file or guarantee rollback of Homebrew apps.

Review shows each step's purpose, command, working directory, and terminal
handoff. Scroll with arrows, `j`/`k`, or Page Up/Down. Readiness checks verify
required commands and Git status; conflicts or unavailable status block the run.
Changed files are disclosed, and a changed Git status at confirmation requires
another review. These checks are not a filesystem lock or a hash of every source
file. Result distinguishes completed, failed, and unrun steps. Retry reviews only
the failed step and remaining queue, with fresh readiness checks.

The final checks establish command completion, a queryable system profile, and
Homebrew dependency status. They do not prove every application works or that
every upgrade offered by the interactive activation was accepted.

CLI uses the same Mini plan:

```sh
sys-bozo plan update
sys-bozo plan update nix-update nds topgrade
sys-bozo run recommended
```

`run` is an explicit execution command; use `plan` or the TUI first to review.
For compatibility, Mini selections `all`, `ndu`, `hmu`, and `hms` expand into the
same system-owned workflow without duplicate input updates or a separate Home
Manager apply. Other machines retain their existing workflows pending drift
reconnaissance.

### Existing workflows

The Home screen has three launch entries: `1` Weekly Maintenance, `2` Add
Package, and `3` Inspect System. When the detected dotfiles repository is dirty
or Git status is unavailable, its status row also becomes selectable. Use the
arrow keys or `j`/`k` to move, `Enter` to open, `Escape` to go back, and `q` to
quit.

For maintenance, open Weekly Maintenance, use `Space` to select one or more
available actions, and press `Enter` to review. The Review screen shows the
exact command queue. Press `Enter` again to confirm or `Escape` to return
without running. The safety rule is simple: review every mutating plan before
the program executes it.

Commands that need a password, prompt, or other native input use an
interactive terminal handoff. The TUI gives the child process the terminal,
then restores the Result screen when it exits. Interactive input is not copied
into sys-bozo's captured output or history.

On macOS, normal `brew` and combined `all` maintenance upgrade formulae and an
explicit list of outdated casks with DisplayLink excluded. When DisplayLink is
outdated, a separate `displaylink` action appears and remains unchecked until
selected. Its Review shows `brew upgrade --cask displaylink`; confirmation uses
the interactive terminal because the installer may require a password and
reboot.

### Repository Triage

Open the highlighted repository row on Home, or choose Repository under
Inspect System. `REPO/TRIAGE` lists every exact porcelain-v2 Git entry; a Git
failure is shown as `STATUS UNAVAILABLE`, never as a clean worktree.

- Use `j`/`k` or Up/Down to move and `Space` to select multiple entries.
- Press `Enter` for the selected file's staged/unstaged diff; `Tab` switches
  between FILES and DIFF.
- `C` commits only selected paths, `S` stashes only selected paths, and `R`
  restores selected tracked paths.
- `D` is available only for untracked selections. It shows `git clean -nd`
  output and requires typing `DELETE UNTRACKED` before Review.
- Conflicts remain inspect-only. Resolve them in your editor, refresh with
  `Shift+R`, then select the resulting non-conflicted entries.

Every action builds an immutable Review with exact paths and argv. Confirmation
rechecks the complete Git status plus selected file bytes or symlink text. A
stale Review runs nothing. Commits use native terminal handoff for hooks,
signing, and credential prompts; history stores only the action kind and count.
All repository action paths and diff/delete previews use literal Git pathspecs,
so filenames containing wildcards or pathspec syntax cannot select other files.

### Add Package

From Home, press `2` or select Add Package:

1. Enter a query and press `Enter` to search Nix and Homebrew. A failed
   provider is shown as a warning while results from the other provider remain
   usable; Nix is the default when available.
2. Use the arrow keys or `j`/`k` to choose a result, then press `Enter`.
3. Choose shared, platform, or host scope. Homebrew host scope maps formulae
   and casks to the detected macOS host's `extraBrews` and `extraCasks`;
   Homebrew platform scope remains unsupported. Other unsupported
   provider/scope pairs fail without writing. For supported flat lists, choose
   the destination section; `Misc` is selected when present. Ambiguous
   supported files use `$EDITOR` on a temporary copy instead of guessing.
4. Review the exact file, complete diff, apply command, and verification. Use
   `j`/`k` or `PgUp`/`PgDn` to inspect a long diff. Nothing changes until the
   final `Enter` confirmation.

Confirmation atomically edits the declarative Nix or Homebrew config, applies
the matching Home Manager or nix-darwin action, and verifies the selected
provider. The declaration target is protected against arbitrary atomic
concurrent edits with inode, mode, hash, and exchange checks; conflicts abort
and retain recovery evidence instead of silently installing an unrecognized
inode. Proposal and recovery files live in a random adjacent same-filesystem
staging directory with mode `0700`, and the target path remains present through
every exchange. If the process stops, the target can contain old or new bytes
and that private staging directory may remain for manual recovery.

The staging paths are process-owned. Portable POSIX filesystems do not provide
an unlink-if-inode primitive, so hostile same-UID mutation inside those private
internal paths is outside this guarantee. Cleanup still checks the recorded
identity immediately before removal; a detected mismatch is retained and its
path is reported. A stale proposal can be briefly visible before validation
rolls it back. If the
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

For the pinned Nix build, run `nix build`. The committed `flake.lock` fixes the
package set used by that build.

Tests cover project smoke checks, planning and execution, terminal handoff,
the TUI, package search/edit/apply/verify/revert behavior, and fake-home safety.
Catalog/profile resolution and full installer coverage remain future work.

See [TESTING.md](TESTING.md) for the full test strategy, including fake-home tests, Linux containers, and macOS runner/VM tests.
