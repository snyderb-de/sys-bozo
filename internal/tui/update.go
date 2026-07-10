package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

// ── Init ──────────────────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	return nil
}

// ── Update ────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.logVP = viewport.New(m.logWidth(), m.logHeight())
		m.logVP.SetContent(m.renderLog())
		if m.logFollow {
			m.logVP.GotoBottom()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case lineMsg:
		line := classifyLine(msg.text)
		m.logLines = append(m.logLines, line)
		m.logVP.SetContent(m.renderLog())
		if m.logFollow {
			m.logVP.GotoBottom()
		}
		return m, m.readNextLine()

	case stepDoneMsg:
		if msg.err != nil {
			m.logLines = append(m.logLines, logLine{kind: logError,
				text: fmt.Sprintf("  ✗ failed: %s", msg.err)})
			m.mode = modeDone
			m.screen = screenResult
			m.logVP.SetContent(m.renderLog())
			m.logVP.GotoBottom()
			history.Append(history.Entry{
				Ts:     time.Now(),
				Action: m.runAction,
				Secs:   time.Since(m.runStart).Seconds(),
				OK:     false,
			})
			return m, nil
		}
		m.logLines = append(m.logLines, logLine{kind: logSuccess,
			text: fmt.Sprintf("  ✓ done in %s", msg.elapsed.Round(time.Second))})
		m.logVP.SetContent(m.renderLog())
		m.logVP.GotoBottom()
		m.queuePos++
		return m, m.advanceQueue()

	case auditReadyMsg:
		m.auditItems = msg.items
		m.auditReady = true
		return m, nil

	case editorDoneMsg:
		if msg.err == nil {
			m.applyPrompt = true
			m.applyEditPath = msg.path
		}
		return m, nil

	case sudoReadyMsg:
		// unused: kept for runSudoPreflight compatibility
		_ = msg
		return m, nil

	case spinner.TickMsg:
		if m.mode == modeRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	if m.mode == modeRunning || m.mode == modeDone {
		var cmd tea.Cmd
		m.logVP, cmd = m.logVP.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Apply prompt intercepts all keys
	if m.applyPrompt {
		switch strings.ToLower(msg.String()) {
		case "h":
			m.applyPrompt = false
			m.openMaintenance("hms")
			m.reviewSelection()
		case "n":
			m.applyPrompt = false
			m.openMaintenance("nds")
			m.reviewSelection()
		case "b":
			m.applyPrompt = false
			m.openMaintenance("hms", "nds")
			m.reviewSelection()
		default:
			m.applyPrompt = false
		}
		return m, nil
	}

	if m.screen == screenHome {
		switch strings.ToLower(msg.String()) {
		case "h":
			m.openMaintenance("hms")
			return m, nil
		case "n":
			m.openMaintenance("nds")
			return m, nil
		}
	}
	if m.screen == screenReview || m.screen == screenRunning {
		switch msg.String() {
		case "tab", "right", "shift+tab", "left", "1", "2", "3", "4", "5":
			return m, nil
		}
	}
	if msg.String() == "enter" || msg.String() == " " {
		switch m.screen {
		case screenMaintenance:
			if !m.hasAvailableSelection() {
				available := m.availableTasks()
				if m.cursor >= 0 && m.cursor < len(available) {
					m.selected = map[string]bool{available[m.cursor].ID: true}
				}
			}
			m.reviewSelection()
			return m, nil
		case screenReview:
			return m, m.confirmReviewedPlan()
		}
	}

	switch msg.String() {
	case "ctrl+c", "q":
		if m.mode == modeDone {
			m.mode = modeView
			m.logLines = nil
			m.queue = nil
			m.queuePos = 0
			m.selected = map[string]bool{}
			m.reviewed = reviewedPlan{}
			m.syncScreenToTab()
			return m, nil
		}
		return m, tea.Quit

	case "Q":
		return m, tea.Quit

	case "tab", "right":
		m.nextTab()
		if cmd := m.auditCmdIfNeeded(); cmd != nil {
			return m, cmd
		}
	case "shift+tab", "left":
		m.prevTab()
	case "1", "2", "3", "4", "5":
		m.tab = int(msg.String()[0] - '1')
		m.cursor = 0
		m.syncScreenToTab()
		if cmd := m.auditCmdIfNeeded(); cmd != nil {
			return m, cmd
		}

	case "j", "down":
		if m.mode == modeView {
			m.moveCursor(1)
		} else {
			m.logFollow = false
			var cmd tea.Cmd
			m.logVP, cmd = m.logVP.Update(msg)
			return m, cmd
		}
	case "k", "up":
		if m.mode == modeView {
			m.moveCursor(-1)
		} else {
			m.logFollow = false
			var cmd tea.Cmd
			m.logVP, cmd = m.logVP.Update(msg)
			return m, cmd
		}
	case "f":
		m.logFollow = !m.logFollow
		if m.logFollow {
			m.logVP.GotoBottom()
		}

	case "enter", " ":
		switch m.tabs[m.tab] {
		case "Actions":
			if m.mode == modeView {
				avail := m.availableTasks()
				if m.cursor < len(avail) {
					m.openMaintenance(avail[m.cursor].ID)
					m.reviewSelection()
				}
				return m, nil
			}
		case "Config":
			if m.mode == modeView && m.configCursor < len(m.configFiles) {
				return m, m.openEditor(m.configFiles[m.configCursor].path)
			}
		}

	case "r":
		if m.mode == modeView {
			m.facts = system.Probe()
			m.runCtx = runner.Build()
			m.tasks = runner.DefaultTasks(m.runCtx)
			m.configFiles = buildConfigFiles(m.runCtx)
			m.auditReady = false
			m.auditItems = nil
			if m.tabs[m.tab] == "Audit" {
				return m, m.runAudit()
			}
		}

	case "a":
		if m.tabs[m.tab] == "Audit" {
			m.auditReady = false
			m.auditItems = nil
			return m, m.runAudit()
		}
	}

	return m, nil
}

func (m Model) auditCmdIfNeeded() tea.Cmd {
	if m.tabs[m.tab] == "Audit" && !m.auditReady {
		return m.runAudit()
	}
	return nil
}

// ── Navigation ────────────────────────────────────────────────────────────

func (m *Model) nextTab() {
	m.tab = (m.tab + 1) % len(m.tabs)
	m.cursor = 0
	m.syncScreenToTab()
}

func (m *Model) prevTab() {
	m.tab--
	if m.tab < 0 {
		m.tab = len(m.tabs) - 1
	}
	m.cursor = 0
	m.syncScreenToTab()
}

func (m *Model) syncScreenToTab() {
	if m.tab < 0 || m.tab >= len(m.tabs) {
		m.screen = screenHome
		return
	}
	switch m.tabs[m.tab] {
	case "Actions":
		m.screen = screenMaintenance
	case "Config":
		m.screen = screenConfig
	case "Audit":
		m.screen = screenAudit
	case "Doctor":
		m.screen = screenDoctor
	default:
		m.screen = screenHome
	}
}

func (m *Model) moveCursor(delta int) {
	switch m.tabs[m.tab] {
	case "Actions":
		n := len(m.availableTasks())
		if n == 0 {
			return
		}
		m.cursor = (m.cursor + delta + n) % n
	case "Config":
		n := len(m.configFiles)
		if n == 0 {
			return
		}
		m.configCursor = (m.configCursor + delta + n) % n
	}
}
