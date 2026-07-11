# Native Package Convergence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After a reviewed catalog declaration, converge DNF/APT packages through exact terminal-handoff commands, verify native receipts, and offer a separately reviewed catalog revert when convergence fails.

**Architecture:** A pure native convergence planner converts a validated provider candidate plus resolved host capabilities into exact argument arrays and receipt specs. The TUI applies the catalog transaction first, runs native install as an interactive `runner.WorkItem`, verifies via RPM/DPKG receipts, and never silently reverts or removes packages.

**Tech Stack:** Go 1.24.2, Bubble Tea `ExecProcess`, existing runner queue/history, DNF or DNF5, APT Get, RPM, DPKG Query, fake executables and temporary repositories.

**Prerequisite:** Complete both `2026-07-11-live-os-aware-package-discovery.md` and `2026-07-11-declarative-catalog-placement.md`; this plan consumes their provider states and reviewed catalog transaction.

## Global Constraints

- DNF/APT installation must always originate from an applied reviewed catalog transaction.
- Commands use exact argument arrays; never interpolate package IDs through a shell.
- Native install is interactive terminal work so sudo/password input owns the real terminal.
- DNF uses the detected `dnf` or `dnf5`; APT uses `apt-get`.
- DNF verification uses `rpm -q`; APT verification uses `dpkg-query` status and version output.
- Never guess executable names from package IDs.
- Verification failure is failure even when the install command exits zero.
- Failure after catalog apply keeps the declaration visible and offers a separate reviewed hash-gated catalog revert.
- Never automatically uninstall a native package during catalog revert.
- Retry must skip catalog edits already applied and run only the failed/waiting convergence tail.
- Cancellation, timeout, install failure, verification failure, cleanup warning, and revert failure remain distinct.
- Tests use fake commands, fake receipts, and temporary repos; never call real sudo, DNF, APT, RPM, DPKG, or real configs.
- No password, token, decrypted secret, or unbounded raw output enters history or errors.

## File Map

- `internal/runner/runner.go`: resolve `apt-get`, `rpm`, and `dpkg-query` paths.
- `internal/runner/runner_test.go`: capability resolution and native WorkItem execution mode.
- `internal/system/probe.go`: show detected native verification tools in Doctor/Inspect facts.
- `internal/packages/native.go`: validated convergence plan and exact commands.
- `internal/packages/native_test.go`: provider/ID/capability matrix.
- `internal/packages/types.go`: native receipt fields in `VerifySpec`.
- `internal/packages/verify.go`: RPM and DPKG receipt verification.
- `internal/packages/verify_test.go`: fake receipt success/failure tests.
- `internal/tui/package_flow.go`: build native queue and verification spec.
- `internal/tui/execution.go`: transaction-first/native-queue/verification lifecycle and retry tail.
- `internal/tui/update.go`: native result and revert states.
- `internal/tui/view_package.go`, `view_plan.go`: exact command, handoff warning, receipts, and Result copy.
- `internal/tui/model_test.go`: end-to-end fake Fedora/Debian flows and no-real-write assertions.
- `internal/history/history.go`, `history_test.go`: provider/package receipt summary without raw output.
- `README.md`, `TESTING.md`, `TODO.md`: supported matrices, safe tests, and completed roadmap items.

---

### Task 1: Plan Exact Native Install and Receipt Commands

**Files:**
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/runner_test.go`
- Modify: `internal/system/probe.go`
- Modify: `internal/packages/types.go`
- Create: `internal/packages/native.go`
- Create: `internal/packages/native_test.go`

**Interfaces:**
- Produces: `NativeCapabilities`, `NativeConvergencePlan`, and `PlanNativeConvergence`.
- Extends: `runner.Context` with `AptGetBin`, `RPMBin`, and `DpkgQueryBin`.
- Consumes: a validated DNF/APT `Candidate`; does not execute commands.

- [ ] **Step 1: Write failing exact-command tests**

```go
func TestPlanNativeConvergenceBuildsExactDNFCommands(t *testing.T) {
	plan, err := PlanNativeConvergence(Candidate{Provider: ProviderDNF, Kind: KindPackage, ID: "lazydocker"}, NativeCapabilities{
		SudoBin: "/usr/bin/sudo", DnfBin: "/usr/bin/dnf5", RPMBin: "/usr/bin/rpm",
	})
	if err != nil { t.Fatal(err) }
	wantInstall := CommandSpec{Name: "/usr/bin/sudo", Args: []string{"-H", "/usr/bin/dnf5", "install", "-y", "lazydocker"}, Interactive: true}
	wantReceipt := CommandSpec{Name: "/usr/bin/rpm", Args: []string{"-q", "--qf", "%{NAME}\\t%{VERSION}-%{RELEASE}.%{ARCH}\\n", "--", "lazydocker"}}
	if !reflect.DeepEqual(plan.Install, wantInstall) || !reflect.DeepEqual(plan.Receipt, wantReceipt) { t.Fatalf("plan=%#v", plan) }
}

