package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/snyderb-de/sys-bozo/internal/repostate"
)

func TestHomeRepositoryAlert(t *testing.T) {
	for _, plain := range []bool{true, false} {
		m := studioFixture()
		m.screen = screenHome
		m.width, m.height = 80, 24
		m.styles = newUIStyles(plain)
		m.facts.DotfilesDirty = 1
		for _, want := range []string{"! Uncommitted changes · 1 file", "G Review changes", "dotfiles and diffs"} {
			if !strings.Contains(m.View(), want) {
				t.Fatalf("missing %q in home alert", want)
			}
		}
		if lipgloss.Height(m.rawView()) > 24 {
			t.Fatal("alert pushed home controls offscreen")
		}
		if plain && strings.Contains(m.View(), "\x1b") {
			t.Fatal("NO_COLOR alert contains ANSI")
		}
		m.facts.DotfilesDirty = 0
		if strings.Contains(m.View(), "Uncommitted changes") || strings.Contains(m.View(), "G Review changes") {
			t.Fatal("clean repo shows change alert")
		}
		m.facts.DotfilesStatusUnavailable = true
		if !strings.Contains(m.View(), "Dotfiles status unavailable") || strings.Contains(m.View(), "Uncommitted changes") {
			t.Fatal("failed status presented as known changes")
		}
	}
}

func TestHomeAlertShortcutOpensReadOnlyFilesAndReturns(t *testing.T) {
	for _, key := range []string{"g", "G"} {
		m := testGuidedModel()
		m.width, m.height = 80, 24
		m.facts.DotfilesDirty = 1
		m.homeCursor = 2 // Shortcut does not depend on which row has focus.
		m.repoFlow.tab = repoDiff
		m.inspectRepo = func(context.Context, string) repostate.Status {
			return repostate.Status{Entries: []repostate.Entry{{Path: "flake.lock", Worktree: repostate.StateModified}}}
		}
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		m = next.(Model)
		if m.screen != screenRepoTriage || m.repoFlow.tab != repoFiles || len(m.reviewed.Items) != 0 || cmd == nil {
			t.Fatal("shortcut did not open file review")
		}
		next, _ = m.Update(cmd())
		m = next.(Model)
		if len(m.repoFlow.status.Entries) != 1 {
			t.Fatal("files did not load")
		}
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if next.(Model).screen != screenHome {
			t.Fatal("Escape did not return home")
		}
	}
}

func TestHomeAlertRefreshesOnReturnAndClearsAfterCleanStatus(t *testing.T) {
	m := testGuidedModel()
	m.screen = screenInspect
	m.width, m.height = 80, 24
	m.inspectRepo = func(context.Context, string) repostate.Status {
		return repostate.Status{Entries: []repostate.Entry{{Path: "flake.lock", Worktree: repostate.StateModified}}}
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("return home did not schedule a repository refresh")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.facts.DotfilesDirty != 1 {
		t.Fatal("new changes did not reach home alert")
	}
	m.homeRepoFocused = true
	m.acceptRepoStatus(repoStatusMsg{requestID: m.repoFlow.requestID, status: repostate.Status{}})
	if factsRepoActionable(m.facts) || m.homeRepoFocused {
		t.Fatal("clean status left stale alert or focus")
	}
	m.acceptRepoStatus(repoStatusMsg{requestID: m.repoFlow.requestID, status: repostate.Status{Err: errors.New("fixture status failed")}})
	if !m.facts.DotfilesStatusUnavailable {
		t.Fatal("status failure was hidden")
	}
}
