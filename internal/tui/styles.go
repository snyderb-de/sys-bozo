package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/snyderb-de/sys-bozo/internal/system"
)

// uiStyles is the shared terminal palette. Meaning never depends on color alone.
type uiStyles struct {
	field, major, title, label, text, muted  lipgloss.Style
	attention, active, success, danger, rule lipgloss.Style
	panel, selected, badge                   lipgloss.Style
	noColor                                  bool
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
		field:     lipgloss.NewStyle().Background(color("#191724")).Foreground(color("#eee9ff")),
		major:     lipgloss.NewStyle().Foreground(color("#c4a7ff")).Bold(!noColor),
		title:     lipgloss.NewStyle().Foreground(color("#eee9ff")).Bold(!noColor),
		label:     lipgloss.NewStyle().Foreground(color("#b7a8d1")),
		text:      lipgloss.NewStyle().Foreground(color("#eee9ff")),
		muted:     lipgloss.NewStyle().Foreground(color("#a59ab8")),
		attention: lipgloss.NewStyle().Foreground(color("#f6c177")).Bold(!noColor),
		active:    lipgloss.NewStyle().Foreground(color("#c4a7ff")).Bold(!noColor),
		success:   lipgloss.NewStyle().Foreground(color("#9de0b2")).Bold(!noColor),
		danger:    lipgloss.NewStyle().Foreground(color("#ff8fa3")).Bold(!noColor),
		rule:      lipgloss.NewStyle().Foreground(color("#514466")),
		panel:     lipgloss.NewStyle().Background(color("#221e30")).Foreground(color("#eee9ff")),
		selected:  lipgloss.NewStyle().Background(color("#392d50")).Foreground(color("#eee9ff")),
		badge:     lipgloss.NewStyle().Background(color("#c4a7ff")).Foreground(color("#191724")).Bold(!noColor).Padding(0, 1),
		noColor:   noColor,
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

// layoutWidth follows the terminal so a resized window uses the new columns.
// The frame carries no border, so the full width renders without wrapping.
func layoutWidth(width int) int {
	if width <= 0 {
		return 100
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

	label = truncateVisible(label, max(1, width-4-lipgloss.Width(number)-lipgloss.Width(renderedStatus)))
	left := marker + numberStyle.Render(number) + " " + labelStyle.Render(label)
	gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(renderedStatus))
	row := left + strings.Repeat(" ", gap) + renderedStatus
	if active {
		plain := marker + number + " " + label + strings.Repeat(" ", gap) + ansi.Strip(renderedStatus)
		return s.selected.Bold(!s.noColor).Width(width).Render(plain)
	}
	return row
}

func packagePipelineRow(s uiStyles, label, status string, width int, kind statusKind) string {
	if width <= 0 {
		return ""
	}
	label = packageDisplayText(label)
	status = strings.TrimSpace(packageDisplayText(status))
	if status == "" {
		return s.text.Render(truncateVisible(label, width))
	}
	label = truncateVisible(label, max(1, width/3))
	remaining := width - lipgloss.Width(label) - 1
	if remaining <= 0 {
		return s.text.Render(truncateVisible(label, width))
	}
	status = truncateVisible(status, remaining)
	gap := max(1, width-lipgloss.Width(label)-lipgloss.Width(status))
	return s.text.Render(label) + strings.Repeat(" ", gap) + statusText(s, status, kind)
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
