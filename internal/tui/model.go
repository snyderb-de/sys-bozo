package tui

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

// ── Palette (Tokyo Night Storm) ───────────────────────────────────────────

var (
	clrBg     = lipgloss.Color("#1e2030")
	clrPanel  = lipgloss.Color("#222436")
	clrBorder = lipgloss.Color("#2d3f6a")
	clrText   = lipgloss.Color("#c8d3f5")
	clrMuted  = lipgloss.Color("#636da6")
	clrFaint  = lipgloss.Color("#444a73")
	clrGold   = lipgloss.Color("#ffc777")
	clrCyan   = lipgloss.Color("#86e1fc")
	clrBlue   = lipgloss.Color("#82aaff")
	clrGreen  = lipgloss.Color("#c3e88d")
	clrRed    = lipgloss.Color("#ff757f")
	clrOrange = lipgloss.Color("#ff966c")
	clrPurple = lipgloss.Color("#c099ff")
)

// ── Styles ────────────────────────────────────────────────────────────────

var (
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleTitle  = lipgloss.NewStyle().Foreground(clrGold).Bold(true)
	styleMuted  = lipgloss.NewStyle().Foreground(clrMuted)
	styleFaint  = lipgloss.NewStyle().Foreground(clrFaint)
	styleGood   = lipgloss.NewStyle().Foreground(clrGreen).Bold(true)
	styleWarn   = lipgloss.NewStyle().Foreground(clrOrange).Bold(true)
	styleErr    = lipgloss.NewStyle().Foreground(clrRed).Bold(true)
	styleCmd    = lipgloss.NewStyle().Foreground(clrMuted)
	styleAccent = lipgloss.NewStyle().Foreground(clrCyan)
	stylePurple = lipgloss.NewStyle().Foreground(clrPurple)

	styleCard = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(clrBorder).
			Background(clrPanel).
			Padding(1, 2)

	styleActiveTab = lipgloss.NewStyle().
			Foreground(clrBg).
			Background(clrBlue).
			Bold(true).
			Padding(0, 2)

	styleTab = lipgloss.NewStyle().
			Foreground(clrMuted).
			Padding(0, 2)

	styleLogPane = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(clrBorder).
			Background(clrPanel)

	styleShell = lipgloss.NewStyle().
			Background(clrBg).
			Foreground(clrText).
			Padding(1, 2)
)

// ── Log line types ────────────────────────────────────────────────────────

type logKind int

const (
	logHeader  logKind = iota // task section header
	logCmd                    // $ command
	logOutput                 // normal stdout/stderr
	logSuccess                // line containing success indicators
	logError                  // line containing error indicators
)

type logLine struct {
	kind logKind
	text string
}

// ── Run state ─────────────────────────────────────────────────────────────

type appMode int

const (
	modeView appMode = iota
	modeRunning
	modeDone
)

// ── Tea messages ─────────────────────────────────────────────────────────

type lineMsg struct{ text string }
type stepDoneMsg struct {
	err     error
	elapsed time.Duration
}
type auditReadyMsg struct{ items []system.AuditItem }
type sudoReadyMsg struct{ err error }

// ── Model ─────────────────────────────────────────────────────────────────

type Model struct {
	facts  system.Facts
	runCtx runner.Context
	tasks  []runner.Task

	tabs   []string
	tab    int
	cursor int
	width  int
	height int

	mode      appMode
	queue     []runner.WorkItem
	queuePos  int
	runStart  time.Time
	stepStart time.Time

	logLines  []logLine
	logVP     viewport.Model
	logFollow bool
	spinner   spinner.Model

	activeScanner *bufio.Scanner
	activeWait    func() error

	auditItems []system.AuditItem
	auditReady bool
}

