package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/snyderb-de/sys-bozo/internal/plan"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

var (
	gold  = lipgloss.Color("#f3c969")
	cyan  = lipgloss.Color("#79e6d9")
	blue  = lipgloss.Color("#78a9ff")
	green = lipgloss.Color("#9be58f")
	red   = lipgloss.Color("#ff7d90")
	muted = lipgloss.Color("#9daabd")
	faint = lipgloss.Color("#667286")
	panel = lipgloss.Color("#131720")
	bg    = lipgloss.Color("#0d0f14")

	titleStyle = lipgloss.NewStyle().Foreground(gold).Bold(true)
	mutedStyle = lipgloss.NewStyle().Foreground(muted)
	faintStyle = lipgloss.NewStyle().Foreground(faint)
	goodStyle  = lipgloss.NewStyle().Foreground(green).Bold(true)
	warnStyle  = lipgloss.NewStyle().Foreground(gold).Bold(true)
	stopStyle  = lipgloss.NewStyle().Foreground(red).Bold(true)

	shellStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e7edf7")).
			Background(bg).
			Padding(1, 2)

	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#2d3748")).
			Background(panel).
			Padding(1, 2)

	activeTabStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#080a0f")).
			Background(cyan).
			Bold(true).
			Padding(0, 1)

	tabStyle = lipgloss.NewStyle().
			Foreground(muted).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(lipgloss.Color("#2d3748")).
			Padding(0, 1)
)

type updateTask struct {
	ID          string
	Label       string
	Description string
	Selected    bool
}

type configTarget struct {
	Name string
	Path string
}

type Model struct {
	facts        system.Facts
	tabs         []string
	tab          int
	cursor       int
	width        int
	height       int
	updates      []updateTask
	configs      []configTarget
	packageInput textinput.Model
	preview      *plan.Plan
}

func New() Model {
	input := textinput.New()
	input.Placeholder = "search package..."
	input.CharLimit = 80
	input.Prompt = "> "
	input.Focus()

	return Model{
		facts: system.Probe(),
		tabs:  []string{"Dashboard", "Updates", "Packages", "Configs", "Secrets", "Audit"},
		updates: []updateTask{
			{ID: "brew-update", Label: "Brew update", Description: "Refresh Homebrew metadata.", Selected: true},
			{ID: "brew-upgrade", Label: "Brew selected upgrades", Description: "Upgrade chosen formulae/casks once picker lands.", Selected: true},
			{ID: "brew-autoremove", Label: "Brew autoremove", Description: "Remove unused Homebrew dependencies."},
			{ID: "nix-flake-update", Label: "Nix flake update", Description: "Update flake inputs by scope/input.", Selected: true},
			{ID: "home-manager-apply", Label: "Home Manager apply", Description: "Apply user profile."},
			{ID: "darwin-apply", Label: "nix-darwin apply", Description: "Apply macOS host profile."},
		},
		configs: []configTarget{
			{Name: "zsh", Path: "~/.zshrc"},
			{Name: "ssh", Path: "~/.ssh/config"},
			{Name: "git", Path: "~/.gitconfig"},
			{Name: "starship", Path: "~/.config/starship.toml"},
			{Name: "atuin", Path: "~/.config/atuin/config.toml"},
			{Name: "home-manager", Path: "home/common/home.nix"},
			{Name: "nix-darwin", Path: "flake.nix"},
		},
		packageInput: input,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "tab", "right":
			m.nextTab()
		case "shift+tab", "left":
			m.prevTab()
		case "1", "2", "3", "4", "5", "6":
			m.tab = int(msg.String()[0] - '1')
			m.cursor = 0
			m.preview = nil
		case "j", "down":
			m.moveCursor(1)
		case "k", "up":
			m.moveCursor(-1)
		case " ":
			if m.tabs[m.tab] == "Updates" && len(m.updates) > 0 {
				m.updates[m.cursor].Selected = !m.updates[m.cursor].Selected
				m.preview = nil
			}
		case "enter", "p":
			m.previewPlan()
		case "r":
			m.facts = system.Probe()
		}
	}

	if m.tabs[m.tab] == "Packages" {
		var cmd tea.Cmd
		m.packageInput, cmd = m.packageInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 110
	}
	contentWidth := clamp(width-4, 76, 132)
	shell := shellStyle.Width(contentWidth)

	parts := []string{
		m.header(contentWidth),
		m.tabsView(),
		m.body(contentWidth),
		m.footer(),
	}
	return shell.Render(strings.Join(parts, "\n\n"))
}

