# Monolith Guided TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace immediate-run tabs with the approved Home → Select → Review → Running → Result workflow and render it with Monolith spacing plus Afterburner colors.

**Architecture:** Introduce an explicit screen state machine and immutable reviewed plan while preserving existing task definitions and runner behavior. Split the oversized TUI file by responsibility, then migrate Config, Audit, Doctor, and History behind an Inspect menu.

**Tech Stack:** Go 1.24.2, Bubble Tea 1.3.10, Bubbles viewport/spinner, Lip Gloss 1.1.0.

## Global Constraints

- Every mutating action must pass through Review and a confirmation key.
- `hms`, `nds`, and other shortcuts preselect work; they never execute directly.
- Use graphite `#0a0d10`, bone `#dae4ea`/`#f4f7f8`, steel `#60717c`, amber `#ffcb6b`, cyan `#66d9ef`, green `#7ee787`, rust `#ff8f70`, and rule `#27343c` only for semantic roles defined in the design.
- Layout must remain useful at 80x24, target 100 columns, and cap expansion at 140 columns.
- `NO_COLOR` must preserve labels, symbols, spacing, and selection markers.
- Preserve Config, Audit, Doctor, History, streamed logs, and terminal handoff.
- No package-add implementation belongs in this plan; Home renders Add Package as disabled `LOCKED` and navigation skips it until the package plan enables it.

## File Map

- `internal/tui/model.go`: model data and constructors only.
- `internal/tui/update.go`: message handling and navigation.
- `internal/tui/execution.go`: review construction, confirmation, queue progression, and terminal handoff.
- `internal/tui/styles.go`: Monolith/Afterburner semantic styles and responsive primitives.
- `internal/tui/view_home.go`: Home and Inspect launch surfaces.
- `internal/tui/view_plan.go`: Select, Review, Running, and Result screens.
- `internal/tui/view_inspect.go`: Config, Audit, Doctor, and History rendering.
- `internal/tui/model_test.go`: state machine, review gate, responsive rendering, and preserved inspect behavior.
- `internal/history/history.go`: read recent history in addition to append.
- `internal/history/history_test.go`: XDG-backed history parsing tests.

---

### Task 1: Explicit Screen State and Review Gate

**Files:**
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Produces: `type screen uint8` and screen constants.
- Produces: `type reviewedPlan struct { Action string; Items []runner.WorkItem }`.
- Produces: `openMaintenance`, `reviewSelection`, and `confirmReviewedPlan` methods.
- Consumes: `runner.BuildQueue`, `runner.Task`, and terminal handoff from the interactive execution plan.

- [ ] **Step 1: Write failing review-gate tests**

```go
func TestMaintenanceSelectionBuildsReviewWithoutRunning(t *testing.T) {
	m := testGuidedModel()
	m.openMaintenance("hms")
	if m.screen != screenMaintenance || !m.selected["hms"] {
		t.Fatalf("screen=%v selected=%v", m.screen, m.selected)
	}

	m.reviewSelection()
	if m.screen != screenReview || len(m.reviewed.Items) != 1 {
		t.Fatalf("screen=%v reviewed=%#v", m.screen, m.reviewed)
	}
	if m.mode == modeRunning || len(m.queue) != 0 {
		t.Fatal("review must not start execution")
	}
}

func TestConfirmRunsExactReviewedItems(t *testing.T) {
	m := testGuidedModel()
	want := runner.WorkItem{Name: "home-manager", Args: []string{"switch"}}
	m.screen = screenReview
	m.reviewed = reviewedPlan{Action: "hms", Items: []runner.WorkItem{want}}
	m.terminalExec = func(runner.WorkItem, time.Time) tea.Cmd {
		return func() tea.Msg { return stepDoneMsg{} }
	}

	cmd := m.confirmReviewedPlan()
	if cmd == nil || m.screen != screenRunning || m.mode != modeRunning {
		t.Fatalf("screen=%v mode=%v cmd=%v", m.screen, m.mode, cmd)
	}
	if diff := cmpWorkItems(m.queue, []runner.WorkItem{want}); diff != "" {
		t.Fatal(diff)
	}
}

func TestShortcutPreselectsWithoutExecuting(t *testing.T) {
	m := testGuidedModel()
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	got := next.(Model)
	if got.screen != screenMaintenance || !got.selected["hms"] || got.mode == modeRunning {
		t.Fatalf("screen=%v selected=%v mode=%v", got.screen, got.selected, got.mode)
	}
}
```

Add deterministic helpers in the test file:

