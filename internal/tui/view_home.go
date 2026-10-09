package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

// ── View ──────────────────────────────────────────────────────────────────

var homeEntries = []struct {
	number, label string
	target        screen
}{
	{"01", "Weekly maintenance", screenMaintenance},
	{"02", "Add package", screenPackage},
	{"03", "Inspect system", screenInspect},
}

func (m Model) rawView() string {
	switch m.screen {
	case screenHome:
		return m.viewHome()
	case screenMaintenance:
		return m.viewMaintenance()
	case screenReview:
		return m.viewReview()
	case screenRunning:
		return m.viewRunning()
	case screenResult:
		return m.viewResult()
	case screenInspect:
		return m.viewInspect()
	case screenConfig:
		return m.viewConfig()
	case screenAudit:
		return m.viewAudit()
	case screenDoctor:
		return m.viewDoctor()
	case screenHistory:
		return m.viewHistory()
	case screenPackage:
		return m.viewPackage()
	case screenRepoTriage:
		return m.viewRepoTriage()
	}
	return m.viewLegacy()
}

func (m Model) viewLegacy() string {
	w := m.width
	if w <= 0 {
		w = 120
	}
	inner := max(80, w-4)

	parts := []string{
		m.viewHeader(inner),
		m.viewTabs(),
		m.viewBody(inner),
		m.viewFooter(inner),
	}

	return styleShell.Width(inner).Render(strings.Join(parts, "\n\n"))
}

func (m Model) viewHome() string {
	w, s := primaryContentWidth(m.width), m.styles
	branch := m.facts.DotfilesBranch
	if branch == "" {
		branch = "unknown"
	}
	repo, repoKind := "Clean", statusSuccess
	if m.facts.DotfilesStatusUnavailable {
		repo, repoKind = "Status unavailable", statusDanger
	} else if m.facts.DotfilesDirty > 0 {
		repo, repoKind = fmt.Sprintf("%d changed files", m.facts.DotfilesDirty), statusAttention
	}
	updates, updateKind := "No updates reported", statusMuted
	if m.facts.BrewOutdated > 0 {
		updates, updateKind = fmt.Sprintf("%d updates available", m.facts.BrewOutdated), statusAttention
	}
	if m.facts.BrewPath == "" && m.runCtx.BrewBin == "" {
		updates = "Not available on this host"
	}
	rows := []string{
		screenTitle(s, "HOME", w),
		s.muted.Render(truncateVisible(m.targetHost()+"  /  "+branch, w)),
		majorRule(s, w, true),
		"",
		s.title.Render("Workstation"),
		homeRepoRow(s, repo, repoKind, m.homeRepoFocused),
		"  " + s.label.Render("Homebrew    ") + statusText(s, updates, updateKind),
		"",
		s.title.Render("Actions"),
	}
	descriptions := []string{"Update tools and apply configuration", "Search Nix and Homebrew", "Configuration, diagnostics, history, and Git"}
	for i, entry := range homeEntries {
		if i == 0 && runner.HasManagedMacWorkflow(m.runCtx) {
			entry.label = "Update workstation"
		}
		active := i == m.homeCursor && !m.homeRepoFocused
		rows = append(rows, numberedRow(s, entry.number, entry.label, "", w, active))
		rows = append(rows, "     "+s.muted.Render(truncateVisible(descriptions[i], w-5)))
	}
	rows = append(rows, "", s.title.Render("Last run"))
	if m.latestHistory == nil {
		rows = append(rows, s.muted.Render("No history yet"))
	} else {
		entry := m.latestHistory
		kind := statusMuted
		switch entry.EffectiveStatus() {
		case history.StatusSuccess:
			kind = statusSuccess
		case history.StatusFailure:
			kind = statusDanger
		case history.StatusCancelled:
			kind = statusAttention
		}
		rows = append(rows, dashboardPair(s.text.Render(entry.Action), statusText(s, strings.ToUpper(string(entry.EffectiveStatus())), kind), w),
			s.muted.Render(entry.Ts.Local().Format(time.DateTime)))
	}
	return m.dashboardFrame(rows, majorRule(s, w, false), helpLine(s, w, "↑/↓", "MOVE", "1–3", "OPEN", "ENTER", "OPEN", "Q", "QUIT", "?", "HELP"))
}

// dashboardPair aligns status without letting long host or action names wrap.
func dashboardPair(left, right string, width int) string {
	left = truncateVisible(left, max(1, width-lipgloss.Width(right)-2))
	return left + strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right))) + right
}

