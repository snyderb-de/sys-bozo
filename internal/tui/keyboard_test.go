package tui

import (
	"context"
	"fmt"
	"github.com/snyderb-de/sys-bozo/internal/repostate"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
	"github.com/snyderb-de/sys-bozo/internal/terminal"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInspectEscapeBack(t *testing.T) {
	for _, size := range []struct{ width, height int }{{80, 24}, {120, 40}, {59, 24}, {80, 19}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := testGuidedModel()
			m.width, m.height = 80, 24
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
			m = next.(Model)
			if m.screen != screenInspect {
				t.Fatal("option 3 did not open Inspect")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
			m = next.(Model)
			if m.screen != screenDoctor {
				t.Fatal("Inspect option 3 did not open Doctor")
			}
			next, _ = m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			m = next.(Model)
			for _, target := range []screen{screenInspect, screenHome} {
				next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
				m = next.(Model)
				if m.screen != target {
					t.Fatalf("Escape: got screen %d, want %d", m.screen, target)
				}
			}
		})
	}
}

func TestTinyTerminalEscapeClosesHelpFirst(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 30, 8
	m.screen = screenInspect
	m.helpVisible = true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if cmd != nil || m.helpVisible || m.screen != screenInspect {
		t.Fatal("Escape should dismiss help before navigating back")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(Model).screen != screenHome {
		t.Fatal("Escape should return home after help closes")
	}
}

func TestRepeatedEscapeStillNavigatesBack(t *testing.T) {
	for _, active := range []screen{screenInspect, screenRepoTriage, screenDoctor} {
		m := testGuidedModel()
		m.width, m.height = 80, 24
		m.screen, m.repoReturn = active, screenInspect
		// Bubble Tea decodes two Escape bytes in one terminal read as Alt+Esc.
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc, Alt: true})
		if next.(Model).screen == active {
			t.Fatalf("repeated Escape was ignored on screen %d", active)
		}
	}
}

func TestCtrlCQuitsEveryInputState(t *testing.T) {
	for _, name := range []string{"home", "package-query", "config-prompt", "result", "help", "mini-updates", "repository-message", "repository-delete", "repository", "package-results", "package-placement", "review", "running", "inspect", "config", "audit", "doctor", "history", "small"} {
		t.Run(name, func(t *testing.T) {
			m := testGuidedModel()
			m.width, m.height = 80, 24
			switch name {
			case "package-query":
				m.openPackageFlow()
			case "config-prompt":
				m.screen = screenConfig
				m.applyPrompt = true
			case "result":
				m.screen = screenResult
				m.mode = modeDone
			case "help":
				m.helpVisible = true
			case "mini-updates":
				m = miniUpdateModel()
			case "repository-delete":
				m.screen = screenRepoTriage
				m.repoFlow.stage = repoDeleteConfirm
			case "repository":
				m.screen = screenRepoTriage
			case "package-results":
				m.openPackageFlow()
				m.packageFlow.stage = packageChoose
			case "package-placement":
				m.openPackageFlow()
				m.packageFlow.stage = packagePlacement
			case "review":
				m.screen = screenReview
			case "running":
				m.screen = screenRunning
				m.mode = modeRunning
			case "inspect":
				m.screen = screenInspect
			case "config":
				m.screen = screenConfig
			case "audit":
				m.screen = screenAudit
			case "doctor":
				m.screen = screenDoctor
			case "history":
				m.screen = screenHistory
			case "small":
				m.width, m.height = 30, 8
			case "repository-message":
				m.screen = screenRepoTriage
				m.repoFlow.stage = repoCommitMessage
			}
			_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			if cmd == nil {
				t.Fatal("Ctrl-C was swallowed instead of quitting")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Fatal("Ctrl-C did not produce QuitMsg")
			}
		})
	}
}