```go
func testGuidedModel() Model {
	ctx := runner.Context{HomeManager: "home-manager", OS: "darwin"}
	return Model{
		screen: screenHome,
		runCtx: ctx,
		tasks: runner.DefaultTasks(ctx),
		selected: map[string]bool{},
		terminalExec: func(runner.WorkItem, time.Time) tea.Cmd { return nil },
	}
}

func cmpWorkItems(got, want []runner.WorkItem) string {
	if !reflect.DeepEqual(got, want) {
		return fmt.Sprintf("got %#v, want %#v", got, want)
	}
	return ""
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/tui -run 'TestMaintenanceSelectionBuildsReviewWithoutRunning|TestConfirmRunsExactReviewedItems|TestShortcutPreselectsWithoutExecuting' -v`

Expected: compile failure because screen and reviewed-plan APIs do not exist.

- [ ] **Step 3: Add screen state and immutable review construction**

```go
type screen uint8

const (
	screenHome screen = iota
	screenMaintenance
	screenReview
	screenRunning
	screenResult
	screenInspect
	screenConfig
	screenAudit
	screenDoctor
	screenHistory
)

type reviewedPlan struct {
	Action string
	Items  []runner.WorkItem
}
```

Add Model fields:

```go
	screen      screen
	homeCursor  int
	inspectCursor int
	selected    map[string]bool
	reviewed    reviewedPlan
```

Initialize `screenHome` and an empty selection map in `New`. Implement:

```go
func (m *Model) openMaintenance(ids ...string) {
	m.screen = screenMaintenance
	m.selected = map[string]bool{}
	for _, id := range ids {
		m.selected[id] = true
	}
}

func (m *Model) reviewSelection() {
	var items []runner.WorkItem
	var ids []string
	for _, task := range m.tasks {
		if !m.selected[task.ID] || !task.Available(m.runCtx) {
			continue
		}
		ids = append(ids, task.ID)
		items = append(items, runner.BuildQueue(task, m.runCtx)...)
	}
	if len(items) == 0 {
		return
	}
	m.reviewed = reviewedPlan{Action: strings.Join(ids, "+"), Items: append([]runner.WorkItem(nil), items...)}
	m.screen = screenReview
}

func (m *Model) confirmReviewedPlan() tea.Cmd {
	if len(m.reviewed.Items) == 0 {
		return nil
	}
	m.queue = append([]runner.WorkItem(nil), m.reviewed.Items...)
	m.queuePos = 0
	m.mode = modeRunning
	m.screen = screenRunning
	m.runAction = m.reviewed.Action
	m.runStart = time.Now()
	m.logLines = nil
	m.logFollow = true
	m.logVP = viewport.New(m.logWidth(), m.logHeight())
	return tea.Batch(m.advanceQueue(), m.spinner.Tick)
}
```

Route `h` and `n` on Home to `openMaintenance("hms")` and `openMaintenance("nds")`. On Maintenance, Enter calls `reviewSelection`; on Review, Enter calls `confirmReviewedPlan`. Remove all direct `startTask` calls from key handling.

- [ ] **Step 4: Run focused and full tests**

Run: `go test ./internal/tui -v && go test ./...`

Expected: all tests pass; update old tests that expect Enter to run immediately so they now assert Review.

- [ ] **Step 5: Commit the review gate**

```bash
git add internal/tui/model.go internal/tui/model_test.go
git commit -m "feat(tui): require reviewed run plans"
```

---

### Task 2: Split the TUI by Responsibility

**Files:**
- Modify: `internal/tui/model.go`
- Create: `internal/tui/update.go`
- Create: `internal/tui/execution.go`
- Create: `internal/tui/styles.go`
- Create: `internal/tui/view_home.go`
- Create: `internal/tui/view_plan.go`
- Create: `internal/tui/view_inspect.go`

**Interfaces:**
- Preserves: public `New`, `Init`, `Update`, and `View` methods.
- Preserves: all existing unexported view helpers until later tasks replace their output.
- Produces: focused files listed in File Map.

- [ ] **Step 1: Add characterization test for pre-split behavior**

