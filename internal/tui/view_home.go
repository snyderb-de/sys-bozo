package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
