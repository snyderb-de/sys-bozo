package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/snyderb-de/sys-bozo/internal/history"
)

var inspectEntries = []struct {
	number, label string
	target        screen
}{
	{"01", "CONFIG", screenConfig},
	{"02", "AUDIT", screenAudit},
	{"03", "DOCTOR", screenDoctor},
	{"04", "HISTORY", screenHistory},
}

func (m Model) viewInspect() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	rows := []string{
		s.major.Render("INSPECT/SYSTEM"),
		s.label.Render("READ-ONLY SYSTEM SURFACES"),
		majorRule(s, contentWidth, true),
		"",
	}
	for i, entry := range inspectEntries {
		rows = append(rows, numberedRow(s, entry.number, entry.label, statusText(s, "OPEN", statusSuccess), contentWidth, i == m.inspectCursor))
	}
	rows = append(rows, "", majorRule(s, contentWidth, false), "", s.muted.Render("ESCAPE BACK   ENTER OPEN"))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func (m Model) viewHistory() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	rows := []string{
		s.major.Render("INSPECT/HISTORY"),
		s.label.Render("RECENT EXECUTION METADATA"),
		majorRule(s, contentWidth, true),
		"",
	}
	entries := history.Read(20)
	visible := len(entries)
	if m.height > 0 && m.height <= 24 {
		visible = min(14, visible)
	}
	if visible == 0 {
		rows = append(rows, s.muted.Render("NO HISTORY"))
	}
	for i, entry := range entries[:visible] {
		status := entry.EffectiveStatus()
		kind := statusSuccess
		if status != history.StatusSuccess {
			kind = statusDanger
		}
		renderedStatus := statusText(s, strings.ToUpper(string(status)), kind)
		label := entry.Ts.Local().Format("2006-01-02 15:04") + "  " + entry.Action + "  " + formatRunElapsed(time.Duration(entry.Secs*float64(time.Second)))
		labelWidth := max(1, contentWidth-6-lipgloss.Width(renderedStatus))
		label = truncateVisible(label, labelWidth)
		rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), label, renderedStatus, contentWidth, false))
	}
	footer := "ESCAPE BACK"
	if visible < len(entries) {
		footer = fmt.Sprintf("SHOWING %d OF %d   ESCAPE BACK", visible, len(entries))
	}
	rows = append(rows, "", majorRule(s, contentWidth, false), "", s.muted.Render(footer))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

// ── Config ────────────────────────────────────────────────────────────────

func (m Model) viewConfig() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	rows := []string{
		s.major.Render("INSPECT/CONFIG"),
		s.label.Render("DECLARATIVE SOURCE FILES"),
		majorRule(s, contentWidth, true),
		"",
	}

	for i, f := range m.configFiles {
		label := f.label
		if f.hint != "" {
			label += "  " + f.hint
		}
		rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), label, statusText(s, "EDIT", statusActive), contentWidth, i == m.configCursor))
	}

	if m.applyPrompt {
		rows = append(rows, "", s.attention.Render("CHOOSE REVIEWED REBUILD"), s.muted.Render("H HMS   N NDS   B BOTH   ANY OTHER KEY SKIPS"))
	}
	if m.configNotice != "" {
		rows = append(rows, "", s.muted.Render(truncateVisible(m.configNotice, contentWidth)))
	}
	rows = append(rows, "", majorRule(s, contentWidth, false), "", s.muted.Render("ESCAPE BACK   J/K MOVE   ENTER EDIT"))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

// ── Audit ─────────────────────────────────────────────────────────────────