func TestPlanNativeConvergenceBuildsExactAPTCommands(t *testing.T) {
	plan, err := PlanNativeConvergence(Candidate{Provider: ProviderAPT, Kind: KindPackage, ID: "lazygit"}, NativeCapabilities{
		SudoBin: "/usr/bin/sudo", AptGetBin: "/usr/bin/apt-get", DpkgQueryBin: "/usr/bin/dpkg-query",
	})
	if err != nil { t.Fatal(err) }
	wantInstall := CommandSpec{Name: "/usr/bin/sudo", Args: []string{"-H", "/usr/bin/apt-get", "install", "-y", "lazygit"}, Interactive: true}
	wantReceipt := CommandSpec{Name: "/usr/bin/dpkg-query", Args: []string{"-W", "-f=${db:Status-Abbrev}\t${binary:Package}\t${Version}\\n", "lazygit"}}
	if !reflect.DeepEqual(plan.Install, wantInstall) || !reflect.DeepEqual(plan.Receipt, wantReceipt) { t.Fatalf("plan=%#v", plan) }
}
```

- [ ] **Step 2: Run native planner tests and verify RED**

Run: `go test ./internal/packages -run TestPlanNativeConvergence -count=1 -v`

Expected: compile failure for undefined convergence types.

- [ ] **Step 3: Define validated command metadata**

```go
type NativeCapabilities struct {
	SudoBin, DnfBin, AptGetBin, RPMBin, DpkgQueryBin string
}

type CommandSpec struct {
	Name string
	Args []string
	Interactive bool
}

type NativeConvergencePlan struct {
	Provider Provider
	PackageID string
	Install CommandSpec
	Receipt CommandSpec
}

func PlanNativeConvergence(Candidate, NativeCapabilities) (NativeConvergencePlan, error)
```

Validate provider/kind coherence and package IDs with `^[A-Za-z0-9][A-Za-z0-9+._:@-]*$`; reject `/`, whitespace, controls, leading `-`, empty executable paths, unsupported providers, and missing sudo. Clone every argument slice.

- [ ] **Step 4: Resolve native command paths in runtime facts**

Add to `runner.Context`:

```go
	AptGetBin string
	RPMBin string
	DpkgQueryBin string
```

Resolve with `findExe("apt-get", "/usr/bin/apt-get")`, `findExe("rpm", "/usr/bin/rpm")`, and `findExe("dpkg-query", "/usr/bin/dpkg-query")`. Mirror paths in `system.Facts` and show RPM only with Fedora/DNF, and DPKG Query only with Debian/Ubuntu/APT.

- [ ] **Step 5: Add missing-capability and hostile-ID tests**

Table tests must reject missing sudo/provider/receipt binaries, formula/cask kinds, `-y`, `foo bar`, `foo;rm`, `../foo`, and control bytes. Assert the candidate input and returned argument slices do not alias.

Run: `go test ./internal/runner ./internal/system ./internal/packages -run 'TestPlanNative|TestBuild' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the pure convergence planner**

```bash
git add internal/runner internal/system internal/packages
git commit -m "feat(packages): plan native package convergence"
```

---

### Task 2: Verify Native Provider Receipts

**Files:**
- Modify: `internal/packages/types.go`
- Modify: `internal/packages/verify.go`
- Modify: `internal/packages/verify_test.go`

**Interfaces:**
- Consumes: native provider, exact token, RPM/DPKG executable, and `OutputRunner`.
- Produces: truthful `VerifyResult` for DNF/APT receipts.
- Preserves: existing Nix/Homebrew verification behavior.

- [ ] **Step 1: Write failing RPM/DPKG receipt tests**

```go
func TestVerifyDNFRequiresExactRPMReceipt(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"rpm -q --qf %{NAME}\\t%{VERSION}-%{RELEASE}.%{ARCH}\\n -- lazydocker": {out: "lazydocker\t0.24.1-1.fc44.x86_64\n"},
	}}
	got := Verify(context.Background(), runner, nil, VerifySpec{Provider: ProviderDNF, Kind: KindPackage, Token: "lazydocker", RPMBin: "rpm"})
	if !got.OK || !strings.Contains(got.Detail, "RPM receipt") { t.Fatalf("got=%#v", got) }
}

func TestVerifyAPTRequiresInstalledDPKGStatus(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"dpkg-query -W -f=${db:Status-Abbrev}\\t${binary:Package}\\t${Version}\\n lazygit": {out: "ii \tlazygit\t0.40.2\n"},
	}}
	got := Verify(context.Background(), runner, nil, VerifySpec{Provider: ProviderAPT, Kind: KindPackage, Token: "lazygit", DpkgQueryBin: "dpkg-query"})
	if !got.OK || !strings.Contains(got.Detail, "DPKG receipt") { t.Fatalf("got=%#v", got) }
}
```