func TestRefreshReturnsWhileHostProbeIsBlocked(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	t.Setenv("PATH", dir)
	t.Setenv("BOZO_TEST_STARTED", started)
	t.Setenv("BOZO_TEST_RELEASE", release)
	script := "#!/bin/sh\n: > \"$BOZO_TEST_STARTED\"\nwhile [ ! -e \"$BOZO_TEST_RELEASE\" ]; do /bin/sleep 0.01; done\n"
	if err := os.WriteFile(filepath.Join(dir, "brew"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	m := testGuidedModel()
	m.width, m.height = 80, 24
	m.screen = screenDoctor
	type response struct {
		model tea.Model
		cmd   tea.Cmd
	}
	returned := make(chan response, 1)
	consumed := false
	go func() {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		returned <- response{next, cmd}
	}()
	// Always release the fake process, including when the pre-fix loop is blocked.
	defer func() {
		os.WriteFile(release, nil, 0600)
		if !consumed {
			select {
			case <-returned:
			case <-time.After(time.Second):
			}
		}
	}()
	select {
	case got := <-returned:
		consumed = true
		if !got.model.(Model).refreshing {
			t.Fatal("refresh busy state was lost")
		}
		if got.cmd == nil {
			t.Fatal("refresh did not schedule the host probe")
		}
		finished := make(chan tea.Msg, 1)
		go func() { finished <- got.cmd() }()
		deadline := time.After(time.Second)
		for {
			if _, err := os.Stat(started); err == nil {
				break
			}
			select {
			case <-deadline:
				t.Fatal("probe never reached fake brew")
			default:
				time.Sleep(time.Millisecond)
			}
		}
		next, _ := got.model.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
		if next.(Model).screen != screenInspect {
			t.Fatal("Escape did not navigate while refreshing")
		}
		_, quit := next.(Model).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if quit == nil {
			t.Fatal("Ctrl-C ignored while refreshing")
		}
		os.WriteFile(release, nil, 0600)
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("probe did not finish after release")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("host probe blocks Update; Escape and Ctrl-C cannot be processed")
	}
}

func TestLateHostRefreshCannotChangeAnotherWorkflow(t *testing.T) {
	for _, target := range []screen{screenInspect, screenReview, screenRunning} {
		m := testGuidedModel()
		m.screen = screenDoctor
		m.refreshHostCmd()
		msg := hostRefreshedMsg{id: m.refreshID, origin: screenDoctor, facts: system.Facts{Hostname: "replacement"}, runCtx: runner.Context{Hostname: "replacement"}}
		m.screen = target
		next, _ := m.Update(msg)
		got := next.(Model)
		if got.refreshing || got.facts.Hostname == "replacement" || got.runCtx.Hostname == "replacement" {
			t.Fatalf("late refresh changed screen %d context", target)
		}
	}
}

func TestHostRefreshCoalescesAndAcceptsCurrentResult(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 80, 24
	m.screen = screenDoctor
	if m.refreshHostCmd() == nil || m.refreshHostCmd() != nil {
		t.Fatal("refresh should schedule once")
	}
	msg := hostRefreshedMsg{id: m.refreshID, origin: m.screen, facts: system.Facts{Hostname: "fresh"}, runCtx: runner.Context{Hostname: "fresh"}}
	next, cmd := m.Update(msg)
	got := next.(Model)
	if cmd != nil || got.refreshing || got.facts.Hostname != "fresh" || got.runCtx.Hostname != "fresh" {
		t.Fatal("refresh failed to apply current result")
	}
}

func TestCtrlCCancelsActivePackageSearch(t *testing.T) {
	m := testGuidedModel()
	m.openPackageFlow()
	cancelled := false
	m.packageSearchCancel = func() { cancelled = true }
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || !cancelled {
		t.Fatal("quit leaked the active search")
	}
}

// Driven by a PTY harness: confirm the harmless printf, navigate after the
// embedded terminal completes, and exit with Ctrl-C from the package text field.
func TestPTYKeyboardAfterHandoff(t *testing.T) {
	if os.Getenv("SYS_BOZO_KEYBOARD_PTY") != "1" {
		t.Skip("requires PTY keyboard driver")
	}
	if !isTerminalFile(os.Stdin) || !isTerminalFile(os.Stdout) {
		t.Fatal("requires terminal input/output")
	}
	m := testGuidedModel()
	m.styles = newUIStyles(os.Getenv("NO_COLOR") != "")
	m.width, m.height = 80, 24
	m.screen = screenReview
	m.terminalExec = nil
	m.terminalGroup = terminal.NewGroup()
	defer m.Close()
	m.inspectRepo = func(context.Context, string) repostate.Status {
		return repostate.Status{Entries: []repostate.Entry{{
			Kind: '1', Path: "fixture.nix", Index: repostate.StateUnmodified, Worktree: repostate.StateModified,
		}}}
	}
	m.reviewed = reviewedPlan{Action: "keyboard-fixture", Items: []runner.WorkItem{{Name: "/usr/bin/printf", Args: []string{"KEYBOARD_CHILD_OK\n"}, Mode: runner.ExecutionInteractive}}}
	var model tea.Model = m
	var traceLog *os.File
	options := []tea.ProgramOption{tea.WithAltScreen()}
	if path := os.Getenv("SYS_BOZO_KEYBOARD_TRACE"); path != "" {
		log, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer log.Close()
		traceLog = log
		fmt.Fprintf(log, "{\"event\":\"fixture\",\"home\":%d,\"review\":%d,\"result\":%d,\"inspect\":%d,\"repo\":%d,\"package\":%d}\n",
			screenHome, screenReview, screenResult, screenInspect, screenRepoTriage, screenPackage)
		traced, input := TraceInput(m, os.Stdin, log)
		model = traced
		options = append(options, tea.WithInput(input))
	}
	final, err := tea.NewProgram(model, options...).Run()
	if err != nil {
		t.Fatal(err)
	}
	if traced, ok := final.(tracedModel); ok {
		final = traced.Model
	}
	got := final.(Model)
	if got.screen != screenPackage || got.packageFlow.stage != packageSearch {
		t.Fatalf("expected Ctrl-C to quit from package input, got screen=%d", got.screen)
	}
	fmt.Println("KEYBOARD_PTY_OK")
	if traceLog != nil {
		fmt.Fprintln(traceLog, `{"event":"fixture_done"}`)
	}
}
