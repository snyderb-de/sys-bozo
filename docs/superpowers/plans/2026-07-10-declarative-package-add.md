# Declarative Package Add Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users search Nix and Homebrew, choose provider/scope/section, review an exact config diff, apply through Home Manager or nix-darwin, and verify the installed package.

**Architecture:** Add a pure `internal/packages` domain with command adapters, safe list transformations, atomic writes, and verification. The TUI owns workflow state and places one immutable package operation—edit, rebuild, verify—inside the existing Review/Running/Result lifecycle.

**Tech Stack:** Go 1.24.2, Bubble Tea/Bubbles text input, standard `crypto/sha256`, `os/exec`, and filesystem APIs; no new third-party parsing dependency.

## Global Constraints

- Nix is selected by default when both providers return a candidate.
- Never perform a one-off undeclared install.
- Automatic edits support only one uniquely identified flat list assignment.
- Preserve comments, whitespace, permissions, and unrelated bytes.
- Review must show the exact diff, rebuild command, and verification before one confirmation.
- Abort on stale file hashes; never overwrite concurrent edits.
- Failed rebuilds keep the declaration visible and offer exact-hash revert with its own review.
- Search, tests, and previews must not mutate real package managers, profiles, or user config.
- No private aliases, tokens, passwords, decrypted SOPS values, or secret environment values may enter results or logs.

## File Map

- `internal/packages/types.go`: providers, kinds, scopes, candidates, targets, proposals, applied edits, verification specs.
- `internal/packages/search.go`: Nix/Brew adapters and concurrent search aggregation.
- `internal/packages/search_test.go`: fake-runner parsing and partial failure tests.
- `internal/packages/edit.go`: target resolution, section discovery, pure insertion, hashes, and unified diff.
- `internal/packages/edit_test.go`: Nix/Brew fixtures and fail-closed cases.
- `internal/packages/apply.go`: atomic write, stale protection, and exact revert.
- `internal/packages/apply_test.go`: temp-repo filesystem tests.
- `internal/packages/verify.go`: executable, Brew receipt, and cask artifact verification.
- `internal/packages/verify_test.go`: fake command/path tests.
- `internal/tui/package_flow.go`: package workflow state, async commands, and combined review execution.
- `internal/tui/view_package.go`: Monolith/Afterburner package search, placement, diff, and result rendering.
- `internal/tui/model.go`, `update.go`, `execution.go`, `view_home.go`: package destination and lifecycle integration.
- `internal/tui/model_test.go`: package state and no-mutation-before-confirm tests.

---

### Task 1: Package Domain and Concurrent Search

**Files:**
- Create: `internal/packages/types.go`
- Create: `internal/packages/search.go`
- Create: `internal/packages/search_test.go`

**Interfaces:**
- Produces: `Provider`, `Kind`, `Scope`, `Candidate`, `SearchResult`, and `OutputRunner`.
- Produces: `func Search(context.Context, OutputRunner, string, string, string) SearchResult`.
- Consumes: `nix search --json nixpkgs <query>`, `brew search --formula <query>`, and `brew search --cask <query>` output.

- [ ] **Step 1: Write failing parser/default/failure tests**

```go
type fakeOutputRunner struct {
	responses map[string]struct{ out string; err error }
}

func (f fakeOutputRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	r := f.responses[key]
	return []byte(r.out), r.err
}

func TestSearchCombinesProvidersAndDefaultsToNix(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct{ out string; err error }{
		"nix search --json nixpkgs lazydocker": {out: `{"legacyPackages.aarch64-darwin.lazydocker":{"pname":"lazydocker","version":"0.24.1","description":"Docker TUI"}}`},
		"brew search --formula lazydocker": {out: "lazydocker\n"},
		"brew search --cask lazydocker":    {out: ""},
	}}
	got := Search(context.Background(), runner, "nix", "brew", "lazydocker")
	if len(got.Candidates) != 2 || got.Candidates[0].Provider != ProviderNix {
		t.Fatalf("got %#v", got)
	}
	if got.Selected != 0 {
		t.Fatalf("selected=%d want Nix index 0", got.Selected)
	}
}

func TestSearchKeepsBrewWhenNixFails(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct{ out string; err error }{
		"nix search --json nixpkgs yazi": {err: errors.New("nix unavailable")},
		"brew search --formula yazi":     {out: "yazi\n"},
		"brew search --cask yazi":        {out: ""},
	}}
	got := Search(context.Background(), runner, "nix", "brew", "yazi")
	if got.NixErr == nil || len(got.Candidates) != 1 || got.Candidates[0].Provider != ProviderBrew {
		t.Fatalf("got %#v", got)
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/packages -run 'TestSearchCombinesProvidersAndDefaultsToNix|TestSearchKeepsBrewWhenNixFails' -v`

