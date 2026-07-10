package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

// ── Guided planning ───────────────────────────────────────────────────────

func (m Model) viewMaintenance() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles

	rows := []string{
		s.major.Render("SELECT"),
		s.label.Render("WEEKLY MAINTENANCE"),
		majorRule(s, contentWidth, true),
		"",
	}

	lastGroup := ""
	availableIndex := 0
	compact := m.height > 0 && m.height <= 24
	for _, task := range m.tasks {
		available := task.Available(m.runCtx)
		if compact && !available {
			continue
		}
		if !compact && task.Group != lastGroup {
			if lastGroup != "" {
				rows = append(rows, "")
			}
			rows = append(rows, s.title.Render(task.Group))
			lastGroup = task.Group
		}

		active := available && availableIndex == m.cursor
		marker := "  "
		labelStyle := s.muted
		if active {
			marker = "> "
			labelStyle = s.active
		} else if available {
			labelStyle = s.text
		}

		checkbox := "[ ]"
		status := statusText(s, "LOCKED", statusMuted)
		if available {
			status = statusText(s, "READY", statusSuccess)
		}
		if available && m.selected[task.ID] {
			checkbox = "[x]"
			status = statusText(s, "SELECTED", statusActive)
		}

		label := task.Label
		if compact {
			label = task.Group + "/" + task.Label
		}
		left := marker + labelStyle.Render(checkbox+" "+label) + "  " + s.muted.Render(task.Desc)
		gap := max(1, contentWidth-lipgloss.Width(left)-lipgloss.Width(status))
		rows = append(rows, left+strings.Repeat(" ", gap)+status)
		if available {
			availableIndex++
		}
	}

	rows = append(rows,
		"",
		majorRule(s, contentWidth, false),
		"",
		s.muted.Render("ESCAPE BACK   SPACE TOGGLE")+"   "+s.active.Render("ENTER REVIEW"),
	)

	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func (m Model) viewReview() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles

	rows := []string{
		s.major.Render("REVIEW"),
		s.label.Render("IMMUTABLE EXECUTION PLAN"),
		majorRule(s, contentWidth, true),
		"",
		s.label.Render("TARGET HOST") + "  " + s.text.Render(m.targetHost()),
		s.label.Render("ACTION") + "  " + s.text.Render(m.reviewed.Action),
	}
	if m.facts.DotfilesDirty > 0 {
		rows = append(rows, statusText(s, fmt.Sprintf("WARNING  DOTFILES REPOSITORY DIRTY — %d FILES", m.facts.DotfilesDirty), statusDanger))
	}
	rows = append(rows, "", majorRule(s, contentWidth, false), "")

	for i, item := range m.reviewed.Items {
		status := statusText(s, "READY", statusMuted)
		if item.Mode == runner.ExecutionInteractive {
			status = statusText(s, "TTY", statusDanger)
		}
		rows = append(rows, reviewCommandRows(s, fmt.Sprintf("%02d", i+1), runner.CmdLabel(item), status, contentWidth)...)
	}

	rows = append(rows,
		"",
		majorRule(s, contentWidth, false),
		"",
		s.muted.Render("ESCAPE BACK")+"   "+s.active.Render("ENTER CONFIRM"),
	)

	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func reviewCommandRows(s uiStyles, number, command, renderedStatus string, width int) []string {
	prefix := "  " + s.muted.Render(number) + " "
	commandWidth := max(1, width-lipgloss.Width(prefix)-lipgloss.Width(renderedStatus)-1)
	lines := wrapText(command, commandWidth)
	if len(lines) == 1 {
		return []string{numberedRow(s, number, command, renderedStatus, width, false)}
	}

	rows := []string{prefix + s.text.Render(lines[0])}
	continuation := strings.Repeat(" ", lipgloss.Width(prefix))
	for _, line := range lines[1 : len(lines)-1] {
		rows = append(rows, continuation+s.text.Render(line))
	}
	last := continuation + s.text.Render(lines[len(lines)-1])
	gap := max(1, width-lipgloss.Width(last)-lipgloss.Width(renderedStatus))
	rows = append(rows, last+strings.Repeat(" ", gap)+renderedStatus)
	return rows
}

func wrapText(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" || width < 1 {
		return []string{text}
	}

	var lines []string
	for lipgloss.Width(text) > width {
		runes := []rune(text)
		cut, lastSpace := 0, -1
		for i := range runes {
			if lipgloss.Width(string(runes[:i+1])) > width {
				break
			}
			cut = i + 1
			if runes[i] == ' ' {
				lastSpace = i
			}
		}
		if lastSpace > 0 {
			cut = lastSpace
		}
		if cut == 0 {
			cut = 1
		}
		lines = append(lines, strings.TrimSpace(string(runes[:cut])))
		text = strings.TrimSpace(string(runes[cut:]))
	}
	return append(lines, text)
}

