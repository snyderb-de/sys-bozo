# Live OS-Aware Package Discovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the aggregate Nix/Homebrew search with truthful host-aware Nix/Homebrew/DNF/APT progress events, a Staged Pipeline animation, and provider tabs that unlock independently.

**Architecture:** Runtime capabilities produce an ordered provider specification: Nix when available plus exactly one detected native manager. Focused provider adapters emit real phase changes and candidates into a request-scoped event stream; Bubble Tea owns only animation frames, tab state, and stale-request rejection.

**Tech Stack:** Go 1.24.2, Bubble Tea 1.3.10, Bubbles, Lip Gloss, `context`, `os/exec`, `/etc/os-release`, fake command runners.

## Global Constraints

- Host identity comes from live runtime facts, not inference from Nix or repository files.
- macOS selects Homebrew; Fedora selects DNF; Debian/Ubuntu select APT.
- Exactly one detected native provider is shown beside Nix.
- Unsupported or missing native managers remain visible and disabled with a specific reason.
- A completed provider tab becomes browsable while other providers continue searching.
- Provider lists, selection, scroll, error, and ranking remain independent.
- Animation may change glyphs only; phases, counts, completion, and failure come from real events.
- Never fabricate percentage completion.
- `Esc` cancels unfinished providers without erasing completed results.
- Tests must use fake runners and must not contact package repositories or mutate package state.
- `NO_COLOR` must retain semantic labels without ANSI.
- Search events and errors must not expose raw unbounded subprocess output or secrets.

## File Map

- `internal/runner/runner.go`: resolve `apt-cache` and carry it in `runner.Context`.
- `internal/system/probe.go`: expose APT capability in system facts/status.
- `internal/packages/types.go`: DNF/APT providers, phases, provider specs, and search events.
- `internal/packages/providers.go`: pure host-to-provider selection.
- `internal/packages/providers_test.go`: OS/provider matrix and disabled reasons.
- `internal/packages/search.go`: Nix/Homebrew/DNF/APT adapter parsing.
- `internal/packages/search_session.go`: concurrent event session and cancellation.
- `internal/packages/search_test.go`: provider parser and command tests.
- `internal/packages/search_session_test.go`: out-of-order events, cancellation, and closure.
- `internal/tui/package_flow.go`: provider-local state, event subscription, keys, and ticks.
- `internal/tui/view_package.go`: Staged Pipeline and result tabs.
- `internal/tui/model.go`, `internal/tui/update.go`: injected session and message handling.
- `internal/tui/model_test.go`: immediate browsing, independent tabs, stale events, and cancellation.
- `TESTING.md`: deterministic search UI commands and real-PTY animation smoke.

---

### Task 1: Detect the Host-Native Search Provider

**Files:**
- Modify: `internal/runner/runner.go`
- Modify: `internal/system/probe.go`
- Modify: `internal/packages/types.go`
- Create: `internal/packages/providers.go`
- Create: `internal/packages/providers_test.go`
- Test: `internal/runner/runner_test.go`
- Test: `internal/system/probe_test.go`

**Interfaces:**
- Produces: `ProviderDNF`, `ProviderAPT`, `SearchPhase`, `ProviderSpec`, `HostCapabilities`.
- Produces: `func DetectProviderSpecs(HostCapabilities) []ProviderSpec`.
- Extends: `runner.Context.AptCacheBin string` and `system.Facts.AptCachePath string`.
- Consumes: `OS`, `OSID`, and resolved executable paths only.

- [ ] **Step 1: Write the failing provider matrix test**