Expected: compile failure because package domain does not exist.

- [ ] **Step 3: Define domain types**

```go
package packages

type Provider string
const (
	ProviderNix Provider = "nix"
	ProviderBrew Provider = "brew"
)

type Kind string
const (
	KindPackage Kind = "package"
	KindFormula Kind = "formula"
	KindCask Kind = "cask"
)

type Scope string
const (
	ScopeShared Scope = "shared"
	ScopePlatform Scope = "platform"
	ScopeHost Scope = "host"
)

type Candidate struct {
	Provider Provider
	Kind Kind
	ID string
	Name string
	Version string
	Description string
}

type SearchResult struct {
	Candidates []Candidate
	Selected int
	NixErr error
	BrewErr error
}

type OutputRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}
```

- [ ] **Step 4: Implement provider searches and aggregation**

Implement Nix JSON parsing by sorting attribute keys and deriving `ID` from the final attribute segment. Implement Brew formula/cask parsing as trimmed non-empty lines, excluding lines containing `=>`. Run Nix and Brew searches in two goroutines, receive exactly two typed channel results, concatenate Nix before Brew, and set `Selected` to the first Nix candidate or zero when only Brew exists.

Use this concrete command runner in `search.go`:

```go
type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
```

- [ ] **Step 5: Run tests and commit**

Run: `gofmt -w internal/packages && go test ./internal/packages -v`

Expected: all search tests pass.

```bash
git add internal/packages/types.go internal/packages/search.go internal/packages/search_test.go
git commit -m "feat(packages): search Nix and Homebrew"
```

---

### Task 2: Safe Target Resolution and Pure Diff Proposal

**Files:**
- Modify: `internal/packages/types.go`
- Create: `internal/packages/edit.go`
- Create: `internal/packages/edit_test.go`

**Interfaces:**
- Produces: `Target`, `Section`, `Proposal`, `ResolveTarget`, `Sections`, and `ProposeAdd`.
- Consumes: package provider/kind/scope and original tracked-file bytes.

- [ ] **Step 1: Write failing Nix/Brew fixture tests**

```go
func TestProposeAddPreservesNixFileAndInsertsInSection(t *testing.T) {
	original := []byte("{ pkgs, ... }:\n{\n  home.packages = with pkgs; [\n    # Git tooling\n    gh\n\n    # Misc\n    sqlite\n  ];\n}\n")
	target := Target{Assignment: "home.packages", Quoted: false}
	proposal, err := ProposeAdd(original, target, "Misc", "lazydocker")
	if err != nil { t.Fatal(err) }
	want := "    # Misc\n    sqlite\n    lazydocker\n"
	if !strings.Contains(string(proposal.Proposed), want) { t.Fatalf("%s", proposal.Proposed) }
	if !strings.Contains(proposal.Diff, "+    lazydocker") { t.Fatalf("diff=%s", proposal.Diff) }
	if string(original) == string(proposal.Proposed) { t.Fatal("proposal did not change") }
}

func TestProposeAddQuotesBrewItemAndRejectsDuplicate(t *testing.T) {
	original := []byte("{\n  casks = [\n    # Core baseline\n    \"raycast\"\n  ];\n}\n")
	target := Target{Assignment: "casks", Quoted: true}
	proposal, err := ProposeAdd(original, target, "Core baseline", "zed")
	if err != nil || !bytes.Contains(proposal.Proposed, []byte(`    "zed"`)) { t.Fatalf("proposal=%#v err=%v", proposal, err) }
	_, err = ProposeAdd(proposal.Proposed, target, "Core baseline", "zed")
	if !errors.Is(err, ErrAlreadyDeclared) { t.Fatalf("err=%v", err) }
}

func TestProposeAddFailsClosedOnMultipleAssignments(t *testing.T) {
	original := []byte("home.packages = [ ];\nhome.packages = [ ];\n")
	_, err := ProposeAdd(original, Target{Assignment: "home.packages"}, "Misc", "x")
	if !errors.Is(err, ErrAmbiguousTarget) { t.Fatalf("err=%v", err) }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/packages -run 'TestProposeAdd' -v`

