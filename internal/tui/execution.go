package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/repostate"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

func (m *Model) openMaintenance(ids ...string) {
	m.updatesRecovery, m.updatesNotice, m.updatesOffset = false, "", 0
	m.screen = screenMaintenance
	for i, tab := range m.tabs {
		if tab == "Actions" {
			m.tab = i
			m.cursor = 0
			break
		}
	}
	m.selected = map[string]bool{}
	for _, id := range ids {
		if runner.HasManagedMacWorkflow(m.runCtx) && id == "hms" {
			id = "nds"
		}
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
	m.reviewed = reviewedPlan{
		Action: strings.Join(ids, "+"),
		Items:  cloneWorkItems(items),
	}
	m.screen = screenReview
}

func (m Model) hasAvailableSelection() bool {
	for _, task := range m.tasks {
		if m.selected[task.ID] && task.Available(m.runCtx) {
			return true
		}
	}
	return false
}

func (m *Model) confirmReviewedPlan() tea.Cmd {
	if m.reviewed.Updates != nil {
		if m.reviewed.Updates.Problem != "" {
			return nil
		}
		return m.checkMiniUpdates(true)
	}
	if m.reviewed.Repo != nil {
		if m.reviewed.Repo.Validating || m.validateRepo == nil {
			return nil
		}
		m.repoValidationID++
		requestID := m.repoValidationID
		review := m.reviewed.Repo
		operation := cloneRepoOperation(m.reviewed.Repo.Operation)
		validate := m.validateRepo
		m.reviewed.Repo.Validating = true
		m.reviewed.Repo.Notice = "validating exact status and bytes…"
		return func() tea.Msg {
			err := validate(context.Background(), operation)
			return repoValidatedMsg{requestID: requestID, review: review, operation: operation, err: err}
		}
	}
	if m.reviewed.Config != nil {
		m.beginReviewedRun()
		if m.reviewed.Config.EditApplied {
			return m.advanceQueue()
		}
		return m.applyConfigCmd(m.reviewed.Config.Proposal)
	}
	if m.reviewed.Package != nil {
		m.beginReviewedRun()
		if m.reviewed.Package.EditApplied {
			return m.advanceQueue()
		}
		return m.applyPackageCmd(m.reviewed.Package.Proposal)
	}
	if len(m.reviewed.Items) == 0 {
		return nil
	}
	m.beginReviewedRun()
	return tea.Batch(m.advanceQueue(), m.spinner.Tick)
}

func repoWorkItems(operation repostate.Operation) []runner.WorkItem {
	items := make([]runner.WorkItem, len(operation.Commands))
	for i, command := range operation.Commands {
		mode := runner.ExecutionStreamed
		if command.Interactive {
			mode = runner.ExecutionInteractive
		}
		items[i] = runner.WorkItem{
			TaskLabel: string(operation.Kind), TaskFirst: i == 0,
			Name: command.Name, Args: append([]string(nil), command.Args...), Dir: operation.Repo,
			Mode: mode,
		}
	}
	return items
}

func (m *Model) beginReviewedRun() {
	m.updatesOffset = 0
	m.queue = cloneWorkItems(m.reviewed.Items)
	m.queuePos = 0
	m.mode = modeRunning
	m.screen = screenRunning
	m.runAction = m.reviewed.Action
	m.runStart = time.Now()
	m.runErr = nil
	m.runCancelled = false
	m.runElapsed = 0
	m.stepResults = nil
	m.stepLogStart = 0
	m.termCapture = nil
	m.resultLogVisible = false
	m.logLines = nil
	m.logFollow = true
	m.logVP = viewport.New(m.logWidth(), m.logHeight())
}

func cloneWorkItems(items []runner.WorkItem) []runner.WorkItem {
	if items == nil {
		return nil
	}
	cloned := make([]runner.WorkItem, len(items))
	copy(cloned, items)
	for i := range cloned {
		cloned[i].Args = append([]string(nil), items[i].Args...)
		cloned[i].EnvExtra = append([]string(nil), items[i].EnvExtra...)
	}
	return cloned
}

// ── Run logic ─────────────────────────────────────────────────────────────

// runInteractiveWork hands the terminal to the child process and tees its
// stderr into capture. Bubbletea only wires up a stream it finds unset, so
// presetting Stderr keeps stdin and stdout pointed at the real terminal: the
// child still sees a tty for prompts and progress, and errors are still
// printed, they are just also recorded for the result screen.
func runInteractiveWork(item runner.WorkItem, start time.Time, capture io.Writer) tea.Cmd {
	cmd := runner.Command(item)
	if capture != nil {
		var output io.Writer = os.Stderr
		if term.IsTerminal(os.Stderr.Fd()) {
			output = &terminalLineWriter{output: output}
		}
		cmd.Stderr = io.MultiWriter(output, capture)
	}
	return tea.Exec(&terminalExecCommand{Cmd: cmd}, func(err error) tea.Msg {
		return stepDoneMsg{err: err, elapsed: time.Since(start), cancelled: terminalWorkCancelled(err)}
	})
}

// Keep the actual terminal file when input tracing is enabled. os/exec turns
// an arbitrary io.Reader into a pipe and waits for its copying goroutine;
// a terminal read can then block Wait even after the child has exited.
type terminalExecCommand struct{ *exec.Cmd }

func (c *terminalExecCommand) SetStdin(input io.Reader) {
	if traced, ok := input.(*tracedInput); ok {
		input = traced.File
	}
	if c.Stdin == nil {
		c.Stdin = input
	}
}

func (c *terminalExecCommand) SetStdout(output io.Writer) {
	if c.Stdout == nil {
		c.Stdout = output
	}
}

func (c *terminalExecCommand) SetStderr(output io.Writer) {
	if c.Stderr == nil {
		c.Stderr = output
	}
}

func terminalWorkCancelled(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return false
	}
	return terminalStatusCancelled(status)
}