```go
func TestDetectProviderSpecsUsesExactlyOneNativeManager(t *testing.T) {
	tests := []struct {
		name string
		host HostCapabilities
		want []ProviderSpec
	}{
		{"mac", HostCapabilities{OS: "darwin", NixBin: "nix", BrewBin: "brew"}, []ProviderSpec{
			{Provider: ProviderNix, Label: "NIX", Command: "nix", Enabled: true},
			{Provider: ProviderBrew, Label: "HOMEBREW", Command: "brew", Enabled: true},
		}},
		{"fedora", HostCapabilities{OS: "linux", OSID: "fedora", NixBin: "nix", DnfBin: "dnf5"}, []ProviderSpec{
			{Provider: ProviderNix, Label: "NIX", Command: "nix", Enabled: true},
			{Provider: ProviderDNF, Label: "DNF", Command: "dnf5", Enabled: true},
		}},
		{"ubuntu missing apt", HostCapabilities{OS: "linux", OSID: "ubuntu", NixBin: "nix"}, []ProviderSpec{
			{Provider: ProviderNix, Label: "NIX", Command: "nix", Enabled: true},
			{Provider: ProviderAPT, Label: "APT", DisabledReason: "apt-cache is not installed"},
		}},
		{"arch unsupported", HostCapabilities{OS: "linux", OSID: "arch", NixBin: "nix"}, []ProviderSpec{
			{Provider: ProviderNix, Label: "NIX", Command: "nix", Enabled: true},
			{Provider: ProviderNativeUnsupported, Label: "PACMAN", DisabledReason: "pacman search is not supported yet"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DetectProviderSpecs(tt.host); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v want %#v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `go test ./internal/packages -run TestDetectProviderSpecsUsesExactlyOneNativeManager -count=1 -v`

Expected: compile failure for undefined provider capability types.

- [ ] **Step 3: Add the exact provider and capability types**

```go
type Provider string

const (
	ProviderNix               Provider = "nix"
	ProviderBrew              Provider = "brew"
	ProviderDNF               Provider = "dnf"
	ProviderAPT               Provider = "apt"
	ProviderNativeUnsupported Provider = "native-unsupported"
)

type SearchPhase string

const (
	SearchStarting  SearchPhase = "starting"
	SearchQuerying  SearchPhase = "querying-index"
	SearchParsing   SearchPhase = "parsing"
	SearchDone      SearchPhase = "done"
	SearchFailed    SearchPhase = "failed"
	SearchCancelled SearchPhase = "cancelled"
	SearchTimedOut  SearchPhase = "timed-out"
)

type HostCapabilities struct {
	OS, OSID, Arch                  string
	NixBin, BrewBin, DnfBin         string
	AptCacheBin                     string
}

