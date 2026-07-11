# Declarative Catalog Placement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn a selected provider candidate into a reviewed `catalog/tools.yaml` entry plus current-host or profile membership, applied through a recoverable identity-safe multi-file transaction.

**Architecture:** First invert the existing dependency so neutral `internal/fileedit` owns the hardened single-file CAS engine and `internal/packages` wraps it. Build a transaction coordinator over prepared file edits, then use `yaml.v3` node graphs to propose minimal catalog/host changes without losing comments or unrelated schema.

**Tech Stack:** Go 1.24.2, `gopkg.in/yaml.v3`, `golang.org/x/sys/unix`, SHA-256, existing Darwin/Linux atomic exchange primitives, Bubble Tea review/viewport flow.

**Prerequisite:** Complete `2026-07-11-live-os-aware-package-discovery.md`; this plan consumes DNF/APT candidates and provider-local TUI state defined there.

## Global Constraints

- Default placement is catalog plus the current host's `include` list.
- Profile-wide placement must be explicit and updates `defaultProfiles` instead of host `include`.
- Preserve exact provider-native IDs under `nix`, `brew`, `brewCask`, `dnf`, or `apt`.
- Preserve existing provider mappings and unrelated catalog/host YAML nodes, comments, order, and scalar style.
- Reject malformed YAML, missing host identity, ambiguous logical keys, conflicting provider IDs, duplicate paths, and paths outside the repository before mutation.
- Review must contain immutable hashes and complete diffs for every file.
- Revalidate and stage every target before the first exchange.
- Cross-file apply is fail-closed, identity-safe, and recoverable; it is not advertised as POSIX-atomic.
- Never delete or overwrite an ambiguous concurrent replacement.
- Retain and report recovery artifacts when automatic rollback cannot be verified.
- Revert is a separate reviewed transaction against exact applied hashes.
- Tests use temporary repositories only and never touch real dotfiles or package managers.
- No secrets, decrypted values, or private aliases may enter catalog fixtures, errors, or history.

## File Map

- `internal/fileedit/types.go`: neutral proposals, prepared edits, applied edits, and transaction results.
- `internal/fileedit/apply.go`: hardened single-file prepare/commit/rollback CAS implementation.
- `internal/fileedit/rename_darwin.go`, `rename_linux.go`: platform exchange primitives moved from `internal/packages`.
- `internal/fileedit/transaction.go`: validation, repo lock, prepare-all, deterministic commit, rollback, and revert proposal.
- `internal/fileedit/apply_test.go`, `transaction_test.go`: identity races and multi-file failure recovery.
- `internal/packages/apply.go`: compatibility wrappers around `fileedit`.
- `internal/packages/types.go`: package-facing aliases/conversions only.
- `internal/catalog/types.go`: catalog schema vocabulary and placement request/result.
- `internal/catalog/parse.go`: `yaml.Node` parsing/navigation/validation.
- `internal/catalog/place.go`: pure tools/hosts proposal transformation.
- `internal/catalog/parse_test.go`, `place_test.go`: real schema fixtures and preservation tests.
- `go.mod`, `go.sum`, `flake.nix`: direct YAML dependency and updated Nix vendor hash.
- `internal/tui/package_flow.go`, `model.go`, `update.go`: catalog placement and transaction lifecycle.
- `internal/tui/view_package.go`: host/profile scope and multi-file diff Review.
- `internal/tui/model_test.go`: no-write-before-confirm, stale transaction, and reviewed revert.
- `TESTING.md`: safe fake-repository catalog workflow.

---

### Task 1: Make `fileedit` Own the Hardened Single-File CAS Engine

**Files:**
- Create: `internal/fileedit/types.go`
- Create: `internal/fileedit/apply.go`
- Create: `internal/fileedit/apply_test.go`
- Create: `internal/fileedit/rename_darwin.go`
- Create: `internal/fileedit/rename_linux.go`
- Modify: `internal/fileedit/fileedit.go`
- Modify: `internal/packages/apply.go`
- Modify: `internal/packages/types.go`
- Modify: `internal/packages/apply_test.go`
- Delete: `internal/packages/rename_darwin.go`
- Delete: `internal/packages/rename_linux.go`