```go
func TestSplitPreservesAuditConfigAndDoctorViews(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 100, 36
	m.configFiles = []configFile{{label: "flake.nix", path: "/repo/flake.nix", hint: "system"}}
	m.auditReady = true
	m.auditItems = []system.AuditItem{{Name: "ssh config", OK: true, Detail: "managed"}}
	m.facts = system.Facts{DotfilesBranch: "main", HMGeneration: "gen 4", AgeKeyExists: true, GitHubKeyExists: true}

	for _, tc := range []struct {
		name string
		view func() string
		want string
	}{
		{"config", func() string { return m.viewConfig(92) }, "flake.nix"},
		{"audit", func() string { return m.viewAudit(92) }, "ssh config"},
		{"doctor", func() string { return m.viewDoctor(92) }, "gen 4"},
	} {
		if out := tc.view(); !strings.Contains(out, tc.want) {
			t.Fatalf("%s missing %q:\n%s", tc.name, tc.want, out)
		}
	}
}
```

- [ ] **Step 2: Run characterization test and verify GREEN before moving code**

Run: `go test ./internal/tui -run TestSplitPreservesAuditConfigAndDoctorViews -v`

Expected: pass before moving code; this locks the existing inspect content.

- [ ] **Step 3: Move symbols without changing bodies**

Use `apply_patch` to move exact symbol groups:

```text
update.go: Init, Update, handleKey, auditCmdIfNeeded, navigation methods
execution.go: start/run/review methods, advanceQueue, readNextLine, terminal handoff, sudo keepalive
styles.go: palette, style declarations, layout/string utility functions
view_home.go: View, header/footer, Home launch surface
view_plan.go: maintenance list, review, running log, result, log classification
view_inspect.go: config, audit, doctor, history, wrapping helpers
model.go: message types, state enums/structs, Model, New, buildConfigFiles
```

Do not rename symbols during this move. Delete each moved definition from `model.go` in the same patch so every symbol exists exactly once.

- [ ] **Step 4: Format and verify no behavior changed**

Run: `gofmt -w internal/tui/*.go && go test ./internal/tui -v && go test ./...`

Expected: all tests pass.

- [ ] **Step 5: Commit the structural split**

```bash
git add internal/tui
git commit -m "refactor(tui): split model by responsibility"
```

---

### Task 3: Monolith/Afterburner Styles and Responsive Primitives

**Files:**
- Modify: `internal/tui/styles.go`
- Modify: `internal/tui/model.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Produces: `type uiStyles struct` and `newUIStyles(noColor bool) uiStyles`.
- Produces: `layoutWidth`, `majorRule`, `numberedRow`, and `statusText` helpers.
- Consumes: Model width and `NO_COLOR` environment.

- [ ] **Step 1: Write failing style and width tests**

```go
func TestLayoutWidthTargets100AndCaps140(t *testing.T) {
	for _, tc := range []struct{ input, want int }{{72, 72}, {80, 80}, {100, 100}, {160, 140}} {
		if got := layoutWidth(tc.input); got != tc.want {
			t.Fatalf("layoutWidth(%d)=%d want %d", tc.input, got, tc.want)
		}
	}
}

func TestNoColorStylesRenderSemanticLabelsWithoutANSI(t *testing.T) {
	s := newUIStyles(true)
	out := s.active.Render("03 ACTIVE") + " " + s.success.Render("DONE") + " " + s.danger.Render("TTY")
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("NO_COLOR output contains ANSI: %q", out)
	}
	for _, want := range []string{"03 ACTIVE", "DONE", "TTY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %q", want, out)
		}
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/tui -run 'TestLayoutWidthTargets100AndCaps140|TestNoColorStylesRenderSemanticLabelsWithoutANSI' -v`

Expected: compile failure because style APIs do not exist.

- [ ] **Step 3: Implement semantic style construction**

```go
type uiStyles struct {
	major, title, label, text, muted lipgloss.Style
	attention, active, success, danger, rule lipgloss.Style
}

func newUIStyles(noColor bool) uiStyles {
	color := func(hex string) lipgloss.TerminalColor {
		if noColor {
			return lipgloss.NoColor{}
		}
		return lipgloss.Color(hex)
	}
	return uiStyles{
		major:     lipgloss.NewStyle().Foreground(color("#f4f7f8")).Bold(true),
		title:     lipgloss.NewStyle().Foreground(color("#dae4ea")).Bold(true),
		label:     lipgloss.NewStyle().Foreground(color("#60717c")),
		text:      lipgloss.NewStyle().Foreground(color("#dae4ea")),
		muted:     lipgloss.NewStyle().Foreground(color("#60717c")),
		attention: lipgloss.NewStyle().Foreground(color("#ffcb6b")).Bold(true),
		active:    lipgloss.NewStyle().Foreground(color("#66d9ef")).Bold(true),
		success:   lipgloss.NewStyle().Foreground(color("#7ee787")).Bold(true),
		danger:    lipgloss.NewStyle().Foreground(color("#ff8f70")).Bold(true),
		rule:      lipgloss.NewStyle().Foreground(color("#27343c")),
	}
}