type ProviderSpec struct {
	Provider       Provider
	Label          string
	Command        string
	Enabled        bool
	DisabledReason string
}
```

- [ ] **Step 4: Implement the pure host/provider mapping**

```go
func DetectProviderSpecs(host HostCapabilities) []ProviderSpec {
	var specs []ProviderSpec
	if host.NixBin != "" {
		specs = append(specs, ProviderSpec{Provider: ProviderNix, Label: "NIX", Command: host.NixBin, Enabled: true})
	}
	native := ProviderSpec{}
	switch {
	case host.OS == "darwin":
		native = availableSpec(ProviderBrew, "HOMEBREW", host.BrewBin, "Homebrew is not installed")
	case host.OS == "linux" && host.OSID == "fedora":
		native = availableSpec(ProviderDNF, "DNF", host.DnfBin, "dnf/dnf5 is not installed")
	case host.OS == "linux" && (host.OSID == "debian" || host.OSID == "ubuntu"):
		native = availableSpec(ProviderAPT, "APT", host.AptCacheBin, "apt-cache is not installed")
	case host.OS == "linux" && (host.OSID == "arch" || host.OSID == "manjaro"):
		native = ProviderSpec{Provider: ProviderNativeUnsupported, Label: "PACMAN", DisabledReason: "pacman search is not supported yet"}
	case host.OS == "linux" && strings.Contains(host.OSID, "suse"):
		native = ProviderSpec{Provider: ProviderNativeUnsupported, Label: "ZYPPER", DisabledReason: "zypper search is not supported yet"}
	case host.OS == "linux" && host.OSID == "alpine":
		native = ProviderSpec{Provider: ProviderNativeUnsupported, Label: "APK", DisabledReason: "apk search is not supported yet"}
	case host.OS == "linux":
		native = ProviderSpec{Provider: ProviderNativeUnsupported, Label: "NATIVE", DisabledReason: "native search is not supported for " + valueOr(host.OSID, "this Linux distribution")}
	}
	if native.Label != "" {
		specs = append(specs, native)
	}
	return specs
}
```

Add private helpers `availableSpec` and `valueOr` in the same file; `availableSpec` sets `Enabled` only when `command != ""`.

- [ ] **Step 5: Resolve APT capability from the host**

Add `AptCacheBin string` to `runner.Context` and initialize it with `findExe("apt-cache", "/usr/bin/apt-cache")`. Add `AptCachePath string` to `system.Facts`, populate it with `exec.LookPath("apt-cache")`, and render it only for Debian/Ubuntu status. Extend runner/system tests with injected Linux facts; do not read the developer machine's `/etc/os-release` in unit tests.

- [ ] **Step 6: Run the task gate and commit**

Run: `gofmt -w internal/runner internal/system internal/packages && go test ./internal/runner ./internal/system ./internal/packages -count=1`

Expected: PASS.

```bash
git add internal/runner internal/system internal/packages/types.go internal/packages/providers.go internal/packages/providers_test.go
git commit -m "feat(packages): detect native search provider"
```

---

### Task 2: Add Provider Adapters and Incremental Search Events

**Files:**
- Modify: `internal/packages/search.go`
- Create: `internal/packages/search_session.go`
- Modify: `internal/packages/search_test.go`
- Create: `internal/packages/search_session_test.go`

**Interfaces:**
- Consumes: `ProviderSpec`, `OutputRunner`, query text, and `context.Context`.
- Produces: `SearchAdapter`, `SearchRequest`, `SearchEvent`, `NewSearchAdapters`, and `StartSearch`.
- Preserves: exact provider-native IDs in `Candidate.ID`.

- [ ] **Step 1: Write failing DNF and APT parser tests**

```go
func TestDNFSearchParsesNameAndSummaryWithoutHeaders(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"dnf search --all --quiet lazy": {out: "lazydocker.x86_64 : Docker terminal UI\npython3-lazy-object-proxy.x86_64 : Proxy objects\n"},
	}}
	got, err := searchDNF(context.Background(), runner, "dnf", "lazy", nil)
	if err != nil { t.Fatal(err) }
	want := Candidate{Provider: ProviderDNF, Kind: KindPackage, ID: "lazydocker", Name: "lazydocker", Description: "Docker terminal UI"}
	if len(got) != 2 || !reflect.DeepEqual(got[0], want) { t.Fatalf("got %#v", got) }
}

func TestAPTSearchPreservesExactPackageID(t *testing.T) {
	runner := fakeOutputRunner{responses: map[string]fakeOutputResponse{
		"apt-cache search --names-only lazy": {out: "lazydocker - Docker terminal UI\npython3-lazy-object-proxy - Proxy objects\n"},
	}}
	got, err := searchAPT(context.Background(), runner, "apt-cache", "lazy", nil)
	if err != nil { t.Fatal(err) }
	want := Candidate{Provider: ProviderAPT, Kind: KindPackage, ID: "lazydocker", Name: "lazydocker", Description: "Docker terminal UI"}
	if len(got) != 2 || !reflect.DeepEqual(got[0], want) { t.Fatalf("got %#v", got) }
}
```

- [ ] **Step 2: Run parser tests and verify RED**

Run: `go test ./internal/packages -run 'TestDNFSearch|TestAPTSearch' -count=1 -v`

Expected: compile failure for undefined search functions.

- [ ] **Step 3: Implement adapters with real phase callbacks**

Define:

```go
type PhaseReporter func(SearchPhase)

type SearchAdapter interface {
	Provider() Provider
	Search(context.Context, string, PhaseReporter) ([]Candidate, error)
}