**Interfaces:**
- Produces: neutral `fileedit.Proposal`, `PreparedEdit`, `AppliedEdit`, `Prepare`, `Apply`, `ProposeReplacement`, and `ProposeRevert`.
- Preserves: current package/config CAS behavior, recovery paths, modes, and race tests.
- Removes: `internal/fileedit -> internal/packages` dependency.

- [ ] **Step 1: Write a dependency-direction and compatibility test**

Add `internal/fileedit/apply_test.go`:

```go
func TestApplyPreservesModeAndRejectsStaleBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte("old\n"), 0o640); err != nil { t.Fatal(err) }
	proposal := ProposeReplacement(path, []byte("old\n"), []byte("new\n"))
	if err := os.WriteFile(path, []byte("raced\n"), 0o640); err != nil { t.Fatal(err) }
	if _, err := Apply(proposal); !errors.Is(err, ErrStaleFile) { t.Fatalf("err=%v", err) }
	got, _ := os.ReadFile(path)
	if string(got) != "raced\n" { t.Fatalf("target overwritten: %q", got) }
}
```

Add a repository test that reads `internal/fileedit/*.go` and fails if any file imports `internal/packages`.

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./internal/fileedit -run 'TestApplyPreservesMode|TestFileeditDoesNotImportPackages' -count=1 -v`

Expected: dependency-direction test fails because `fileedit.go` imports packages.

- [ ] **Step 3: Move the neutral types and CAS implementation**

Use these public neutral types:

```go
type Proposal struct {
	Path string
	Original, Proposed []byte
	OriginalHash, ProposedHash [32]byte
	Diff string
}

type AppliedEdit struct {
	Path string
	Before, After []byte
	BeforeHash, AfterHash [32]byte
}

type PreparedEdit struct { state *exchangeApplyState }

var ErrStaleFile = errors.New("file changed after review")

