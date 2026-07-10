package tui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

func testGuidedModel() Model {
	ctx := runner.Context{HomeManager: "home-manager", OS: "darwin"}
	return Model{
		screen:   screenHome,
		runCtx:   ctx,
		tasks:    runner.DefaultTasks(ctx),
		tabs:     []string{"Dashboard", "Actions", "Config", "Audit", "Doctor"},
		selected: map[string]bool{},
		terminalExec: func(runner.WorkItem, time.Time) tea.Cmd {
			return nil
		},
	}
}

func cmpWorkItems(got, want []runner.WorkItem) string {
	if !reflect.DeepEqual(got, want) {
		return fmt.Sprintf("got %#v, want %#v", got, want)
	}
	return ""
}

type ptyHandoffDoneMsg struct{ err error }

type ptyHandoffSmokeModel struct {
	returned bool
	err      error
}

func (m ptyHandoffSmokeModel) Init() tea.Cmd {
	return tea.ExecProcess(exec.Command("/usr/bin/printf", "HANDOFF_CHILD_OK\n"), func(err error) tea.Msg {
		return ptyHandoffDoneMsg{err: err}
	})
}

func (m ptyHandoffSmokeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if done, ok := msg.(ptyHandoffDoneMsg); ok {
		m.returned = true
		m.err = done.err
		return m, tea.Quit
	}
	return m, nil
}

func (m ptyHandoffSmokeModel) View() string {
	if m.returned {
		return "HANDOFF_TUI_RESTORED\n"
	}
	return "HANDOFF_TUI_ACTIVE\n"
}

func TestPTYTerminalHandoffSmoke(t *testing.T) {
	if os.Getenv("SYS_BOZO_PTY_SMOKE") != "1" {
		t.Skip("set SYS_BOZO_PTY_SMOKE=1 and run under a real PTY")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	stdinTTY, stdoutTTY := isTerminalFile(os.Stdin), isTerminalFile(os.Stdout)
	if !stdinTTY || !stdoutTTY {
		t.Fatalf("PTY smoke requires terminal-backed stdin and stdout: stdin=%v stdout=%v", stdinTTY, stdoutTTY)
	}

	final, err := tea.NewProgram(ptyHandoffSmokeModel{}, tea.WithAltScreen()).Run()
	if err != nil {
		t.Fatalf("program run: %v", err)
	}
	got, ok := final.(ptyHandoffSmokeModel)
	if !ok || !got.returned || got.err != nil {
		t.Fatalf("handoff result=%T(%#v)", final, final)
	}
	fmt.Fprintln(os.Stdout, "HANDOFF_RESTORED_OK")
}

func isTerminalFile(file *os.File) bool {
	cmd := exec.Command("/usr/bin/tty", "-s")
	cmd.Stdin = file
	return cmd.Run() == nil
}

func TestLayoutWidthTargets100AndCaps140(t *testing.T) {
	for _, tc := range []struct{ input, want int }{{72, 72}, {80, 80}, {100, 100}, {160, 140}} {
		if got := layoutWidth(tc.input); got != tc.want {
			t.Fatalf("layoutWidth(%d)=%d want %d", tc.input, got, tc.want)
		}
	}
}

func TestNoColorStylesRenderSemanticLabelsWithoutANSI(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

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

func TestFieldStyleUsesGraphiteAndDropsBackgroundWithoutColor(t *testing.T) {
	colored := newUIStyles(false)
	background, ok := colored.field.GetBackground().(lipgloss.Color)
	if !ok || string(background) != "#0a0d10" {
		t.Fatalf("field background=%T(%v), want graphite #0a0d10", colored.field.GetBackground(), colored.field.GetBackground())
	}
	foreground, ok := colored.field.GetForeground().(lipgloss.Color)
	if !ok || string(foreground) != "#dae4ea" {
		t.Fatalf("field foreground=%T(%v), want bone #dae4ea", colored.field.GetForeground(), colored.field.GetForeground())
	}

	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	plain := newUIStyles(true)
	out := plain.field.Render("WORKSTATION CONTROL")
	if out != "WORKSTATION CONTROL" || strings.Contains(out, "\x1b[") {
		t.Fatalf("NO_COLOR field rendered styling: %q", out)
	}
	if _, ok := plain.field.GetBackground().(lipgloss.NoColor); !ok {
		t.Fatalf("NO_COLOR field retained background: %T(%v)", plain.field.GetBackground(), plain.field.GetBackground())
	}
}

func TestStatusTextPreservesNoColorSemanticLabels(t *testing.T) {
	s := newUIStyles(true)
	tests := []struct {
		text string
		kind statusKind
	}{
		{"LOCKED", statusMuted},
		{"DIRTY", statusAttention},
		{"ACTIVE", statusActive},
		{"DONE", statusSuccess},
		{"TTY", statusDanger},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := statusText(s, tt.text, tt.kind)
			if got != tt.text {
				t.Fatalf("statusText(%q, %d)=%q", tt.text, tt.kind, got)
			}
			if strings.Contains(got, "\x1b[") {
				t.Fatalf("NO_COLOR output contains ANSI: %q", got)
			}
		})
	}
}

func TestNumberedRowAlignsRenderedStatusByVisibleWidth(t *testing.T) {
	s := newUIStyles(true)
	status := statusText(s, "READY", statusSuccess)
	got := numberedRow(s, "03", "INSPECT SYSTEM", status, 40, false)

	if lipgloss.Width(got) != 40 {
		t.Fatalf("row width=%d want 40: %q", lipgloss.Width(got), got)
	}
	if !strings.HasSuffix(got, "READY") {
		t.Fatalf("status is not right-aligned: %q", got)
	}
}

func TestNumberedRowPreservesSelectionMarkerWithoutColor(t *testing.T) {
	s := newUIStyles(true)
	status := statusText(s, "READY", statusSuccess)
	active := numberedRow(s, "03", "INSPECT SYSTEM", status, 40, true)
	inactive := numberedRow(s, "03", "INSPECT SYSTEM", status, 40, false)

	if active == inactive {
		t.Fatalf("active and inactive rows are identical: %q", active)
	}
	if !strings.HasPrefix(active, "> ") {
		t.Fatalf("active row missing marker: %q", active)
	}
	if !strings.HasPrefix(inactive, "  ") {
		t.Fatalf("inactive row missing marker space: %q", inactive)
	}
	if strings.Contains(active+inactive, "\x1b[") {
		t.Fatalf("NO_COLOR rows contain ANSI: active=%q inactive=%q", active, inactive)
	}
	if lipgloss.Width(active) != 40 || lipgloss.Width(inactive) != 40 {
		t.Fatalf("row widths active=%d inactive=%d want 40", lipgloss.Width(active), lipgloss.Width(inactive))
	}
}

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
		{"config", func() string { return m.viewConfig() }, "flake.nix"},
		{"audit", func() string { return m.viewAudit() }, "ssh config"},
		{"doctor", func() string { return m.viewDoctor() }, "gen 4"},
	} {
		if out := tc.view(); !strings.Contains(out, tc.want) {
			t.Fatalf("%s missing %q:\n%s", tc.name, tc.want, out)
		}
	}
}