type commandSearchAdapter struct {
	provider Provider
	runner OutputRunner
	command string
}
```

`commandSearchAdapter.Search` must emit `SearchQuerying` immediately before command execution and `SearchParsing` only after successful output is received. It dispatches to the existing Nix/Homebrew functions or the new DNF/APT functions. DNF executes `dnf search --all --quiet <query>`; APT executes `apt-cache search --names-only <query>`. Parsers sort by exact ID, discard headers/blank lines, bound descriptions to 512 bytes, and reject IDs containing whitespace or control characters.

Add:

```go
func NewSearchAdapters(specs []ProviderSpec, runner OutputRunner) []SearchAdapter {
	adapters := make([]SearchAdapter, 0, len(specs))
	for _, spec := range specs {
		if spec.Enabled {
			adapters = append(adapters, commandSearchAdapter{provider: spec.Provider, runner: runner, command: spec.Command})
		}
	}
	return adapters
}
```

- [ ] **Step 4: Write the failing out-of-order session test**

```go
func TestStartSearchEmitsProviderResultsImmediatelyAndCloses(t *testing.T) {
	nixGate := make(chan struct{})
	adapters := []SearchAdapter{
		fakeAdapter{provider: ProviderNix, gate: nixGate, candidates: []Candidate{{Provider: ProviderNix, ID: "nix-result"}}},
		fakeAdapter{provider: ProviderDNF, candidates: []Candidate{{Provider: ProviderDNF, ID: "dnf-result"}}},
	}
	events := StartSearch(context.Background(), SearchRequest{RequestID: 9, Query: "tool"}, adapters)
	first := <-events
	for first.Phase != SearchDone { first = <-events }
	if first.Provider != ProviderDNF || first.Candidates[0].ID != "dnf-result" { t.Fatalf("first done=%#v", first) }
	close(nixGate)
	var sawNix, sawFinished bool
	for event := range events {
		sawNix = sawNix || event.Provider == ProviderNix && event.Phase == SearchDone
		sawFinished = sawFinished || event.Phase == SearchSessionFinished
	}
	if !sawNix || !sawFinished { t.Fatalf("nix=%v finished=%v", sawNix, sawFinished) }
}
```

- [ ] **Step 5: Implement the context-safe event session**

Add:

```go
const SearchSessionFinished SearchPhase = "session-finished"

type SearchRequest struct { RequestID uint64; Query string }

type SearchEvent struct {
	RequestID uint64
	Provider Provider
	Phase SearchPhase
	Candidates []Candidate
	Err error
	At time.Time
}

func StartSearch(ctx context.Context, request SearchRequest, adapters []SearchAdapter) <-chan SearchEvent
```

Implementation requirements:

1. Return a buffered channel sized to `max(4, len(adapters)*4)`.
2. Start one goroutine per adapter and emit `SearchStarting` before `adapter.Search`.
3. Use a context-aware `emit` helper for intermediate events: `select { case events <- event: case <-ctx.Done(): }`.
4. Classify `context.Canceled` as `SearchCancelled`, `context.DeadlineExceeded` as `SearchTimedOut`, and all other errors as `SearchFailed`.
5. Clone candidate slices before sending.
6. Terminal provider events and `SearchSessionFinished` use a non-blocking `emitTerminal` helper (`select { case events <- event: default: }`) so cancellation remains observable to an active consumer but an abandoned consumer cannot leak a goroutine.
7. Close the channel only after a `sync.WaitGroup` has joined every adapter and the final terminal event has been attempted.
8. Never send raw command output in an event.

- [ ] **Step 6: Verify cancellation and run the package gate**

Add tests proving cancellation closes the channel, a failed provider does not stop a successful one, and no sender blocks when the consumer cancels.

Run: `go test -race ./internal/packages -run 'Test(StartSearch|DNFSearch|APTSearch|Search)' -count=1`

Expected: PASS under the race detector.

```bash
git add internal/packages
git commit -m "feat(packages): stream provider search events"
```

---

### Task 3: Store Provider-Local TUI State and Unlock Results Immediately

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/package_flow.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: `DetectProviderSpecs`, `NewSearchAdapters`, and `StartSearch` from Tasks 1-2.
- Produces: provider-local TUI state, channel subscription commands, tabs, and animation ticks.
- Replaces: aggregate `searchPackage func(context.Context, string) packages.SearchResult`.

- [ ] **Step 1: Write failing immediate-browsing and independent-selection tests**

```go
func TestCompletedProviderIsBrowsableWhileOtherProviderSearches(t *testing.T) {
	m := packageTabsFixture()
	m.packageFlow.stage = packageSearching
	m.packageFlow.providers = []packageProviderState{
		{Spec: packages.ProviderSpec{Provider: packages.ProviderNix, Label: "NIX", Enabled: true}, Phase: packages.SearchDone, Candidates: []packages.Candidate{{Provider: packages.ProviderNix, ID: "hello"}}},
		{Spec: packages.ProviderSpec{Provider: packages.ProviderDNF, Label: "DNF", Enabled: true}, Phase: packages.SearchQuerying},
	}
	next, _ := m.handlePackageKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(Model)
	if got.packageFlow.stage != packagePlacement { t.Fatalf("stage=%v", got.packageFlow.stage) }
}

