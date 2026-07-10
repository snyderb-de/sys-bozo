package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── View ──────────────────────────────────────────────────────────────────

var homeEntries = []struct {
	number, label string
	target        screen
}{
	{"01", "WEEKLY MAINTENANCE", screenMaintenance},
	{"02", "ADD PACKAGE", screenHome},
	{"03", "INSPECT SYSTEM", screenInspect},
}

func (m Model) View() string {
	switch m.screen {
	case screenHome:
		return m.viewHome()
	case screenMaintenance:
		return m.viewMaintenance()
	case screenReview:
		return m.viewReview()
	}
	return m.viewLegacy()
}

func (m Model) viewLegacy() string {
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

func (m Model) viewHome() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles

	health := statusText(s, "SYSTEM HEALTHY", statusSuccess)
	if m.facts.DotfilesDirty > 0 || m.facts.BrewOutdated > 0 {
		health = statusText(s, "SYSTEM NEEDS ATTENTION", statusAttention)
	}

	host := m.targetHost()
	branch := m.facts.DotfilesBranch
	if branch == "" {
		branch = "unknown"
	}
	repo := "CLEAN"
	repoKind := statusSuccess
	if m.facts.DotfilesDirty > 0 {
		repo = fmt.Sprintf("%d DIRTY", m.facts.DotfilesDirty)
		repoKind = statusDanger
	}
	updates := "CURRENT"
	updatesKind := statusSuccess
	if m.facts.BrewOutdated > 0 {
		updates = fmt.Sprintf("%d PENDING", m.facts.BrewOutdated)
		updatesKind = statusAttention
	}

	rows := []string{
		s.major.Render("SYS/BOZO"),
		s.label.Render("WORKSTATION CONTROL"),
		majorRule(s, contentWidth, true),
		"",
		s.title.Render("SYSTEM") + "  " + health,
		"",
		s.label.Render("HOST") + "  " + s.text.Render(host),
		s.label.Render("BRANCH") + "  " + s.text.Render(branch),
		s.label.Render("REPOSITORY") + "  " + statusText(s, repo, repoKind),
		s.label.Render("UPDATES") + "  " + statusText(s, updates, updatesKind),
		"",
		majorRule(s, contentWidth, false),
		"",
	}

	for i, entry := range homeEntries {
		kind := statusMuted
		label := "LOCKED"
		if !homeEntryLocked(i) {
			kind = statusSuccess
			label = "READY"
		}
		rows = append(rows, numberedRow(s, entry.number, entry.label, statusText(s, label, kind), contentWidth, i == m.homeCursor && !homeEntryLocked(i)))
	}
	rows = append(rows, "", s.muted.Render("↑/↓ MOVE   ENTER OPEN   Q QUIT"))

	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
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
		return m.viewConfig(cw)
	case "Audit":
		return m.viewAudit(cw)
	case "Doctor":
		return m.viewDoctor(cw)
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
