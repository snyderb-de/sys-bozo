package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// screenTitle gives every workflow a recognizable location without spending
// extra terminal rows on decoration or internal route names.
func screenTitle(s uiStyles, route string, width int) string {
	titles := map[string]string{
		"HOME": "Overview", "SELECT": "Maintenance", "REVIEW": "Review changes",
		"RUN/ACTIVE": "Running", "RUN/RESULT": "Run summary",
		"INSPECT/SYSTEM": "Inspect system", "INSPECT/CONFIG": "Configuration",
		"INSPECT/AUDIT": "Configuration audit", "INSPECT/DOCTOR": "Diagnostics",
		"INSPECT/HISTORY": "History", "ADD/PACKAGE": "Packages",
		"REPO/TRIAGE": "Repository", "REVIEW/REPOSITORY": "Review repository changes",
		"REVIEW/PACKAGE": "Review package changes", "REVIEW/CONFIG": "Review configuration",
		"UPDATES / MAC MINI": "Updates", "RECOVERY / MAC MINI": "Recovery",
		"REVIEW / MAC MINI": "Review update", "RESULT / MAC MINI": "Update summary",
		"KEYBOARD": "Keyboard shortcuts",
	}
	title := titles[route]
	if title == "" {
		title = route
	}
	return dashboardPair(s.badge.Render("sys-bozo"), s.title.Render(title), width)
}

func panel(s uiStyles, title, body string, width int, focused bool) string {
	border := s.rule.GetForeground()
	if focused {
		border = s.active.GetForeground()
	}
	return s.panel.Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(border).
		Padding(0, 1).Width(max(1, width-1)).Render(s.title.Render(title) + "\n" + body)
}

// Bubbles help handles visible cell widths and ANSI styles. Split groups before
// rendering so essential commands wrap rather than disappearing off the right.
func helpLine(s uiStyles, width int, pairs ...string) string {
	h := help.New()
	h.Width = width
	h.ShortSeparator = "   "
	h.Styles.ShortKey, h.Styles.ShortDesc, h.Styles.ShortSeparator = s.active, s.muted, s.rule
	var rows []string
	var bindings []key.Binding
	used := 0
	for i := 0; i+1 < len(pairs); i += 2 {
		size := lipgloss.Width(pairs[i]) + 1 + lipgloss.Width(pairs[i+1])
		if used+size+3 > width && len(bindings) > 0 {
			rows = append(rows, h.ShortHelpView(bindings))
			bindings = nil
			used = 0
		}
		bindings = append(bindings, key.NewBinding(key.WithKeys(pairs[i]), key.WithHelp(pairs[i], pairs[i+1])))
		used += size + 3
	}
	if len(bindings) > 0 {
		rows = append(rows, h.ShortHelpView(bindings))
	}
	return strings.Join(rows, "\n")
}

func progressMeter(s uiStyles, completed, total, width int) string {
	fraction := 0.0
	if total > 0 {
		fraction = float64(completed) / float64(total)
	}
	fraction = math.Min(1, math.Max(0, fraction))
	p := progress.New(progress.WithSolidFill("#8bbcff"), progress.WithColorProfile(lipgloss.ColorProfile()), progress.WithWidth(max(1, width-7)), progress.WithoutPercentage(), progress.WithFillCharacters('━', '─'))
	p.EmptyColor = "#394658"
	if s.noColor {
		p = progress.New(progress.WithColorProfile(termenv.Ascii), progress.WithWidth(max(1, width-7)), progress.WithoutPercentage(), progress.WithFillCharacters('━', '─'))
	}
	return p.ViewAs(fraction) + "  " + s.active.Render(fmt.Sprintf("%3d%%", int(fraction*100)))
}

func resultBanner(s uiStyles, state string) string {
	switch state {
	case "COMPLETE", "STEPS COMPLETED":
		return s.success.Render("✓ " + state)
	case "CANCELLED":
		return s.attention.Render("– " + state)
	default:
		return s.danger.Render("! " + state)
	}
}

func (m Model) editingText() bool {
	return m.applyPrompt || (m.screen == screenPackage && m.packageFlow.stage == packageSearch) ||
		(m.screen == screenRepoTriage && (m.repoFlow.stage == repoCommitMessage || m.repoFlow.stage == repoDeleteConfirm))
}

func (m Model) View() string {
	if m.terminalTooSmall() {
		text := "Resize to 60 × 20. Q quits."
		if m.mode == modeRunning {
			text = "Run active. Resize to 60 × 20."
		}
		return m.styles.attention.Render(truncateVisible(text, max(1, m.width)))
	}
	if m.helpVisible {
		return m.viewHelp()
	}
	out := m.rawView()
	if m.refreshing {
		lines := strings.Split(out, "\n")
		if len(lines) > 2 {
			width := primaryContentWidth(m.width)
			notice := m.styles.attention.Render(truncateVisible("↻ Refreshing host facts… Esc back · Ctrl-C quit", width))
			lines[2] = m.styles.field.Width(layoutWidth(m.width)).Padding(0, primaryFramePadding).Render(notice)
			out = strings.Join(lines, "\n")
		}
	}
	if m.height <= 0 || lipgloss.Height(out) <= m.height {
		return m.styles.field.Width(layoutWidth(m.width)).Height(m.height).Render(out)
	}
	return m.pagedFrame(out)
}