func TestHomeUsesMonolithHierarchyAt80And100Columns(t *testing.T) {
	for _, width := range []int{80, 100} {
		m := testGuidedModel()
		m.width, m.height = width, 30
		m.styles = newUIStyles(true)
		m.facts = system.Facts{User: "bag", Hostname: "mini", DotfilesBranch: "main", BrewOutdated: 3}
		out := m.View()
		for _, want := range []string{"SYS/BOZO", "SYSTEM", "WEEKLY MAINTENANCE", "ADD PACKAGE", "INSPECT SYSTEM"} {
			if !strings.Contains(out, want) {
				t.Fatalf("width %d missing %q:\n%s", width, want, out)
			}
		}
		if lipgloss.Width(out) > width {
			t.Fatalf("width %d rendered %d", width, lipgloss.Width(out))
		}
	}
}

func TestReviewShowsExactCommandsAndTTYWarning(t *testing.T) {
	m := testGuidedModel()
	m.styles = newUIStyles(true)
	m.screen = screenReview
	m.reviewed = reviewedPlan{
		Action: "brew",
		Items: []runner.WorkItem{{
			Name: "brew",
			Args: []string{"upgrade"},
			Mode: runner.ExecutionInteractive,
		}},
	}
	out := m.View()
	for _, want := range []string{"REVIEW", "brew upgrade", "TTY", "ENTER CONFIRM"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestResultShowsCompletedFailedAndElapsed(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 100, 36
	m.styles = newUIStyles(true)
	m.screen = screenResult
	m.mode = modeDone
	m.reviewed = reviewedPlan{Action: "all", Items: []runner.WorkItem{{Name: "nix"}, {Name: "brew"}, {Name: "topgrade"}}}
	m.queuePos = 1
	m.runErr = errors.New("exit status 1")
	m.runElapsed = 5 * time.Second
	m.stepResults = []stepResult{
		{Item: m.reviewed.Items[0], Status: history.StatusSuccess, Duration: 2 * time.Second},
		{Item: m.reviewed.Items[1], Status: history.StatusFailure, Duration: time.Second, Err: m.runErr},
	}
	m.logLines = []logLine{{kind: logOutput, text: "  harmless fixture output"}}
	m.logVP = viewport.New(m.logWidth(), m.logHeight())
	m.logVP.SetContent(m.renderLog())

	out := m.View()
	for _, want := range []string{"RUN/RESULT", "FAILED", "nix", "brew", "topgrade", "WAITING", "exit status 1", "00:05", "00:02", "HISTORY FAILURE", "L VIEW LOG", "R REVIEW RETRY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("log toggle returned command: %v", cmd)
	}
	out = m.View()
	for _, want := range []string{"OUTPUT", "harmless fixture output", "L SUMMARY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("toggled log missing %q:\n%s", want, out)
		}
	}
}

func TestResultRetryReturnsToReviewWithoutExecuting(t *testing.T) {
	m := testGuidedModel()
	m.screen = screenResult
	m.mode = modeDone
	m.reviewed = reviewedPlan{Action: "fixture", Items: []runner.WorkItem{{Name: "fixture-command"}}}
	m.queue = cloneWorkItems(m.reviewed.Items)
	m.queuePos = 1
	m.runErr = errors.New("exit status 1")

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	got := next.(Model)
	if cmd != nil || got.screen != screenReview || got.mode != modeView || len(got.queue) != 0 || got.queuePos != 0 || got.runErr != nil {
		t.Fatalf("cmd=%v screen=%v mode=%v queue=%v queuePos=%d runErr=%v", cmd, got.screen, got.mode, got.queue, got.queuePos, got.runErr)
	}
	if got.reviewed.Action != "fixture" || len(got.reviewed.Items) != 1 {
		t.Fatalf("retry lost reviewed plan: %#v", got.reviewed)
	}
}

func TestResultEscapeReturnsWithoutExecuting(t *testing.T) {
	m := testGuidedModel()
	m.tab = 1
	m.screen = screenResult
	m.mode = modeDone
	m.reviewed = reviewedPlan{Action: "fixture", Items: []runner.WorkItem{{Name: "fixture-command"}}}
	m.queue = cloneWorkItems(m.reviewed.Items)

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got := next.(Model)
	if cmd != nil || got.mode != modeView || got.screen != screenMaintenance || len(got.queue) != 0 || len(got.reviewed.Items) != 0 {
		t.Fatalf("cmd=%v mode=%v screen=%v queue=%v reviewed=%#v", cmd, got.mode, got.screen, got.queue, got.reviewed)
	}
}

func TestRunningShowsProgressTTYAndStreamedLog(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 100, 36
	m.styles = newUIStyles(true)
	m.screen = screenRunning
	m.mode = modeRunning
	m.runStart = time.Now().Add(-5 * time.Second)
	m.queue = []runner.WorkItem{
		{Name: "nix", Args: []string{"flake", "check"}},
		{Name: "brew", Args: []string{"upgrade"}, Mode: runner.ExecutionInteractive},
	}
	m.reviewed = reviewedPlan{Action: "all", Items: cloneWorkItems(m.queue)}
	m.queuePos = 1
	m.logLines = []logLine{{kind: logOutput, text: "  harmless fixture output"}}
	m.logVP = viewport.New(m.logWidth(), m.logHeight())
	m.logVP.SetContent(m.renderLog())

	out := m.View()
	for _, want := range []string{"RUN/ACTIVE", "50%", "nix flake check", "DONE", "brew upgrade", "TTY", "harmless fixture output"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestReviewWrapsLongExactCommandWithin80Columns(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 80, 30
	m.styles = newUIStyles(true)
	m.screen = screenReview
	m.facts = system.Facts{User: "bag", Hostname: "mini", DotfilesDirty: 2}
	item := runner.WorkItem{
		Name: "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-home-manager/bin/home-manager",
		Args: []string{"switch", "--flake", ".#bag@mini"},
		Mode: runner.ExecutionInteractive,
	}
	m.reviewed = reviewedPlan{Action: "hms", Items: []runner.WorkItem{item}}

	out := m.View()
	if lipgloss.Width(out) > 80 {
		t.Fatalf("rendered width=%d want <=80:\n%s", lipgloss.Width(out), out)
	}
	compact := strings.Join(strings.Fields(out), "")
	compactCommand := strings.ReplaceAll(runner.CmdLabel(item), " ", "")
	if !strings.Contains(compact, compactCommand) {
		t.Fatalf("wrapped output lost or reordered command characters %q:\n%s", runner.CmdLabel(item), out)
	}
	for _, want := range []string{"bag@mini", "DIRTY", "TTY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestMaintenanceShowsGroupedCheckboxesAt80And100Columns(t *testing.T) {
	for _, width := range []int{80, 100} {
		m := testGuidedModel()
		m.width, m.height = width, 30
		m.styles = newUIStyles(true)
		m.screen = screenMaintenance
		m.selected["hms"] = true
		out := m.View()
		for _, want := range []string{"SELECT", "home-manager", "[x]", "hms", "SPACE TOGGLE", "ENTER REVIEW"} {
			if !strings.Contains(out, want) {
				t.Fatalf("width %d missing %q:\n%s", width, want, out)
			}
		}
		if lipgloss.Width(out) > width {
			t.Fatalf("width %d rendered %d", width, lipgloss.Width(out))
		}
	}
}

func TestMaintenanceRemainsUsableAt80By24(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 80, 24
	m.styles = newUIStyles(true)
	m.openMaintenance("hms")

	out := m.View()
	if lines := strings.Count(out, "\n") + 1; lines > 24 {
		t.Fatalf("rendered height=%d want <=24:\n%s", lines, out)
	}
	for _, want := range []string{"SELECT", "[x]", "SPACE TOGGLE", "ENTER REVIEW"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestFullyProvisionedDarwinMaintenanceFits80By24(t *testing.T) {
	ctx := runner.Context{
		Repo:          "/repo",
		User:          "bag",
		Hostname:      "mini",
		OS:            "darwin",
		NixBin:        "nix",
		BrewBin:       "brew",
		HomeManager:   "home-manager",
		DarwinRebuild: "darwin-rebuild",
		Topgrade:      "topgrade",
	}
	m := testGuidedModel()
	m.runCtx = ctx
	m.tasks = runner.DefaultTasks(ctx)
	m.width, m.height = 80, 24
	m.styles = newUIStyles(true)
	m.openMaintenance("hms")

	if available := m.availableTasks(); len(available) != 10 {
		t.Fatalf("fully provisioned Darwin fixture has %d available tasks, want 10", len(available))
	}
	out := m.View()
	if lines := strings.Count(out, "\n") + 1; lines > 24 {
		t.Fatalf("rendered height=%d want <=24:\n%s", lines, out)
	}
	for _, want := range []string{
		"> [ ] nix-darwin/nds", "[x] home-manager/hms", "brew/brew", "misc/topgrade", "combined/all",
		"ESCAPE BACK", "SPACE TOGGLE", "ENTER REVIEW",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestHomeNavigationSkipsLockedPackageAndRoutesAvailableEntries(t *testing.T) {
	m := testGuidedModel()

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	got := next.(Model)
	if cmd != nil || got.screen != screenHome || got.homeCursor != 2 {
		t.Fatalf("down: cmd=%v screen=%v cursor=%d", cmd, got.screen, got.homeCursor)
	}

	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	got = next.(Model)
	if cmd != nil || got.screen != screenHome {
		t.Fatalf("locked shortcut: cmd=%v screen=%v", cmd, got.screen)
	}

	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = next.(Model)
	if cmd != nil || got.screen != screenMaintenance {
		t.Fatalf("maintenance entry: cmd=%v screen=%v", cmd, got.screen)
	}

	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	got = next.(Model)
	if cmd != nil || got.screen != screenInspect {
		t.Fatalf("inspect shortcut: cmd=%v screen=%v", cmd, got.screen)
	}
}

func TestInspectRenderingDoesNotUseStaleMaintenanceTab(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 100, 30
	m.styles = newUIStyles(true)
	m.configFiles = []configFile{{label: "flake.nix", path: "/repo/flake.nix", hint: "system configuration"}}

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = next.(Model)
	if m.screen != screenMaintenance || m.tabs[m.tab] != "Actions" {
		t.Fatalf("maintenance: screen=%v tab=%q", m.screen, m.tabs[m.tab])
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.screen != screenHome || m.tabs[m.tab] != "Actions" {
		t.Fatalf("home with retained tab: screen=%v tab=%q", m.screen, m.tabs[m.tab])
	}
	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	m = next.(Model)
	if m.screen != screenInspect {
		t.Fatalf("inspect: screen=%v", m.screen)
	}
	if m.tabs[m.tab] != "Config" {
		t.Fatalf("inspect entry did not align legacy tab: %q", m.tabs[m.tab])
	}
	m.tab = 1 // rendering must remain stable even if legacy state becomes stale

	out := m.View()
	for _, want := range []string{"INSPECT/SYSTEM", "CONFIG"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inspection output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "enter run") {
		t.Fatalf("inspection output leaked stale Actions content:\n%s", out)
	}
}

func TestInspectListsAndRoutesConfigAuditDoctorAndHistory(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 100, 30
	m.styles = newUIStyles(true)
	m.screen = screenInspect

	out := m.View()
	for _, want := range []string{"INSPECT/SYSTEM", "CONFIG", "AUDIT", "DOCTOR", "HISTORY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}

	wantScreens := []screen{screenConfig, screenAudit, screenDoctor, screenHistory}
	for i, want := range wantScreens {
		m.screen = screenInspect
		m.inspectCursor = i
		next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
		got := next.(Model)
		if got.screen != want {
			t.Fatalf("entry %d routed to %v, want %v", i, got.screen, want)
		}
	}
}

func TestHistoryRendersNewestTwentyEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for i := 0; i < 21; i++ {
		history.Append(history.Entry{
			Ts:     time.Unix(int64(i+1), 0),
			Action: fmt.Sprintf("fixture-%02d", i),
			Secs:   float64(i),
			OK:     true,
			Status: history.StatusSuccess,
		})
	}

	m := testGuidedModel()
	m.width, m.height = 100, 36
	m.styles = newUIStyles(true)
	m.screen = screenHistory
	out := m.View()
	for _, want := range []string{"INSPECT/HISTORY", "fixture-20", "fixture-01", "SUCCESS"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "fixture-00") {
		t.Fatalf("history rendered more than newest 20 entries:\n%s", out)
	}
}

func TestInspectChildScreensUseMonolithRulesWithoutCards(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 100, 36
	m.styles = newUIStyles(true)
	m.configFiles = []configFile{{label: "flake.nix", path: "/fixture/flake.nix", hint: "system configuration"}}
	m.auditReady = true
	m.auditItems = []system.AuditItem{{Name: "ssh config", OK: true, Detail: "managed"}}
	m.facts = system.Facts{DotfilesBranch: "main", HMGeneration: "gen 4", AgeKeyExists: true, GitHubKeyExists: true}

	for _, tc := range []struct {
		screen screen
		header string
		body   string
	}{
		{screenConfig, "INSPECT/CONFIG", "flake.nix"},
		{screenAudit, "INSPECT/AUDIT", "ssh config"},
		{screenDoctor, "INSPECT/DOCTOR", "gen 4"},
	} {
		m.screen = tc.screen
		out := m.View()
		for _, want := range []string{tc.header, tc.body, "━"} {
			if !strings.Contains(out, want) {
				t.Fatalf("screen %v missing %q:\n%s", tc.screen, want, out)
			}
		}
		if strings.ContainsAny(out, "╭╮╰╯") {
			t.Fatalf("screen %v retained rounded card:\n%s", tc.screen, out)
		}
	}
}

func TestConfigApplyChoiceReturnsToSelectWithoutExecuting(t *testing.T) {
	for _, tc := range []struct {
		key  rune
		want []string
	}{
		{'h', []string{"hms"}},
		{'n', []string{"nds"}},
		{'b', []string{"hms", "nds"}},
	} {
		t.Run(string(tc.key), func(t *testing.T) {
			m := testGuidedModel()
			m.screen = screenConfig
			m.applyPrompt = true

			next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.key}})
			got := next.(Model)
			if cmd != nil || got.screen != screenMaintenance || got.mode == modeRunning || len(got.queue) != 0 || len(got.reviewed.Items) != 0 {
				t.Fatalf("cmd=%v screen=%v mode=%v queue=%v reviewed=%#v", cmd, got.screen, got.mode, got.queue, got.reviewed)
			}
			for _, id := range tc.want {
				if !got.selected[id] {
					t.Fatalf("choice %q missing selection %q: %v", tc.key, id, got.selected)
				}
			}
		})
	}
}

func TestInspectChildNavigationDoesNotDependOnLegacyTab(t *testing.T) {
	m := testGuidedModel()
	m.screen = screenInspect
	m.tab = 1
	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = next.(Model)
	if m.screen != screenAudit || cmd == nil {
		t.Fatalf("audit shortcut screen=%v cmd=%v", m.screen, cmd)
	}

	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.screen != screenInspect {
		t.Fatalf("audit escape screen=%v", m.screen)
	}

	m.screen = screenConfig
	m.configFiles = []configFile{{label: "one"}, {label: "two"}}
	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if cmd != nil || m.configCursor != 1 || m.screen != screenConfig {
		t.Fatalf("config move cmd=%v cursor=%d screen=%v", cmd, m.configCursor, m.screen)
	}

	m.screen = screenAudit
	m.auditReady = true
	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = next.(Model)
	if cmd == nil || m.auditReady {
		t.Fatalf("audit rescan cmd=%v ready=%v", cmd, m.auditReady)
	}
}

func TestTask5VisualSmokeFitsTargetTerminals(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for i := 0; i < 21; i++ {
		history.Append(history.Entry{Ts: time.Unix(int64(i+1), 0), Action: fmt.Sprintf("fixture-%02d", i), Secs: 3, OK: true, Status: history.StatusSuccess})
	}

	base := testGuidedModel()
	base.styles = newUIStyles(true)
	base.facts = system.Facts{
		User: "fixture", Hostname: "host", OS: "darwin", DotfilesBranch: "main",
		HMGeneration: "gen 4", AgeKeyExists: true, GitHubKeyExists: true,
	}
	base.configFiles = []configFile{{label: "flake.nix", path: "/fixture/flake.nix", hint: "system configuration"}}
	base.auditReady = true
	for i := 0; i < 12; i++ {
		base.auditItems = append(base.auditItems, system.AuditItem{Name: fmt.Sprintf("check-%02d", i), OK: true, Detail: "managed"})
	}
	base.auditItems[0] = system.AuditItem{Name: "ssh config", Detail: "unmanaged", Description: "fixture explanation", Fix: "fixture fix"}
	items := []runner.WorkItem{
		{Name: "fixture-stream", Args: []string{"--safe"}},
		{Name: "fixture-terminal", Args: []string{"--safe"}, Mode: runner.ExecutionInteractive},
	}
	base.reviewed = reviewedPlan{Action: "fixture", Items: cloneWorkItems(items)}
	base.queue = cloneWorkItems(items)
	base.runStart = time.Now().Add(-5 * time.Second)
	base.runElapsed = 5 * time.Second
	base.logLines = []logLine{{kind: logOutput, text: "  harmless fixture output"}}

	screens := []struct {
		name   string
		screen screen
		setup  func(*Model)
	}{
		{"home", screenHome, nil},
		{"select", screenMaintenance, nil},
		{"review", screenReview, nil},
		{"running", screenRunning, func(m *Model) { m.mode, m.queuePos = modeRunning, 1 }},
		{"result-success", screenResult, func(m *Model) {
			m.mode, m.queuePos = modeDone, len(items)
			m.stepResults = []stepResult{{Item: items[0], Status: history.StatusSuccess, Duration: 2 * time.Second}, {Item: items[1], Status: history.StatusSuccess, Duration: 3 * time.Second}}
		}},
		{"result-failure", screenResult, func(m *Model) {
			m.mode, m.queuePos, m.runErr = modeDone, 1, errors.New("exit status 1")
			m.stepResults = []stepResult{{Item: items[0], Status: history.StatusSuccess, Duration: 2 * time.Second}, {Item: items[1], Status: history.StatusFailure, Duration: 3 * time.Second, Err: m.runErr}}
		}},
		{"result-log", screenResult, func(m *Model) {
			m.mode, m.queuePos, m.runErr, m.resultLogVisible = modeDone, 1, errors.New("exit status 1"), true
		}},
		{"inspect", screenInspect, nil},
		{"config", screenConfig, nil},
		{"audit", screenAudit, nil},
		{"doctor", screenDoctor, nil},
		{"history", screenHistory, nil},
	}
	for _, size := range []struct{ width, height int }{{80, 24}, {110, 36}} {
		for _, tc := range screens {
			t.Run(fmt.Sprintf("%dx%d/%s", size.width, size.height, tc.name), func(t *testing.T) {
				m := base
				m.width, m.height, m.screen = size.width, size.height, tc.screen
				if tc.setup != nil {
					tc.setup(&m)
				}
				m.logVP = viewport.New(m.logWidth(), m.logHeight())
				m.logVP.SetContent(m.renderLog())
				out := m.View()
				if got := lipgloss.Width(out); got > size.width {
					t.Fatalf("width=%d want <=%d:\n%s", got, size.width, out)
				}
				if got := strings.Count(out, "\n") + 1; got > size.height {
					t.Fatalf("height=%d want <=%d:\n%s", got, size.height, out)
				}
				if os.Getenv("TASK5_VISUAL_LOG") != "" {
					t.Logf("\n%s", out)
				}
			})
		}
	}
}

func TestTask5NoColorHomeReviewAndResultHaveNoANSI(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })

	for _, target := range []screen{screenHome, screenReview, screenResult} {
		m := testGuidedModel()
		m.width, m.height = 100, 30
		m.styles = newUIStyles(true)
		m.screen = target
		m.reviewed = reviewedPlan{Action: "fixture", Items: []runner.WorkItem{{Name: "fixture-command"}}}
		m.mode = modeDone
		m.queuePos = 1
		m.runElapsed = time.Second
		out := m.View()
		if strings.Contains(out, "\x1b[") {
			t.Fatalf("screen %v contains ANSI:\n%q", target, out)
		}
	}
}

func TestMaintenanceSpaceTogglesAndReviewEscapeReturnsWithoutRunning(t *testing.T) {
	m := testGuidedModel()
	m.openMaintenance()
	available := m.availableTasks()
	if len(available) == 0 {
		t.Fatal("need available maintenance task")
	}
	wantID := available[0].ID

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(Model)
	if cmd != nil || m.screen != screenMaintenance || !m.selected[wantID] {
		t.Fatalf("toggle: cmd=%v screen=%v selected=%v", cmd, m.screen, m.selected)
	}

	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil || m.screen != screenReview || m.mode == modeRunning || m.reviewed.Action != wantID {
		t.Fatalf("review: cmd=%v screen=%v mode=%v reviewed=%#v", cmd, m.screen, m.mode, m.reviewed)
	}

	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(Model)
	if cmd != nil || m.screen != screenReview || m.mode == modeRunning {
		t.Fatalf("review space: cmd=%v screen=%v mode=%v", cmd, m.screen, m.mode)
	}

	next, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if cmd != nil || m.screen != screenMaintenance || m.mode == modeRunning || !m.selected[wantID] {
		t.Fatalf("back: cmd=%v screen=%v mode=%v selected=%v", cmd, m.screen, m.mode, m.selected)
	}
}

func TestMaintenanceIgnoresLegacyTabNavigation(t *testing.T) {
	keys := []tea.KeyMsg{
		{Type: tea.KeyTab},
		{Type: tea.KeyRight},
		{Type: tea.KeyLeft},
		{Type: tea.KeyShiftTab},
		{Type: tea.KeyRunes, Runes: []rune{'1'}},
		{Type: tea.KeyRunes, Runes: []rune{'2'}},
		{Type: tea.KeyRunes, Runes: []rune{'3'}},
		{Type: tea.KeyRunes, Runes: []rune{'4'}},
		{Type: tea.KeyRunes, Runes: []rune{'5'}},
	}
	for _, key := range keys {
		t.Run(key.String(), func(t *testing.T) {
			m := testGuidedModel()
			m.openMaintenance()
			wantTab := m.tab

			next, cmd := m.handleKey(key)
			got := next.(Model)
			if cmd != nil || got.screen != screenMaintenance || got.tab != wantTab {
				t.Fatalf("key=%q cmd=%v screen=%v tab=%d want screen=%v tab=%d", key.String(), cmd, got.screen, got.tab, screenMaintenance, wantTab)
			}
		})
	}
}

func TestMaintenanceStillAllowsQuitKeys(t *testing.T) {
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyCtrlC},
	}
	for _, key := range keys {
		t.Run(key.String(), func(t *testing.T) {
			m := testGuidedModel()
			m.openMaintenance()

			_, cmd := m.handleKey(key)
			if cmd == nil {
				t.Fatalf("key=%q returned nil quit command", key.String())
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatalf("key=%q command did not return tea.QuitMsg", key.String())
			}
		})
	}
}

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
	want := runner.WorkItem{
		Name: "test-interactive-sentinel",
		Args: []string{"never-executed"},
		Mode: runner.ExecutionInteractive,
	}
	m.screen = screenReview
	m.reviewed = reviewedPlan{Action: "hms", Items: []runner.WorkItem{want}}
	var executed runner.WorkItem
	called := false
	m.terminalExec = func(item runner.WorkItem, _ time.Time) tea.Cmd {
		called = true
		executed = item
		return func() tea.Msg { return stepDoneMsg{} }
	}

	cmd := m.confirmReviewedPlan()
	if cmd == nil || m.screen != screenRunning || m.mode != modeRunning {
		t.Fatalf("screen=%v mode=%v cmd=%v", m.screen, m.mode, cmd)
	}
	if diff := cmpWorkItems(m.queue, []runner.WorkItem{want}); diff != "" {
		t.Fatal(diff)
	}
	if !called {
		t.Fatal("reviewed interactive work did not use injected terminal executor")
	}
	if diff := cmpWorkItems([]runner.WorkItem{executed}, []runner.WorkItem{want}); diff != "" {
		t.Fatal(diff)
	}
	if m.activeScanner != nil {
		t.Fatal("injected interactive work must not create captured scanner")
	}
}

func TestReviewedAndQueuedWorkItemsDoNotAlias(t *testing.T) {
	args := []string{"original-arg"}
	env := []string{"TEST_VALUE=original"}
	task := runner.Task{
		ID:        "safe-test-task",
		Available: func(runner.Context) bool { return true },
		Steps: []runner.Step{{
			Mode: runner.ExecutionInteractive,
			Cmd: func(runner.Context) (string, []string) {
				return "test-interactive-sentinel", args
			},
		}},
		Env: func(runner.Context) []string { return env },
	}
	m := Model{
		runCtx:   runner.Context{},
		tasks:    []runner.Task{task},
		selected: map[string]bool{"safe-test-task": true},
		terminalExec: func(runner.WorkItem, time.Time) tea.Cmd {
			return func() tea.Msg { return stepDoneMsg{} }
		},
	}

	m.reviewSelection()
	args[0] = "mutated-source-arg"
	env[0] = "TEST_VALUE=mutated-source"
	if got := m.reviewed.Items[0]; got.Args[0] != "original-arg" || got.EnvExtra[0] != "TEST_VALUE=original" {
		t.Fatalf("review aliases source work item: %#v", got)
	}

	m.confirmReviewedPlan()
	m.reviewed.Items[0].Args[0] = "mutated-review-arg"
	m.queue[0].EnvExtra[0] = "TEST_VALUE=mutated-queue"
	if got := m.queue[0].Args[0]; got != "original-arg" {
		t.Fatalf("queue args alias review: %q", got)
	}
	if got := m.reviewed.Items[0].EnvExtra[0]; got != "TEST_VALUE=original" {
		t.Fatalf("review env aliases queue: %q", got)
	}
}

func TestShortcutPreselectsWithoutExecuting(t *testing.T) {
	m := testGuidedModel()
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	got := next.(Model)
	if got.screen != screenMaintenance || got.tabs[got.tab] != "Actions" || !got.selected["hms"] || got.mode == modeRunning {
		t.Fatalf("screen=%v tab=%q selected=%v mode=%v", got.screen, got.tabs[got.tab], got.selected, got.mode)
	}
}

func TestActionsEnterReviewsCurrentAvailableTaskWithoutRunning(t *testing.T) {
	m := testGuidedModel()
	m.openMaintenance()
	available := m.availableTasks()
	if len(available) < 2 || available[1].ID == "hms" || available[1].ID == "nds" {
		t.Fatalf("need deterministic non-shortcut task, available=%v", available)
	}
	m.cursor = 1
	wantTask := available[m.cursor]
	wantItems := runner.BuildQueue(wantTask, m.runCtx)

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(Model)
	if cmd != nil || got.screen != screenReview || got.reviewed.Action != wantTask.ID {
		t.Fatalf("cmd=%v screen=%v action=%q", cmd, got.screen, got.reviewed.Action)
	}
	if !got.selected[wantTask.ID] {
		t.Fatalf("current task %q was not selected: %v", wantTask.ID, got.selected)
	}
	if diff := cmpWorkItems(got.reviewed.Items, wantItems); diff != "" {
		t.Fatal(diff)
	}
	if got.mode == modeRunning || len(got.queue) != 0 {
		t.Fatal("Actions Enter must review without executing")
	}
}

func TestResultCloseClearsStaleWorkflowBeforeReviewingNewCursor(t *testing.T) {
	m := testGuidedModel()
	available := m.availableTasks()
	if len(available) < 2 {
		t.Fatalf("need two available tasks, got %v", available)
	}
	m.tab = 1
	m.cursor = 0
	m.screen = screenResult
	m.mode = modeDone
	m.selected = map[string]bool{available[0].ID: true}
	m.reviewed = reviewedPlan{Action: available[0].ID, Items: runner.BuildQueue(available[0], m.runCtx)}

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = next.(Model)
	if len(m.selected) != 0 || m.reviewed.Action != "" || len(m.reviewed.Items) != 0 {
		t.Fatalf("stale workflow survived result close: selected=%v reviewed=%#v", m.selected, m.reviewed)
	}

	next, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	wantTask := available[m.cursor]
	if wantTask.ID == available[0].ID {
		t.Fatalf("cursor did not move: cursor=%d task=%q", m.cursor, wantTask.ID)
	}
	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(Model)
	if cmd != nil || got.screen != screenReview || got.reviewed.Action != wantTask.ID {
		t.Fatalf("cmd=%v screen=%v action=%q", cmd, got.screen, got.reviewed.Action)
	}
	if len(got.selected) != 1 || !got.selected[wantTask.ID] {
		t.Fatalf("selected=%v, want only %q", got.selected, wantTask.ID)
	}
	if diff := cmpWorkItems(got.reviewed.Items, runner.BuildQueue(wantTask, got.runCtx)); diff != "" {
		t.Fatal(diff)
	}
	if got.mode == modeRunning || len(got.queue) != 0 {
		t.Fatal("new cursor review must not execute")
	}
}

func TestUnavailableSelectionFallsBackToCurrentAvailableCursor(t *testing.T) {
	m := testGuidedModel()
	m.openMaintenance("nds")
	if m.tasks[0].Available(m.runCtx) {
		t.Fatal("test requires nds to be unavailable")
	}
	available := m.availableTasks()
	if len(available) < 2 {
		t.Fatalf("need two available tasks, got %v", available)
	}
	m.cursor = 1
	wantTask := available[m.cursor]

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got := next.(Model)
	if cmd != nil || got.screen != screenReview || got.reviewed.Action != wantTask.ID {
		t.Fatalf("cmd=%v screen=%v action=%q", cmd, got.screen, got.reviewed.Action)
	}
	if len(got.selected) != 1 || !got.selected[wantTask.ID] || got.selected["nds"] {
		t.Fatalf("selected=%v, want only %q", got.selected, wantTask.ID)
	}
	if diff := cmpWorkItems(got.reviewed.Items, runner.BuildQueue(wantTask, got.runCtx)); diff != "" {
		t.Fatal(diff)
	}
	if got.mode == modeRunning || len(got.queue) != 0 {
		t.Fatal("fallback review must not execute")
	}
}

func TestWorkflowScreensIgnoreTabNavigation(t *testing.T) {
	workflows := []struct {
		name   string
		screen screen
		mode   appMode
	}{
		{name: "review", screen: screenReview, mode: modeView},
		{name: "running", screen: screenRunning, mode: modeRunning},
	}
	keys := []tea.KeyMsg{
		{Type: tea.KeyTab},
		{Type: tea.KeyRunes, Runes: []rune{'3'}},
	}
	for _, workflow := range workflows {
		for _, key := range keys {
			t.Run(workflow.name+"/"+key.String(), func(t *testing.T) {
				m := testGuidedModel()
				m.screen = workflow.screen
				m.mode = workflow.mode
				m.tab = 1

				next, cmd := m.handleKey(key)
				got := next.(Model)
				if cmd != nil || got.screen != workflow.screen || got.tab != 1 {
					t.Fatalf("cmd=%v screen=%v tab=%d", cmd, got.screen, got.tab)
				}
			})
		}
	}
}

func TestHomeNumericShortcutsUseLaunchEntries(t *testing.T) {
	tests := []struct {
		key        rune
		wantScreen screen
	}{
		{key: '1', wantScreen: screenMaintenance},
		{key: '2', wantScreen: screenHome},
		{key: '3', wantScreen: screenInspect},
	}
	for _, tt := range tests {
		t.Run(string(tt.key), func(t *testing.T) {
			m := testGuidedModel()
			next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			got := next.(Model)
			if got.screen != tt.wantScreen {
				t.Fatalf("screen=%v, want %v", got.screen, tt.wantScreen)
			}
		})
	}
}

func TestMaintenanceShortcutsCannotEscapeNonHomeScreens(t *testing.T) {
	for _, wantScreen := range []screen{screenMaintenance, screenReview, screenInspect, screenConfig, screenAudit, screenDoctor} {
		t.Run(fmt.Sprint(wantScreen), func(t *testing.T) {
			m := testGuidedModel()
			m.screen = wantScreen

			next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
			got := next.(Model)
			if got.screen != wantScreen || len(got.selected) != 0 {
				t.Fatalf("shortcut escaped screen: screen=%v selected=%v", got.screen, got.selected)
			}
		})
	}
}

func TestClosingCompletedRunRestoresActiveTabScreen(t *testing.T) {
	m := testGuidedModel()
	m.tab = 1
	m.screen = screenResult
	m.mode = modeDone

	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	got := next.(Model)
	if got.mode != modeView || got.screen != screenMaintenance {
		t.Fatalf("mode=%v screen=%v", got.mode, got.screen)
	}
}

func testModelOnUpdatesTab() Model {
	return Model{
		tab:  1,
		tabs: []string{"Dashboard", "Updates", "Audit", "Doctor"},
		mode: modeView,
	}
}

func TestTabIntoAuditStartsScan(t *testing.T) {
	m := testModelOnUpdatesTab()

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	model := next.(Model)

	if model.tabs[model.tab] != "Audit" {
		t.Fatalf("expected Audit tab, got %q", model.tabs[model.tab])
	}
	if cmd == nil {
		t.Fatal("expected entering Audit with tab to start audit scan")
	}
}

func TestRefreshOnAuditRestartsScan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DOTFILES_REPO", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	m := testModelOnUpdatesTab()
	m.tab = 2
	m.auditReady = true

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model := next.(Model)

	if model.auditReady {
		t.Fatal("expected refresh to mark audit as not ready")
	}
	if cmd == nil {
		t.Fatal("expected refresh on Audit tab to start audit scan")
	}
}