func terminalStatusCancelled(status syscall.WaitStatus) bool {
	if status.Signaled() {
		signal := status.Signal()
		return signal == syscall.SIGINT || signal == syscall.SIGTERM
	}
	exitCode := status.ExitStatus()
	return exitCode == 130 || exitCode == 143
}

func (m Model) availableTasks() []runner.Task {
	var out []runner.Task
	for _, t := range m.tasks {
		if t.Available(m.runCtx) {
			out = append(out, t)
		}
	}
	return out
}

func (m Model) runSudoPreflight(sudoBin string) tea.Cmd {
	cmd := exec.Command(sudoBin, "-v")
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return sudoReadyMsg{err: err}
	})
}

func (m *Model) advanceQueue() tea.Cmd {
	if m.queuePos >= len(m.queue) {
		if m.reviewed.Config != nil && m.reviewed.Config.CleanupErr != nil {
			m.finishRun(m.reviewed.Config.CleanupErr, false, time.Since(m.runStart))
			return nil
		}
		if m.reviewed.Package != nil && m.reviewed.Package.EditApplied && !m.reviewed.Package.verificationStarted {
			m.reviewed.Package = clonePackageReview(m.reviewed.Package)
			if m.reviewed.Package.Revert {
				elapsed := time.Since(m.runStart)
				m.logLines = append(m.logLines, logLine{kind: logSuccess, text: "  ✓ previous declaration restored"})
				m.finishRun(nil, false, elapsed)
				return nil
			}
			m.reviewed.Package.verificationStarted = true
			m.logLines = append(m.logLines, logLine{kind: logHeader, text: "  ● verify package"})
			return m.verifyPackageCmd(m.reviewed.Package.Verify)
		}
		elapsed := time.Since(m.runStart)
		m.logLines = append(m.logLines, logLine{kind: logHeader, text: ""})
		m.logLines = append(m.logLines, logLine{kind: logSuccess,
			text: fmt.Sprintf("  ✓ all done · %s", elapsed.Round(time.Second))})
		m.finishRun(nil, false, elapsed)
		return nil
	}

	item := m.queue[m.queuePos]

	if item.TaskFirst {
		m.logLines = append(m.logLines, logLine{kind: logHeader,
			text: fmt.Sprintf("  ● %s", item.TaskLabel)})
	}

	m.logLines = append(m.logLines, logLine{kind: logCmd,
		text: "    $ " + runner.CmdLabel(item)})
	m.stepLogStart = len(m.logLines)

	m.stepStart = time.Now()
	if item.Mode == runner.ExecutionInteractive {
		if m.terminalExec == nil {
			return m.startEmbeddedTerminal(item)
		}
		m.logLines = append(m.logLines, logLine{
			kind: logOutput,
			text: "  ! terminal handoff — input stays outside sys-bozo",
		})
		m.logVP.SetContent(m.renderLog())
		m.logVP.GotoBottom()
		execInteractive := m.terminalExec
		m.termCapture = newTerminalCapture(stepOutputTailLines)
		return execInteractive(item, m.stepStart, m.termCapture)
	}

	scanner, wait, err := runner.StartWork(item)
	if err != nil {
		stepElapsed := time.Since(m.stepStart)
		m.recordStepResult(m.queuePos, history.StatusFailure, stepElapsed, err)
		elapsed := time.Since(m.runStart)
		m.logLines = append(m.logLines, logLine{kind: logError,
			text: "  ✗ " + err.Error()})
		m.finishRun(err, false, elapsed)
		return nil
	}

	m.activeScanner = scanner
	m.activeWait = wait
	m.logVP.SetContent(m.renderLog())
	m.logVP.GotoBottom()

	return m.readNextLine()
}

