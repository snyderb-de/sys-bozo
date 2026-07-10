package tui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/snyderb-de/sys-bozo/internal/fileedit"
	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/packages"
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
	screenPackage
)

type reviewedPlan struct {
	Action  string
	Items   []runner.WorkItem
	Package *packageReview
	Config  *configReview
}

type stepResult struct {
	Item     runner.WorkItem
	Status   history.Status
	Duration time.Duration
	Err      error
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
	err       error
	elapsed   time.Duration
	cancelled bool
}
type auditReadyMsg struct{ items []system.AuditItem }
type sudoReadyMsg struct{ err error }
type configEditorRequest struct {
	file     configFile
	original []byte
}
type configEditorDoneMsg struct {
	file               configFile
	original, proposed []byte
	err                error
}

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
	latestHistory *history.Entry

	tabs   []string
	tab    int
	cursor int
	width  int
	height int

	mode             appMode
	queue            []runner.WorkItem
	queuePos         int
	runStart         time.Time
	stepStart        time.Time
	runAction        string // ID of the running action, for history
	runErr           error
	runCancelled     bool
	runElapsed       time.Duration
	stepResults      []stepResult
	resultLogVisible bool
	revertErr        error

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
	configFiles       []configFile
	configCursor      int
	applyPrompt       bool // choose reviewed rebuild after temp-file edit
	pendingConfig     fileedit.Proposal
	configNotice      string
	configEditor      func(configEditorRequest) tea.Cmd
	configExecProcess func(*exec.Cmd, tea.ExecCallback) tea.Cmd
	applyConfig       func(fileedit.Proposal) (fileedit.AppliedEdit, error)

	packageFlow          packageFlow
	searchPackage        func(context.Context, string) packages.SearchResult
	packageSearchCancel  context.CancelFunc
	packageSearchRequest uint64
	packageSearchTimeout time.Duration
	applyPackage         func(packages.Proposal) (packages.AppliedEdit, error)
	verifyPackage        func(packages.VerifySpec) packages.VerifyResult
	proposePackageRevert func(packages.AppliedEdit) (packages.Proposal, error)
	packageEditor        func(packageEditorRequest) tea.Cmd
	packageExecProcess   func(*exec.Cmd, tea.ExecCallback) tea.Cmd
}

func New() Model {
	ctx := runner.Build()
	tasks := runner.DefaultTasks(ctx)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(clrCyan)
	searchPackage := func(searchCtx context.Context, query string) packages.SearchResult {
		return packages.Search(searchCtx, packages.ExecRunner{}, ctx.NixBin, ctx.BrewBin, query)
	}
	verifyPackage := func(spec packages.VerifySpec) packages.VerifyResult {
		return packages.Verify(context.Background(), packages.ExecRunner{}, exec.LookPath, spec)
	}

	model := Model{
		facts:                system.Probe(),
		runCtx:               ctx,
		tasks:                tasks,
		screen:               screenHome,
		styles:               newUIStyles(os.Getenv("NO_COLOR") != ""),
		selected:             map[string]bool{},
		tabs:                 []string{"Dashboard", "Actions", "Config", "Audit", "Doctor"},
		logFollow:            true,
		spinner:              sp,
		configFiles:          buildConfigFiles(ctx),
		terminalExec:         runInteractiveWork,
		searchPackage:        searchPackage,
		packageSearchTimeout: 30 * time.Second,
		applyPackage:         packages.Apply,
		verifyPackage:        verifyPackage,
		proposePackageRevert: packages.ProposeRevert,
		applyConfig:          fileedit.Apply,
	}
	model.refreshLatestHistory()
	return model
}

func (m *Model) refreshLatestHistory() {
	entries := history.Read(1)
	m.latestHistory = nil
	if len(entries) > 0 {
		entry := entries[0]
		m.latestHistory = &entry
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