- [ ] **Step 2: Run receipt tests and verify RED**

Run: `go test ./internal/packages -run 'TestVerifyDNF|TestVerifyAPT' -count=1 -v`

Expected: failure because `Verify` rejects DNF/APT providers.

- [ ] **Step 3: Extend `VerifySpec` and provider dispatch**

Add `RPMBin string` and `DpkgQueryBin string` to `VerifySpec`. Implement:

```go
func verifyRPMReceipt(ctx context.Context, runner OutputRunner, spec VerifySpec) VerifyResult
func verifyDPKGReceipt(ctx context.Context, runner OutputRunner, spec VerifySpec) VerifyResult
```

RPM runs `rpm -q --qf '%{NAME}\t%{VERSION}-%{RELEASE}.%{ARCH}\n' -- <token>`, splits one tab-delimited record, and requires exact package name plus non-empty version/release/architecture text. DPKG runs the exact format command from Task 1, splits one tab-delimited record, requires status prefix `ii `, exact package token (allowing only an architecture suffix `:<arch>`), and non-empty version. Store only bounded sanitized receipt summary in `Detail`; do not store raw output.

- [ ] **Step 4: Add failure-closed receipt cases**

Cover missing runner/binary/token, nonzero exit with misleading stdout, empty RPM output, a different RPM package, DPKG `rc`/`un` states, different package, malformed tabs, empty version, architecture suffix acceptance, control sanitization, and output larger than the bounded parser limit.

Run: `go test -race ./internal/packages -run 'TestVerify' -count=1`

Expected: PASS with all prior Nix/Homebrew cases unchanged.

- [ ] **Step 5: Commit native verification**

```bash
git add internal/packages/types.go internal/packages/verify.go internal/packages/verify_test.go
git commit -m "feat(packages): verify native package receipts"
```

---

### Task 3: Run Native Convergence Through Terminal Handoff

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/package_flow.go`
- Modify: `internal/tui/execution.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/view_package.go`
- Modify: `internal/tui/view_plan.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: catalog transaction from the placement plan and `NativeConvergencePlan` from Task 1.
- Produces: native `runner.WorkItem`, receipt `VerifySpec`, retry tail, and reviewed catalog revert.
- Uses: existing `tea.ExecProcess` terminal handoff.

- [ ] **Step 1: Write the failing Fedora end-to-end state test**

```go
func TestFedoraPackageReviewAppliesCatalogThenHandsDNFTheTerminal(t *testing.T) {
	repo := fixtureCatalogAndDotfilesRepo(t)
	m := testModelWithRepo(repo, "butler", "linux", "fedora")
	m.runCtx.SudoBin, m.runCtx.DnfBin, m.runCtx.RPMBin = "sudo", "dnf5", "rpm"
	m.packageFlow = completedProviderFixture(packages.Candidate{Provider: packages.ProviderDNF, Kind: packages.KindPackage, ID: "lazydocker", Name: "lazydocker"})
	var handed runner.WorkItem
	m.terminalExec = func(item runner.WorkItem, _ time.Time) tea.Cmd {
		handed = item
		return func() tea.Msg { return stepDoneMsg{} }
	}
	if !m.buildCatalogPackageReview(packages.Candidate{Provider: packages.ProviderDNF, Kind: packages.KindPackage, ID: "lazydocker", Name: "lazydocker"}, catalog.ScopeCurrentHost) { t.Fatal("review not built") }
	cmd := m.confirmReviewedPlan()
	msg := cmd()
	next, cmd := m.Update(msg)
	m = next.(Model)
	for cmd != nil && handed.Name == "" { next, cmd = m.Update(cmd()); m = next.(Model) }
	if handed.Name != "sudo" || !reflect.DeepEqual(handed.Args, []string{"-H", "dnf5", "install", "-y", "lazydocker"}) || handed.Mode != runner.ExecutionInteractive {
		t.Fatalf("handoff=%#v", handed)
	}
}
```

- [ ] **Step 2: Run the state test and verify RED**

Run: `go test ./internal/tui -run TestFedoraPackageReviewAppliesCatalogThenHandsDNFTheTerminal -count=1 -v`