func (m *Model) recordStepResult(index int, status history.Status, duration time.Duration, err error) {
	if index < 0 || index >= len(m.queue) {
		return
	}
	for len(m.stepResults) <= index {
		m.stepResults = append(m.stepResults, stepResult{})
	}
	item := cloneWorkItems(m.queue[index : index+1])[0]
	var output []string
	if status != history.StatusSuccess {
		if item.Mode == runner.ExecutionInteractive && m.termCapture != nil {
			output = m.termCapture.Lines()
		}
		if len(output) == 0 {
			output = m.stepOutputExcerpt()
		}
	}
	m.stepResults[index] = stepResult{Item: item, Status: status, Duration: duration, Err: err, Output: output, PrivateOutput: m.embeddedStep}
}

const stepOutputTailLines = 8

// stepOutputExcerpt returns what a reader needs to diagnose a failed step. An
// exec error is only ever "exit status 1", so the tool's own words are the
// actionable part. Tools disagree on where those words sit: Nix prints the root
// cause first and then cascading "Cannot build" lines, so a plain tail keeps
// only the cascade. Keep the first error line as well, and say how much was cut.
// Interactive steps write straight to the terminal and return nothing here.
func (m Model) stepOutputExcerpt() []string {
	var lines []string
	firstError := -1
	for _, line := range m.logLines[clamp(m.stepLogStart, 0, len(m.logLines)):] {
		if line.kind != logOutput && line.kind != logError {
			continue
		}
		text := strings.TrimSpace(line.text)
		if text == "" {
			continue
		}
		if firstError < 0 && line.kind == logError {
			firstError = len(lines)
		}
		lines = append(lines, text)
	}
	if len(lines) <= stepOutputTailLines {
		return lines
	}
	tailFrom := len(lines) - stepOutputTailLines
	// The first error already survives inside the tail, or there is no error
	// line to rescue; the tail alone is the whole story.
	if firstError < 0 || firstError >= tailFrom {
		return lines[tailFrom:]
	}
	excerpt := []string{lines[firstError]}
	if omitted := tailFrom - firstError - 1; omitted > 0 {
		excerpt = append(excerpt, fmt.Sprintf("… %d more lines …", omitted))
	}
	return append(excerpt, lines[tailFrom:]...)
}

// failedStep reports the step a run stopped on, so history and the result
// screen name it instead of only the action.
func (m Model) failedStep() (stepResult, bool) {
	for i := len(m.stepResults) - 1; i >= 0; i-- {
		if m.stepResults[i].Status == history.StatusFailure || m.stepResults[i].Status == history.StatusCancelled {
			return m.stepResults[i], true
		}
	}
	return stepResult{}, false
}

func (m *Model) finishRun(err error, cancelled bool, elapsed time.Duration) {
	m.updatesOffset = 0
	status := runStatus(err, cancelled)
	m.mode = modeDone
	m.screen = screenResult
	m.runErr = err
	m.runCancelled = cancelled
	m.runElapsed = elapsed
	m.logVP.SetContent(m.renderLog())
	m.logVP.GotoBottom()
	entry := history.Entry{
		Ts:     time.Now(),
		Action: m.runAction,
		Secs:   elapsed.Seconds(),
		OK:     status == history.StatusSuccess,
		Status: status,
	}
	if status != history.StatusSuccess {
		if failed, ok := m.failedStep(); ok {
			entry.Step = failed.Item.Title
			if !failed.PrivateOutput {
				entry.Output = failed.Output
			}
			if failed.Err != nil {
				entry.Error = failed.Err.Error()
			}
		}
		if entry.Error == "" && err != nil {
			entry.Error = err.Error()
		}
	}
	history.Append(entry)
	m.refreshLatestHistory()
}

func runStatus(err error, cancelled bool) history.Status {
	if cancelled {
		return history.StatusCancelled
	}
	if err != nil {
		return history.StatusFailure
	}
	return history.StatusSuccess
}

func (m Model) readNextLine() tea.Cmd {
	scanner := m.activeScanner
	wait := m.activeWait
	start := m.stepStart

	return func() tea.Msg {
		if scanner.Scan() {
			text := strings.TrimSuffix(scanner.Text(), "\n")
			text = strings.TrimSuffix(text, "\r")
			return lineMsg{text: text}
		}
		err := errors.Join(scanner.Err(), wait())
		return stepDoneMsg{err: err, elapsed: time.Since(start)}
	}
}

func (m Model) runAudit() tea.Cmd {
	return func() tea.Msg {
		return auditReadyMsg{items: system.LocalAudit()}
	}
}

func countTasks(queue []runner.WorkItem) int {
	n := 0
	for _, item := range queue {
		if item.TaskFirst {
			n++
		}
	}
	return n
}

func countCompletedTasks(queue []runner.WorkItem, pos int) int {
	n := 0
	for i := 0; i < pos && i < len(queue); i++ {
		if queue[i].TaskFirst {
			n++
		}
	}
	return n
}

func sudoCommand(queue []runner.WorkItem) string {
	for _, item := range queue {
		if item.Name == "sudo" || strings.HasSuffix(item.Name, "/sudo") {
			return item.Name
		}
	}
	return ""
}