Expected: compile failure because proposal APIs do not exist.

- [ ] **Step 3: Define edit types and exact target mapping**

```go
type Target struct {
	Path string
	Assignment string
	Quoted bool
	ApplyAction string
}

type Proposal struct {
	Target Target
	Original []byte
	Proposed []byte
	OriginalHash [32]byte
	ProposedHash [32]byte
	Diff string
}

var (
	ErrAlreadyDeclared = errors.New("package already declared")
	ErrAmbiguousTarget = errors.New("declaration target is missing or ambiguous")
	ErrSectionNotFound = errors.New("declaration section not found")
)
```

Implement exact shared/platform mappings:

```go
func ResolveTarget(repo, goos string, provider Provider, kind Kind, scope Scope) (Target, error) {
	switch {
	case provider == ProviderNix && scope == ScopeShared:
		return Target{Path: filepath.Join(repo, "home/modules/packages.nix"), Assignment: "home.packages", ApplyAction: "hms"}, nil
	case provider == ProviderNix && scope == ScopePlatform && goos == "darwin":
		return Target{Path: filepath.Join(repo, "home/darwin/default.nix"), Assignment: "home.packages", ApplyAction: "hms"}, nil
	case provider == ProviderNix && scope == ScopePlatform && goos == "linux":
		return Target{Path: filepath.Join(repo, "home/linux/default.nix"), Assignment: "home.packages", ApplyAction: "hms"}, nil
	case provider == ProviderBrew && scope == ScopeShared && kind == KindFormula:
		return Target{Path: filepath.Join(repo, "homebrew.nix"), Assignment: "brews", Quoted: true, ApplyAction: "nds"}, nil
	case provider == ProviderBrew && scope == ScopeShared && kind == KindCask:
		return Target{Path: filepath.Join(repo, "homebrew.nix"), Assignment: "casks", Quoted: true, ApplyAction: "nds"}, nil
	default:
		return Target{}, ErrAmbiguousTarget
	}
}
```

- [ ] **Step 4: Implement fail-closed line transformation**

Scan original lines while retaining newline endings. Locate exactly one assignment line whose trimmed text starts with `target.Assignment + " ="`; require an opening `[` and a later closing line trimmed to `];`. Inside that range, collect section comments matching `# <name>`. Insert immediately before the next section comment or closing bracket, after removing only blank lines at that insertion edge. Preserve all other bytes.

Normalize Nix items as `indent + item`; normalize Brew items as `indent + strconv.Quote(item)`. Reject an existing normalized item. Compute SHA-256 hashes and a unified diff with `--- original`, `+++ proposed`, one `@@` header, context lines prefixed with one space, removed lines with `-`, and added lines with `+`.

- [ ] **Step 5: Run tests and commit**

Run: `gofmt -w internal/packages && go test ./internal/packages -v`

Expected: all edit and search tests pass.

```bash
git add internal/packages/types.go internal/packages/edit.go internal/packages/edit_test.go
git commit -m "feat(packages): propose safe declaration edits"
```

---

### Task 3: Atomic Apply, Stale Protection, and Exact Revert

**Files:**
- Modify: `internal/packages/types.go`
- Create: `internal/packages/apply.go`
- Create: `internal/packages/apply_test.go`

**Interfaces:**
- Produces: `AppliedEdit`, `Apply`, and `ProposeRevert`.
- Consumes: `Proposal` hashes and bytes from Task 2.

- [ ] **Step 1: Write failing filesystem tests**

```go
func TestApplyRejectsStaleFileAndPreservesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packages.nix")
	original := []byte("home.packages = [\n  # Misc\n];\n")
	if err := os.WriteFile(path, original, 0o640); err != nil { t.Fatal(err) }
	proposal, err := ProposeAdd(original, Target{Path: path, Assignment: "home.packages"}, "Misc", "yazi")
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte("concurrent edit\n"), 0o640); err != nil { t.Fatal(err) }
	_, err = Apply(proposal)
	if !errors.Is(err, ErrStaleFile) { t.Fatalf("err=%v", err) }
	got, _ := os.ReadFile(path)
	if string(got) != "concurrent edit\n" { t.Fatalf("file=%q", got) }
}

func TestApplyPreservesPermissionsAndRevertChecksPostHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homebrew.nix")
	original := []byte("casks = [\n  # Misc\n];\n")
	if err := os.WriteFile(path, original, 0o640); err != nil { t.Fatal(err) }
	proposal, _ := ProposeAdd(original, Target{Path: path, Assignment: "casks", Quoted: true}, "Misc", "zed")
	applied, err := Apply(proposal)
	if err != nil { t.Fatal(err) }
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 { t.Fatalf("mode=%o", info.Mode().Perm()) }
	revert, err := ProposeRevert(applied)
	if err != nil { t.Fatal(err) }
	if _, err := Apply(revert); err != nil { t.Fatal(err) }
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) { t.Fatalf("file=%q", got) }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/packages -run 'TestApply' -v`