func TestPackageTabsPreserveSelectionPerProvider(t *testing.T) {
	m := packageTabsFixture()
	m.packageFlow.activeProvider = 0
	m.packageFlow.providers[0].Selected = 2
	m.packageFlow.providers[1].Selected = 1
	next, _ := m.handlePackageKey(tea.KeyMsg{Type: tea.KeyTab})
	got := next.(Model)
	if got.packageFlow.activeProvider != 1 || got.packageFlow.providers[0].Selected != 2 || got.packageFlow.providers[1].Selected != 1 {
		t.Fatalf("flow=%#v", got.packageFlow)
	}
}
```

Add this fixture in `internal/tui/model_test.go` with the tests:

```go
func packageTabsFixture() Model {
	m := New()
	m.width, m.height = 80, 24
	m.packageFlow = newPackageFlow(80)
	m.packageFlow.stage = packageSearching
	m.packageFlow.providers = []packageProviderState{
		{Spec: packages.ProviderSpec{Provider: packages.ProviderNix, Label: "NIX", Command: "nix", Enabled: true}, Phase: packages.SearchDone, Candidates: []packages.Candidate{{Provider: packages.ProviderNix, Kind: packages.KindPackage, ID: "one"}, {Provider: packages.ProviderNix, Kind: packages.KindPackage, ID: "two"}, {Provider: packages.ProviderNix, Kind: packages.KindPackage, ID: "three"}}},
		{Spec: packages.ProviderSpec{Provider: packages.ProviderDNF, Label: "DNF", Command: "dnf", Enabled: true}, Phase: packages.SearchQuerying, Candidates: []packages.Candidate{{Provider: packages.ProviderDNF, Kind: packages.KindPackage, ID: "native-one"}, {Provider: packages.ProviderDNF, Kind: packages.KindPackage, ID: "native-two"}}},
	}
	return m
}
```

- [ ] **Step 2: Run focused TUI tests and verify RED**

Run: `go test ./internal/tui -run 'TestCompletedProviderIsBrowsable|TestPackageTabsPreserveSelection' -count=1 -v`

Expected: compile failure for provider-local state.

- [ ] **Step 3: Replace aggregate result state with provider-local state**

Add to `package_flow.go`:

```go
type packageProviderState struct {
	Spec packages.ProviderSpec
	Phase packages.SearchPhase
	PhaseDetail string
	StartedAt time.Time
	Elapsed time.Duration
	Candidates []packages.Candidate
	Err error
	Selected int
	Scroll int
}

type packageSearchEventMsg struct {
	requestID uint64
	events <-chan packages.SearchEvent
	event packages.SearchEvent
	ok bool
}

