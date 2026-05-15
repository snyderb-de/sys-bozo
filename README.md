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

Phase 0 is documentation and shape-setting.

- Static docs live in `docs/`.
- The install/profile model is documented in `docs/install.html`.
- No installer is implemented yet.

Open the docs locally:

```sh
open docs/index.html
```

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

Build the smallest useful prototype:

```sh
sys-bozo doctor
sys-bozo plan
sys-bozo install docs
```

That prototype must run without Nix, Homebrew, or Home Manager.

## Tests

Go is the project test harness from the start:

```sh
go test ./...
```

Early tests are smoke tests for project structure, docs drift, and catalog shape. As the installer grows, tests should cover catalog parsing, profile resolution, exclusions, dry-run planning, and filesystem safety.

See [TESTING.md](TESTING.md) for the full test strategy, including fake-home tests, Linux containers, and macOS runner/VM tests.
