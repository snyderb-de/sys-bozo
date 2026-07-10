package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

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
	m := Model{mode: modeRunning, runAction: "brew", runStart: time.Now()}
	next, _ := m.Update(stepDoneMsg{err: errors.New("exit status 1"), elapsed: time.Second})
	got := next.(Model)
	if got.mode != modeDone || !strings.Contains(got.renderLog(), "exit status 1") {
		t.Fatalf("mode=%v log=%q", got.mode, got.renderLog())
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
