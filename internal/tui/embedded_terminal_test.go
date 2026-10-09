package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/terminal"
)

func embeddedFixture(t *testing.T, script string) Model {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := testGuidedModel()
	m.styles = newUIStyles(os.Getenv("NO_COLOR") != "")
	m.width, m.height = 80, 24
	m.terminalExec = nil
	m.terminalGroup = terminal.NewGroup()
	t.Cleanup(m.Close)
	m.reviewed = reviewedPlan{Action: "embedded-fixture", Items: []runner.WorkItem{{Name: "/bin/sh", Args: []string{"-c", script}, Mode: runner.ExecutionInteractive}}}
	return m
}

func startEmbeddedFixture(t *testing.T, script string) Model {
	t.Helper()
	m := embeddedFixture(t, script)
	m.beginReviewedRun()
	cmd := m.advanceQueue()
	if !m.terminalStarting || cmd == nil {
		t.Fatal("interactive work did not start embedded terminal")
	}
	msg, ok := cmd().(terminalStartedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("start=%+v", msg)
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func awaitPane(t *testing.T, m Model, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(m.activeTerminal.Snapshot().Plain, want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("missing %q in %q", want, m.activeTerminal.Snapshot().Plain)
}

func completeEmbeddedFixture(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	select {
	case <-m.activeTerminal.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("child did not exit")
	}
	next, cmd := m.Update(terminalTickMsg{m.terminalGeneration})
	return next.(Model), cmd
}

func TestEmbeddedFocusInputAndPrivateFailureHistory(t *testing.T) {
	m := startEmbeddedFixture(t, `printf 'READY\n'; read -r value; printf 'PRIVATE:%s\n' "$value"; exit 7`)
	awaitPane(t, m, "READY")
	for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("q?j")}, {Type: tea.KeyEnter}} {
		next, _ := m.Update(key)
		m = next.(Model)
	}
	m, _ = completeEmbeddedFixture(t, m)
	if m.helpVisible || m.screen != screenResult || m.runErr == nil || !m.stepResults[0].PrivateOutput {
		t.Fatal("child input triggered dashboard action or lost failure")
	}
	if !strings.Contains(strings.Join(m.stepResults[0].Output, "\n"), "PRIVATE:q?j") {
		t.Fatal("missing in-memory error detail")
	}
	if m.latestHistory == nil || len(m.latestHistory.Output) != 0 {
		t.Fatal("private terminal output persisted in history")
	}
}

func TestEmbeddedCancellationAfterChildExitDoesNotAdvanceQueue(t *testing.T) {
	m := startEmbeddedFixture(t, `printf 'done\n'`)
	<-m.activeTerminal.Done()
	m.queue = append(m.queue, runner.WorkItem{Name: "/must-not-run", Mode: runner.ExecutionInteractive})
	next, cleanup := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	cleanup()
	m, quit := completeEmbeddedFixture(t, m)
	if quit == nil || m.queuePos != 0 || !m.runCancelled || !errors.Is(m.runErr, context.Canceled) {
		t.Fatal("quit raced completion and advanced the queue")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("quit was lost")
	}
}

func TestEmbeddedQuitWhileStarting(t *testing.T) {
	m := embeddedFixture(t, "exit 0")
	m.beginReviewedRun()
	start := m.advanceQueue()
	next, cleanup := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	cleanup()
	next, quit := m.Update(start())
	m = next.(Model)
	if quit == nil || !m.runCancelled || m.screen != screenResult {
		t.Fatal("quit during startup did not finish")
	}
}

func TestEmbeddedLayoutAndEscapeAtAllSizes(t *testing.T) {
	m := startEmbeddedFixture(t, `printf '\033[32mREADY\033[0m\n'; read -r reply`)
	awaitPane(t, m, "READY")
	for _, size := range [][2]int{{80, 24}, {120, 36}, {60, 24}, {30, 8}} {
		for _, plain := range []bool{false, true} {
			next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = next.(Model)
			m.styles = newUIStyles(plain)
			view := m.View()
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] || plain && strings.Contains(view, "\x1b") {
				t.Fatalf("invalid pane at %v plain=%v: %q", size, plain, view)
			}
			m.terminalFocused = true
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(Model)
			if m.terminalFocused || m.screen != screenRunning {
				t.Fatal("Escape failed to return to dashboard controls")
			}
		}
	}
}

// A real outer PTY drives this reviewed, harmless script. Password input uses
// /dev/tty with echo disabled, just as an interactive authentication prompt does.
func TestPTYEmbeddedTerminal(t *testing.T) {
	if os.Getenv("SYS_BOZO_EMBEDDED_PTY") != "1" {
		t.Skip("requires scripts/embedded-terminal-pty-smoke.py")
	}
	script := `stty -echo
printf '\033[2J\033[HPassword: '
IFS= read -r value </dev/tty
stty echo
test "$value" = 'fixture-secret' || exit 7
printf '\r\nAUTH_OK\r\n'
printf 'RESIZE_READY\r\n'
read -r reply
stty size
printf 'CONTINUE_READY\r\n'
read -r reply
printf 'CHILD_DONE\r\n'`
	if os.Getenv("SYS_BOZO_EMBEDDED_CANCEL") == "1" {
		script = `trap '' TERM HUP; printf 'CANCEL_READY\n'; while :; do sleep 1; done`
	}
	// Keep script text out of the displayed command so markers only originate
	// from actual child output.
	path := filepath.Join(t.TempDir(), "prompt-fixture.sh")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	m := embeddedFixture(t, "")
	m.screen = screenReview
	m.reviewed.Items[0].Args = []string{path}
	var trace strings.Builder
	traced, input := TraceInput(m, os.Stdin, &trace)
	final, err := tea.NewProgram(traced, tea.WithAltScreen(), tea.WithInput(input)).Run()
	if err != nil {
		t.Fatal(err)
	}
	got := final.(tracedModel).Model
	if got.activeTerminal != nil || got.screen != screenResult || len(got.stepResults) != 1 {
		t.Fatal("command did not return to a result")
	}
	if strings.Contains(trace.String(), "fixture-secret") {
		t.Fatal("typed text leaked into trace")
	}
	if os.Getenv("SYS_BOZO_EMBEDDED_CANCEL") == "1" {
		if !got.runCancelled {
			t.Fatal("cancellation not recorded")
		}
	} else if got.runErr != nil {
		t.Fatal(got.runErr)
	}
	fmt.Println("EMBEDDED_PTY_OK")
}
