# OS-Aware Package Search Design

Date: 2026-07-10
Status: Approved

## Goal

Make package discovery visually informative and operationally truthful. The TUI must show what host was detected, which package sources are being searched, real per-provider progress, and provider-separated results that become usable as soon as each provider completes. Selecting a result must end in a reviewed declarative catalog change, not an unrecorded one-off install.

The selected visual direction is **Staged Pipeline**, using the existing Monolith spacing and Afterburner semantic palette.

## Host Awareness

Host identity comes from live runtime facts, not inference from the user's Nix declarations.

- macOS selects Homebrew as the native provider.
- Fedora selects DNF (`dnf5` or `dnf`).
- Debian and Ubuntu select APT.
- Nix is included wherever its executable is available.
- Exactly one detected native provider is presented alongside Nix.
- If a native provider is recognized but unsupported or unavailable, its tab remains visible and disabled with a specific reason.

Weekly Maintenance continues to use the same principle: Linux distribution identity is read from `/etc/os-release`, then executable discovery determines whether the matching maintenance task is available.

## Search Pipeline

The search screen has four visible stages:

1. **DETECT** — show OS, architecture, Nix availability, and the detected native manager.
2. **DISPATCH** — start all available provider searches concurrently.
3. **RANK** — parse, normalize, and rank each provider's candidates independently.
4. **PRESENT** — unlock each provider tab as soon as its candidates are ready.

Provider rows move through typed phases such as `STARTING`, `QUERYING INDEX`, `PARSING`, `DONE`, `FAILED`, `CANCELLED`, and `TIMED OUT`. Candidate counts and elapsed time are shown when known.

Animation is cosmetic and driven by Bubble Tea ticks. Provider phase, count, completion, and failure are driven only by real search events. The UI must not invent percentage completion for commands that do not expose it.

## Result Tabs

Tabs are provider-specific and host-aware: for example `NIX` + `HOMEBREW` on macOS, `NIX` + `DNF` on Fedora, and `NIX` + `APT` on Debian or Ubuntu.

- A provider tab unlocks immediately when that provider completes, even if another provider is still searching.
- `Tab` and `Shift+Tab` switch among enabled tabs.
- `j`/`k` and arrow keys move within the active provider's results.
- `Enter` continues to package placement with the selected provider candidate.
- Each tab preserves its own selected row and scroll offset.
- Nix is initially active when available; otherwise the native provider is active.
- Disabled tabs show their reason and cannot receive selection focus.
- Failed tabs remain selectable so the user can read the sanitized error and retry guidance.
- `Esc` cancels unfinished searches. Results already received remain visible until the user leaves or starts a new query.

Provider lists are never merged. Ranking and default selection remain local to each provider so a candidate from one ecosystem cannot silently outrank or impersonate another.

## Declarative Placement

The package catalog becomes implemented project data rather than a documentation-only sketch. A selected candidate produces one logical tool entry in `catalog/tools.yaml`, preserving its exact provider-native ID under `nix`, `brew`, `brewCask`, `dnf`, or `apt`.

The default placement is **catalog + current host**:

- create or extend the logical tool entry in `catalog/tools.yaml`;
- add the logical tool key to the current host's `include` list in `catalog/hosts.yaml`;
- preserve existing mappings when the same logical tool already has another provider;
- reject conflicting provider IDs, duplicate membership, malformed YAML, missing host identity, and ambiguous logical keys before Review.

The placement screen also offers a deliberate profile-wide scope. That scope adds the current host profile to the tool's `defaultProfiles` instead of adding a host-local `include`. No package is made globally shared merely because it was discovered on one machine.

The catalog editor uses `yaml.v3` node graphs rather than map re-marshalling. It preserves comments, key order, scalar style, and unrelated nodes, and emits deterministic changes only to the selected tool and host/profile membership. The Review screen shows catalog, host/profile, and provider-declaration diffs as one immutable operation.

Because placement may touch multiple files, apply uses a multi-file compare-and-swap transaction:

- every target's original identity and hash is captured in the immutable Review;
- a repo-local advisory lock prevents cooperating sys-bozo instances from applying overlapping transactions;
- every target is revalidated and staged before any exchange begins;
- staged files use private adjacent same-filesystem directories and the existing identity-aware exchange protocol;
- if any exchange fails, already-exchanged targets are rolled back only when identities still match;
- ambiguous concurrent replacements are never deleted or overwritten; recovery artifacts and paths are retained and reported;
- revert is a new reviewed multi-file transaction against the exact applied hashes.

POSIX cannot provide a single atomic commit spanning several files. The guarantee is therefore fail-closed, identity-safe, and recoverable—not fictional cross-file atomicity.

Applying the declaration converges the selected provider:

- Nix and Homebrew continue through their existing declaration targets and rebuild/verification pipeline, with the catalog edit included in the same reviewed operation.
- DNF and APT use the catalog as the source of truth, then run an exact review-gated native install command for the selected ID (`dnf`/`dnf5 install` or `apt-get install`). Password entry receives the existing native terminal handoff.
- Native verification uses provider receipts (`rpm -q` for DNF-family packages and `dpkg-query` for APT-family packages), not guessed executables.
- If convergence or verification fails after catalog apply, the Result screen offers a separately reviewed, hash-gated catalog revert. It never silently removes the declaration or package.