func ProposeReplacement(path string, original, proposed []byte) Proposal
func Prepare(Proposal) (*PreparedEdit, error)
func (p *PreparedEdit) Commit() (AppliedEdit, error)
func (p *PreparedEdit) Abort() error
func Apply(Proposal) (AppliedEdit, error)
func ProposeRevert(AppliedEdit) (Proposal, error)
```

Move the current private staging directory, exact mode/write checks, identity-aware exchange, ambiguous-old-temp retention, typed cleanup, directory sync, and Darwin/Linux exchange code without weakening any invariant. `Prepare` performs all reads, hash checks, staging writes, and final pre-exchange identity validation. `Commit` performs the exchange and cleanup. `Abort` removes only identity-matched owned staging artifacts.

- [ ] **Step 4: Keep package APIs as compatibility wrappers**

`packages.Apply` converts `packages.Proposal` to `fileedit.Proposal`; `packages.ProposeRevert` converts the result back while retaining `Target`. Package tests must continue to exercise provider target metadata, while the detailed filesystem race matrix moves to `fileedit/apply_test.go`.

- [ ] **Step 5: Run every old CAS race under the neutral owner**

Run: `go test -race ./internal/fileedit ./internal/packages -run 'TestApply|TestProposeRevert' -count=1`

Expected: PASS, including competitor-before-exchange, old-temp replacement, recovery replacement, cleanup mismatch, short write, exact permissions, and artifact-path reporting.

- [ ] **Step 6: Commit the dependency inversion**

```bash
git add internal/fileedit internal/packages
git commit -m "refactor: move atomic edits into fileedit"
```

---

### Task 2: Add a Prepared Multi-File Transaction

**Files:**
- Modify: `internal/fileedit/types.go`
- Create: `internal/fileedit/transaction.go`
- Create: `internal/fileedit/transaction_test.go`
- Create: `internal/fileedit/lock_unix.go`

**Interfaces:**
- Consumes: `Proposal`, `Prepare`, `PreparedEdit.Commit`, and `ProposeRevert` from Task 1.
- Produces: `TransactionProposal`, `TransactionResult`, `ApplyTransaction`, and `ProposeTransactionRevert`.
- Accepts: an explicit repo root and common Git lock path.

- [ ] **Step 1: Write the failing prepare-all/no-partial-write test**

```go
func TestApplyTransactionPreparesEveryEditBeforeWritingAnyTarget(t *testing.T) {
	repo := t.TempDir()
	a := writeFixture(t, repo, "catalog/tools.yaml", "tools: {}\n")
	b := writeFixture(t, repo, "catalog/hosts.yaml", "hosts: {}\n")
	proposal := TransactionProposal{Repo: repo, LockPath: filepath.Join(repo, ".git", "sys-bozo.lock"), Edits: []Proposal{
		ProposeReplacement(a, []byte("tools: {}\n"), []byte("tools:\n  yazi: {}\n")),
		ProposeReplacement(b, []byte("stale original\n"), []byte("hosts:\n  box: {}\n")),
	}}
	_, err := ApplyTransaction(proposal)
	if !errors.Is(err, ErrStaleFile) { t.Fatalf("err=%v", err) }
	got, _ := os.ReadFile(a)
	if string(got) != "tools: {}\n" { t.Fatalf("first target changed: %q", got) }
}
```

Add this helper in the same test file:

```go
func writeFixture(t *testing.T, repo, relative, content string) string {
	t.Helper()
	path := filepath.Join(repo, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil { t.Fatal(err) }
	return path
}
```

Create `filepath.Join(repo, ".git")` with mode `0700` in this test before constructing `LockPath`.

- [ ] **Step 2: Run the transaction test and verify RED**

Run: `go test ./internal/fileedit -run TestApplyTransactionPreparesEveryEditBeforeWritingAnyTarget -count=1 -v`

Expected: compile failure for undefined transaction types.

- [ ] **Step 3: Define immutable transaction metadata**

```go
type TransactionProposal struct {
	Repo string
	LockPath string
	Edits []Proposal
}

type TransactionResult struct {
	Edits []AppliedEdit
	RecoveryPaths []string
}

func ApplyTransaction(TransactionProposal) (TransactionResult, error)
func ProposeTransactionRevert(TransactionResult, string, string) (TransactionProposal, error)
```

Validate that the repo and lock path are absolute, every edit is `Valid`, every target is a regular file inside `Repo` after `filepath.EvalSymlinks` of its parent, and normalized target paths are unique. Sort a cloned edit slice by path so lock/commit order is deterministic.

- [ ] **Step 4: Implement cooperative locking and prepare-all commit**

`lock_unix.go` uses `unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)` on a mode-0600 file under the repository's real Git directory. Return `ErrTransactionBusy` when already locked; always unlock and close with `errors.Join`.

`ApplyTransaction` must:

1. acquire the lock;
2. call `Prepare` for every sorted proposal before any commit;
3. abort all prepared edits on preparation failure;
4. commit in sorted path order;
5. on failure, create exact-hash reverts for already committed edits and apply them in reverse order while the lock remains held;
6. preserve the concurrent target and return named recovery paths when a revert is stale or unverified;
7. return cloned applied edits only after every commit succeeds.

Do not call `ApplyTransaction` recursively during rollback; use the already-locked private `applyPreparedRevert` helper.

- [ ] **Step 5: Add deterministic partial-commit and competitor tests**

Inject transaction hooks immediately after prepare-all, before each commit, and before each rollback. Cover:

- second commit failure restores the first file;
- competitor replacement of the first target before rollback remains untouched;
- duplicate/symlink/out-of-repo paths fail before prepare;
- lock contention performs no writes;
- recovery errors contain every retained path;
- `ProposeTransactionRevert` rejects any applied target whose current hash differs.

Run: `go test -race ./internal/fileedit -run 'TestApplyTransaction|TestProposeTransactionRevert' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the transaction engine**

```bash
git add internal/fileedit
git commit -m "feat(fileedit): apply reviewed file transactions"
```

---

### Task 3: Parse and Validate the Existing Catalog Schema