func TestAuditViewShowsFailureGuidance(t *testing.T) {
	m := testModelOnUpdatesTab()
	m.tab = 2
	m.auditReady = true
	m.auditItems = []system.AuditItem{
		{
			Name:        "ssh config",
			Detail:      "unmanaged file",
			Description: "SSH aliases and encrypted host includes should be managed by dotfiles.",
			Fix:         "Move host entries into secrets/ssh-config.yaml, then run home-manager switch.",
		},
	}

	out := m.viewAudit()
	for _, want := range []string{"ssh config", "unmanaged file", "why:", "SSH aliases", "fix:", "home-manager", "switch."} {
		if !strings.Contains(out, want) {
			t.Fatalf("audit view missing %q:\n%s", want, out)
		}
	}
}

func TestCompactAuditKeepsIssueStatusWithoutLongGuidance(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 80, 24
	m.styles = newUIStyles(true)
	m.auditReady = true
	m.auditItems = []system.AuditItem{{
		Name:        "ssh config",
		Detail:      "unmanaged file",
		Description: "long explanation hidden only in compact layout",
		Fix:         "long fix hidden only in compact layout",
	}}

	out := m.viewAudit()
	if !strings.Contains(out, "ISSUE") || strings.Contains(out, "PASS") {
		t.Fatalf("compact failure status is wrong:\n%s", out)
	}
	if strings.Contains(out, "long explanation") || strings.Contains(out, "long fix") {
		t.Fatalf("compact audit retained long guidance:\n%s", out)
	}
	if got := strings.Count(out, "\n") + 1; got > 24 {
		t.Fatalf("height=%d want <=24:\n%s", got, out)
	}
}