Expected: Review has no native queue and the assertion fails.

- [ ] **Step 3: Build the native queue and verification spec at Review time**

Convert `NativeConvergencePlan.Install` to one immutable work item:

```go
runner.WorkItem{
	TaskLabel: strings.ToUpper(string(plan.Provider)) + " install",
	TaskFirst: true,
	Name: plan.Install.Name,
	Args: append([]string(nil), plan.Install.Args...),
	Dir: m.runCtx.Repo,
	Mode: runner.ExecutionInteractive,
	Retryable: true,
}
```

Build `VerifySpec` only from the reviewed plan: DNF receives `RPMBin`; APT receives `DpkgQueryBin`. Store a cloned `*packages.NativeConvergencePlan` in `packageReview.Native`; clone command args. Review renders exact catalog diffs, exact install command, `TERMINAL HANDOFF · PASSWORD INPUT STAYS NATIVE`, and exact receipt check.

Add a production helper `func cloneTransactionResult(fileedit.TransactionResult) fileedit.TransactionResult` that clones the edit slice, every before/after byte slice, and recovery path slice. Use it whenever applied transaction state enters or leaves Review.

- [ ] **Step 4: Enforce transaction → install → receipt order**

`confirmReviewedPlan` first applies `Catalog`. Only `packageTransactionAppliedMsg` may advance to the native queue. Queue completion starts `verifyPackageCmd`; receipt success finishes Result. A cleanup warning after a committed transaction remains attached and is reported after verification rather than skipping install.

If transaction application fails before any edit, do not run the queue. If it partially applies and cannot safely restore, stop and report recovery paths; never install from an unverified declaration state.

- [ ] **Step 5: Preserve terminal ownership and truthful cancellation**

Native work must take the existing `ExecutionInteractive` branch in `advanceQueue`, invoking `tea.ExecProcess`. Tests inject `terminalExec`; no test executes sudo. Exit 130/143 is `CANCELLED`, leaves the applied catalog declaration visible, and offers reviewed revert. The TUI must not prime sudo credentials or capture password input.

Run: `go test -race ./internal/tui -run 'Test(Fedora|APT|Native|TerminalHandoff)' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit terminal-native convergence**

```bash
git add internal/tui
git commit -m "feat(tui): converge native catalog packages"
```

---

### Task 4: Add Retry, Receipt Failure, Revert, History, and Full OS Matrix

**Files:**
- Modify: `internal/tui/package_flow.go`
- Modify: `internal/tui/execution.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/model_test.go`
- Modify: `internal/history/history.go`
- Modify: `internal/history/history_test.go`
- Modify: `README.md`
- Modify: `TESTING.md`
- Modify: `TODO.md`

**Interfaces:**
- Consumes: applied transaction, step results, and native receipt result.
- Produces: safe retry tail, reviewed transaction revert, bounded history summary, and documented support matrix.

- [ ] **Step 1: Write failing retry and receipt-failure tests**

```go
func TestNativeRetrySkipsAppliedCatalogAndRunsOnlyFailedTail(t *testing.T) {
	m := failedNativeFixture(t, history.StatusFailure)
	originalApplied := cloneTransactionResult(*m.reviewed.Package.CatalogApplied)
	m.reviewFailedTail()
	if !m.reviewed.Package.CatalogEditApplied || !reflect.DeepEqual(*m.reviewed.Package.CatalogApplied, originalApplied) { t.Fatal("retry lost applied catalog state") }
	if len(m.reviewed.Items) != 1 || m.reviewed.Items[0].Name != "sudo" { t.Fatalf("retry=%#v", m.reviewed.Items) }
}

