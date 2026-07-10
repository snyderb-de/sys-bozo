package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ── Actions ───────────────────────────────────────────────────────────────

func (m Model) viewActions(w int) string {
	cw := innerWidth(w)

	var rows []string
	rows = append(rows, styleTitle.Render("Actions")+"  "+styleMuted.Render("enter run · j/k move · r refresh"))
	rows = append(rows, "")

	avail := m.availableTasks()
	lastGroup := ""

	for _, t := range m.tasks {
		isAvail := t.Available(m.runCtx)

		if t.Group != lastGroup {
			if lastGroup != "" {
				rows = append(rows, "")
			}
			rows = append(rows, "  "+styleGroupHeader.Render(t.Group))
			lastGroup = t.Group
		}

		myIdx := -1
		for ai, at := range avail {
			if at.ID == t.ID {
				myIdx = ai
				break
			}
		}

		cur := "  "
		var labelStyle, descStyle lipgloss.Style
		if isAvail {
			if myIdx == m.cursor {
				cur = styleCursor.Render("▶ ")
				labelStyle = styleActionLabel
			} else {
				labelStyle = styleActionAvail
			}
			descStyle = styleMuted
		} else {
			labelStyle = lipgloss.NewStyle().Foreground(clrFaint)
			descStyle = lipgloss.NewStyle().Foreground(clrFaint)
		}

		label := labelStyle.Render(fmt.Sprintf("%-8s", t.Label))
		desc := descStyle.Render(fmt.Sprintf("%-36s", t.Desc))
		hint := ""
		if t.Hint != "" {
			hint = styleMuted.Render(t.Hint)
		}
		rows = append(rows, cur+label+"  "+desc+"  "+hint)
	}

	actionList := styleCard.Width(cw).Render(strings.Join(rows, "\n"))

	if m.mode == modeView {
		return actionList
	}

	var logTitle string
	if m.mode == modeRunning {
		logTitle = m.spinner.View() + " " + styleAccent.Render("running") +
			"  " + styleFaint.Render(time.Since(m.runStart).Round(time.Second).String())
	} else {
		logTitle = styleGood.Render("✓ complete") + "  " +
			styleFaint.Render(time.Since(m.runStart).Round(time.Second).String()) +
			"  " + styleFaint.Render("q close")
	}

	followIndicator := styleFaint.Render("follow")
	if !m.logFollow {
		followIndicator = styleWarn.Render("scroll")
	}

	logHeader := lipgloss.JoinHorizontal(lipgloss.Top,
		"  "+logTitle,
		strings.Repeat(" ", max(1, cw-lipgloss.Width(logTitle)-lipgloss.Width(followIndicator)-4)),
		followIndicator+"  ",
	)

	logContent := styleLogPane.Width(cw).Render(logHeader + "\n" + m.logVP.View())

	return lipgloss.JoinVertical(lipgloss.Left, actionList, "", logContent)
}

// ── Log rendering ─────────────────────────────────────────────────────────

func (m Model) renderLog() string {
	var sb strings.Builder
	for _, line := range m.logLines {
		switch line.kind {
		case logHeader:
			sb.WriteString(styleBold.Foreground(clrGold).Render(line.text))
		case logCmd:
			sb.WriteString(styleCmd.Render(line.text))
		case logSuccess:
			sb.WriteString(styleGood.Render(line.text))
		case logError:
			sb.WriteString(styleErr.Render(line.text))
		default:
			sb.WriteString(line.text)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func classifyLine(text string) logLine {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "error:") || strings.Contains(lower, "failed") ||
		strings.Contains(lower, "✗"):
		return logLine{kind: logError, text: "  " + text}
	case strings.Contains(lower, "activating") || strings.Contains(lower, "✓") ||
		strings.Contains(text, "Done") || strings.Contains(text, "switched"):
		return logLine{kind: logSuccess, text: "  " + text}
	default:
		return logLine{kind: logOutput, text: "  " + text}
	}
}