func TestSudoCommandDetectsQueuedSudoWork(t *testing.T) {
	queue := []runner.WorkItem{
		{Name: "nix", Args: []string{"flake", "update"}},
		{Name: "/usr/bin/sudo", Args: []string{"/usr/bin/dnf", "upgrade"}},
	}

	if got := sudoCommand(queue); got != "/usr/bin/sudo" {
		t.Fatalf("expected sudo command, got %q", got)
	}
}

func TestDashboardIsCompact(t *testing.T) {
	m := Model{
		facts: system.Facts{
			Hostname:       "butler",
			User:           "bag",
			OS:             "linux",
			OSID:           "fedora",
			Arch:           "amd64",
			Shell:          "/run/current-system/sw/bin/zsh",
			DotfilesRepo:   "/home/bag/code/dotfiles",
			DotfilesBranch: "main",
			HMGeneration:   "gen 32",
			NixPath:        "/nix/var/nix/profiles/default/bin/nix",
			HomeManager:    "/home/bag/.nix-profile/bin/home-manager",
			Topgrade:       "/home/bag/.nix-profile/bin/topgrade",
			DnfPath:        "/usr/bin/dnf",
			SudoPath:       "/usr/bin/sudo",
			TailscaleIP:    "100.80.183.111",
		},
	}

	out := m.viewDashboard(92)
	lines := strings.Count(out, "\n") + 1
	if lines > 8 {
		t.Fatalf("dashboard should stay compact, got %d lines:\n%s", lines, out)
	}
	for _, want := range []string{"Home", "bag@butler", "fedora", "dnf ok", "tailscale"} {
		if !strings.Contains(out, want) {
			t.Fatalf("dashboard missing %q:\n%s", want, out)
		}
	}
}

