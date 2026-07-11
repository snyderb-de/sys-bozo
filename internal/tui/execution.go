package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/repostate"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

func (m *Model) openMaintenance(ids ...string) {
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
	if m.reviewed.Repo != nil {
		if m.reviewed.Repo.Validating || m.validateRepo == nil {
			return nil
		}
		m.repoValidationID++
		requestID := m.repoValidationID
		operation := cloneRepoOperation(m.reviewed.Repo.Operation)
		validate := m.validateRepo
		m.reviewed.Repo.Validating = true
		m.reviewed.Repo.Notice = "validating exact status and bytes…"
		return func() tea.Msg {
			return repoValidatedMsg{requestID: requestID, err: validate(context.Background(), operation)}
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

func runInteractiveWork(item runner.WorkItem, start time.Time) tea.Cmd {
	return tea.ExecProcess(runner.Command(item), func(err error) tea.Msg {
		return stepDoneMsg{err: err, elapsed: time.Since(start), cancelled: terminalWorkCancelled(err)}
	})
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

	m.stepStart = time.Now()
	if item.Mode == runner.ExecutionInteractive {
		m.logLines = append(m.logLines, logLine{
			kind: logOutput,
			text: "  ! terminal handoff — input stays outside sys-bozo",
		})
		m.logVP.SetContent(m.renderLog())
		m.logVP.GotoBottom()
		execInteractive := m.terminalExec
		if execInteractive == nil {
			execInteractive = runInteractiveWork
		}
		return execInteractive(item, m.stepStart)
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
	m.stepResults[index] = stepResult{Item: item, Status: status, Duration: duration, Err: err}
}

func (m *Model) finishRun(err error, cancelled bool, elapsed time.Duration) {
	status := runStatus(err, cancelled)
	m.mode = modeDone
	m.screen = screenResult
	m.runErr = err
	m.runCancelled = cancelled
	m.runElapsed = elapsed
	m.logVP.SetContent(m.renderLog())
	m.logVP.GotoBottom()
	history.Append(history.Entry{
		Ts:     time.Now(),
		Action: m.runAction,
		Secs:   elapsed.Seconds(),
		OK:     status == history.StatusSuccess,
		Status: status,
	})
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