Expected: compile failure because atomic apply APIs do not exist.

- [ ] **Step 3: Implement atomic apply and revert proposal**

```go
var ErrStaleFile = errors.New("declaration file changed after review")

type AppliedEdit struct {
	Path string
	Before []byte
	After []byte
	BeforeHash [32]byte
	AfterHash [32]byte
}
```

`Apply` must read current bytes, compare against `OriginalHash`, stat permissions, create a temporary file in `filepath.Dir(path)`, chmod to the original mode, write and sync proposed bytes, close, rename over target, and remove the temp file on all pre-rename failures. Return hashes and bytes in `AppliedEdit`.

`ProposeRevert` must read the current path and require `sha256.Sum256(current) == applied.AfterHash`; then return a `Proposal` whose original is current and proposed is `applied.Before`, with a reverse unified diff. Revert remains a proposal until the TUI reviews and confirms it.

- [ ] **Step 4: Run tests and commit**

Run: `gofmt -w internal/packages && go test ./internal/packages -v`

Expected: all tests pass.

```bash
git add internal/packages/types.go internal/packages/apply.go internal/packages/apply_test.go
git commit -m "feat(packages): apply declarations atomically"
```

---

### Task 4: Truthful Package Verification

**Files:**
- Modify: `internal/packages/types.go`
- Create: `internal/packages/verify.go`
- Create: `internal/packages/verify_test.go`

**Interfaces:**
- Produces: `VerifySpec`, `VerifyResult`, `PathLookup`, and `Verify`.
- Consumes: `OutputRunner` and chosen `Candidate`.

- [ ] **Step 1: Write failing CLI and cask verification tests**

```go
func TestVerifyCLIRequiresResolvedExecutable(t *testing.T) {
	lookup := func(name string) (string, error) {
		if name == "yazi" { return "/nix/store/test/bin/yazi", nil }
		return "", exec.ErrNotFound
	}
	got := Verify(context.Background(), fakeOutputRunner{}, lookup, VerifySpec{Provider: ProviderNix, Kind: KindPackage, Token: "yazi", Executable: "yazi"})
	if !got.OK || got.Path != "/nix/store/test/bin/yazi" { t.Fatalf("got %#v", got) }
}

func TestVerifyBrewCaskRequiresReceiptAndArtifact(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]struct{ out string; err error }{
		"brew list --cask --versions zed": {out: "zed 0.190.0\n"},
	}}
	lookup := func(string) (string, error) { return "", exec.ErrNotFound }
	dir := t.TempDir()
	app := filepath.Join(dir, "Zed.app")
	if err := os.Mkdir(app, 0o755); err != nil { t.Fatal(err) }
	got := Verify(context.Background(), runner, lookup, VerifySpec{Provider: ProviderBrew, Kind: KindCask, Token: "zed", AppPath: app, BrewBin: "brew"})
	if !got.OK { t.Fatalf("got %#v", got) }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/packages -run 'TestVerify' -v`

Expected: compile failure because verification APIs do not exist.

- [ ] **Step 3: Implement provider-aware verification**

```go
type VerifySpec struct {
	Provider Provider
	Kind Kind
	Token string
	Executable string
	VersionArgs []string
	AppPath string
	BrewBin string
}

type VerifyResult struct {
	OK bool
	Path string
	Detail string
	Err error
}

type PathLookup func(string) (string, error)
```

For a non-empty executable, use `PathLookup`; when `VersionArgs` is non-empty, run the resolved executable with those arguments and require exit 0 before returning its path. For Brew formulae without an executable, run `brew list --formula --versions <token>`. For casks, run `brew list --cask --versions <token>` and, when `AppPath` is non-empty, require `os.Stat(AppPath)` to succeed. Return explicit failure details; never infer success from rebuild exit status alone.