func (m Model) viewRunning() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	items := m.queue
	if len(items) == 0 {
		items = m.reviewed.Items
	}
	completed := min(max(0, m.queuePos), len(items))
	percent := 0
	if len(items) > 0 {
		percent = completed * 100 / len(items)
	}

	rows := []string{
		s.major.Render("RUN/ACTIVE"),
		s.label.Render("EXECUTION IN PROGRESS"),
		majorRule(s, contentWidth, true),
		"",
		s.title.Render(fmt.Sprintf("%02d/%02d", completed, len(items))) +
			"  " + s.active.Render(fmt.Sprintf("%d%%", percent)) +
			"  " + s.muted.Render(formatRunElapsed(time.Since(m.runStart))),
		"",
	}
	for i, item := range items {
		label := "WAITING"
		kind := statusMuted
		if i < m.queuePos {
			label = "DONE"
			kind = statusSuccess
		} else if i == m.queuePos {
			label = "ACTIVE"
			kind = statusActive
			if item.Mode == runner.ExecutionInteractive {
				label = "TTY"
				kind = statusDanger
			}
		}
		rows = append(rows, reviewCommandRows(s, fmt.Sprintf("%02d", i+1), runner.CmdLabel(item), statusText(s, label, kind), contentWidth)...)
	}
	if m.height == 0 || m.height >= 28 {
		rows = append(rows, "", majorRule(s, contentWidth, false), "", s.label.Render("OUTPUT"), m.logVP.View())
	}
	rows = append(rows, "", s.muted.Render("J/K SCROLL   F FOLLOW   Q FORCE QUIT"))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func (m Model) viewResult() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	historyStatus := m.resultHistoryStatus()
	state := "COMPLETE"
	kind := statusSuccess
	if historyStatus == history.StatusCancelled {
		state = "CANCELLED"
		kind = statusDanger
	} else if historyStatus == history.StatusFailure {
		state = "FAILED"
		kind = statusDanger
	}

	rule := s.success.Render(strings.Repeat("━", contentWidth))
	if kind == statusDanger {
		rule = s.danger.Render(strings.Repeat("━", contentWidth))
	}
	rows := []string{
		s.major.Render("RUN/RESULT"),
		s.label.Render("EXECUTION FINISHED"),
		rule,
		"",
		s.title.Render(state) + "  " + s.muted.Render(formatRunElapsed(m.runElapsed)),
		"",
		s.label.Render("HISTORY ") + statusText(s, strings.ToUpper(string(historyStatus)), kind),
		"",
	}
	if m.resultLogVisible {
		rows = append(rows, majorRule(s, contentWidth, false), "", s.label.Render("OUTPUT"), m.logVP.View(), "", majorRule(s, contentWidth, false), "", s.muted.Render("L SUMMARY   ESCAPE BACK   Q CLOSE"))
		return primaryFrame(s, m.width, strings.Join(rows, "\n"))
	}
	for i, item := range m.reviewed.Items {
		label := "WAITING"
		rowKind := statusMuted
		command := runner.CmdLabel(item)
		if i < len(m.stepResults) && m.stepResults[i].Status != "" {
			result := m.stepResults[i]
			if result.Item.Name != "" {
				command = runner.CmdLabel(result.Item)
			}
			if result.Duration > 0 {
				command += "  " + formatRunElapsed(result.Duration)
			}
			switch result.Status {
			case history.StatusSuccess:
				label = "DONE"
				rowKind = statusSuccess
			case history.StatusCancelled:
				label = "CANCELLED"
				rowKind = statusDanger
			default:
				label = "FAILED"
				rowKind = statusDanger
			}
		}
		rows = append(rows, reviewCommandRows(s, fmt.Sprintf("%02d", i+1), command, statusText(s, label, rowKind), contentWidth)...)
	}
	if m.runErr != nil {
		rows = append(rows, "", s.danger.Render(m.runErr.Error()))
	}
	rows = append(rows, "", majorRule(s, contentWidth, false), "", s.muted.Render("L VIEW LOG   R REVIEW RETRY   ESCAPE BACK   Q CLOSE"))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func (m Model) resultHistoryStatus() history.Status {
	return runStatus(m.runErr, m.runCancelled)
}

func formatRunElapsed(elapsed time.Duration) string {
	elapsed = elapsed.Round(time.Second)
	if elapsed < 0 {
		elapsed = 0
	}
	total := int(elapsed / time.Second)
	return fmt.Sprintf("%02d:%02d", total/60, total%60)
}

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
