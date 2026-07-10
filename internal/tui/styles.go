package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/snyderb-de/sys-bozo/internal/system"
)

// uiStyles contains semantic roles for the Monolith/Afterburner visual system.
// Existing screens keep their legacy styles until they migrate to this system.
type uiStyles struct {
	field, major, title, label, text, muted  lipgloss.Style
	attention, active, success, danger, rule lipgloss.Style
}

type statusKind uint8

const (
	statusMuted statusKind = iota
	statusAttention
	statusActive
	statusSuccess
	statusDanger
)

func newUIStyles(noColor bool) uiStyles {
	color := func(hex string) lipgloss.TerminalColor {
		if noColor {
			return lipgloss.NoColor{}
		}
		return lipgloss.Color(hex)
	}

	return uiStyles{
		field:     lipgloss.NewStyle().Background(color("#0a0d10")).Foreground(color("#dae4ea")),
		major:     lipgloss.NewStyle().Foreground(color("#f4f7f8")).Bold(!noColor),
		title:     lipgloss.NewStyle().Foreground(color("#dae4ea")).Bold(!noColor),
		label:     lipgloss.NewStyle().Foreground(color("#60717c")),
		text:      lipgloss.NewStyle().Foreground(color("#dae4ea")),
		muted:     lipgloss.NewStyle().Foreground(color("#60717c")),
		attention: lipgloss.NewStyle().Foreground(color("#ffcb6b")).Bold(!noColor),
		active:    lipgloss.NewStyle().Foreground(color("#66d9ef")).Bold(!noColor),
		success:   lipgloss.NewStyle().Foreground(color("#7ee787")).Bold(!noColor),
		danger:    lipgloss.NewStyle().Foreground(color("#ff8f70")).Bold(!noColor),
		rule:      lipgloss.NewStyle().Foreground(color("#27343c")),
	}
}

// ── Palette (Tokyo Night Storm) ───────────────────────────────────────────

var (
	clrBg     = lipgloss.Color("#1e2030")
	clrPanel  = lipgloss.Color("#222436")
	clrBorder = lipgloss.Color("#2d3f6a")
	clrText   = lipgloss.Color("#c8d3f5")
	clrMuted  = lipgloss.Color("#a9b4e6") // secondary text — readable on the dark bg
	clrFaint  = lipgloss.Color("#7e8ac0") // de-emphasized, still legible (was #444a73, unreadable)
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

	styleGroupHeader = lipgloss.NewStyle().Foreground(clrPurple)
	styleCursor      = lipgloss.NewStyle().Foreground(clrCyan)
	styleActionLabel = lipgloss.NewStyle().Foreground(clrText).Bold(true)
	styleActionAvail = lipgloss.NewStyle().Foreground(clrMuted)
)

// ── Layout helpers ────────────────────────────────────────────────────────

func layoutWidth(width int) int {
	if width <= 0 {
		return 100
	}
	if width > 140 {
		return 140
	}
	return width
}

func majorRule(s uiStyles, width int, active bool) string {
	style := s.rule
	if active {
		style = s.active
	}
	return style.Render(strings.Repeat("━", max(1, width)))
}

func statusText(s uiStyles, text string, kind statusKind) string {
	style := s.muted
	switch kind {
	case statusAttention:
		style = s.attention
	case statusActive:
		style = s.active
	case statusSuccess:
		style = s.success
	case statusDanger:
		style = s.danger
	}
	return style.Render(text)
}

func numberedRow(s uiStyles, number, label, renderedStatus string, width int, active bool) string {
	numberStyle := s.muted
	labelStyle := s.text
	marker := "  "
	if active {
		numberStyle = s.active
		labelStyle = s.active
		marker = "> "
	}

	left := marker + numberStyle.Render(number) + " " + labelStyle.Render(label)
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(renderedStatus))
	return left + strings.Repeat(" ", gap) + renderedStatus
}

func (m Model) logHeight() int {
	bodyH := m.height - 10
	h := bodyH - len(m.tasks) - 10
	return max(5, h)
}

func (m Model) logWidth() int {
	w := m.width - 8
	if w < 40 {
		return 40
	}
	return w
}

// ── Utility ───────────────────────────────────────────────────────────────

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

func compactManagerStatus(name, path string) string {
	if path == "" {
		return styleWarn.Render(name + " missing")
	}
	return styleGood.Render(name + " ok")
}

func baseName(path string) string {
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "unknown"
	}
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
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