func TestAdvanceQueueUsesTerminalHandoffForInteractiveWork(t *testing.T) {
	called := false
	m := Model{
		mode:  modeRunning,
		queue: []runner.WorkItem{{Name: "sudo", Args: []string{"-v"}, Mode: runner.ExecutionInteractive}},
		terminalExec: func(item runner.WorkItem, start time.Time) tea.Cmd {
			called = true
			return func() tea.Msg { return stepDoneMsg{elapsed: time.Second} }
		},
	}

	cmd := m.advanceQueue()
	if !called || cmd == nil {
		t.Fatal("interactive work did not use terminal handoff")
	}
	if m.activeScanner != nil {
		t.Fatal("interactive work must not create captured scanner")
	}
}

func TestInteractiveHandoffReturnAdvancesToSuccessResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	item := runner.WorkItem{Name: "fixture-terminal", Args: []string{"--safe"}, Mode: runner.ExecutionInteractive}
	m := Model{
		mode:      modeRunning,
		screen:    screenRunning,
		queue:     []runner.WorkItem{item},
		runAction: "fixture-terminal",
		runStart:  time.Now().Add(-time.Second),
		terminalExec: func(runner.WorkItem, time.Time) tea.Cmd {
			return func() tea.Msg { return stepDoneMsg{elapsed: time.Second} }
		},
	}

	cmd := m.advanceQueue()
	if cmd == nil {
		t.Fatal("terminal handoff command is nil")
	}
	next, nextCmd := m.Update(cmd())
	got := next.(Model)
	if nextCmd != nil || got.mode != modeDone || got.screen != screenResult || got.runErr != nil || got.queuePos != 1 {
		t.Fatalf("cmd=%v mode=%v screen=%v runErr=%v queuePos=%d", nextCmd, got.mode, got.screen, got.runErr, got.queuePos)
	}
	if len(got.stepResults) != 1 || got.stepResults[0].Status != history.StatusSuccess || got.stepResults[0].Duration != time.Second || got.stepResults[0].Item.Name != item.Name {
		t.Fatalf("handoff step result=%#v", got.stepResults)
	}
	entries := history.Read(1)
	if len(entries) != 1 || entries[0].Status != history.StatusSuccess {
		t.Fatalf("handoff history=%#v", entries)
	}
}

