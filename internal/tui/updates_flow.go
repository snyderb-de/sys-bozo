package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

type updatesReview struct {
	Notes             []string
	Readiness         runner.UpdateReadiness
	Checking, Checked bool
	Problem           string
}

type updatesCheckedMsg struct {
	ID        uint64
	Confirm   bool
	Readiness runner.UpdateReadiness
	Err       error
}

func (m Model) miniUpdateOptions() []runner.UpdateOption {
	var options []runner.UpdateOption
	for _, option := range runner.MiniUpdateOptions(m.runCtx) {
		if option.Recovery == m.updatesRecovery {
			options = append(options, option)
		}
	}
	return options
}

func (m Model) handleMiniUpdatesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	options := m.miniUpdateOptions()
	switch strings.ToLower(msg.String()) {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.screen = screenHome
		return m, nil
	case "tab", "shift+tab":
		m.updatesRecovery = !m.updatesRecovery
		m.selected, m.cursor, m.updatesNotice = map[string]bool{}, 0, ""
	case "a":
		if !m.updatesRecovery {
			m.selected = map[string]bool{}
			for _, id := range runner.RecommendedMiniUpdates(m.runCtx) {
				m.selected[id] = true
			}
			m.updatesNotice = "Recommended options selected. Enter reviews the exact order."
		}
	case "j", "down":
		m.cursor = (m.cursor + 1) % len(options)
		m.updatesNotice = ""
	case "k", "up":
		m.cursor = (m.cursor - 1 + len(options)) % len(options)
		m.updatesNotice = ""
	case " ":
		m.toggleMiniUpdate()
	case "enter":
		if len(m.selectedUpdateIDs()) == 0 {
			m.toggleMiniUpdate()
		}
		return m, m.prepareMiniUpdatesReview()
	}
	return m, nil
}

func (m *Model) toggleMiniUpdate() {
	options := m.miniUpdateOptions()
	if m.cursor < 0 || m.cursor >= len(options) {
		return
	}
	option := options[m.cursor]
	if !option.Available {
		m.updatesNotice = "Unavailable: required tools or a pending update are missing."
		return
	}
	if m.selected == nil {
		m.selected = map[string]bool{}
	}
	m.selected[option.ID] = !m.selected[option.ID]
	if m.selected[option.ID] && (option.ID == "nds" || option.ID == "brew") {
		other := "brew"
		if option.ID == "brew" {
			other = "nds"
		}
		if m.selected[other] {
			m.updatesNotice = "Apply includes Homebrew review; choose Apply or Homebrew only."
		}
		delete(m.selected, other)
	}
}

func (m Model) selectedUpdateIDs() []string {
	var ids []string
	for _, option := range m.miniUpdateOptions() {
		if m.selected[option.ID] {
			ids = append(ids, option.ID)
		}
	}
	return ids
}

func (m *Model) prepareMiniUpdatesReview() tea.Cmd {
	ids := m.selectedUpdateIDs()
	p, err := runner.BuildMiniUpdates(m.runCtx, ids)
	if err != nil {
		m.updatesNotice = err.Error()
		return nil
	}
	m.reviewed = reviewedPlan{Action: strings.Join(ids, "+"), Items: cloneWorkItems(p.Items), Updates: &updatesReview{Notes: append([]string(nil), p.Notes...)}}
	m.updatesOffset = 0
	m.screen = screenReview
	return m.checkMiniUpdates(false)
}

func (m *Model) checkMiniUpdates(confirm bool) tea.Cmd {
	if m.reviewed.Updates == nil || m.reviewed.Updates.Checking {
		return nil
	}
	copy := *m.reviewed.Updates
	copy.Checking, copy.Problem = true, ""
	m.reviewed.Updates = &copy
	m.updatesCheckID++
	id, ctx, items := m.updatesCheckID, m.runCtx, cloneWorkItems(m.reviewed.Items)
	check := m.checkUpdates
	if check == nil {
		check = runner.CheckUpdateReadiness
	}
	return func() tea.Msg {
		readiness, err := check(ctx, items)
		return updatesCheckedMsg{ID: id, Confirm: confirm, Readiness: readiness, Err: err}
	}
}

func (m *Model) acceptUpdatesCheck(msg updatesCheckedMsg) tea.Cmd {
	if m.screen != screenReview || m.reviewed.Updates == nil || msg.ID != m.updatesCheckID {
		return nil
	}
	r := *m.reviewed.Updates
	r.Checking = false
	m.reviewed.Updates = &r
	if msg.Err != nil {
		r.Problem, r.Checked = msg.Err.Error(), false
		return nil
	}
	if msg.Confirm && r.Checked && r.Readiness.StatusHash != msg.Readiness.StatusHash {
		r.Problem, r.Checked = "Repository changed after Review. Refresh readiness and review again.", false
		return nil
	}
	wasChecked := r.Checked
	r.Readiness, r.Checked, r.Problem = msg.Readiness, true, ""
	m.facts.DotfilesDirty, m.facts.DotfilesStatusUnavailable = msg.Readiness.Dirty, false
	if !msg.Confirm || !wasChecked {
		return nil
	}
	m.beginReviewedRun()
	return tea.Batch(m.advanceQueue(), m.spinner.Tick)
}

func (m *Model) scrollUpdates(key string, lineCount int) bool {
	page := max(3, m.height-12)
	switch key {
	case "j", "down":
		m.updatesOffset++
	case "k", "up":
		m.updatesOffset--
	case "pgdown":
		m.updatesOffset += page
	case "pgup":
		m.updatesOffset -= page
	case "home":
		m.updatesOffset = 0
	case "end":
		m.updatesOffset = lineCount
	default:
		return false
	}
	m.updatesOffset = min(max(0, m.updatesOffset), max(0, lineCount-page))
	return true
}

func updateStepTitle(item runner.WorkItem) string {
	if item.Title != "" {
		return item.Title
	}
	return runner.CmdLabel(item)
}

func (m Model) updatesReadinessLabel() string {
	r := m.reviewed.Updates
	if r.Checking {
		return "Checking readiness; no update has run."
	}
	if r.Problem != "" {
		return "BLOCKED: " + r.Problem
	}
	if !r.Checked {
		return "Readiness must be checked before execution."
	}
	if r.Readiness.Dirty > 0 {
		return fmt.Sprintf("%d changed repository entries. Updates use these local changes.", r.Readiness.Dirty)
	}
	return "Required tools found. Repository is clean."
}
