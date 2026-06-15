package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testModelOnUpdatesTab() Model {
	return Model{
		tab:  1,
		tabs: []string{"Dashboard", "Updates", "Audit", "Doctor"},
		mode: modeView,
	}
}

func TestTabIntoAuditStartsScan(t *testing.T) {
	m := testModelOnUpdatesTab()

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	model := next.(Model)

	if model.tabs[model.tab] != "Audit" {
		t.Fatalf("expected Audit tab, got %q", model.tabs[model.tab])
	}
	if cmd == nil {
		t.Fatal("expected entering Audit with tab to start audit scan")
	}
}

func TestRefreshOnAuditRestartsScan(t *testing.T) {
	m := testModelOnUpdatesTab()
	m.tab = 2
	m.auditReady = true

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	model := next.(Model)

	if model.auditReady {
		t.Fatal("expected refresh to mark audit as not ready")
	}
	if cmd == nil {
		t.Fatal("expected refresh on Audit tab to start audit scan")
	}
}