// Overflow pages preserve the location and action footer. They never discard
// commands or errors: Page Up/Down exposes every body line.
func (m Model) pagedFrame(out string) string {
	lines := strings.Split(out, "\n")
	if m.height < 10 {
		return primaryFrame(m.styles, m.width, "Resize the terminal to at least 60 × 20.\nQ quits; no action was started.")
	}
	const top, bottom = 4, 4
	capacity := max(1, m.height-top-bottom-1)
	body := lines[top : len(lines)-bottom]
	start := min(max(0, m.frameOffset), max(0, len(body)-capacity))
	rows := append([]string{}, lines[:top]...)
	rows = append(rows, body[start:min(len(body), start+capacity)]...)
	note := fmt.Sprintf("PgUp/PgDn scroll · lines %d–%d of %d", start+1, min(len(body), start+capacity), len(body))
	rows = append(rows, m.styles.muted.Render(truncateVisible("   "+note, m.width)))
	rows = append(rows, lines[len(lines)-bottom:]...)
	return strings.Join(rows, "\n")
}

func (m *Model) scrollFrame(k string) bool {
	if (k != "pgdown" && k != "pgup") || m.editingText() || m.height < 10 {
		return false
	}
	lines := lipgloss.Height(m.rawView())
	if lines <= m.height {
		return false
	}
	page := max(1, m.height-9)
	switch k {
	case "pgdown":
		m.frameOffset = min(lines-m.height, m.frameOffset+page)
	case "pgup":
		m.frameOffset = max(0, m.frameOffset-page)
	default:
		return false
	}
	return true
}

func (m Model) viewHelp() string {
	s, w := m.styles, primaryContentWidth(m.width)
	rows := []string{screenTitle(s, "KEYBOARD", w), s.muted.Render("Navigation and actions"), majorRule(s, w, true), ""}
	rows = append(rows, s.title.Render("Getting around"), helpLine(s, w, "↑/↓ or j/k", "move", "Enter", "open / continue", "Esc", "back"), helpLine(s, w, "PgUp/PgDn", "scroll long pages", "?", "this guide", "q", "quit"), "")
	rows = append(rows, s.title.Render("Select → review → run"), s.text.Render("Space toggles a selection. Enter opens its exact plan."), s.text.Render("Enter on Review confirms execution. Esc returns to selection."), "")
	rows = append(rows, s.title.Render("Packages & repository"), s.text.Render("Tab switches package sources or repository files/diff."), s.text.Render("Repository: C commit · S stash · R restore · D delete."), "")
	rows = append(rows, s.title.Render("After a run"), helpLine(s, w, "l", "log / summary", "r", "review retry", "v", "review package revert"), s.muted.Render("Only available actions appear in each screen's footer."), "", majorRule(s, w, false), helpLine(s, w, "? / Esc", "close guide"))
	out := primaryFrame(s, m.width, strings.Join(rows, "\n"))
	if m.height > 0 && lipgloss.Height(out) > m.height {
		compact := []string{screenTitle(s, "KEYBOARD", w), "", helpLine(s, w, "↑/↓ j/k", "move", "Enter", "open / confirm", "Esc", "back"), helpLine(s, w, "Space", "select", "Tab", "source / view", "PgUp/PgDn", "scroll"), helpLine(s, w, "l", "log", "r", "review retry", "q", "quit"), "", s.text.Render("Review exact commands before confirming."), "", helpLine(s, w, "? / Esc", "close guide")}
		out = primaryFrame(s, m.width, strings.Join(compact, "\n"))
	}
	return out
}

func (m Model) terminalTooSmall() bool {
	return (m.width > 0 && m.width < 60) || (m.height > 0 && m.height < 20)
}

// Keep keyboard focus visible when a menu needs more than one screen.
func (m *Model) revealSelection() {
	if m.height < 20 {
		return
	}
	out := m.rawView()
	if lipgloss.Height(out) <= m.height {
		m.frameOffset = 0
		return
	}
	capacity := m.height - 9
	for i, line := range strings.Split(ansi.Strip(out), "\n") {
		if !strings.HasPrefix(strings.TrimLeft(line, " │"), "> ") {
			continue
		}
		row := i - 4
		if row < m.frameOffset {
			m.frameOffset = max(0, row)
		}
		if row >= m.frameOffset+capacity {
			m.frameOffset = row - capacity + 1
		}
		return
	}
}