## Architecture

### Provider-neutral session

`internal/packages` exposes a provider-neutral search session. It accepts host capabilities, a query, provider adapters, and a context, and emits typed events:

- provider discovered or unavailable;
- provider phase changed;
- provider candidates ready;
- provider failed;
- provider cancelled or timed out;
- session finished.

Events include a provider identity and request identity. They contain sanitized operational metadata only; command output is parsed inside the adapter and is not copied wholesale into TUI history.

### Provider adapters

Each adapter owns command construction, execution, cancellation, and output parsing for one provider:

- Nix adapter;
- Homebrew adapter;
- DNF adapter;
- APT adapter.

Adapters return normalized candidates while preserving provider-native package IDs and kinds needed by the existing declarative proposal and verification flow. Native search support does not imply direct installation: package changes remain review-gated and declarative.

### Catalog domain

A focused catalog module owns parsing, validation, proposal generation, and host/profile membership. It does not execute package managers. Provider execution remains behind runner/verification interfaces so catalog edits can be tested as pure transformations.

The catalog module supports the existing schema and adds `dnf` and `apt` provider keys. Existing `groups`, `category`, `defaultProfiles`, `platforms`, `optional`, and `config` data must survive a package edit unchanged.

### TUI state

The TUI keeps one state record per presented provider:

- identity and display label;
- availability and disabled reason;
- current phase and phase detail;
- start time and elapsed time;
- candidates and sanitized failure;
- selected row and scroll offset.

The current aggregate `packageSearchMsg` becomes incremental provider/session messages. Existing request IDs continue to reject late results from cancelled or superseded searches.

### Rendering

The package view renders the Staged Pipeline during search and a tab strip during both search and result browsing. Completed results and active provider progress may coexist on screen. Rendering remains responsive at 80×24 and uses semantic labels in `NO_COLOR` mode rather than relying on color or motion alone.

## Cancellation and Errors

- Search uses a shared session context with provider child contexts.
- Starting a new search cancels the prior session.
- `Esc` cancels unfinished adapters without erasing completed provider results.
- A provider failure never discards another provider's candidates.
- Cancellation, timeout, unsupported, unavailable, and command failure are distinct states.
- User-facing errors are sanitized for control characters and bounded to the available layout.
- No search event or history record may contain tokens, passwords, decrypted secret values, or unbounded raw subprocess output.
- Native install commands are created only from validated provider IDs and argument arrays; package text is never interpolated through a shell.
- The complete catalog/host diff and exact install/apply command are immutable once Review is entered.

## Testing

All automated search tests use fake provider adapters and temporary repositories. They must not invoke real package repositories, mutate package-manager state, or modify real dotfiles.

Required coverage:

- macOS selects Nix and Homebrew;
- Fedora selects Nix and DNF;
- Debian/Ubuntu selects Nix and APT;
- unsupported or missing native manager produces a disabled tab with a reason;
- Nix-only and native-only hosts select the first available provider;
- provider events may complete out of order;
- completed results are browsable while another provider is active;
- tabs preserve independent selection and scroll state;
- one provider failure preserves other results;
- cancel, timeout, retry, and superseded-request events remain distinct and stale-safe;
- provider-native IDs survive normalization into declarative placement;
- existing catalog YAML parses and round-trips without losing unrelated fields or comments;
- catalog + current-host placement is the default and profile-wide placement is explicit;
- conflicting IDs, duplicate membership, unknown host, malformed YAML, and stale files fail before mutation;
- DNF and APT apply commands use exact argument arrays and native terminal handoff;
- DNF verifies with a fake `rpm -q` receipt and APT with a fake `dpkg-query` receipt;
- failed native convergence offers a reviewed hash-gated catalog revert;
- 80×24 views remain usable;
- `NO_COLOR` output contains no ANSI and retains all semantic states;
- a real-PTY smoke confirms animation ticks do not corrupt terminal restoration.

## Delivery Milestones

The feature is implemented as three gated milestones on one branch:

1. **Live discovery** — host capability selection, Nix/Homebrew/DNF/APT adapters, incremental events, Staged Pipeline animation, and provider tabs. Native results may be inspected but cannot be confirmed until catalog placement is present.
2. **Declarative catalog placement** — catalog parser/validator, logical-tool mapping, current-host/profile scope, immutable multi-file Review, identity-safe apply, and reviewed revert.
3. **Native convergence** — review-gated DNF/APT commands, terminal handoff, receipt verification, history/result integration, and complete end-to-end fake-repository tests.

No milestone is considered the delivered feature by itself. The branch is merge-ready only after all three pass the full safety and visual gates.

## Non-Goals

- Unrecorded imperative installation through DNF, APT, Homebrew, or Nix.
- Searching every installed package manager instead of the detected native one.
- Inferring the running OS from repository layout or Nix host declarations.
- Fabricated percentages or simulated provider completion.
- Supporting every Linux distribution in the first implementation. Recognized unsupported managers remain visible with an explanatory disabled state.