func TestInteractiveFailureStopsQueueAndRestoresDoneState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resolvedHome, err := os.UserHomeDir()
	if err != nil || resolvedHome != home {
		t.Fatalf("isolated HOME not active: home=%q err=%v", resolvedHome, err)
	}

	item := runner.WorkItem{Name: "fixture-failure"}
	m := Model{mode: modeRunning, screen: screenRunning, queue: []runner.WorkItem{item}, reviewed: reviewedPlan{Items: []runner.WorkItem{item}}, runAction: "brew", runStart: time.Now()}
	next, _ := m.Update(stepDoneMsg{err: errors.New("exit status 1"), elapsed: time.Second})
	got := next.(Model)
	if got.mode != modeDone || got.screen != screenResult || !strings.Contains(got.renderLog(), "exit status 1") {
		t.Fatalf("mode=%v screen=%v log=%q", got.mode, got.screen, got.renderLog())
	}
	if got.runErr == nil || got.runErr.Error() != "exit status 1" || got.runElapsed <= 0 || got.runCancelled {
		t.Fatalf("runErr=%v runElapsed=%s runCancelled=%v", got.runErr, got.runElapsed, got.runCancelled)
	}
	if len(got.stepResults) != 1 || got.stepResults[0].Status != history.StatusFailure || got.stepResults[0].Duration != time.Second || got.stepResults[0].Err == nil {
		t.Fatalf("failure step result=%#v", got.stepResults)
	}
	historyPath := filepath.Join(home, ".local", "state", "sys-bozo", "history.jsonl")
	if _, err := os.Stat(historyPath); err != nil {
		t.Fatalf("history was not isolated under temp HOME %q: %v", historyPath, err)
	}
	entries := history.Read(1)
	if len(entries) != 1 || entries[0].OK || entries[0].Status != history.StatusFailure {
		t.Fatalf("failure history=%#v", entries)
	}
}