- [ ] **Step 4: Run tests and commit**

Run: `gofmt -w internal/packages && go test ./internal/packages -v`

Expected: all package tests pass.

```bash
git add internal/packages/types.go internal/packages/verify.go internal/packages/verify_test.go
git commit -m "feat(packages): verify installed providers"
```

---

### Task 5: Package Workflow UI and Combined Review

**Files:**
- Create: `internal/tui/package_flow.go`
- Create: `internal/tui/view_package.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/execution.go`
- Modify: `internal/tui/view_home.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Produces: `packageFlow`, package stage constants, search/apply/verify messages, and package views.
- Extends: `reviewedPlan.Package *packageReview`.
- Consumes: all `internal/packages` APIs from Tasks 1-4 and runner task lookup for `hms`/`nds`.

- [ ] **Step 1: Write failing workflow tests**

```go
func TestPackageSearchDefaultsToNixAndDoesNotWrite(t *testing.T) {
	m := testGuidedModel()
	m.screen = screenPackage
	m.packageFlow = packageFlow{stage: packageChoose, result: packages.SearchResult{
		Candidates: []packages.Candidate{
			{Provider: packages.ProviderNix, ID: "lazydocker", Name: "lazydocker"},
			{Provider: packages.ProviderBrew, Kind: packages.KindFormula, ID: "lazydocker", Name: "lazydocker"},
		},
		Selected: 0,
	}}
	if got := m.packageFlow.result.Candidates[m.packageFlow.result.Selected].Provider; got != packages.ProviderNix {
		t.Fatalf("provider=%s", got)
	}
	if len(m.queue) != 0 || m.mode == modeRunning { t.Fatal("search must not execute") }
}

func TestPackageReviewContainsDiffApplyAndVerify(t *testing.T) {
	m := testGuidedModel()
	proposal := packages.Proposal{Diff: "--- original\n+++ proposed\n+    lazydocker\n", Target: packages.Target{ApplyAction: "hms"}}
	m.buildPackageReview(proposal, packages.VerifySpec{Executable: "lazydocker"})
	if m.screen != screenReview || m.reviewed.Package == nil { t.Fatal("missing package review") }
	out := m.View()
	for _, want := range []string{"lazydocker", "home-manager", "VERIFY", "ENTER CONFIRM"} {
		if !strings.Contains(out, want) { t.Fatalf("missing %q:\n%s", want, out) }
	}
}