func (m Model) header(width int) string {
	left := titleStyle.Render("sys-bozo control center")
	right := faintStyle.Render("plan first | apply later | q quits | r refresh")
	return lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right))), right)
}

func (m Model) tabsView() string {
	var rendered []string
	for i, tab := range m.tabs {
		label := fmt.Sprintf("%d %s", i+1, tab)
		if i == m.tab {
			rendered = append(rendered, activeTabStyle.Render(label))
		} else {
			rendered = append(rendered, tabStyle.Render(label))
		}
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

func (m Model) body(width int) string {
	var main string
	switch m.tabs[m.tab] {
	case "Dashboard":
		main = m.dashboard(width)
	case "Updates":
		main = m.updatesView(width)
	case "Packages":
		main = m.packagesView(width)
	case "Configs":
		main = m.configsView(width)
	case "Secrets":
		main = m.secretsView(width)
	case "Audit":
		main = m.auditView(width)
	}

	if m.preview == nil {
		return main
	}

	return lipgloss.JoinVertical(lipgloss.Left, main, "", m.planView(width))
}

func (m Model) dashboard(width int) string {
	if width < 108 {
		cardWidth := innerCardWidth(width)
		return lipgloss.JoinVertical(lipgloss.Left,
			m.hostCard(cardWidth),
			"",
			m.stateCard(cardWidth),
			"",
			m.managerCard(cardWidth),
		)
	}

	colWidth := max(24, (width-22)/3)
	return lipgloss.JoinHorizontal(lipgloss.Top, m.hostCard(colWidth), "  ", m.stateCard(colWidth), "  ", m.managerCard(colWidth))
}

func (m Model) hostCard(width int) string {
	return cardStyle.Width(width).Render(strings.Join([]string{
		titleStyle.Render("Host"),
		"Name:  " + value(m.facts.Hostname),
		"User:  " + value(m.facts.User),
		"OS:    " + m.facts.OS + "/" + m.facts.Arch,
		"Shell: " + shortPath(m.facts.Shell),
	}, "\n"))
}

func (m Model) stateCard(width int) string {
	return cardStyle.Width(width).Render(strings.Join([]string{
		titleStyle.Render("State"),
		fmt.Sprintf("Repo dirty:   %d files", m.facts.GitDirtyCount),
		fmt.Sprintf("Brew pending: %d", m.facts.BrewOutdated),
		"Workdir:      " + shortPath(m.facts.WorkingDir),
	}, "\n"))
}

func (m Model) managerCard(width int) string {
	managers := []string{
		titleStyle.Render("Managers"),
		managerLine("nix", m.facts.NixPath),
		managerLine("brew", m.facts.BrewPath),
		managerLine("home-manager", m.facts.HomeManager),
	}
	if m.facts.OS == "darwin" {
		managers = append(managers, managerLine("nix-darwin", m.facts.DarwinRebuild))
	}
	return cardStyle.Width(width).Render(strings.Join(managers, "\n"))
}

func managerLine(name, path string) string {
	if path == "" {
		return name + ": " + stopStyle.Render("missing")
	}
	return name + ": " + goodStyle.Render(shortPath(path))
}

func (m Model) updatesView(width int) string {
	var rows []string
	rows = append(rows, titleStyle.Render("Update picker")+" "+mutedStyle.Render("space toggles, p previews"))
	for i, task := range m.updates {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}
		check := "[ ]"
		if task.Selected {
			check = "[x]"
		}
		rows = append(rows, fmt.Sprintf("%s %s %-24s %s", cursor, check, task.Label, mutedStyle.Render(task.Description)))
	}
	return cardStyle.Width(innerCardWidth(width)).Render(strings.Join(rows, "\n"))
}

func (m Model) packagesView(width int) string {
	rows := []string{
		titleStyle.Render("Package workbench"),
		mutedStyle.Render("Search package. Enter previews Nix/Brew search plus catalog/profile edit."),
		"",
		m.packageInput.View(),
		"",
		"Planned flows:",
		"  - install now only",
		"  - add to catalog/profile",
		"  - move Brew -> Nix",
		"  - move Nix -> Brew",
		"  - install tarball under ~/.local/opt",
	}
	return cardStyle.Width(innerCardWidth(width)).Render(strings.Join(rows, "\n"))
}

func (m Model) configsView(width int) string {
	rows := []string{titleStyle.Render("Config editor"), mutedStyle.Render("j/k choose, enter previews editor + validation plan"), ""}
	for i, target := range m.configs {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}
		rows = append(rows, fmt.Sprintf("%s %-14s %s", cursor, target.Name, mutedStyle.Render(target.Path)))
	}
	return cardStyle.Width(innerCardWidth(width)).Render(strings.Join(rows, "\n"))
}

