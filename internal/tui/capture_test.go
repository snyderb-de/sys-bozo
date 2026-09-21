package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

func TestTerminalCaptureKeepsTailAcrossPartialWrites(t *testing.T) {
	c := newTerminalCapture(3)
	// Output arrives in arbitrary chunks, not whole lines.
	io.WriteString(c, "one\ntw")
	io.WriteString(c, "o\nthree\r\nfour\n")

	got := c.Lines()
	want := []string{"two", "three", "four"}
	if len(got) != len(want) {
		t.Fatalf("lines=%q want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lines=%q want %q", got, want)
		}
	}
}

func TestTerminalCaptureIncludesUnterminatedPrompt(t *testing.T) {
	c := newTerminalCapture(4)
	io.WriteString(c, "pnpm failed:\n")
	// Topgrade's retry prompt never ends in a newline.
	io.WriteString(c, "Retry? (y)es/(N)o/(s)hell/(q)uit")

	got := c.Lines()
	if len(got) != 2 || !strings.Contains(got[1], "Retry?") {
		t.Fatalf("lines=%q, want the trailing prompt kept", got)
	}
}

func TestTerminalCaptureDropsBlankLines(t *testing.T) {
	c := newTerminalCapture(5)
	io.WriteString(c, "\n\n   \nreal line\n\n")

	if got := c.Lines(); len(got) != 1 || got[0] != "real line" {
		t.Fatalf("lines=%q want [real line]", got)
	}
}

func TestFailedInteractiveStepReportsCapturedStderr(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 100, 40
	m.styles = newUIStyles(true)
	m.mode = modeRunning
	m.runStart = time.Now()
	m.queue = []runner.WorkItem{{
		Title: "Run Topgrade", TaskLabel: "Run Topgrade", TaskFirst: true,
		Name: "topgrade", Mode: runner.ExecutionInteractive,
	}}
	m.reviewed = reviewedPlan{Action: "topgrade", Items: cloneWorkItems(m.queue)}

	// Stand in for the real terminal handoff: write to the capture the way a
	// failing topgrade run does, then report the failure.
	m.terminalExec = func(_ runner.WorkItem, _ time.Time, capture io.Writer) tea.Cmd {
		io.WriteString(capture, "[ERROR] The configured global bin directory is not in PATH\n")
		io.WriteString(capture, "pnpm failed:\n")
		return func() tea.Msg {
			return stepDoneMsg{err: errTestInteractive, elapsed: time.Second}
		}
	}

	if cmd := m.advanceQueue(); cmd != nil {
		cmd()
	}
	m.recordStepResult(0, history.StatusFailure, time.Second, errTestInteractive)

	output := m.stepResults[0].Output
	if len(output) != 2 || !strings.Contains(output[0], "not in PATH") {
		t.Fatalf("captured output=%q", output)
	}

	rendered := strings.Join(stepFailureRows(m.styles, m.stepResults[0], 100, false), "\n")
	if !strings.Contains(rendered, "not in PATH") {
		t.Fatalf("result screen missing captured stderr:\n%s", rendered)
	}
}

func TestInteractiveStepWithNoCaptureStillReportsError(t *testing.T) {
	m := testGuidedModel()
	m.styles = newUIStyles(true)
	m.queue = []runner.WorkItem{{Title: "Apply", Name: "sudo", Mode: runner.ExecutionInteractive}}
	m.termCapture = nil

	m.recordStepResult(0, history.StatusFailure, time.Second, errTestInteractive)

	if got := m.stepResults[0].Output; len(got) != 0 {
		t.Fatalf("expected no output without a capture, got %q", got)
	}
	rendered := strings.Join(stepFailureRows(m.styles, m.stepResults[0], 80, false), "\n")
	if !strings.Contains(rendered, "interactive failure") {
		t.Fatalf("error still must render:\n%s", rendered)
	}
}

var errTestInteractive = &captureTestError{}

type captureTestError struct{}

func (*captureTestError) Error() string { return "interactive failure" }