**Files:**
- Create: `internal/catalog/types.go`
- Create: `internal/catalog/parse.go`
- Create: `internal/catalog/parse_test.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `flake.nix`

**Interfaces:**
- Produces: `Store`, `Tool`, `Host`, `PlacementScope`, `PlacementRequest`, `Load`, and validation errors.
- Produces: `func Load(repo string) (*Store, error)`.
- Consumes: `catalog/tools.yaml` and `catalog/hosts.yaml` as `yaml.Node` graphs.
- Does not mutate files or execute commands.

- [ ] **Step 1: Add the direct YAML dependency**

Run: `go get gopkg.in/yaml.v3@v3.0.1`

Expected: `go.mod` contains direct `gopkg.in/yaml.v3 v3.0.1`; `go.sum` records its checksums.

- [ ] **Step 2: Write failing real-schema parse tests**

```go
func TestLoadPreservesCatalogVocabularyAndHostProfile(t *testing.T) {
	repo := fixtureCatalogRepo(t, realToolsFixture, realHostsFixture)
	store, err := Load(repo)
	if err != nil { t.Fatal(err) }
	tool := store.Tools["atuin"]
	if tool.ProviderIDs["nix"] != "atuin" || tool.ConfigSync != "local-only" { t.Fatalf("tool=%#v", tool) }
	host := store.Hosts["butler"]
	if host.Profile != "linux-home" || host.User != "bag" { t.Fatalf("host=%#v", host) }
}

func TestLoadRejectsMalformedOrDuplicateSemanticKeys(t *testing.T) {
	for _, tools := range []string{"tools: [", "tools:\n  yazi:\n    nix: one\n    nix: two\n"} {
		repo := fixtureCatalogRepo(t, tools, "hosts: {}\n")
		if _, err := Load(repo); err == nil { t.Fatalf("accepted %q", tools) }
	}
}
```

Define the parser fixtures explicitly:

```go
const realToolsFixture = "# catalog\ntools:\n  atuin:\n    category: shell\n    defaultProfiles: [linux-home]\n    nix: atuin\n    config:\n      sync: local-only\n"
const realHostsFixture = "hosts:\n  butler:\n    os: linux\n    arch: x86_64\n    profile: linux-home\n    user: bag\n"

