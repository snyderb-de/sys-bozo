package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

func miniUpdateModel() Model {
	m := testGuidedModel()
	m.runCtx = runner.Context{OS: "darwin", Hostname: "bags-Mac-mini", Repo: "/fixture", NixBin: "nix", NixStoreBin: "nix-store", DarwinRebuild: "darwin-rebuild", HomeManager: "home-manager", SudoBin: "sudo", BrewBin: "brew", Topgrade: "topgrade", BrewOutdatedCasks: []string{"displaylink", "zed"}}
	m.tasks = runner.DefaultTasks(m.runCtx)
	m.width, m.height = 80, 24
	m.styles = newUIStyles(true)
	m.openMaintenance()
	return m
}

func TestMiniRecommendedSelectionDoesNotExecute(t *testing.T) {
	m := miniUpdateModel()
	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := next.(Model)
	if cmd != nil || got.screen != screenMaintenance || len(got.queue) != 0 {
		t.Fatal("selecting recommendations must stay in the picker without executing")
	}
	for _, id := range []string{"nix-update", "nds", "topgrade"} {
		if !got.selected[id] {
			t.Errorf("recommended action %s was not selected", id)
		}
	}
	for _, id := range []string{"brew", "brew-cleanup", "displaylink", "ndR", "hmr", "hms"} {
		if got.selected[id] {
			t.Errorf("optional, duplicate, or recovery action %s was selected", id)
		}
	}
	view := got.View()
	for _, label := range []string{"Update Nix version pins", "Apply Mac mini configuration", "Run Topgrade", "RECOMMENDED"} {
		if !strings.Contains(view, label) {
			t.Errorf("missing update description %q:\n%s", label, view)
		}
	}
	if lipgloss.Width(view) > 80 || strings.Count(view, "\n")+1 > 24 || strings.Contains(view, "\x1b[") {
		t.Fatalf("picker exceeds 80x24 or NO_COLOR boundary:\n%s", view)
	}
}

func TestMiniRecoveryClearsUpdateSelection(t *testing.T) {
	m := miniUpdateModel()
	m.selected = map[string]bool{"nix-update": true, "nds": true, "topgrade": true}
	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := next.(Model)
	if cmd != nil || len(got.selected) != 0 || got.screen != screenMaintenance {
		t.Fatal("recovery must clear routine update selection without executing")
	}
	view := got.View()
	if !strings.Contains(view, "RECOVERY") || strings.Contains(view, "Update Nix version pins") {
		t.Fatalf("recovery is mixed with routine updates:\n%s", view)
	}
}

func miniFixtureModel(t *testing.T) Model {
	t.Helper()
	m := miniUpdateModel()
	m.runCtx.Repo = initTUIRepo(t)
	if err := os.WriteFile(filepath.Join(m.runCtx.Repo, "flake.nix"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTUIOutput(t, m.runCtx.Repo, "add", "flake.nix")
	gitTUIOutput(t, m.runCtx.Repo, "commit", "-qm", "fixture flake")
	bin := t.TempDir()
	for _, name := range []string{"nix", "nix-store", "sudo", "darwin-rebuild", "brew", "topgrade"} {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'harmless fixture output\\n'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	m.runCtx.NixBin, m.runCtx.NixStoreBin = filepath.Join(bin, "nix"), filepath.Join(bin, "nix-store")
	m.runCtx.SudoBin, m.runCtx.DarwinRebuild = filepath.Join(bin, "sudo"), filepath.Join(bin, "darwin-rebuild")
	m.runCtx.BrewBin, m.runCtx.Topgrade = filepath.Join(bin, "brew"), filepath.Join(bin, "topgrade")
	return m
}

func miniReviewedModel(t *testing.T) Model {
	t.Helper()
	m := miniFixtureModel(t)
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = next.(Model)
	next, check := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if check == nil || m.screen != screenReview || len(m.queue) != 0 {
		t.Fatal("Review must check readiness before execution")
	}
	next, execute := m.Update(check())
	m = next.(Model)
	if execute != nil || !m.reviewed.Updates.Checked || m.reviewed.Updates.Checking || len(m.queue) != 0 {
		t.Fatal("readiness check must settle in Review without executing")
	}
	return m
}

func TestMiniReviewRejectsRepositoryChangesAfterReview(t *testing.T) {
	m := miniReviewedModel(t)
	if err := os.WriteFile(filepath.Join(m.runCtx.Repo, "tracked.txt"), []byte("changed after review\n"), 0600); err != nil {
		t.Fatal(err)
	}
	next, check := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if check == nil || len(m.queue) != 0 {
		t.Fatal("confirmation must recheck readiness before any update")
	}
	next, execute := m.Update(check())
	m = next.(Model)
	if execute != nil || len(m.queue) != 0 || m.screen != screenReview || m.reviewed.Updates.Problem == "" {
		t.Fatal("a stale review must not execute")
	}
}

func TestMiniReviewScrollsEveryCommandAt80x24(t *testing.T) {
	m := miniReviewedModel(t)
	first := m.View()
	if os.Getenv("MINI_VISUAL_LOG") != "" {
		t.Logf("\n%s", first)
	}
	for _, item := range m.reviewed.Items {
		if !strings.Contains(strings.Join(strings.Fields(strings.Join(m.updateReviewRows(), "\n")), ""), strings.Join(strings.Fields(runner.CmdLabel(item)), "")) {
			t.Fatalf("review dropped command %s", item.Title)
		}
	}
	next, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEnd})
	m = next.(Model)
	last := m.View()
	if first == last || !strings.Contains(last, "Check Homebrew dependencies") {
		t.Fatalf("cannot scroll to final check:\n%s", last)
	}
	for _, view := range []string{first, last} {
		if lipgloss.Width(view) > 80 || strings.Count(view, "\n")+1 > 24 {
			t.Fatalf("review exceeds 80x24:\n%s", view)
		}
	}
}

func TestMiniResultRetryRetainsReviewedTailAndReadinessGate(t *testing.T) {
	m := miniReviewedModel(t)
	m.queue = cloneWorkItems(m.reviewed.Items)
	m.mode, m.screen = modeDone, screenResult
	m.stepResults = []stepResult{{Item: m.queue[0], Status: "success"}, {Item: m.queue[1], Status: "failure"}}
	next, execute := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = next.(Model)
	if execute != nil || m.screen != screenReview || m.reviewed.Updates == nil || m.reviewed.Updates.Checked {
		t.Fatal("retry must retain the Mini review and request fresh readiness")
	}
	if len(m.reviewed.Items) != 5 || m.reviewed.Items[0].Title != "Refresh Homebrew metadata" {
		t.Fatal("retry repeated a completed update or dropped remaining steps")
	}
}
