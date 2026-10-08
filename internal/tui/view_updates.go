package tui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"strings"

	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

func (m Model) viewMiniUpdates() string {
	s, width := m.styles, primaryContentWidth(m.width)
	title, subtitle := "UPDATES / MAC MINI", "Select your updates. Review the exact plan before anything runs."
	if m.updatesRecovery {
		title, subtitle = "RECOVERY / MAC MINI", "Restore a previous generation. Review the recovery plan first."
	}
	rows := []string{screenTitle(s, title, width), s.muted.Render(subtitle), majorRule(s, width, true), ""}
	options := m.miniUpdateOptions()
	selected := 0
	listWidth := width
	wide := width >= 104 && m.height >= 28
	if wide {
		listWidth = width * 3 / 5
	}
	var choices []string
	for i, option := range options {
		mark, state, kind := "[ ] ", "optional", statusMuted
		if option.Recommended {
			state = "recommended"
		}
		if option.Recovery {
			state = "recovery"
		}
		if !option.Available {
			state = "unavailable"
		}
		if m.selected[option.ID] {
			mark, state, kind = "[x] ", "SELECTED", statusActive
			selected++
		}
		choices = append(choices, numberedRow(s, fmt.Sprintf("%02d", i+1), mark+option.Label, statusText(s, state, kind), listWidth, i == m.cursor))
		if wide {
			choices = append(choices, "")
		}
	}
	var details []string
	if m.cursor >= 0 && m.cursor < len(options) {
		option := options[m.cursor]
		detailWidth := width
		if wide {
			detailWidth = width - listWidth - 6
		}
		details = append(details, s.title.Render("💡 "+option.Label))
		for _, line := range wrapText(option.Description+" "+option.Detail, detailWidth) {
			details = append(details, s.text.Render(line))
		}
	}
	if wide {
		side := panel(s, "ABOUT THIS STEP", strings.Join(details, "\n"), width-listWidth-2, false)
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(choices, "\n"), "  ", side))
	} else {
		rows = append(rows, choices...)
		rows = append(rows, "", majorRule(s, width, false))
		// The focused option remains explained even at 80 × 24.
		if len(details) > 1 {
			rows = append(rows, details[1:]...)
		}
	}
	rows = append(rows, "", s.active.Render(fmt.Sprintf("%d selected", selected))+"  "+s.muted.Render("① Select  →  ② Review  →  ③ Run"))
	if m.updatesNotice != "" {
		rows = append(rows, s.attention.Render(truncateVisible(m.updatesNotice, width)))
	}
	footer := helpLine(s, width, "A", "SELECT RECOMMENDED", "SPACE", "TOGGLE", "ENTER", "REVIEW")
	other := helpLine(s, width, "TAB", "RECOVERY", "ESC", "BACK", "?", "HELP")
	if m.updatesRecovery {
		footer = helpLine(s, width, "SPACE", "SELECT", "ENTER", "REVIEW")
		other = helpLine(s, width, "TAB", "UPDATES", "ESC", "BACK", "?", "HELP")
	}
	rows = append(rows, footer, other)
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func (m Model) updateReviewRows() []string {
	s, width := m.styles, primaryContentWidth(m.width)
	var rows []string
	for _, note := range m.reviewed.Updates.Notes {
		for _, line := range wrapText(note, width) {
			rows = append(rows, s.attention.Render(line))
		}
		rows = append(rows, "")
	}
	for i, item := range m.reviewed.Items {
		state := "CHANGE"
		if item.ReadOnly {
			state = "CHECK"
		} else if item.Mode == runner.ExecutionInteractive {
			state = "TERMINAL"
		}
		rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), updateStepTitle(item), statusText(s, state, statusMuted), width, false))
		for _, line := range wrapText(item.Description, width-3) {
			rows = append(rows, "   "+s.text.Render(line))
		}
		for _, line := range wrapText("$ "+runner.CmdLabel(item), width-3) {
			rows = append(rows, "   "+s.muted.Render(line))
		}
		if item.Dir != "" {
			for _, line := range wrapText("In: "+item.Dir, width-3) {
				rows = append(rows, "   "+s.muted.Render(line))
			}
		}
		rows = append(rows, "")
	}
	return rows
}

