package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/runner"
)

func TestTerminalLineWriterPreservesProgressAndSplitCRLF(t *testing.T) {
	var output bytes.Buffer
	w := &terminalLineWriter{output: &output}
	chunks := []string{"first\n", "second\r", "\n", "\x1b[32m10%\r20%\x1b[0m", "\n", "Password: "}
	for _, chunk := range chunks {
		if n, err := io.WriteString(w, chunk); n != len(chunk) || err != nil {
			t.Fatalf("write returned %d, %v", n, err)
		}
	}
	want := "first\r\nsecond\r\n\x1b[32m10%\r20%\x1b[0m\r\nPassword: "
	if got := output.String(); got != want {
		t.Fatalf("terminal output=%q want %q", got, want)
	}
}

type shortTerminalWriter struct{ err error }

func (w shortTerminalWriter) Write([]byte) (int, error) { return 0, w.err }

func TestTerminalLineWriterReportsWriteFailures(t *testing.T) {
	failure := errors.New("terminal closed")
	for _, err := range []error{nil, failure} {
		w := &terminalLineWriter{output: shortTerminalWriter{err: err}}
		want := err
		if want == nil {
			want = io.ErrShortWrite
		}
		if n, got := w.Write([]byte("line\n")); n != 0 || !errors.Is(got, want) {
			t.Fatalf("write returned %d, %v; want 0, %v", n, got, want)
		}
	}
}

type terminalOutputFixture struct {
	capture *terminalCapture
	done    bool
	err     error
}

func (m terminalOutputFixture) Init() tea.Cmd {
	// Simulate a child (such as sudo) disabling output post-processing while
	// sys-bozo copies piped stderr to the terminal. Keep that mode until the
	// harness has checked the bytes and sends Enter. No maintenance or sudo.
	script := `state=$(stty -g)
trap 'stty "$state"' EXIT
stty -opost
printf 'OUTPUT_FIRST\nOUTPUT_SECOND\n' >&2
IFS= read -r reply`
	return runInteractiveWork(runner.WorkItem{Name: "/bin/sh", Args: []string{"-c", script}}, time.Now(), m.capture)
}

func (m terminalOutputFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if done, ok := msg.(stepDoneMsg); ok {
		m.done, m.err = true, done.err
		return m, tea.Quit
	}
	return m, nil
}

func (m terminalOutputFixture) View() string { return "Terminal output fixture\n" }

func TestPTYInteractiveOutput(t *testing.T) {
	if os.Getenv("SYS_BOZO_OUTPUT_PTY") != "1" {
		t.Skip("requires scripts/terminal-output-pty-smoke.py")
	}
	capture := newTerminalCapture(4)
	options := []tea.ProgramOption{tea.WithAltScreen()}
	if os.Getenv("SYS_BOZO_OUTPUT_TRACE") == "1" {
		_, input := TraceInput(Model{}, os.Stdin, io.Discard)
		options = append(options, tea.WithInput(input))
	}
	final, err := tea.NewProgram(terminalOutputFixture{capture: capture}, options...).Run()
	if err != nil {
		t.Fatal(err)
	}
	m := final.(terminalOutputFixture)
	if !m.done || m.err != nil {
		t.Fatalf("child completion: done=%v err=%v", m.done, m.err)
	}
	if got := strings.Join(capture.Lines(), "\n"); got != "OUTPUT_FIRST\nOUTPUT_SECOND" {
		t.Fatalf("capture changed: %q", got)
	}
	fmt.Println("OUTPUT_RESTORED_OK")
}
