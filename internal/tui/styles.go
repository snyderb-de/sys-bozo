package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/snyderb-de/sys-bozo/internal/system"
)

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