func TestConfirmPackageReviewAppliesOnlyAfterConfirmation(t *testing.T) {
	m := testGuidedModel()
	called := false
	m.applyPackage = func(packages.Proposal) (packages.AppliedEdit, error) { called = true; return packages.AppliedEdit{}, nil }
	m.screen = screenReview
	m.reviewed.Package = &packageReview{Proposal: packages.Proposal{}}
	if called { t.Fatal("apply called before confirmation") }
	cmd := m.confirmReviewedPlan()
	if cmd == nil { t.Fatal("confirmation did not schedule apply") }
	_ = cmd()
	if !called { t.Fatal("apply not called after confirmation") }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/tui -run 'TestPackageSearchDefaultsToNixAndDoesNotWrite|TestPackageReviewContainsDiffApplyAndVerify|TestConfirmPackageReviewAppliesOnlyAfterConfirmation' -v`

Expected: compile failure because package flow APIs do not exist.

- [ ] **Step 3: Add package state and asynchronous messages**

```go
type packageStage uint8
const (
	packageSearch packageStage = iota
	packageSearching
	packageChoose
	packagePlacement
)

type packageFlow struct {
	stage packageStage
	query textinput.Model
	result packages.SearchResult
	scope packages.Scope
	sections []string
	section int
	target packages.Target
	proposal packages.Proposal
}

type packageReview struct {
	Proposal packages.Proposal
	Verify packages.VerifySpec
	Applied *packages.AppliedEdit
}

type packageSearchMsg struct{ result packages.SearchResult }
type packageAppliedMsg struct{ edit packages.AppliedEdit; err error }
type packageVerifiedMsg struct{ result packages.VerifyResult }
```

Add `screenPackage`, `packageFlow packageFlow`, `applyPackage func(packages.Proposal) (packages.AppliedEdit, error)`, and `verifyPackage func(packages.VerifySpec) packages.VerifyResult` to Model. Initialize production functions with `packages.Apply` and a closure calling `packages.Verify` with `packages.ExecRunner{}` and `exec.LookPath`.

- [ ] **Step 4: Implement search, placement, review, and execution phases**

Home entry 02 opens Package Search. Enter on a non-empty query runs `packages.Search` in a `tea.Cmd`. Candidate selection defaults to the result's Nix index. Placement chooses scope and one section returned by `packages.Sections`, with `Misc` selected when present. Unsupported target resolution invokes the existing editor handoff instead of creating a proposal.

Extend reviewed plan:

```go
type reviewedPlan struct {
	Action string
	Items []runner.WorkItem
	Package *packageReview
}
```

`buildPackageReview` resolves the target's `ApplyAction`, builds its runner queue, and stores proposal plus verifier. Package Review renders the unified diff, apply commands, verification label, and one confirmation.

On confirmation, schedule `packages.Apply` first. On `packageAppliedMsg` success, store `AppliedEdit` and start the reviewed runner queue. When that queue completes, schedule verification instead of final success. A failed apply, rebuild, or verification enters failure Result. A rebuild failure offers `REVIEW REVERT`; selecting it calls `packages.ProposeRevert` and returns to Review with the reverse diff.

- [ ] **Step 5: Render Monolith package screens**

Search uses `ADD/PACKAGE`, `DECLARATIVE INSTALL`, and a full-width underlined text input. Results use numbered Nix/Brew rows with `DEFAULT` on Nix. Placement shows provider, scope, and section as explicit rows. Review uses cyan additions, rust removals/warnings, exact file path, exact apply command, and verification. Narrow layout stacks metadata under each result.

- [ ] **Step 6: Run full automated checks**

Run: `gofmt -w internal/packages internal/tui && go test ./... && go vet ./...`

Expected: all tests and vet pass without invoking real Nix, Brew, Home Manager, or nix-darwin.

- [ ] **Step 7: Build with Nix and perform fake-repo smoke test**

Run: `nix build .#packages.$(nix eval --impure --raw --expr builtins.currentSystem).default`

Expected: exit 0.

Set `DOTFILES_REPO` to a temporary fixture repository containing the supported flat lists. Run package search through fake adapter tests, review a Nix insertion, apply to the fixture, use a harmless fake `hms` executable, and verify a fixture executable on PATH. Confirm the real dotfiles repo, Homebrew state, Nix profiles, and home directory remain unchanged.

- [ ] **Step 8: Commit the package workflow**

```bash
git add internal/packages internal/tui
git commit -m "feat(tui): add packages declaratively"
```

---

### Task 6: Documentation and End-to-End Verification

**Files:**
- Modify: `README.md`
- Modify: `TESTING.md`
- Modify: `TODO.md`

**Interfaces:**
- Documents: guided maintenance, terminal handoff, package flow, safety behavior, and test commands.
- Consumes: completed execution, TUI, and package plans.

- [ ] **Step 1: Add documentation assertions to smoke tests**

Add this complete test to `internal/smoke/project_test.go`:

```go
func TestReadmeExplainsGuidedPackageWorkflow(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "README.md"))
	if err != nil { t.Fatal(err) }
	readme := string(data)
	for _, want := range []string{
		"review every mutating plan",
		"interactive terminal handoff",
		"Add Package",
		"declarative Nix or Homebrew config",
	} {
		if !strings.Contains(readme, want) { t.Errorf("README missing %q", want) }
	}
}
```

- [ ] **Step 2: Run smoke test and verify RED**

Run: `go test ./internal/smoke -v`

Expected: failure listing the missing phrases.

- [ ] **Step 3: Update user and testing docs**

Document exact navigation and safety contract in README. Add fake-repo package edit, provider partial failure, stale hash, revert, 80x24, `NO_COLOR`, and terminal handoff cases to TESTING. Mark package search, plan preview, and interactive input items complete in TODO only when their tests pass.

- [ ] **Step 4: Run final verification**

Run: `go test ./... && go vet ./... && nix build .#packages.$(nix eval --impure --raw --expr builtins.currentSystem).default`

Expected: every command exits 0.

Run `git status --short` and confirm only intentional files are present. Confirm no staged or tracked `.superpowers/` companion artifacts and no plaintext secrets.

- [ ] **Step 5: Commit documentation**

```bash
git add README.md TESTING.md TODO.md internal/smoke/project_test.go
git commit -m "docs: explain guided package workflow"
```
