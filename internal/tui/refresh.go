package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

type hostRefreshedMsg struct {
	id     uint64
	origin screen
	facts  system.Facts
	runCtx runner.Context
}

// External probes can take seconds. Keep them outside Bubble Tea's Update loop
// so navigation and quitting remain responsive throughout the refresh.
func (m *Model) refreshHostCmd() tea.Cmd {
	if m.refreshing {
		return nil
	}
	m.refreshing = true
	m.refreshID++
	id, origin := m.refreshID, m.screen
	return func() tea.Msg {
		facts := system.Probe()
		ctx := runner.Build()
		return hostRefreshedMsg{id: id, origin: origin, facts: facts, runCtx: ctx}
	}
}

func (m Model) acceptHostRefresh(msg hostRefreshedMsg) (tea.Model, tea.Cmd) {
	if !m.refreshing || msg.id != m.refreshID {
		return m, nil
	}
	m.refreshing = false
	// A delayed refresh must not alter the context of a different workflow,
	// a reviewed plan, an editor result, or a running command.
	if m.screen != msg.origin || m.mode != modeView || m.applyPrompt || len(m.reviewed.Items) > 0 {
		return m, nil
	}
	m.facts, m.runCtx = msg.facts, msg.runCtx
	m.tasks = runner.DefaultTasks(m.runCtx)
	m.configFiles = buildConfigFiles(m.runCtx)
	m.auditReady = false
	m.auditItems = nil
	if m.screen == screenAudit {
		return m, m.runAudit()
	}
	return m, nil
}
