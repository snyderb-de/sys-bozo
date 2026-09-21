package tui

import (
	"fmt"
	"strings"

	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

func (m Model) viewMiniUpdates() string {
	s, width := m.styles, primaryContentWidth(m.width)
	title, subtitle := "UPDATES / MAC MINI", "Recommended order: version pins, configuration, Topgrade."
	if m.updatesRecovery {
		title, subtitle = "RECOVERY / MAC MINI", "Recovery is separate from routine updates."
	}
	rows := []string{s.major.Render(title), s.muted.Render(subtitle), majorRule(s, width, true), ""}
	options := m.miniUpdateOptions()
	for i, option := range options {
		mark, state, kind := "[ ] ", "OPTIONAL", statusMuted
		if option.Recommended {
			state = "RECOMMENDED"
		}
		if option.Recovery {
			state = "RECOVERY"
		}
		if !option.Available {
			state = "UNAVAILABLE"
		}
		if m.selected[option.ID] {
			mark, state, kind = "[x] ", "SELECTED", statusActive
		}
		rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), mark+option.Label, statusText(s, state, kind), width, i == m.cursor))
	}
	rows = append(rows, "", majorRule(s, width, false))
	if m.cursor >= 0 && m.cursor < len(options) {
		option := options[m.cursor]
		for _, line := range wrapText(option.Description+" "+option.Detail, width) {
			rows = append(rows, s.text.Render(line))
		}
	}
	if m.updatesNotice != "" {
		rows = append(rows, s.attention.Render(truncateVisible(m.updatesNotice, width)))
	}
	footer := "A SELECT RECOMMENDED   SPACE TOGGLE   ENTER REVIEW"
	other := "TAB RECOVERY   ESC BACK"
	if m.updatesRecovery {
		footer, other = "SPACE SELECT   ENTER REVIEW", "TAB UPDATES   ESC BACK"
	}
	rows = append(rows, "", s.active.Render(footer), s.muted.Render(other))
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
	rows := []string{s.major.Render("REVIEW / MAC MINI"), s.muted.Render("Exact execution order. Nothing runs until confirmation."), majorRule(s, width, true)}
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
	rows := []string{s.major.Render("RESULT / MAC MINI"), s.title.Render(state) + "  " + formatRunElapsed(m.runElapsed), majorRule(s, width, true), ""}
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