func (m Model) viewMiniUpdateReview() string {
	s, width := m.styles, primaryContentWidth(m.width)
	rows := []string{screenTitle(s, "REVIEW / MAC MINI", width), s.muted.Render("Exact execution order. Nothing runs until confirmation."), majorRule(s, width, true)}
	label := m.updatesReadinessLabel()
	for _, line := range wrapText(label, width) {
		rows = append(rows, s.attention.Render(line))
	}
	rows = append(rows, "")
	foot := "UP/DOWN SCROLL   ENTER CONFIRM   ESC BACK"
	if m.reviewed.Updates.Problem != "" || !m.reviewed.Updates.Checked {
		foot = "UP/DOWN SCROLL   R CHECK READINESS   ESC BACK"
	}
	return m.updateScrollableFrame(rows, m.updateReviewRows(), foot)
}

func (m Model) updateResultRows() []string {
	s, width := m.styles, primaryContentWidth(m.width)
	var rows []string
	for i, item := range m.reviewed.Items {
		state, kind := "NOT RUN", statusMuted
		if i < len(m.stepResults) {
			switch m.stepResults[i].Status {
			case history.StatusSuccess:
				state, kind = "DONE", statusSuccess
			case history.StatusFailure:
				state, kind = "FAILED", statusDanger
			case history.StatusCancelled:
				state, kind = "CANCELLED", statusDanger
			}
		}
		rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), updateStepTitle(item), statusText(s, state, kind), width, false))
		if i < len(m.stepResults) && m.stepResults[i].Err != nil {
			rows = append(rows, stepFailureRows(s, m.stepResults[i], width, false)...)
		}
	}
	rows = append(rows, "")
	message := "Commands completed. Check terminal summaries for declined or failed app upgrades; not every application is verified."
	if m.runErr != nil {
		message = "Stopped at the failed step. Earlier changes remain applied. View the log or review a retry of the remaining steps."
	}
	for _, line := range wrapText(message, width) {
		rows = append(rows, s.attention.Render(line))
	}
	return rows
}

func (m Model) viewMiniUpdateResult() string {
	s, width := m.styles, primaryContentWidth(m.width)
	state := "STEPS COMPLETED"
	if m.runErr != nil {
		state = "STOPPED ON FAILURE"
	}
	if m.runCancelled {
		state = "CANCELLED"
	}
	rows := []string{screenTitle(s, "RESULT / MAC MINI", width), resultBanner(s, state) + "  " + formatRunElapsed(m.runElapsed), majorRule(s, width, true), ""}
	return m.updateScrollableFrame(rows, m.updateResultRows(), "UP/DOWN SCROLL   "+m.resultFooter(false))
}

func (m Model) updateScrollableFrame(header, body []string, footer string) string {
	s, width := m.styles, primaryContentWidth(m.width)
	height := m.height
	if height == 0 {
		height = 24
	}
	footerLines := wrapText(footer, width)
	capacity := max(1, height-len(header)-len(footerLines)-4)
	start := min(max(0, m.updatesOffset), max(0, len(body)-capacity))
	end := min(len(body), start+capacity)
	rows := append(append([]string(nil), header...), body[start:end]...)
	if end < len(body) || start > 0 {
		rows = append(rows, s.muted.Render(fmt.Sprintf("Lines %d-%d of %d", start+1, end, len(body))))
	} else {
		rows = append(rows, "")
	}
	rows = append(rows, majorRule(s, width, false))
	for _, line := range footerLines {
		rows = append(rows, s.muted.Render(line))
	}
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}
