# TODO

## Phase 0: Shape

- [x] Create standalone `sys-bozo` project folder.
- [x] Copy current HTML docs into `docs/`.
- [x] Document install profiles and optional management model.
- [x] Document testing strategy.
- [x] Pin bagbook-pro SSH state:
  - reachable over Tailscale with lid open
  - closed-lid SSH currently times out
  - revisit clamshell/maintenance-mode power policy later
- [x] Add initial decision log with stable IDs DEC-001 through DEC-012.
- [x] Record DEC-005 partial decision:
  - DisplayLink Manager is Mini-only.
  - Elgato Stream Deck is Mini-only.
- [x] Update decision log with Bag's DEC-001 through DEC-012 answers.
- [x] Add `docs/dec-004.html` Brew CLI decision list.
- [x] Add `docs/dec-005.html` GUI app decision list.
- [x] Record DEC-004 item decisions from Bag:
  - Ansible cleanup.
  - git-filter-repo, ffmpeg, ghostscript, libpst, mole, neonctl, neovim, poppler, supabase, syncthing, and xcodegen shared.
  - Go and Node.js shared as convenience tools; project versions are owned by Nix dev shells plus direnv.
  - OpenJDK as-needed.
  - python-tk project-specific.
- [x] Record DEC-005 item decisions from Bag:
  - Ivanti ignored/manual-private.
  - No Man's Sky, Affinity Designer 2, Affinity Photo 2, Perplexity, and CameraController cleanup.
  - Brave, Firefox Developer Edition, ChatGPT Atlas, GarageBand, MDB tools, Magnet, Overcast, Vivaldi, balenaEtcher, cmux, Asana, Godot, Microsoft Edge, TestFlight, TickTick, and Transmission shared.
  - iLok License Manager shared.
  - Logi Options, Native Access, OpenClaw/Hermes, SABnzbd, and Stats Mini-only.
  - Claude Code URL Handler, Crimson Desert, and Ledger Live MacBook-only.
  - Developer pinned to inspect.
- [ ] Research DEC-006 closed-lid SSH pros/cons before choosing a policy.
- [x] Decide DEC-013 Go/Node project version-management approach:
  - Nix dev shells plus direnv are default for app repos.
  - Runtime versions live with the project.
- [x] Decide DEC-014 global Go/Node policy:
  - shared convenience tools on both Macs.
  - not project truth.
- [x] Decide DEC-015 Java/OpenJDK policy:
  - as-needed.
  - do not remove existing installs blindly.
  - do not mark shared/global unless a workflow requires it.
- [ ] Choose docs build strategy:
  - likely Tailwind or UnoCSS
  - compiled locally
  - no CDN dependency
- [ ] Draft data schemas:
  - `catalog/tools.yaml`
  - `catalog/profiles.yaml`
  - `catalog/hosts.example.yaml`
  - `catalog/secrets.yaml`

## Phase 1: Bootstrap

- [x] Add Go test harness.
- [x] Keep `go test ./...` green from the beginning.
- [ ] Build installer around a planner/action model before writing mutating commands.
- [x] Build TUI around the same planner/action model, not separate shell scripts.
- [ ] Add fake-home test support.
- [x] Create `scripts/sys-bozo`.
- [x] Implement `sys-bozo doctor`.
- [x] Implement `sys-bozo plan`.
- [ ] Implement `sys-bozo install docs`.
- [ ] Ensure Phase 1 works without:
  - Nix
  - Homebrew
  - Home Manager
  - nix-darwin
- [ ] Keep all writes under repo, `~/.config`, `~/.local`, or explicitly selected target paths.

## Phase 1.5: Control Center TUI

- [x] Document the control-center target in `docs/control-center.html`.
- [x] Use Go with Bubble Tea, Bubbles, and Lip Gloss.
- [x] Add `sys-bozo` default TUI entrypoint.
- [x] Add non-interactive command parity for every TUI action — `sys-bozo run <action>`.
- [x] Preview exact maintenance/package plans and require confirmation before execution.
- [x] Preserve native interactive input with terminal handoff and restore the TUI afterward.
- [x] Show exact dirty repository entries with FILES/DIFF triage, multi-select,
  stale Review validation, and path-scoped commit/stash/restore/untracked delete.
- [ ] Build dashboard stats:
  - host facts
  - repo dirty state
  - Nix/Homebrew presence
  - package counts
  - pending Brew updates
  - last audit status
- [ ] Build update picker:
  - Brew update only
  - Brew selected upgrades
  - Brew autoremove
  - Nix flake update by input/scope
  - Home Manager apply
  - nix-darwin apply
- [x] Build package search:
  - search Nix
  - search Brew
  - compare results and preserve one provider when the other fails
  - choose placement scope and reject unsupported provider/scope pairs
  - open/edit the chosen config instead of hiding changes behind early generated variables
- [ ] Build package move plans:
  - Brew to Nix
  - Nix to Brew
  - verify new provider before removing old provider
- [ ] Build tarball installer:
  - default to `~/.local/opt/<name>/<version>`
  - symlink into `~/.local/bin`
  - write uninstall manifest
  - require explicit target path for anything outside XDG/local paths
- [ ] Build config editor launcher:
  - zsh
  - ssh
  - git
  - starship
  - atuin
  - Home Manager
  - nix-darwin
- [ ] Add validation after config edits.
- [ ] Add package verification after config edits:
  - `command -v <tool>`
  - version check when available
  - provider path check for Brew-to-Nix or Nix-to-Brew moves
- [ ] Save run history under `~/.local/state/sys-bozo/`.
- [ ] Defer clever Nix generators until catalog/profile tests are strong.

## Phase 2: Catalog And Profiles

- [ ] Add Go tests for catalog parsing.
- [ ] Add Go tests for profile defaults.
- [ ] Add Go tests for tool/group exclusions.
- [ ] Add Go tests for host-specific includes.
- [ ] Add host buckets:
  - shared baseline
  - mini-only
  - macbook-only
  - private/manual
  - cleanup candidate
- [ ] Build the tool catalog.
- [ ] Build profile resolution:
  - include defaults
  - apply exclusions
  - apply host-specific includes
- [ ] Add optional tool groups:
  - `fun-tools`
  - `tui-tools`
  - `git-tools`
  - `gui-apps`
- [ ] Generate dry-run output for:
  - shell-lite
  - Nix CLI
  - Home Manager
  - Homebrew casks
  - nix-darwin
  - Linux Home Manager

## Phase 3: Secrets

- [ ] Implement `sys-bozo secrets doctor`.
- [ ] Implement `sys-bozo secrets init`.
- [ ] Detect or install `age` and `sops` where appropriate.
- [ ] Generate age key under `~/.config/sops/age/keys.txt`.
- [ ] Generate `.sops.yaml` from the user's age public key.
- [ ] Create starter encrypted secret templates:
  - SSH private config
  - audit hosts
  - optional env secrets
- [ ] Never store secret values in this repo.

## Phase 4: Extraction Quality

- [ ] Add tests for catalog/profile resolution.
- [ ] Add tests for dry-run filesystem safety.
- [ ] Add tests proving no command mutates outside allowed paths without explicit opt-in.
- [ ] Add Linux container smoke tests.
- [ ] Add macOS runner or VM smoke tests.
- [ ] Add docs screenshots or visual checks.
- [ ] Add install dry-run fixtures.
- [ ] Decide first release shape.