func TestNativeReceiptFailureOffersCatalogRevertWithoutUninstall(t *testing.T) {
	m := failedReceiptFixture(t)
	m.reviewPackageRevert()
	if !m.reviewed.Package.CatalogRevert { t.Fatal("missing catalog revert review") }
	for _, item := range m.reviewed.Items {
		if item.Name == "dnf" || item.Name == "apt-get" || item.Name == "sudo" { t.Fatalf("revert queued uninstall: %#v", item) }
	}
}
```

Add these exact helpers beside the tests:

```go
func failedNativeFixture(t *testing.T, status history.Status) Model {
	t.Helper()
	repo := fixtureCatalogAndDotfilesRepo(t)
	path := filepath.Join(repo, "catalog/tools.yaml")
	before := mustRead(t, path)
	after := append(append([]byte(nil), before...), []byte("# applied fixture\n")...)
	result := fileedit.TransactionResult{Edits: []fileedit.AppliedEdit{{Path: path, Before: before, After: after, BeforeHash: sha256.Sum256(before), AfterHash: sha256.Sum256(after)}}}
	item := runner.WorkItem{TaskLabel: "DNF install", TaskFirst: true, Name: "sudo", Args: []string{"-H", "dnf", "install", "-y", "lazydocker"}, Mode: runner.ExecutionInteractive, Retryable: true}
	m := testModelWithRepo(repo, "butler", "linux", "fedora")
	m.reviewed = reviewedPlan{Action: "package:add:dnf:lazydocker", Items: []runner.WorkItem{item}, Package: &packageReview{
		CatalogApplied: &result, CatalogEditApplied: true,
		Native: &packages.NativeConvergencePlan{Provider: packages.ProviderDNF, PackageID: "lazydocker"},
	}}
	m.queue = []runner.WorkItem{item}
	m.stepResults = []stepResult{{Item: item, Status: status, Err: errors.New("fixture failure")}}
	m.runErr = errors.New("fixture failure")
	return m
}

func failedReceiptFixture(t *testing.T) Model {
	m := failedNativeFixture(t, history.StatusSuccess)
	m.reviewed.Package.Result = &packages.VerifyResult{OK: false, Detail: "RPM receipt missing", Err: errors.New("receipt missing")}
	return m
}
```

Update the two tests to call `failedNativeFixture(t, history.StatusFailure)` and `failedReceiptFixture(t)`.

- [ ] **Step 2: Run retry/revert tests and verify RED**

Run: `go test ./internal/tui -run 'TestNativeRetry|TestNativeReceiptFailure' -count=1 -v`

Expected: current retry/revert logic assumes one package proposal and fails.

- [ ] **Step 3: Implement retry and reviewed catalog revert**

Retry clones the immutable applied transaction, marks catalog edit `DONE`, and copies only failed/waiting native queue items. It never calls `ApplyTransaction` again. Receipt failure counts as a failed tail with no queue items; retry reruns verification only when the install step succeeded, otherwise reruns install then verification.

Revert calls `ProposeTransactionRevert` against every applied file hash, enters the same multi-file Review viewport, queues no uninstall, and labels verification `DECLARATION REVERT ONLY · INSTALLED PACKAGE LEFT UNCHANGED`. Stale files leave the user in Result with exact sanitized detail.

- [ ] **Step 4: Store bounded provider/package history**

Extend history entries with optional `Provider` and `PackageID` JSON fields. Record logical action such as `package:add:dnf:lazydocker`, step statuses/durations, and bounded receipt summary. Do not record raw command output, environment, catalog bytes, or password prompts. Old history JSON without the new fields must still decode.

- [ ] **Step 5: Add complete fake Fedora/Debian/macOS matrices**

Fake-repository tests cover:

- Fedora DNF success, install failure, cancellation, receipt failure, retry, and catalog revert;
- Debian/Ubuntu APT equivalents;
- Nix/Homebrew flows remain declarative and unchanged;
- unsupported native tab remains disabled and never forms a convergence plan;
- no command is created from a hostile ID;
- no real repo files, package receipts, profiles, or home paths are touched;
- 80x24 and `NO_COLOR` Review/Running/Result views remain usable;
- real PTY smoke shows TUI active, child owns terminal, and TUI restored.

- [ ] **Step 6: Update docs and run the final feature gate**

Document in `README.md` that host identity comes from `/etc/os-release`, provider tabs are immediate, and native installs are catalog-backed/review-gated. Add exact fake and PTY commands to `TESTING.md`. Mark the corresponding search/catalog/native TODO items complete without claiming unsupported package managers.

Run:

```sh
gofmt -w internal/runner internal/system internal/fileedit internal/catalog internal/packages internal/tui internal/history
go test -race ./... -count=1
go test ./... -count=1
go vet ./...
git diff --check
NO_COLOR=1 go test ./internal/tui -run 'Test(PackagePipeline|Catalog|Native|PackageWorkflowViewsFit80x24)' -count=1 -v
nix build .#packages.$(nix eval --impure --raw --expr builtins.currentSystem).default
```

Compile `internal/tui`'s test binary and run `TestPTYTerminalHandoffSmoke` under `/usr/bin/script` with `SYS_BOZO_PTY_SMOKE=1`; require `HANDOFF_CHILD_OK` and `HANDOFF_RESTORED_OK`. Run the repository TruffleHog hook against staged files. Remove generated `flake.lock` residue and require `git status --short` to contain only intended tracked changes.

```bash
git add internal README.md TESTING.md TODO.md
git commit -m "feat(packages): finish OS-aware native package flow"
```
