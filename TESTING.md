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

The test creates a temporary fake home, points sys-bozo at it, and verifies file output.

Example future shape:

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
2. Add catalog parser tests.
3. Add profile resolver tests.
4. Add exclusion tests.
5. Add dry-run planner tests.
6. Add fake-home install tests.
7. Add Linux container tests.
8. Add macOS runner smoke tests.