func TestFinalSuccessStoresResultAndCompatibleHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	m := Model{
		mode:      modeRunning,
		screen:    screenRunning,
		runAction: "fixture-success",
		runStart:  time.Now().Add(-2 * time.Second),
	}

	if cmd := m.advanceQueue(); cmd != nil {
		t.Fatal("completed queue returned command")
	}
	if m.mode != modeDone || m.screen != screenResult || m.runErr != nil || m.runCancelled || m.runElapsed < 2*time.Second {
		t.Fatalf("mode=%v screen=%v runErr=%v cancelled=%v elapsed=%s", m.mode, m.screen, m.runErr, m.runCancelled, m.runElapsed)
	}
	entries := history.Read(1)
	if len(entries) != 1 || !entries[0].OK || entries[0].Status != history.StatusSuccess {
		t.Fatalf("success history=%#v", entries)
	}
}

func TestTerminalHandoffCancellationStoresCancelledResult(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	item := runner.WorkItem{Name: "fixture-cancel", Mode: runner.ExecutionInteractive}
	m := Model{mode: modeRunning, screen: screenRunning, queue: []runner.WorkItem{item}, reviewed: reviewedPlan{Items: []runner.WorkItem{item}}, runAction: "fixture-cancel", runStart: time.Now().Add(-time.Second)}

	next, _ := m.Update(stepDoneMsg{err: errors.New("signal: interrupt"), elapsed: time.Second, cancelled: true})
	got := next.(Model)
	if got.mode != modeDone || got.screen != screenResult || got.runErr == nil || !got.runCancelled || got.runElapsed < time.Second {
		t.Fatalf("mode=%v screen=%v runErr=%v cancelled=%v elapsed=%s", got.mode, got.screen, got.runErr, got.runCancelled, got.runElapsed)
	}
	if len(got.stepResults) != 1 || got.stepResults[0].Status != history.StatusCancelled || got.stepResults[0].Duration != time.Second || got.stepResults[0].Err == nil {
		t.Fatalf("cancel step result=%#v", got.stepResults)
	}
	entries := history.Read(1)
	if len(entries) != 1 || entries[0].OK || entries[0].Status != history.StatusCancelled {
		t.Fatalf("cancel history=%#v", entries)
	}
	got.styles = newUIStyles(true)
	if out := got.View(); !strings.Contains(out, "CANCELLED") {
		t.Fatalf("cancelled result missing status:\n%s", out)
	}
}