func (m Model) viewAudit() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	compact := m.height > 0 && m.height <= 24
	header := []string{s.major.Render("INSPECT/AUDIT"), s.label.Render("LOCAL CONFIGURATION AUDIT"), majorRule(s, contentWidth, true), ""}

	if !m.auditReady {
		rows := append(header, s.muted.Render("SCANNING..."), "", s.muted.Render("ESCAPE BACK   A RESCAN"))
		return primaryFrame(s, m.width, strings.Join(rows, "\n"))
	}

	rows := header
	configCount := 0
	for i, item := range m.auditItems {
		if configCount == 6 {
			rows = append(rows, "", s.title.Render("TOOLS"))
		}
		label := "PASS"
		kind := statusSuccess
		if !item.OK {
			label = "ISSUE"
			kind = statusDanger
		}
		rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), item.Name+"  "+item.Detail, statusText(s, label, kind), contentWidth, false))
		if !item.OK && !compact {
			rows = append(rows, auditHelpRows("why", item.Description, contentWidth-8, s.muted)...)
			rows = append(rows, auditHelpRows("fix", item.Fix, contentWidth-8, s.text)...)
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
	summary := s.success.Render(fmt.Sprintf("%d OK", ok))
	if fail > 0 {
		summary += "  " + s.danger.Render(fmt.Sprintf("%d ISSUES", fail))
	}
	rows = append(rows, summary)
	rows = append(rows, "", majorRule(s, contentWidth, false), "", s.muted.Render("ESCAPE BACK   A RESCAN"))
	if compact {
		rows = nonEmptyRows(rows)
	}
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
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

func (m Model) viewDoctor() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	compact := m.height > 0 && m.height <= 24
	f := m.facts

	rows := []string{s.major.Render("INSPECT/DOCTOR"), s.label.Render("WORKSTATION DIAGNOSTICS"), majorRule(s, contentWidth, true), ""}

	rows = append(rows, s.title.Render("DOTFILES"))
	branch := f.DotfilesBranch
	if branch == "" {
		branch = "unknown"
	}
	rows = append(rows, s.label.Render("BRANCH")+"  "+s.text.Render(branch))
	if f.DotfilesDirty == 0 {
		rows = append(rows, s.label.Render("WORKTREE")+"  "+s.success.Render("CLEAN"))
	} else {
		rows = append(rows, s.label.Render("WORKTREE")+"  "+s.danger.Render(fmt.Sprintf("%d DIRTY", f.DotfilesDirty)))
	}
	rows = append(rows, "")

	rows = append(rows, s.title.Render("HOME MANAGER"))
	gen := f.HMGeneration
	if gen == "" || gen == "none" {
		rows = append(rows, s.label.Render("GENERATION")+"  "+s.danger.Render("NONE — RUN HMS"))
	} else {
		rows = append(rows, s.label.Render("GENERATION")+"  "+s.success.Render(gen))
	}
	rows = append(rows, "")

	rows = append(rows, s.title.Render("SECRETS / SSH"))
	if f.AgeKeyExists {
		rows = append(rows, s.label.Render("AGE KEY")+"  "+s.success.Render("PRESENT"))
	} else {
		rows = append(rows, s.label.Render("AGE KEY")+"  "+s.danger.Render("MISSING · ~/.config/sops/age/keys.txt"))
	}
	if f.GitHubKeyExists {
		rows = append(rows, s.label.Render("GITHUB KEY")+"  "+s.success.Render("PRESENT"))
	} else {
		rows = append(rows, s.label.Render("GITHUB KEY")+"  "+s.danger.Render("MISSING"))
	}
	rows = append(rows, "")

	rows = append(rows, s.title.Render("PACKAGE MANAGERS"))
	for _, check := range []struct{ name, path string }{
		{"nix", f.NixPath},
		{"home-manager", f.HomeManager},
		{"topgrade", f.Topgrade},
	} {
		rows = append(rows, doctorManagerRow(s, check.name, check.path))
	}
	if f.OS == "linux" && f.OSID == "fedora" {
		for _, check := range []struct{ name, path string }{
			{"dnf", f.DnfPath},
			{"sudo", f.SudoPath},
		} {
			rows = append(rows, doctorManagerRow(s, check.name, check.path))
		}
	}
	if f.OS == "darwin" {
		for _, check := range []struct{ name, path string }{
			{"brew", f.BrewPath},
			{"nix-darwin", f.DarwinRebuild},
		} {
			rows = append(rows, doctorManagerRow(s, check.name, check.path))
		}
	}

	if f.TailscaleIP != "" {
		rows = append(rows, "")
		rows = append(rows, s.title.Render("NETWORK"))
		rows = append(rows, s.label.Render("TAILSCALE")+"  "+s.success.Render(f.TailscaleIP))
	}
	rows = append(rows, "", majorRule(s, contentWidth, false), "", s.muted.Render("ESCAPE BACK   R REFRESH"))
	if compact {
		rows = nonEmptyRows(rows)
	}
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func nonEmptyRows(rows []string) []string {
	compact := rows[:0]
	for _, row := range rows {
		if row != "" {
			compact = append(compact, row)
		}
	}
	return compact
}

func doctorManagerRow(s uiStyles, name, path string) string {
	if path == "" {
		return s.label.Render(strings.ToUpper(name)) + "  " + s.danger.Render("MISSING")
	}
	return s.label.Render(strings.ToUpper(name)) + "  " + s.success.Render(shortPath(path))
}