func (m Model) secretsView(width int) string {
	rows := []string{
		titleStyle.Render("Secrets"),
		"Status: " + warnStyle.Render("doctor not implemented"),
		"",
		"Target:",
		"  - age key setup under ~/.config/sops/age/",
		"  - .sops.yaml recipient generation",
		"  - private SSH/env templates",
		"  - never store secret values in repo",
	}
	return cardStyle.Width(innerCardWidth(width)).Render(strings.Join(rows, "\n"))
}

func (m Model) auditView(width int) string {
	rows := []string{
		titleStyle.Render("Audit"),
		"Status: " + warnStyle.Render("compare engine not implemented"),
		"",
		"Target:",
		"  - local declared-vs-installed drift",
		"  - SSH/Tailscale reachability",
		"  - remote host package/config compare",
		"  - report under ~/.cache/sys-bozo/reports/",
	}
	return cardStyle.Width(innerCardWidth(width)).Render(strings.Join(rows, "\n"))
}

func (m Model) planView(width int) string {
	if m.preview == nil {
		return ""
	}
	lines := m.preview.Lines()
	for i, line := range lines {
		if i == 0 {
			lines[i] = titleStyle.Render(line)
			continue
		}
		if strings.Contains(line, "[mutate]") {
			lines[i] = warnStyle.Render(line)
		}
	}
	return cardStyle.Width(innerCardWidth(width)).BorderForeground(blue).Render(strings.Join(lines, "\n"))
}

func (m Model) footer() string {
	return faintStyle.Render("keys: 1-6 tabs | j/k move | space select | p/enter preview | r refresh | q quit")
}

func (m *Model) nextTab() {
	m.tab = (m.tab + 1) % len(m.tabs)
	m.cursor = 0
	m.preview = nil
}

func (m *Model) prevTab() {
	m.tab--
	if m.tab < 0 {
		m.tab = len(m.tabs) - 1
	}
	m.cursor = 0
	m.preview = nil
}

func (m *Model) moveCursor(delta int) {
	limit := 0
	switch m.tabs[m.tab] {
	case "Updates":
		limit = len(m.updates)
	case "Configs":
		limit = len(m.configs)
	}
	if limit == 0 {
		return
	}
	m.cursor = (m.cursor + delta + limit) % limit
}

func (m *Model) previewPlan() {
	switch m.tabs[m.tab] {
	case "Dashboard":
		preview := plan.Install(plan.InstallOptions{Profile: "docs", Host: m.facts.Hostname})
		m.preview = &preview
	case "Updates":
		var selected []string
		for _, task := range m.updates {
			if task.Selected {
				selected = append(selected, task.ID)
			}
		}
		preview := plan.Update(selected)
		m.preview = &preview
	case "Packages":
		preview := plan.PackageSearch(m.packageInput.Value())
		m.preview = &preview
	case "Configs":
		target := m.configs[m.cursor]
		preview := plan.ConfigEdit(target.Name, target.Path)
		m.preview = &preview
	case "Secrets":
		preview := plan.Install(plan.InstallOptions{Profile: "secrets", Host: m.facts.Hostname})
		m.preview = &preview
	case "Audit":
		preview := plan.Install(plan.InstallOptions{Profile: "audit", Host: m.facts.Hostname})
		m.preview = &preview
	}
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func innerCardWidth(width int) int {
	return max(24, width-8)
}

func value(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func shortPath(path string) string {
	if path == "" {
		return "missing"
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