type packageAnimationTickMsg struct { requestID uint64 }
```

Replace `packageFlow.result` with `providers []packageProviderState`, `activeProvider int`, `searchComplete bool`, and `animationFrame int`. Add helpers `activePackageProvider`, `providerIndex`, `hasUnfinishedProviders`, and `selectedPackageCandidate`; every helper must bounds-check and return `(value, bool)`.

- [ ] **Step 4: Subscribe to one event at a time without blocking Update**

Define:

```go
func waitPackageSearchEvent(requestID uint64, events <-chan packages.SearchEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		return packageSearchEventMsg{requestID: requestID, events: events, event: event, ok: ok}
	}
}

func packageAnimationTick(requestID uint64) tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg {
		return packageAnimationTickMsg{requestID: requestID}
	})
}
```

In `Update`, reject mismatched request IDs. Apply one event immutably to its provider state, then return `waitPackageSearchEvent` again while `ok`. On channel close, clear the cancel function and mark `searchComplete`. Tick messages increment `animationFrame` only while a provider is unfinished and schedule the next tick.

- [ ] **Step 5: Implement immediate tab/key behavior**

During both `packageSearching` and `packageChoose`:

- `Tab`/`Shift+Tab` cycle only enabled or failed provider tabs; disabled tabs are skipped.
- `j`/`down` and `k`/`up` mutate only the active provider's `Selected` and visible-window `Scroll`.
- `Enter` begins placement only when the active provider has at least one completed candidate.
- `Esc` cancels the current context but retains completed state; a second `Esc` returns to a fresh query screen.
- Failed tabs are focusable; they show an error but cannot enter placement.
- The first Nix tab is active when enabled; otherwise choose the first enabled native tab.

Update `Model` injection to:

```go
	startPackageSearch func(context.Context, packages.SearchRequest, []packages.ProviderSpec) <-chan packages.SearchEvent
```

`New` builds specs from `runner.Context`, adapters with `packages.ExecRunner{}`, and delegates to `packages.StartSearch`.

- [ ] **Step 6: Add stale-event, cancellation, and timeout tests**

Tests must prove an old request event is ignored, a new search cancels the old context, `Esc` preserves completed candidates, timeout state differs from cancellation, and a closed event channel does not spin or resubscribe.

Run: `go test -race ./internal/tui -run 'Test(Package|CompletedProvider)' -count=1`

Expected: PASS.

```bash
git add internal/tui/model.go internal/tui/package_flow.go internal/tui/update.go internal/tui/model_test.go
git commit -m "feat(tui): browse provider results during search"
```

---

### Task 4: Render the Staged Pipeline and OS-Aware Result Tabs

**Files:**
- Modify: `internal/tui/view_package.go`
- Modify: `internal/tui/styles.go`
- Modify: `internal/tui/model_test.go`
- Modify: `TESTING.md`

**Interfaces:**
- Consumes: provider-local state from Task 3.
- Produces: `renderPackagePipeline`, `renderPackageTabs`, and provider-specific results.
- Preserves: Monolith spacing, Afterburner semantic colors, 80x24 usability, and `NO_COLOR` meaning.

- [ ] **Step 1: Write failing visual-semantic tests**

```go
func TestPackagePipelineShowsRealHostProviderPhasesAndImmediateTabs(t *testing.T) {
	m := packageTabsFixture()
	m.width, m.height = 80, 24
	m.runCtx.OS, m.runCtx.OSID, m.runCtx.NixSystem = "linux", "fedora", "x86_64-linux"
	m.packageFlow.stage = packageSearching
	m.packageFlow.providers[0].Phase = packages.SearchDone
	m.packageFlow.providers[0].Candidates = []packages.Candidate{{Provider: packages.ProviderNix, ID: "hello"}}
	m.packageFlow.providers[1].Phase = packages.SearchQuerying
	view := m.viewPackage()
	for _, want := range []string{"DISCOVERY PIPELINE", "01  DETECT", "FEDORA / X86_64", "02  DISPATCH", "NIX", "DONE", "DNF", "QUERYING INDEX", "[ NIX 1 ]", "[ DNF … ]"} {
		if !strings.Contains(view, want) { t.Fatalf("missing %q\n%s", want, view) }
	}
	if strings.Count(view, "\n")+1 > 24 { t.Fatalf("view exceeds 24 rows\n%s", view) }
}