func TestStreamedStartFailureLogHasSingleCommandPrefix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	item := runner.WorkItem{
		Name: filepath.Join(t.TempDir(), "missing-command"),
		Mode: runner.ExecutionStreamed,
	}
	m := Model{mode: modeRunning, queue: []runner.WorkItem{item}, runAction: "fixture", runStart: time.Now()}

	if cmd := m.advanceQueue(); cmd != nil {
		t.Fatal("streamed start failure returned a command")
	}
	if m.mode != modeDone {
		t.Fatalf("mode = %v, want %v", m.mode, modeDone)
	}
	got := m.logLines[len(m.logLines)-1].text
	prefix := "  ✗ " + item.Name + ": "
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("log = %q, want prefix %q", got, prefix)
	}
	if strings.HasPrefix(strings.TrimPrefix(got, prefix), item.Name+": ") {
		t.Fatalf("log = %q, duplicate command prefix %q", got, item.Name+": ")
	}
	if m.screen != screenResult || m.runErr == nil || m.runElapsed <= 0 {
		t.Fatalf("screen=%v runErr=%v runElapsed=%s", m.screen, m.runErr, m.runElapsed)
	}
	entries := history.Read(1)
	if len(entries) != 1 || entries[0].OK || entries[0].Status != history.StatusFailure {
		t.Fatalf("start failure history=%#v", entries)
	}
}
