package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

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
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = next.(Model)
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

func TestShortcutsOnlyOpenMaintenanceFromHome(t *testing.T) {
	tests := []struct {
		key        rune
		wantScreen screen
	}{
		{key: '2', wantScreen: screenMaintenance},
		{key: '3', wantScreen: screenConfig},
		{key: '4', wantScreen: screenAudit},
		{key: '5', wantScreen: screenDoctor},
	}
	for _, tt := range tests {
		t.Run(string(tt.key), func(t *testing.T) {
			m := testGuidedModel()
			next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tt.key}})
			got := next.(Model)
			if got.screen != tt.wantScreen {
				t.Fatalf("screen=%v, want %v", got.screen, tt.wantScreen)
			}

			next, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
			got = next.(Model)
			if got.screen != tt.wantScreen || len(got.selected) != 0 {
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

	out := m.viewAudit(100)
	for _, want := range []string{"ssh config", "unmanaged file", "why:", "SSH aliases", "fix:", "home-manager", "switch."} {
		if !strings.Contains(out, want) {
			t.Fatalf("audit view missing %q:\n%s", want, out)
		}
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

func TestInteractiveFailureStopsQueueAndRestoresDoneState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	resolvedHome, err := os.UserHomeDir()
	if err != nil || resolvedHome != home {
		t.Fatalf("isolated HOME not active: home=%q err=%v", resolvedHome, err)
	}

	m := Model{mode: modeRunning, screen: screenRunning, runAction: "brew", runStart: time.Now()}
	next, _ := m.Update(stepDoneMsg{err: errors.New("exit status 1"), elapsed: time.Second})
	got := next.(Model)
	if got.mode != modeDone || got.screen != screenResult || !strings.Contains(got.renderLog(), "exit status 1") {
		t.Fatalf("mode=%v screen=%v log=%q", got.mode, got.screen, got.renderLog())
	}
	historyPath := filepath.Join(home, ".local", "state", "sys-bozo", "history.jsonl")
	if _, err := os.Stat(historyPath); err != nil {
		t.Fatalf("history was not isolated under temp HOME %q: %v", historyPath, err)
	}
}

func TestStreamedStartFailureLogHasSingleCommandPrefix(t *testing.T) {
	item := runner.WorkItem{
		Name: filepath.Join(t.TempDir(), "missing-command"),
		Mode: runner.ExecutionStreamed,
	}
	m := Model{mode: modeRunning, queue: []runner.WorkItem{item}}

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
}