func TestPackagePipelineNoColorHasNoANSIAndKeepsStateLabels(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := packageTabsFixture()
	m.styles = newUIStyles(true)
	view := m.viewPackage()
	if strings.Contains(view, "\x1b[") { t.Fatalf("ANSI in NO_COLOR view: %q", view) }
	for _, want := range []string{"DETECT", "DISPATCH", "DONE", "SEARCHING"} {
		if !strings.Contains(view, want) { t.Fatalf("missing %q", want) }
	}
}
```

- [ ] **Step 2: Run the visual tests and verify RED**

Run: `NO_COLOR=1 go test ./internal/tui -run 'TestPackagePipeline' -count=1 -v`

Expected: failures because the current searching view has no pipeline or tabs.

- [ ] **Step 3: Implement the pipeline renderer**

Add focused render helpers in `view_package.go`:

```go
func renderPackagePipeline(m Model, width int) []string
func renderPackageTabs(m Model, width int) string
func renderProviderResults(m Model, provider packageProviderState, width, limit int) []string
func packagePhaseLabel(SearchPhase) string
func packageSweep(frame, width int) string
```

Render these fixed stages: `01  DETECT`, `02  DISPATCH`, `03  RANK`, and `04  PRESENT`. Detection uses `runCtx.OSID`/`runCtx.OS` and `runCtx.NixSystem`; dispatch renders every provider state. `packageSweep` may animate cyan blocks but must never contain a numeric percentage. Completed rows show candidate count and elapsed duration. Disabled rows show `DISABLED` plus the reason; failures show `FAILED`, a sanitized/truncated error, and `ESC SEARCH AGAIN` retry guidance.

The tab strip uses active style only for the active tab and includes state/count text: `[ NIX 18 ]`, `[ DNF … ]`, `[ APT FAILED ]`, or `[ PACMAN DISABLED ]`. Provider results render only from the active provider and use that provider's `Selected` and `Scroll`.

- [ ] **Step 4: Verify responsive rendering and keyboard hints**

At 80x24 show at most four results beneath the compact pipeline. At taller sizes show up to twelve. Hints must include `TAB SOURCE`, `J/K MOVE`, `ENTER PLACE`, and `ESC CANCEL` while any search is active; after completion use `ESC SEARCH`.

Run: `NO_COLOR=1 PACKAGE_VISUAL_LOG=1 go test ./internal/tui -run 'TestPackagePipeline|TestPackageWorkflowViewsFit80x24' -count=1 -v`

Expected: PASS with no ANSI and no view above 24 rows.

- [ ] **Step 5: Document the deterministic and PTY checks**

Add to `TESTING.md`:

```sh
go test -race ./internal/packages ./internal/tui -run 'Test(StartSearch|PackagePipeline|CompletedProvider|PackageTabs)' -count=1 -v
SYS_BOZO_PTY_SMOKE=1 go test ./internal/tui -run TestPTYTerminalHandoffSmoke -count=1 -v
```

Explain that provider tests use fakes and the PTY test invokes no package manager.

- [ ] **Step 6: Run the milestone gate and commit**

Run:

```sh
gofmt -w internal/runner internal/system internal/packages internal/tui
go test -race ./... -count=1
go vet ./...
git diff --check
nix build .#packages.$(nix eval --impure --raw --expr builtins.currentSystem).default
```

Expected: all commands exit zero. Remove any Nix-generated intent-to-add `flake.lock` residue before committing.

```bash
git add internal/runner internal/system internal/packages internal/tui TESTING.md
git commit -m "feat(tui): animate OS-aware package discovery"
```
