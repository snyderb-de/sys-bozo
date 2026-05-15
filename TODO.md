# TODO

## Phase 0: Shape

- [x] Create standalone `sys-bozo` project folder.
- [x] Copy current HTML docs into `docs/`.
- [x] Document install profiles and optional management model.
- [x] Document testing strategy.
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
- [ ] Keep `go test ./...` green from the beginning.
- [ ] Build installer around a planner/action model before writing mutating commands.
- [ ] Add fake-home test support.
- [ ] Create `scripts/sys-bozo`.
- [ ] Implement `sys-bozo doctor`.
- [ ] Implement `sys-bozo plan`.
- [ ] Implement `sys-bozo install docs`.
- [ ] Ensure Phase 1 works without:
  - Nix
  - Homebrew
  - Home Manager
  - nix-darwin
- [ ] Keep all writes under repo, `~/.config`, `~/.local`, or explicitly selected target paths.

## Phase 2: Catalog And Profiles

- [ ] Add Go tests for catalog parsing.
- [ ] Add Go tests for profile defaults.
- [ ] Add Go tests for tool/group exclusions.
- [ ] Add Go tests for host-specific includes.
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