func layoutWidth(width int) int {
	if width <= 0 { return 100 }
	if width > 140 { return 140 }
	return width
}

func majorRule(s uiStyles, width int, active bool) string {
	style := s.rule
	if active { style = s.active }
	return style.Render(strings.Repeat("━", max(1, width)))
}
```

Add `styles uiStyles` to Model and initialize it with `newUIStyles(os.Getenv("NO_COLOR") != "")`. Replace old palette globals only as each view migrates; do not mix old rounded cards into new screens.

- [ ] **Step 4: Run tests and verify GREEN**

Run: `go test ./internal/tui -v`

Expected: all tests pass.

- [ ] **Step 5: Commit visual primitives**

```bash
git add internal/tui/styles.go internal/tui/model.go internal/tui/model_test.go
git commit -m "feat(tui): add Monolith visual system"
```

---

### Task 4: Home, Select, and Review Screens

**Files:**
- Modify: `internal/tui/view_home.go`
- Modify: `internal/tui/view_plan.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: screen state, reviewed plans, and semantic styles from Tasks 1-3.
- Produces: `viewHome`, `viewMaintenance`, and `viewReview`.

- [ ] **Step 1: Write failing render and navigation tests**

```go
func TestHomeUsesMonolithHierarchyAt80And100Columns(t *testing.T) {
	for _, width := range []int{80, 100} {
		m := testGuidedModel()
		m.width, m.height = width, 30
		m.styles = newUIStyles(true)
		m.facts = system.Facts{User: "bag", Hostname: "mini", DotfilesBranch: "main", BrewOutdated: 3}
		out := m.View()
		for _, want := range []string{"SYS/BOZO", "SYSTEM", "WEEKLY MAINTENANCE", "ADD PACKAGE", "INSPECT SYSTEM"} {
			if !strings.Contains(out, want) { t.Fatalf("width %d missing %q:\n%s", width, want, out) }
		}
		if lipgloss.Width(out) > width { t.Fatalf("width %d rendered %d", width, lipgloss.Width(out)) }
	}
}

func TestReviewShowsExactCommandsAndTTYWarning(t *testing.T) {
	m := testGuidedModel()
	m.styles = newUIStyles(true)
	m.screen = screenReview
	m.reviewed = reviewedPlan{Action: "brew", Items: []runner.WorkItem{{Name: "brew", Args: []string{"upgrade"}, Mode: runner.ExecutionInteractive}}}
	out := m.View()
	for _, want := range []string{"REVIEW", "brew upgrade", "TTY", "ENTER CONFIRM"} {
		if !strings.Contains(out, want) { t.Fatalf("missing %q:\n%s", want, out) }
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/tui -run 'TestHomeUsesMonolithHierarchyAt80And100Columns|TestReviewShowsExactCommandsAndTTYWarning' -v`

Expected: failures because new copy and layout are absent.

- [ ] **Step 3: Implement the three screens**

Use these exact Home entries and status labels:

```go
var homeEntries = []struct{ number, label string; target screen }{
	{"01", "WEEKLY MAINTENANCE", screenMaintenance},
	{"02", "ADD PACKAGE", screenHome},
	{"03", "INSPECT SYSTEM", screenInspect},
}
```

Until the package plan lands, render entry 02 with `LOCKED`; Home cursor movement skips it and no key path opens it. Render Home with `SYS/BOZO`, `WORKSTATION CONTROL`, a major rule, explicit health text, four stats, and the numbered launch rows. Render Maintenance as grouped checkboxes with Space toggle and Enter Review. Render Review with target host, dirty repo warning, numbered `runner.CmdLabel` values, `TTY` on interactive work, Escape Back, and `ENTER CONFIRM`.

- [ ] **Step 4: Run TUI tests at all target widths**

Run: `go test ./internal/tui -v`

Expected: all tests pass at 80 and 100 columns.

- [ ] **Step 5: Commit primary screens**

```bash
git add internal/tui/view_home.go internal/tui/view_plan.go internal/tui/update.go internal/tui/model_test.go
git commit -m "feat(tui): add guided Home and Review screens"
```

---

### Task 5: Running, Result, Inspect, and History

**Files:**
- Modify: `internal/tui/execution.go`
- Modify: `internal/tui/view_plan.go`
- Modify: `internal/tui/view_inspect.go`
- Modify: `internal/tui/update.go`
- Modify: `internal/history/history.go`
- Create: `internal/history/history_test.go`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Produces: `history.Read(limit int) []history.Entry`.
- Produces: Monolith Running, Result, Inspect, Config, Audit, Doctor, and History screens.
- Consumes: existing `history.Entry`, streamed log state, and terminal handoff results.

