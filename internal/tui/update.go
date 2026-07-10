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

	case packageSearchMsg:
		m.acceptPackageSearch(msg.result)
		return m, nil

	case packageAppliedMsg:
		if msg.err != nil {
			m.finishRun(msg.err, false, time.Since(m.runStart))
			return m, nil
		}
		if m.reviewed.Package == nil {
			err := fmt.Errorf("package apply completed without reviewed package")
			m.finishRun(err, false, time.Since(m.runStart))
			return m, nil
		}
		m.reviewed.Package = clonePackageReview(m.reviewed.Package)
		edit := cloneAppliedEdit(msg.edit)
		m.reviewed.Package.Applied = &edit
		return m, m.advanceQueue()

	case packageVerifiedMsg:
		if m.reviewed.Package == nil {
			err := fmt.Errorf("package verification completed without reviewed package")
			m.finishRun(err, false, time.Since(m.runStart))
			return m, nil
		}
		m.reviewed.Package = clonePackageReview(m.reviewed.Package)
		result := msg.result
		m.reviewed.Package.Result = &result
		if !result.OK {
			err := result.Err
			if err == nil {
				err = fmt.Errorf("package verification failed: %s", result.Detail)
			}
			m.finishRun(err, false, time.Since(m.runStart))
			return m, nil
		}
		m.finishRun(nil, false, time.Since(m.runStart))
		return m, nil

	case packageEditorDoneMsg:
		if msg.err != nil {
			m.packageFlow.err = fmt.Errorf("package editor handoff: %w", msg.err)
			return m, nil
		}
		if string(msg.original) == string(msg.proposed) {
			m.packageFlow.err = fmt.Errorf("package editor made no changes")
			return m, nil
		}
		proposal := packageReplacementProposal(msg.target, msg.original, msg.proposed)
		m.packageFlow.proposal = clonePackageProposal(proposal)
		m.buildPackageReview(proposal, packageVerifySpec(msg.candidate, m.runCtx.BrewBin))
		return m, nil

	case lineMsg:
		line := classifyLine(msg.text)
		m.logLines = append(m.logLines, line)
		m.logVP.SetContent(m.renderLog())
		if m.logFollow {
			m.logVP.GotoBottom()
		}
		return m, m.readNextLine()

	case stepDoneMsg:
		status := runStatus(msg.err, msg.cancelled)
		m.recordStepResult(m.queuePos, status, msg.elapsed, msg.err)
		if msg.err != nil {
			elapsed := time.Since(m.runStart)
			verb := "failed"
			if status == history.StatusCancelled {
				verb = "cancelled"
			}
			m.logLines = append(m.logLines, logLine{kind: logError,
				text: fmt.Sprintf("  ✗ %s: %s", verb, msg.err)})
			m.finishRun(msg.err, msg.cancelled, elapsed)
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
		case "n":
			m.applyPrompt = false
			m.openMaintenance("nds")
		case "b":
			m.applyPrompt = false
			m.openMaintenance("hms", "nds")
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
		case "j", "down":
			m.moveHomeCursor(1)
			return m, nil
		case "k", "up":
			m.moveHomeCursor(-1)
			return m, nil
		case "1":
			m.openHomeEntry(0)
			return m, nil
		case "2":
			m.openHomeEntry(1)
			return m, nil
		case "3":
			m.openHomeEntry(2)
			return m, nil
		case "enter":
			m.openHomeEntry(m.homeCursor)
			return m, nil
		}
	}
	if m.screen == screenPackage {
		return m.handlePackageKey(msg)
	}
	if m.screen == screenInspect {
		switch msg.String() {
		case "j", "down":
			m.inspectCursor = (m.inspectCursor + 1) % len(inspectEntries)
			return m, nil
		case "k", "up":
			m.inspectCursor = (m.inspectCursor - 1 + len(inspectEntries)) % len(inspectEntries)
			return m, nil
		case "enter":
			return m, m.openInspectEntry(m.inspectCursor)
		case "1", "2", "3", "4":
			return m, m.openInspectEntry(int(msg.String()[0] - '1'))
		case "esc":
			m.screen = screenHome
			return m, nil
		}
	}
	if m.screen == screenConfig || m.screen == screenAudit || m.screen == screenDoctor || m.screen == screenHistory {
		if m.screen == screenConfig {
			switch msg.String() {
			case "j", "down":
				m.moveConfigCursor(1)
				return m, nil
			case "k", "up":
				m.moveConfigCursor(-1)
				return m, nil
			case "enter", " ":
				if m.configCursor >= 0 && m.configCursor < len(m.configFiles) {
					return m, m.openEditor(m.configFiles[m.configCursor].path)
				}
				return m, nil
			}
		}
		if m.screen == screenAudit {
			switch msg.String() {
			case "a", "r":
				m.auditReady = false
				m.auditItems = nil
				return m, m.runAudit()
			}
		}
		switch msg.String() {
		case "esc":
			m.screen = screenInspect
			return m, nil
		}
	}
	if m.screen == screenResult {
		switch strings.ToLower(msg.String()) {
		case "l":
			m.resultLogVisible = !m.resultLogVisible
			return m, nil
		case "r":
			if _, ok := m.retryStart(); ok {
				m.prepareResultRetry()
			}
			return m, nil
		case "v":
			m.reviewPackageRevert()
			return m, nil
		case "esc":
			m.closeResult()
			return m, nil
		}
	}
	if consumesLegacyRoute(m.screen, msg.String()) {
		return m, nil
	}
	switch m.screen {
	case screenMaintenance:
		switch msg.String() {
		case " ":
			m.toggleMaintenanceSelection()
			return m, nil
		case "enter":
			if !m.hasAvailableSelection() {
				available := m.availableTasks()
				if m.cursor >= 0 && m.cursor < len(available) {
					m.selected = map[string]bool{available[m.cursor].ID: true}
				}
			}
			m.reviewSelection()
			return m, nil
		case "esc":
			m.screen = screenHome
			m.reviewed = reviewedPlan{}
			return m, nil
		}
	case screenReview:
		switch msg.String() {
		case "enter":
			return m, m.confirmReviewedPlan()
		case " ":
			return m, nil
		case "esc":
			if m.reviewed.Package != nil {
				m.screen = screenPackage
				m.packageFlow.stage = packagePlacement
			} else {
				m.screen = screenMaintenance
			}
			m.reviewed = reviewedPlan{}
			return m, nil
		}
	}

	switch msg.String() {
	case "ctrl+c", "q":
		if m.mode == modeDone {
			m.closeResult()
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

func consumesLegacyRoute(active screen, key string) bool {
	switch key {
	case "tab", "shift+tab", "right", "left":
		switch active {
		case screenHome, screenMaintenance, screenReview, screenRunning, screenResult,
			screenInspect, screenConfig, screenAudit, screenDoctor, screenHistory, screenPackage:
			return true
		}
	case "1", "2", "3", "4", "5":
		switch active {
		case screenHome:
			return key == "4" || key == "5"
		case screenInspect:
			return key == "5"
		case screenMaintenance, screenReview, screenRunning, screenResult,
			screenConfig, screenAudit, screenDoctor, screenHistory, screenPackage:
			return true
		}
	}
	return false
}

func (m Model) auditCmdIfNeeded() tea.Cmd {
	if m.tabs[m.tab] == "Audit" && !m.auditReady {
		return m.runAudit()
	}
	return nil
}

// ── Navigation ────────────────────────────────────────────────────────────

func (m *Model) moveHomeCursor(delta int) {
	if len(homeEntries) == 0 {
		return
	}
	for range homeEntries {
		m.homeCursor = (m.homeCursor + delta + len(homeEntries)) % len(homeEntries)
		if !homeEntryLocked(m.homeCursor) {
			return
		}
	}
}

func (m *Model) toggleMaintenanceSelection() {
	available := m.availableTasks()
	if m.cursor < 0 || m.cursor >= len(available) {
		return
	}
	if m.selected == nil {
		m.selected = map[string]bool{}
	}
	id := available[m.cursor].ID
	m.selected[id] = !m.selected[id]
}

func (m *Model) openHomeEntry(index int) {
	if index < 0 || index >= len(homeEntries) || homeEntryLocked(index) {
		return
	}
	m.homeCursor = index
	switch homeEntries[index].target {
	case screenMaintenance:
		m.openMaintenance()
	case screenPackage:
		m.openPackageFlow()
	case screenInspect:
		for i, tab := range m.tabs {
			if tab == "Config" {
				m.tab = i
				break
			}
		}
		m.screen = screenInspect
	}
}

func homeEntryLocked(index int) bool {
	return index < 0 || index >= len(homeEntries)
}

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

func (m *Model) moveConfigCursor(delta int) {
	if len(m.configFiles) == 0 {
		return
	}
	m.configCursor = (m.configCursor + delta + len(m.configFiles)) % len(m.configFiles)
}

func (m *Model) openInspectEntry(index int) tea.Cmd {
	if index < 0 || index >= len(inspectEntries) {
		return nil
	}
	m.inspectCursor = index
	m.screen = inspectEntries[index].target
	if m.screen == screenAudit && !m.auditReady {
		return m.runAudit()
	}
	return nil
}

func (m *Model) prepareResultRetry() {
	start, ok := m.retryStart()
	if !ok {
		return
	}
	action := m.reviewed.Action
	packagePlan := clonePackageReview(m.reviewed.Package)
	if packagePlan != nil {
		packagePlan.Result = nil
		packagePlan.verificationStarted = false
	}
	retryItems := cloneWorkItems(m.queue[start:])
	m.mode = modeView
	m.screen = screenReview
	m.reviewed = reviewedPlan{Action: action, Items: retryItems, Package: packagePlan}
	m.queue = nil
	m.queuePos = 0
	m.runErr = nil
	m.runCancelled = false
	m.runElapsed = 0
	m.stepResults = nil
	m.resultLogVisible = false
	m.revertErr = nil
	m.logLines = nil
}

func (m Model) retryStart() (int, bool) {
	limit := min(len(m.stepResults), len(m.queue))
	for i := 0; i < limit; i++ {
		status := m.stepResults[i].Status
		if status != history.StatusFailure && status != history.StatusCancelled {
			continue
		}
		return i, m.queue[i].Retryable
	}
	return 0, false
}

func (m *Model) closeResult() {
	packageResult := m.reviewed.Package != nil
	m.mode = modeView
	m.logLines = nil
	m.queue = nil
	m.queuePos = 0
	m.runErr = nil
	m.runCancelled = false
	m.runElapsed = 0
	m.stepResults = nil
	m.resultLogVisible = false
	m.selected = map[string]bool{}
	m.reviewed = reviewedPlan{}
	if packageResult {
		m.screen = screenHome
		m.packageFlow = packageFlow{}
		return
	}
	m.syncScreenToTab()
}