func fixtureCatalogRepo(t *testing.T, tools, hosts string) string {
	t.Helper()
	repo := t.TempDir()
	for relative, content := range map[string]string{"catalog/tools.yaml": tools, "catalog/hosts.yaml": hosts} {
		path := filepath.Join(repo, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil { t.Fatal(err) }
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil { t.Fatal(err) }
	return repo
}
```

- [ ] **Step 3: Run catalog tests and verify RED**

Run: `go test ./internal/catalog -run TestLoad -count=1 -v`

Expected: compile failure because the catalog package does not exist.

- [ ] **Step 4: Define the catalog domain and node helpers**

```go
type PlacementScope string
const (
	ScopeCurrentHost PlacementScope = "current-host"
	ScopeProfile PlacementScope = "profile"
)

type Tool struct {
	Key, Category string
	ProviderIDs map[string]string
	DefaultProfiles []string
	ConfigSync string
}

type Host struct {
	Name, OS, Arch, Profile, User string
	Include, Exclude []string
}

type Store struct {
	Repo, ToolsPath, HostsPath string
	Tools map[string]Tool
	Hosts map[string]Host
	toolsDoc, hostsDoc *yaml.Node
	toolsBytes, hostsBytes []byte
}
```

`Load` reads both regular non-symlink files, decodes each into a `yaml.Node`, rejects duplicate mapping keys through an explicit recursive node walk, and validates required root mappings `tools` and `hosts`. Keep original bytes and node graphs for proposal generation. Add focused helpers `mappingValue`, `sequenceStrings`, `scalarString`, and `rejectDuplicateKeys`; none may silently coerce YAML types.

- [ ] **Step 5: Verify existing schema and update the Nix vendor hash**

Run: `gofmt -w internal/catalog && go test ./internal/catalog -count=1 && go test ./... -count=1`

Then run the exact Nix build. If Nix reports a fixed-output hash mismatch, replace `vendorHash` in `flake.nix` with the exact `got:` hash emitted by Nix and rerun until exit zero:

`nix build .#packages.$(nix eval --impure --raw --expr builtins.currentSystem).default`

- [ ] **Step 6: Commit the parser and dependency**

```bash
git add go.mod go.sum flake.nix internal/catalog
git commit -m "feat(catalog): parse tool and host manifests"
```

---

### Task 4: Propose Catalog and Host/Profile Placement Without Losing YAML

**Files:**
- Modify: `internal/catalog/types.go`
- Create: `internal/catalog/place.go`
- Create: `internal/catalog/place_test.go`

**Interfaces:**
- Consumes: `Store`, `packages.Candidate`, host name, and `PlacementScope`.
- Produces: `func (Store) ProposePlacement(PlacementRequest) (fileedit.TransactionProposal, error)`.
- Maps: formula to `brew`, cask to `brewCask`, and package to `nix`, `dnf`, or `apt`.

- [ ] **Step 1: Write failing current-host and profile placement tests**

```go
func TestProposePlacementAddsProviderMappingAndCurrentHostInclude(t *testing.T) {
	store := loadFixtureStore(t)
	proposal, err := store.ProposePlacement(PlacementRequest{
		Candidate: packages.Candidate{Provider: packages.ProviderDNF, Kind: packages.KindPackage, ID: "lazydocker", Name: "lazydocker"},
		LogicalKey: "lazydocker", Hostname: "butler", Scope: ScopeCurrentHost,
	})
	if err != nil { t.Fatal(err) }
	if len(proposal.Edits) != 2 { t.Fatalf("edits=%d", len(proposal.Edits)) }
	assertDiffContains(t, proposal, "catalog/tools.yaml", "+    dnf: lazydocker")
	assertDiffContains(t, proposal, "catalog/hosts.yaml", "+      - lazydocker")
}

func TestProposePlacementProfileScopeAddsDefaultProfileOnly(t *testing.T) {
	store := loadFixtureStore(t)
	proposal, err := store.ProposePlacement(PlacementRequest{
		Candidate: packages.Candidate{Provider: packages.ProviderAPT, Kind: packages.KindPackage, ID: "lazygit", Name: "lazygit"},
		LogicalKey: "lazygit", Hostname: "debian-box", Scope: ScopeProfile,
	})
	if err != nil { t.Fatal(err) }
	assertDiffContains(t, proposal, "catalog/tools.yaml", "defaultProfiles: [linux-home]")
	assertNoEditFor(t, proposal, "catalog/hosts.yaml")
}
```

Use these concrete helpers:

```go
func loadFixtureStore(t *testing.T) *Store {
	t.Helper()
	store, err := Load(fixtureCatalogRepo(t, realToolsFixture, realHostsFixture+"  debian-box:\n    os: linux\n    arch: x86_64\n    profile: linux-home\n    user: bag\n"))
	if err != nil { t.Fatal(err) }
	return store
}

func editForSuffix(t *testing.T, proposal fileedit.TransactionProposal, suffix string) fileedit.Proposal {
	t.Helper()
	for _, edit := range proposal.Edits {
		if strings.HasSuffix(edit.Path, suffix) { return edit }
	}
	t.Fatalf("missing edit %s in %#v", suffix, proposal.Edits)
	return fileedit.Proposal{}
}

func assertDiffContains(t *testing.T, proposal fileedit.TransactionProposal, suffix, want string) {
	t.Helper()
	if diff := editForSuffix(t, proposal, suffix).Diff; !strings.Contains(diff, want) { t.Fatalf("diff missing %q:\n%s", want, diff) }
}

func assertNoEditFor(t *testing.T, proposal fileedit.TransactionProposal, suffix string) {
	t.Helper()
	for _, edit := range proposal.Edits {
		if strings.HasSuffix(edit.Path, suffix) { t.Fatalf("unexpected edit %s", edit.Path) }
	}
}
```

- [ ] **Step 2: Run placement tests and verify RED**

Run: `go test ./internal/catalog -run TestProposePlacement -count=1 -v`

Expected: compile failure for undefined placement APIs.

- [ ] **Step 3: Define placement validation and provider keys**

```go
type PlacementRequest struct {
	Candidate packages.Candidate
	LogicalKey string
	Hostname string
	Scope PlacementScope
}

var logicalKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._+-]*$`)

func providerField(candidate packages.Candidate) (string, error) {
	switch {
	case candidate.Provider == packages.ProviderNix && candidate.Kind == packages.KindPackage: return "nix", nil
	case candidate.Provider == packages.ProviderBrew && candidate.Kind == packages.KindFormula: return "brew", nil
	case candidate.Provider == packages.ProviderBrew && candidate.Kind == packages.KindCask: return "brewCask", nil
	case candidate.Provider == packages.ProviderDNF && candidate.Kind == packages.KindPackage: return "dnf", nil
	case candidate.Provider == packages.ProviderAPT && candidate.Kind == packages.KindPackage: return "apt", nil
	default: return "", ErrUnsupportedProviderKind
	}
}
```

Reject empty/control-containing IDs, unknown hosts, hosts with empty profiles for profile scope, conflicting existing mappings, already-present membership, and logical keys that collide with a non-tool/group entry.

- [ ] **Step 4: Mutate cloned YAML nodes only**

Deep-clone both documents before editing. For a new tool, append a mapping node with logical key, `category: uncategorized`, the provider field, and either `defaultProfiles` for profile scope or no defaults for current-host scope. For an existing tool, preserve all nodes and add only the missing provider/default profile. For current-host scope, create or extend the host's block-style `include` sequence.

Encode with `yaml.Encoder`, indent 2, and close the encoder with `errors.Join`. Create `fileedit.Proposal` only for documents whose bytes changed; sort proposals by path and set `Repo`/`LockPath` using a helper that resolves a normal `.git` directory or a worktree `gitdir:` file.

- [ ] **Step 5: Add preservation and fail-closed cases**

Tests must prove comments, `groups`, `config.sync`, `platforms`, quoted flake keys, notes, excludes, and existing provider mappings survive. Add conflict, duplicate, malformed scalar, unknown host, out-of-repo symlink, and no-op cases. Reparse every proposed YAML document and assert semantic validity.

Run: `go test ./internal/catalog -count=1 -v`

Expected: PASS.

- [ ] **Step 6: Commit pure catalog placement**

```bash
git add internal/catalog
git commit -m "feat(catalog): propose host-aware package placement"
```

---

### Task 5: Review and Apply Catalog Placement in the TUI

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/package_flow.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/view_package.go`
- Modify: `internal/tui/model_test.go`
- Modify: `TESTING.md`

**Interfaces:**
- Consumes: `catalog.Load`, `Store.ProposePlacement`, `fileedit.ApplyTransaction`, and `ProposeTransactionRevert`.
- Produces: catalog scope selection, immutable multi-file Review, applied transaction state, and reviewed revert.
- Preserves: existing Nix/Homebrew declaration proposal and verification flow.

- [ ] **Step 1: Write the no-write-before-confirm TUI test**

```go
func TestCatalogPlacementReviewsBothFilesBeforeWriting(t *testing.T) {
	repo := fixtureCatalogAndDotfilesRepo(t)
	m := testModelWithRepo(repo, "butler", "linux", "fedora")
	m.packageFlow = completedProviderFixture(packages.Candidate{Provider: packages.ProviderDNF, Kind: packages.KindPackage, ID: "lazydocker", Name: "lazydocker"})
	beforeTools := mustRead(t, filepath.Join(repo, "catalog/tools.yaml"))
	beforeHosts := mustRead(t, filepath.Join(repo, "catalog/hosts.yaml"))
	if !m.buildCatalogPackageReview(packages.Candidate{Provider: packages.ProviderDNF, Kind: packages.KindPackage, ID: "lazydocker", Name: "lazydocker"}, catalog.ScopeCurrentHost) { t.Fatal("review not built") }
	got := m
	if got.screen != screenReview || got.reviewed.Package.Catalog == nil { t.Fatalf("screen=%v review=%#v", got.screen, got.reviewed.Package) }
	if !bytes.Equal(beforeTools, mustRead(t, filepath.Join(repo, "catalog/tools.yaml"))) || !bytes.Equal(beforeHosts, mustRead(t, filepath.Join(repo, "catalog/hosts.yaml"))) {
		t.Fatal("catalog changed before confirmation")
	}
}
```

Add these TUI test helpers:

```go
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	return b
}

func fixtureCatalogAndDotfilesRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	write := func(relative, content string) {
		path := filepath.Join(repo, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { t.Fatal(err) }
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil { t.Fatal(err) }
	}
	write("catalog/tools.yaml", "tools: {}\n")
	write("catalog/hosts.yaml", "hosts:\n  butler:\n    os: linux\n    arch: x86_64\n    profile: linux-home\n    user: bag\n")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil { t.Fatal(err) }
	return repo
}

func testModelWithRepo(repo, hostname, goos, osID string) Model {
	m := New()
	m.runCtx.Repo, m.runCtx.Hostname, m.runCtx.OS, m.runCtx.OSID = repo, hostname, goos, osID
	m.width, m.height = 80, 24
	return m
}

func completedProviderFixture(candidate packages.Candidate) packageFlow {
	flow := newPackageFlow(80)
	flow.stage = packageChoose
	flow.providers = []packageProviderState{{Spec: packages.ProviderSpec{Provider: candidate.Provider, Label: strings.ToUpper(string(candidate.Provider)), Enabled: true}, Phase: packages.SearchDone, Candidates: []packages.Candidate{candidate}}}
	return flow
}
```

The production method used above has this exact signature:

```go
func (m *Model) buildCatalogPackageReview(candidate packages.Candidate, scope catalog.PlacementScope) bool
```

It loads the store, creates the immutable transaction, attaches any existing Nix/Homebrew declaration proposal, initializes the multi-file viewport, and returns false with `packageFlow.err` on every fail-closed validation error. Separate key-routing tests must assert that the placement screen passes the selected candidate and selected scope to this method.

- [ ] **Step 2: Run the focused workflow test and verify RED**

Run: `go test ./internal/tui -run TestCatalogPlacementReviewsBothFilesBeforeWriting -count=1 -v`

Expected: compile failure for missing catalog review state.

- [ ] **Step 3: Add catalog placement and reviewed transaction state**

Add two choices to placement: `CURRENT HOST (DEFAULT)` and `PROFILE: <detected profile>`. Store the choice separately from existing provider declaration section/scope.

Extend `packageReview` with:

```go
	Catalog *fileedit.TransactionProposal
	CatalogApplied *fileedit.TransactionResult
	CatalogEditApplied bool
	CatalogRevert bool
```

Inject pure/load/apply seams in `Model`:

```go
	loadCatalog func(string) (*catalog.Store, error)
	applyFileTransaction func(fileedit.TransactionProposal) (fileedit.TransactionResult, error)
	proposeTransactionRevert func(fileedit.TransactionResult, string, string) (fileedit.TransactionProposal, error)
```

Clone every proposal/result byte slice when crossing into Review.

- [ ] **Step 4: Build one immutable multi-file Review**

For every provider, build the catalog transaction first. For Nix/Homebrew, include the existing declaration proposal in the same transaction edit set so every file is revalidated before any write. For DNF/APT at this milestone, Review states `DECLARATION ONLY · NATIVE CONVERGENCE ADDED IN MILESTONE 3` and queues no install command; it must not claim the package is installed.

Render one viewport containing per-file headings and exact unified diffs. Review lists the repo lock, all target paths, catalog scope, provider ID, and any rebuild command. One Enter confirms the immutable transaction and then runs the existing Nix/Homebrew rebuild/verify queue if applicable.

- [ ] **Step 5: Handle stale, partial, cleanup, and revert outcomes truthfully**

On `ErrStaleFile` or `ErrTransactionBusy`, remain in Review with sanitized detail and require refresh. If `TransactionResult.Edits` is non-empty alongside an error, record applied state, retain recovery paths, and offer a separate reverse Review. A failed downstream rebuild/verification offers `ProposeTransactionRevert` only when every current file hash matches the applied result.

Tests must cover stale second file, lock contention, partial rollback, recovery paths, reverse Review, retry after catalog already applied, and no aliasing between mutable flow state and Review.

- [ ] **Step 6: Run the fake-repository visual/safety gate and commit**

Run:

```sh
go test -race ./internal/fileedit ./internal/catalog ./internal/tui -count=1
NO_COLOR=1 go test ./internal/tui -run 'TestCatalog|TestPackageWorkflowViewsFit80x24' -count=1 -v
go test ./... -count=1
go vet ./...
git diff --check
nix build .#packages.$(nix eval --impure --raw --expr builtins.currentSystem).default
```

Expected: PASS; fixture byte assertions prove no real repo/package state changed. Remove generated `flake.lock` residue.

Add the fake catalog command to `TESTING.md`, then commit:

```bash
git add internal/fileedit internal/packages internal/catalog internal/tui go.mod go.sum flake.nix TESTING.md
git commit -m "feat(tui): review declarative catalog placement"
```