- [ ] **Step 1: Write failing history and result tests**

```go
func TestReadReturnsNewestEntriesFirst(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	Append(Entry{Ts: time.Unix(1, 0), Action: "hms", OK: true})
	Append(Entry{Ts: time.Unix(2, 0), Action: "brew", OK: false})
	got := Read(1)
	if len(got) != 1 || got[0].Action != "brew" {
		t.Fatalf("got %#v", got)
	}
}
```

Extend history status without breaking old JSONL entries:

```go
type Status string

const (
	StatusSuccess Status = "success"
	StatusFailure Status = "failure"
	StatusCancelled Status = "cancelled"
)

type Entry struct {
	Ts time.Time `json:"ts"`
	Action string `json:"action"`
	Secs float64 `json:"secs"`
	OK bool `json:"ok"`
	Status Status `json:"status,omitempty"`
}
```

```go
func TestResultShowsCompletedFailedAndElapsed(t *testing.T) {
	m := testGuidedModel()
	m.styles = newUIStyles(true)
	m.screen = screenResult
	m.mode = modeDone
	m.reviewed = reviewedPlan{Action: "all", Items: []runner.WorkItem{{Name: "nix"}, {Name: "brew"}}}
	m.queuePos = 1
	m.runErr = errors.New("exit status 1")
	m.runElapsed = 5 * time.Second
	out := m.View()
	for _, want := range []string{"RUN/RESULT", "FAILED", "nix", "brew", "exit status 1", "00:05"} {
		if !strings.Contains(out, want) { t.Fatalf("missing %q:\n%s", want, out) }
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/history ./internal/tui -run 'TestReadReturnsNewestEntriesFirst|TestResultShowsCompletedFailedAndElapsed' -v`

Expected: compile failure because `Read`, `runErr`, and `runElapsed` do not exist.

- [ ] **Step 3: Implement history reading and terminal result state**

Implement `Read` by scanning `~/.local/state/sys-bozo/history.jsonl`, ignoring malformed lines, reversing entries, and truncating to `limit` when positive. Add `runErr error`, `runCancelled bool`, and `runElapsed time.Duration` to Model. On final success, step failure, or terminal-handoff cancellation, set these fields and `screenResult` before writing `StatusSuccess`, `StatusFailure`, or `StatusCancelled`. Continue writing `OK` for compatibility with existing readers.

```go
func Read(limit int) []Entry {
	home, err := os.UserHomeDir()
	if err != nil { return nil }
	f, err := os.Open(filepath.Join(home, ".local", "state", "sys-bozo", "history.jsonl"))
	if err != nil { return nil }
	defer f.Close()
	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry Entry
		if json.Unmarshal(scanner.Bytes(), &entry) == nil { entries = append(entries, entry) }
	}
	slices.Reverse(entries)
	if limit > 0 && len(entries) > limit { entries = entries[:limit] }
	return entries
}
```

- [ ] **Step 4: Render and route remaining screens**

Running renders numbered rows with `DONE`, `ACTIVE`, `WAITING`, or `TTY`, elapsed time, percentage by completed work items, and the existing viewport below the operation summary when height permits. Result uses green rule and `COMPLETE` on success; rust rule and `FAILED` on error.

Inspect renders numbered Config, Audit, Doctor, and History entries. Config editor completion routes chosen `hms`/`nds`/both actions into `openMaintenance` rather than execution. Audit and Doctor retain existing content with rules instead of rounded cards. History renders the newest 20 entries from `history.Read(20)`.

- [ ] **Step 5: Run full test, vet, and build checks**

Run: `gofmt -w internal/tui internal/history && go test ./... && go vet ./...`

Expected: all checks pass.

Run: `nix build .#packages.$(nix eval --impure --raw --expr builtins.currentSystem).default`

Expected: exit 0.

- [ ] **Step 6: Perform visual smoke checks**

Launch with an 80x24 terminal and a normal 100+ column terminal. Verify Home, Select, Review, Running, interactive handoff return, success Result, failure Result, Config, Audit, Doctor, and History. Set `NO_COLOR=1` and repeat Home/Review/Result. Do not execute a mutating command for layout-only checks; use test fixtures or a harmless task command.

- [ ] **Step 7: Commit completed guided TUI**

```bash
git add internal/tui internal/history
git commit -m "feat(tui): ship Monolith guided workflow"
```
