package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/history"
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
	if len(m.reviewed.Items) == 0 {
		return nil
	}
	m.queue = cloneWorkItems(m.reviewed.Items)
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

func (m Model) openEditor(path string) tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorDoneMsg{path: path, err: err}
	})
}

func runInteractiveWork(item runner.WorkItem, start time.Time) tea.Cmd {
	return tea.ExecProcess(runner.Command(item), func(err error) tea.Msg {
		return stepDoneMsg{err: err, elapsed: time.Since(start)}
	})
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
		m.mode = modeDone
		m.screen = screenResult
		elapsed := time.Since(m.runStart).Round(time.Second)
		m.logLines = append(m.logLines, logLine{kind: logHeader, text: ""})
		m.logLines = append(m.logLines, logLine{kind: logSuccess,
			text: fmt.Sprintf("  ✓ all done · %s", elapsed)})
		m.logVP.SetContent(m.renderLog())
		m.logVP.GotoBottom()
		history.Append(history.Entry{
			Ts:     time.Now(),
			Action: m.runAction,
			Secs:   elapsed.Seconds(),
			OK:     true,
		})
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
		m.logLines = append(m.logLines, logLine{kind: logError,
			text: "  ✗ " + err.Error()})
		m.mode = modeDone
		m.screen = screenResult
		m.logVP.SetContent(m.renderLog())
		m.logVP.GotoBottom()
		return nil
	}

	m.activeScanner = scanner
	m.activeWait = wait
	m.logVP.SetContent(m.renderLog())
	m.logVP.GotoBottom()

	return m.readNextLine()
}

func (m Model) readNextLine() tea.Cmd {
	scanner := m.activeScanner
	wait := m.activeWait
	start := m.stepStart

	return func() tea.Msg {
		if scanner.Scan() {
			return lineMsg{text: scanner.Text()}
		}
		err := wait()
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
