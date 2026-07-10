package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

// ── Log line types ────────────────────────────────────────────────────────

type logKind int

const (
	logHeader logKind = iota
	logCmd
	logOutput
	logSuccess
	logError
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

type screen uint8

const (
	screenHome screen = iota
	screenMaintenance
	screenReview
	screenRunning
	screenResult
	screenInspect
	screenConfig
	screenAudit
	screenDoctor
	screenHistory
)

type reviewedPlan struct {
	Action string
	Items  []runner.WorkItem
}

// ── Config file entry ─────────────────────────────────────────────────────

type configFile struct {
	label string
	path  string // absolute
	hint  string
}

// ── Tea messages ─────────────────────────────────────────────────────────

type lineMsg struct{ text string }
type stepDoneMsg struct {
	err     error
	elapsed time.Duration
}
type auditReadyMsg struct{ items []system.AuditItem }
type sudoReadyMsg struct{ err error }
type editorDoneMsg struct {
	path string
	err  error
}
type applyChoiceMsg struct{ key string }

// ── Model ─────────────────────────────────────────────────────────────────

type Model struct {
	facts  system.Facts
	runCtx runner.Context
	tasks  []runner.Task
	screen screen
	styles uiStyles

	homeCursor    int
	inspectCursor int
	selected      map[string]bool
	reviewed      reviewedPlan

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
	runAction string // ID of the running action, for history

	logLines  []logLine
	logVP     viewport.Model
	logFollow bool
	spinner   spinner.Model

	activeScanner *bufio.Scanner
	activeWait    func() error
	terminalExec  func(runner.WorkItem, time.Time) tea.Cmd

	auditItems []system.AuditItem
	auditReady bool

	// Config tab
	configFiles   []configFile
	configCursor  int
	applyPrompt   bool // show apply-after-edit prompt
	applyEditPath string
}

func New() Model {
	ctx := runner.Build()
	tasks := runner.DefaultTasks(ctx)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(clrCyan)

	return Model{
		facts:        system.Probe(),
		runCtx:       ctx,
		tasks:        tasks,
		screen:       screenHome,
		styles:       newUIStyles(os.Getenv("NO_COLOR") != ""),
		selected:     map[string]bool{},
		tabs:         []string{"Dashboard", "Actions", "Config", "Audit", "Doctor"},
		logFollow:    true,
		spinner:      sp,
		configFiles:  buildConfigFiles(ctx),
		terminalExec: runInteractiveWork,
	}
}

func buildConfigFiles(ctx runner.Context) []configFile {
	repo := ctx.Repo
	files := []configFile{
		{"flake.nix", filepath.Join(repo, "flake.nix"), "system inputs, hosts, Homebrew lists"},
		{"home/common/home.nix", filepath.Join(repo, "home/common/home.nix"), "shared imports"},
		{"home/modules/packages.nix", filepath.Join(repo, "home/modules/packages.nix"), "user packages"},
		{"home/modules/shell.nix", filepath.Join(repo, "home/modules/shell.nix"), "zsh, aliases, env"},
		{"home/modules/git.nix", filepath.Join(repo, "home/modules/git.nix"), "git config"},
	}
	if ctx.OS == "darwin" {
		files = append(files, configFile{
			"home/darwin/default.nix",
			filepath.Join(repo, "home/darwin/default.nix"),
			"darwin user extras",
		})
		hostFile := filepath.Join(repo, "hosts", ctx.Hostname, "darwin.nix")
		if _, err := os.Stat(hostFile); err == nil {
			files = append(files, configFile{
				fmt.Sprintf("hosts/%s/darwin.nix", ctx.Hostname),
				hostFile,
				"this host's system config",
			})
		}
	} else {
		files = append(files, configFile{
			"home/linux/default.nix",
			filepath.Join(repo, "home/linux/default.nix"),
			"linux user extras",
		})
		hostFile := filepath.Join(repo, "hosts", ctx.Hostname, "home.nix")
		if _, err := os.Stat(hostFile); err == nil {
			files = append(files, configFile{
				fmt.Sprintf("hosts/%s/home.nix", ctx.Hostname),
				hostFile,
				"this host's user config",
			})
		}
	}
	homebrew := filepath.Join(repo, "homebrew.nix")
	if _, err := os.Stat(homebrew); err == nil {
		files = append(files, configFile{"homebrew.nix", homebrew, "shared Homebrew declarations"})
	}
	return files
}