func homeRepoRow(s uiStyles, value string, kind statusKind, focused bool) string {
	prefix := "  "
	if focused {
		prefix = "> "
	}
	return prefix + s.label.Render("Repository  ") + statusText(s, value, kind)
}

const primaryFramePadding = 3

func primaryContentWidth(width int) int {
	return max(1, layoutWidth(width)-primaryFramePadding*2)
}

func primaryFrame(s uiStyles, width int, content string) string {
	return s.field.
		Width(layoutWidth(width)).
		Padding(1, primaryFramePadding).
		Render(content)
}

// dashboardFrame anchors controls at the bottom when the body fits. Longer
// views retain every row and use the existing page navigation.
func (m Model) dashboardFrame(rows []string, footer ...string) string {
	bodyHeight := lipgloss.Height(strings.Join(rows, "\n"))
	footerHeight := lipgloss.Height(strings.Join(footer, "\n"))
	for bodyHeight < m.height-footerHeight-2 {
		rows = append(rows, "")
		bodyHeight++
	}
	rows = append(rows, footer...)
	return primaryFrame(m.styles, m.width, strings.Join(rows, "\n"))
}

func (m Model) targetHost() string {
	user := m.facts.User
	if user == "" {
		user = m.runCtx.User
	}
	host := m.facts.Hostname
	if host == "" {
		host = m.runCtx.Hostname
	}
	switch {
	case user != "" && host != "":
		return user + "@" + host
	case host != "":
		return host
	case user != "":
		return user
	default:
		return "unknown"
	}
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
	case "Actions":
		return m.viewActions(cw)
	case "Config":
		return m.viewConfig()
	case "Audit":
		return m.viewAudit()
	case "Doctor":
		return m.viewDoctor()
	}
	return ""
}

// ── Dashboard ─────────────────────────────────────────────────────────────

func (m Model) viewDashboard(w int) string {
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
	brewStr := styleGood.Render("up to date")
	if m.facts.BrewOutdated > 0 {
		brewStr = styleWarn.Render(fmt.Sprintf("%d outdated", m.facts.BrewOutdated))
	}

	managerParts := []string{
		compactManagerStatus("nix", m.facts.NixPath),
		compactManagerStatus("home-manager", m.facts.HomeManager),
		compactManagerStatus("topgrade", m.facts.Topgrade),
	}
	if m.facts.OS == "linux" && m.facts.OSID == "fedora" {
		managerParts = append(managerParts,
			compactManagerStatus("dnf", m.facts.DnfPath),
			compactManagerStatus("sudo", m.facts.SudoPath),
		)
	}
	if m.facts.OS == "darwin" {
		managerParts = append(managerParts,
			compactManagerStatus("brew", m.facts.BrewPath),
			compactManagerStatus("nix-darwin", m.facts.DarwinRebuild),
		)
	}

	rows := []string{
		styleTitle.Render("Home"),
		row("Host ", fmt.Sprintf("%s@%s | %s | %s", m.facts.User, m.facts.Hostname, osLabel(m.facts), baseName(m.facts.Shell))),
		rowStyled("State", fmt.Sprintf("%s | %s | HM %s | brew %s", branch, dirtyStr, hmGen, brewStr)),
		row("Repo ", shortPath(m.facts.DotfilesRepo)),
		row("Tools", strings.Join(managerParts, " | ")),
	}
	if m.facts.TailscaleIP != "" {
		rows = append(rows, row("Net  ", "tailscale "+m.facts.TailscaleIP))
	}

	return styleCard.Padding(0, 1).Width(w).Render(strings.Join(rows, "\n"))
}

// ── Footer ────────────────────────────────────────────────────────────────

func (m Model) viewFooter(w int) string {
	var hints string
	switch {
	case m.applyPrompt:
		hints = "h hms · n nds · b both · any other key skip"
	case m.mode == modeRunning:
		hints = "j/k scroll log · f follow · Q force quit"
	case m.mode == modeDone:
		hints = "j/k scroll · f follow · q close log · Q quit"
	case m.tabs[m.tab] == "Actions":
		hints = "j/k move · enter run · tab switch tabs · r refresh · q quit"
	case m.tabs[m.tab] == "Config":
		hints = "j/k move · enter edit in $EDITOR · r refresh · q quit"
	case m.tabs[m.tab] == "Audit":
		hints = "a rescan · tab switch tabs · r refresh · q quit"
	default:
		hints = "1-5 tabs · tab/shift+tab switch · r refresh · q quit"
	}
	return styleFaint.Render(hints)
}