func New() Model {
	ctx := runner.Build()
	tasks := runner.DefaultTasks(ctx)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(clrCyan)

	return Model{
		facts:     system.Probe(),
		runCtx:    ctx,
		tasks:     tasks,
		tabs:      []string{"Dashboard", "Updates", "Audit", "Doctor"},
		logFollow: true,
		spinner:   sp,
	}
}

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
			m.logVP.SetContent(m.renderLog())
			m.logVP.GotoBottom()
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

	case sudoReadyMsg:
		if msg.err != nil {
			m.logLines = append(m.logLines, logLine{kind: logError,
				text: fmt.Sprintf("  ✗ sudo preflight failed: %s", msg.err)})
			m.mode = modeDone
			m.logVP.SetContent(m.renderLog())
			m.logVP.GotoBottom()
			return m, nil
		}
		m.logLines = append(m.logLines, logLine{kind: logSuccess, text: "  ✓ sudo ready"})
		m.logVP.SetContent(m.renderLog())
		m.logVP.GotoBottom()
		return m, tea.Batch(m.advanceQueue(), m.spinner.Tick)

	case spinner.TickMsg:
		if m.mode == modeRunning {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil
	}

	// Forward scroll events to viewport when log is visible
	if m.mode == modeRunning || m.mode == modeDone {
		var cmd tea.Cmd
		m.logVP, cmd = m.logVP.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.mode == modeDone {
			m.mode = modeView
			m.logLines = nil
			m.queue = nil
			m.queuePos = 0
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
		if cmd := m.auditCmdIfNeeded(); cmd != nil {
			return m, cmd
		}
	case "1", "2", "3", "4":
		m.tab = int(msg.String()[0] - '1')
		m.cursor = 0
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

	case " ":
		if m.tabs[m.tab] == "Updates" && m.mode == modeView {
			if m.cursor < len(m.tasks) {
				m.tasks[m.cursor].Selected = !m.tasks[m.cursor].Selected
			}
		}

	case "r":
		if m.tabs[m.tab] == "Updates" && m.mode == modeView {
			return m, m.startRun()
		}
		if m.mode == modeView {
			m.facts = system.Probe()
			m.runCtx = runner.Build()
			m.tasks = runner.DefaultTasks(m.runCtx)
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

// ── Run logic ─────────────────────────────────────────────────────────────

func (m *Model) startRun() tea.Cmd {
	m.queue = runner.BuildQueue(m.tasks, m.runCtx)
	if len(m.queue) == 0 {
		return nil
	}
	m.queuePos = 0
	m.mode = modeRunning
	m.logLines = nil
	m.logFollow = true
	m.runStart = time.Now()

	m.logVP = viewport.New(m.logWidth(), m.logHeight())

	if sudoBin := sudoCommand(m.queue); sudoBin != "" {
		m.logLines = append(m.logLines, logLine{kind: logHeader, text: "  ● sudo preflight"})
		m.logLines = append(m.logLines, logLine{kind: logCmd, text: "    $ " + sudoBin + " -v"})
		m.logVP.SetContent(m.renderLog())
		m.logVP.GotoBottom()
		return tea.Batch(m.runSudoPreflight(sudoBin), m.spinner.Tick)
	}

	return tea.Batch(m.advanceQueue(), m.spinner.Tick)
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
		elapsed := time.Since(m.runStart).Round(time.Second)
		m.logLines = append(m.logLines, logLine{kind: logHeader, text: ""})
		m.logLines = append(m.logLines, logLine{kind: logSuccess,
			text: fmt.Sprintf("  ✓ all done · %s", elapsed)})
		m.logVP.SetContent(m.renderLog())
		m.logVP.GotoBottom()
		return nil
	}

	item := m.queue[m.queuePos]

	// Task header when first step of a new task
	if item.TaskFirst {
		total := countTasks(m.queue)
		taskNum := countCompletedTasks(m.queue, m.queuePos) + 1
		m.logLines = append(m.logLines, logLine{kind: logHeader,
			text: fmt.Sprintf("  ● [%d/%d] %s", taskNum, total, item.TaskLabel)})
	}

	// Command line
	m.logLines = append(m.logLines, logLine{kind: logCmd,
		text: "    $ " + runner.CmdLabel(item)})

	m.stepStart = time.Now()
	scanner, wait, err := runner.StartWork(item)
	if err != nil {
		m.logLines = append(m.logLines, logLine{kind: logError,
			text: "  ✗ " + err.Error()})
		m.mode = modeDone
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

// ── View ──────────────────────────────────────────────────────────────────

func (m Model) View() string {
	w := m.width
	if w <= 0 {
		w = 120
	}
	inner := clamp(w-4, 80, 140)

	parts := []string{
		m.viewHeader(inner),
		m.viewTabs(),
		m.viewBody(inner),
		m.viewFooter(inner),
	}

	return styleShell.Width(inner).Render(strings.Join(parts, "\n\n"))
}

func (m Model) viewHeader(w int) string {
	left := styleTitle.Render("⊛ sys-bozo")
	branch := m.facts.DotfilesBranch
	if branch == "" {
		branch = "?"
	}
	if m.facts.DotfilesDirty > 0 {
		branch += styleWarn.Render("*")
	}
	right := styleMuted.Render(
		m.facts.User + "@" + m.facts.Hostname +
			"  ·  " + m.facts.OS + "/" + m.facts.Arch +
			"  ·  " + branch,
	)
	pad := max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", pad) + right
}

func (m Model) viewTabs() string {
	var parts []string
	for i, name := range m.tabs {
		label := fmt.Sprintf("%d·%s", i+1, name)
		if i == m.tab {
			parts = append(parts, styleActiveTab.Render(label))
		} else {
			parts = append(parts, styleTab.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (m Model) viewBody(w int) string {
	cw := innerWidth(w)

	switch m.tabs[m.tab] {
	case "Dashboard":
		return m.viewDashboard(cw)
	case "Updates":
		return m.viewUpdates(cw)
	case "Audit":
		return m.viewAudit(cw)
	case "Doctor":
		return m.viewDoctor(cw)
	}
	return ""
}

// ── Dashboard ─────────────────────────────────────────────────────────────

func (m Model) viewDashboard(w int) string {
	colW := max(26, (w-6)/3)

	host := styleCard.Width(colW).Render(strings.Join([]string{
		styleTitle.Render("Host"),
		row("Name ", m.facts.Hostname),
		row("User ", m.facts.User),
		row("OS   ", osLabel(m.facts)),
		row("Shell", shortPath(m.facts.Shell)),
	}, "\n"))

	dirtyStr := styleGood.Render("clean")
	if m.facts.DotfilesDirty > 0 {
		dirtyStr = styleWarn.Render(fmt.Sprintf("%d files", m.facts.DotfilesDirty))
	}
	branch := m.facts.DotfilesBranch
	if branch == "" {
		branch = styleMuted.Render("unknown")
	}
	hmGen := m.facts.HMGeneration
	if hmGen == "" || hmGen == "none" {
		hmGen = styleMuted.Render("none")
	}
	state := styleCard.Width(colW).Render(strings.Join([]string{
		styleTitle.Render("State"),
		rowStyled("Branch", branch),
		rowStyled("Dirty ", dirtyStr),
		rowStyled("HM    ", hmGen),
		row("Repo  ", shortPath(m.facts.DotfilesRepo)),
	}, "\n"))

	var mgrs []string
	mgrs = append(mgrs, styleTitle.Render("Managers"))
	mgrs = append(mgrs, managerLine("nix", m.facts.NixPath))
	if m.facts.OS == "linux" && m.facts.OSID == "fedora" {
		mgrs = append(mgrs, managerLine("dnf", m.facts.DnfPath))
		mgrs = append(mgrs, managerLine("sudo", m.facts.SudoPath))
	}
	if m.facts.OS == "darwin" {
		mgrs = append(mgrs, managerLine("brew", m.facts.BrewPath))
		mgrs = append(mgrs, managerLine("nix-darwin", m.facts.DarwinRebuild))
	}
	mgrs = append(mgrs, managerLine("home-manager", m.facts.HomeManager))
	mgrs = append(mgrs, managerLine("topgrade", m.facts.Topgrade))
	if m.facts.TailscaleIP != "" {
		mgrs = append(mgrs, row("tailscale", m.facts.TailscaleIP))
	}
	managers := styleCard.Width(colW).Render(strings.Join(mgrs, "\n"))

	if w < 100 {
		return lipgloss.JoinVertical(lipgloss.Left, host, "", state, "", managers)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, host, "  ", state, "  ", managers)
}

// ── Updates ───────────────────────────────────────────────────────────────

func (m Model) viewUpdates(w int) string {
	cw := innerWidth(w)

	var rows []string
	rows = append(rows, styleTitle.Render("Updates")+"  "+styleFaint.Render("space toggle · r run · j/k move"))
	rows = append(rows, "")

	for i, t := range m.tasks {
		avail := t.Available(m.runCtx)
		cur := " "
		if i == m.cursor && m.mode == modeView {
			cur = styleAccent.Render("▶")
		}
		var check string
		if !avail {
			check = styleFaint.Render("[-]")
		} else if t.Selected {
			check = styleGood.Render("[✓]")
		} else {
			check = styleMuted.Render("[ ]")
		}
		label := t.Label
		if !avail {
			label = styleFaint.Render(label)
		} else if m.mode == modeRunning || m.mode == modeDone {
			label = styleMuted.Render(label)
		}
		desc := t.Desc
		if !avail {
			desc = styleFaint.Render("unavailable")
		} else {
			desc = styleFaint.Render(desc)
		}
		rows = append(rows, fmt.Sprintf("%s %s %-30s %s", cur, check, label, desc))
	}

	taskList := styleCard.Width(cw).Render(strings.Join(rows, "\n"))

	if m.mode == modeView {
		return taskList
	}

	// Running or done — show log pane below
	totalTasks := countTasks(m.queue)
	doneTasks := countCompletedTasks(m.queue, m.queuePos)
	var logTitle string
	if m.mode == modeRunning {
		logTitle = m.spinner.View() + " " + styleAccent.Render(fmt.Sprintf("running %d/%d", doneTasks, totalTasks)) +
			"  " + styleFaint.Render(time.Since(m.runStart).Round(time.Second).String())
	} else {
		logTitle = styleGood.Render("✓ complete") + "  " +
			styleFaint.Render(time.Since(m.runStart).Round(time.Second).String()) +
			"  " + styleFaint.Render("q close")
	}

	followIndicator := styleFaint.Render("follow")
	if !m.logFollow {
		followIndicator = styleWarn.Render("scroll")
	}

	logHeader := lipgloss.JoinHorizontal(lipgloss.Top,
		"  "+logTitle,
		strings.Repeat(" ", max(1, cw-lipgloss.Width(logTitle)-lipgloss.Width(followIndicator)-4)),
		followIndicator+"  ",
	)

	logContent := styleLogPane.Width(cw).Render(logHeader + "\n" + m.logVP.View())

	return lipgloss.JoinVertical(lipgloss.Left, taskList, "", logContent)
}

// ── Audit ─────────────────────────────────────────────────────────────────

func (m Model) viewAudit(w int) string {
	cw := innerWidth(w)
	header := styleTitle.Render("Local Audit") + "  " + styleFaint.Render("a rescan")

	if !m.auditReady {
		content := strings.Join([]string{
			header,
			"",
			styleMuted.Render("  scanning..."),
		}, "\n")
		return styleCard.Width(cw).Render(content)
	}

	var rows []string
	rows = append(rows, header)
	rows = append(rows, "")

	rows = append(rows, stylePurple.Render("  Config files"))
	configCount := 0
	for _, item := range m.auditItems {
		if configCount == 6 {
			rows = append(rows, "")
			rows = append(rows, stylePurple.Render("  Tools"))
		}
		icon := styleGood.Render("  ✓")
		detail := styleFaint.Render(item.Detail)
		if !item.OK {
			icon = styleErr.Render("  ✗")
			detail = styleWarn.Render(item.Detail)
		}
		rows = append(rows, fmt.Sprintf("%s  %-18s %s", icon, item.Name, detail))
		if !item.OK {
			rows = append(rows, auditHelpRows("why", item.Description, cw-8, styleFaint)...)
			rows = append(rows, auditHelpRows("fix", item.Fix, cw-8, styleMuted)...)
		}
		configCount++
	}

	ok, fail := 0, 0
	for _, item := range m.auditItems {
		if item.OK {
			ok++
		} else {
			fail++
		}
	}
	rows = append(rows, "")
	summary := styleGood.Render(fmt.Sprintf("  %d ok", ok))
	if fail > 0 {
		summary += "  " + styleErr.Render(fmt.Sprintf("%d issues", fail))
	}
	rows = append(rows, summary)

	return styleCard.Width(cw).Render(strings.Join(rows, "\n"))
}

func auditHelpRows(label, text string, width int, style lipgloss.Style) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	prefix := "     " + label + ": "
	continuation := strings.Repeat(" ", lipgloss.Width(prefix))
	lineWidth := max(24, width-lipgloss.Width(prefix))
	wrapped := wrapWords(text, lineWidth)

	rows := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		if i == 0 {
			rows = append(rows, style.Render(prefix+line))
		} else {
			rows = append(rows, style.Render(continuation+line))
		}
	}
	return rows
}

func wrapWords(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if lipgloss.Width(line)+1+lipgloss.Width(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		line += " " + word
	}
	lines = append(lines, line)
	return lines
}

// ── Doctor ────────────────────────────────────────────────────────────────

func (m Model) viewDoctor(w int) string {
	cw := innerWidth(w)
	f := m.facts

	var rows []string
	rows = append(rows, styleTitle.Render("Doctor"))
	rows = append(rows, "")

	// Dotfiles state
	rows = append(rows, stylePurple.Render("  Dotfiles"))
	branch := f.DotfilesBranch
	if branch == "" {
		branch = styleMuted.Render("unknown")
	}
	rows = append(rows, rowStyled("    branch    ", branch))
	if f.DotfilesDirty == 0 {
		rows = append(rows, rowStyled("    dirty     ", styleGood.Render("clean")))
	} else {
		rows = append(rows, rowStyled("    dirty     ", styleWarn.Render(fmt.Sprintf("%d files", f.DotfilesDirty))))
	}
	rows = append(rows, "")

	// Home Manager
	rows = append(rows, stylePurple.Render("  Home Manager"))
	gen := f.HMGeneration
	if gen == "" || gen == "none" {
		rows = append(rows, rowStyled("    generation", styleErr.Render("none — run hms")))
	} else {
		rows = append(rows, rowStyled("    generation", styleGood.Render(gen)))
	}
	rows = append(rows, "")

	// Secrets
	rows = append(rows, stylePurple.Render("  Secrets"))
	if f.AgeKeyExists {
		rows = append(rows, rowStyled("    age key   ", styleGood.Render("present")))
	} else {
		rows = append(rows, rowStyled("    age key   ", styleErr.Render("missing · ~/.config/sops/age/keys.txt")))
	}
	rows = append(rows, "")

	// SSH keys
	rows = append(rows, stylePurple.Render("  SSH"))
	if f.GitHubKeyExists {
		rows = append(rows, rowStyled("    github key", styleGood.Render("present")))
	} else {
		rows = append(rows, rowStyled("    github key", styleErr.Render("missing")))
	}
	rows = append(rows, "")

	// Managers
	rows = append(rows, stylePurple.Render("  Package Managers"))
	for _, check := range []struct{ name, path string }{
		{"nix         ", f.NixPath},
		{"home-manager", f.HomeManager},
		{"topgrade    ", f.Topgrade},
	} {
		if check.path == "" {
			rows = append(rows, rowStyled("    "+check.name, styleErr.Render("missing")))
		} else {
			rows = append(rows, rowStyled("    "+check.name, styleGood.Render(shortPath(check.path))))
		}
	}
	if f.OS == "linux" && f.OSID == "fedora" {
		for _, check := range []struct{ name, path string }{
			{"dnf         ", f.DnfPath},
			{"sudo        ", f.SudoPath},
		} {
			if check.path == "" {
				rows = append(rows, rowStyled("    "+check.name, styleErr.Render("missing")))
			} else {
				rows = append(rows, rowStyled("    "+check.name, styleGood.Render(shortPath(check.path))))
			}
		}
	}
	if f.OS == "darwin" {
		for _, check := range []struct{ name, path string }{
			{"brew        ", f.BrewPath},
			{"nix-darwin  ", f.DarwinRebuild},
		} {
			if check.path == "" {
				rows = append(rows, rowStyled("    "+check.name, styleErr.Render("missing")))
			} else {
				rows = append(rows, rowStyled("    "+check.name, styleGood.Render(shortPath(check.path))))
			}
		}
	}

	if f.TailscaleIP != "" {
		rows = append(rows, "")
		rows = append(rows, stylePurple.Render("  Network"))
		rows = append(rows, rowStyled("    tailscale ", styleGood.Render(f.TailscaleIP)))
	}

	return styleCard.Width(cw).Render(strings.Join(rows, "\n"))
}

// ── Footer ────────────────────────────────────────────────────────────────

func (m Model) viewFooter(w int) string {
	var hints string
	switch {
	case m.mode == modeRunning:
		hints = "j/k scroll log · f follow · Q force quit"
	case m.mode == modeDone:
		hints = "j/k scroll · f follow · q close log · Q quit"
	case m.tabs[m.tab] == "Updates":
		hints = "j/k move · space toggle · r run · tab switch tabs · q quit"
	case m.tabs[m.tab] == "Audit":
		hints = "a rescan · tab switch tabs · r refresh · q quit"
	default:
		hints = "1-4 tabs · tab/shift+tab switch · r refresh · q quit"
	}
	return styleFaint.Render(hints)
}

// ── Log rendering ─────────────────────────────────────────────────────────

func (m Model) renderLog() string {
	var sb strings.Builder
	for _, line := range m.logLines {
		switch line.kind {
		case logHeader:
			sb.WriteString(styleBold.Foreground(clrGold).Render(line.text))
		case logCmd:
			sb.WriteString(styleCmd.Render(line.text))
		case logSuccess:
			sb.WriteString(styleGood.Render(line.text))
		case logError:
			sb.WriteString(styleErr.Render(line.text))
		default:
			sb.WriteString(line.text)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func classifyLine(text string) logLine {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "error:") || strings.Contains(lower, "failed") ||
		strings.Contains(lower, "✗"):
		return logLine{kind: logError, text: "  " + text}
	case strings.Contains(lower, "activating") || strings.Contains(lower, "✓") ||
		strings.Contains(text, "Done") || strings.Contains(text, "switched"):
		return logLine{kind: logSuccess, text: "  " + text}
	default:
		return logLine{kind: logOutput, text: "  " + text}
	}
}

// ── Layout helpers ────────────────────────────────────────────────────────

func (m Model) logHeight() int {
	taskLines := len(m.tasks) + 6
	bodyH := m.height - 10 // header + tabs + footer + padding
	h := bodyH - taskLines - 4
	return max(5, h)
}

func (m Model) logWidth() int {
	w := m.width - 8
	if w < 40 {
		return 40
	}
	return w
}

// ── Navigation ────────────────────────────────────────────────────────────

func (m *Model) nextTab() {
	m.tab = (m.tab + 1) % len(m.tabs)
	m.cursor = 0
}

func (m *Model) prevTab() {
	m.tab--
	if m.tab < 0 {
		m.tab = len(m.tabs) - 1
	}
	m.cursor = 0
}

func (m *Model) moveCursor(delta int) {
	if m.tabs[m.tab] != "Updates" {
		return
	}
	n := len(m.tasks)
	if n == 0 {
		return
	}
	m.cursor = (m.cursor + delta + n) % n
}

// ── Utility ───────────────────────────────────────────────────────────────

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

func osLabel(f system.Facts) string {
	if f.OSID != "" {
		return f.OS + "/" + f.Arch + " (" + f.OSID + ")"
	}
	return f.OS + "/" + f.Arch
}

func row(label, value string) string {
	return styleMuted.Render(label) + "  " + value
}

func rowStyled(label, value string) string {
	return styleMuted.Render(label) + "  " + value
}

func managerLine(name, path string) string {
	if path == "" {
		return styleFaint.Render(name) + "  " + styleFaint.Render("—")
	}
	return styleMuted.Render(name) + "  " + styleGood.Render(shortPath(path))
}

func shortPath(p string) string {
	if p == "" {
		return styleMuted.Render("—")
	}
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

func innerWidth(w int) int {
	return max(30, w-8)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
