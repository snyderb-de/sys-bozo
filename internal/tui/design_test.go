package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/snyderb-de/sys-bozo/internal/history"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

func studioFixture() Model {
	m := miniUpdateModel()
	m.styles = newUIStyles(false)
	m.facts = system.Facts{User: "operator", Hostname: "studio-mini", OS: "darwin", Arch: "arm64", DotfilesBranch: "main", DotfilesDirty: 3, BrewOutdated: 7, BrewPath: "brew", NixPath: "nix", HMGeneration: "generation 42"}
	m.latestHistory = &history.Entry{Action: "recommended", Status: history.StatusSuccess, Ts: time.Date(2026, 9, 25, 9, 41, 0, 0, time.UTC)}
	m.selected = map[string]bool{"nix-update": true, "nds": true, "topgrade": true}
	return m
}

func TestStudioScreenSizesAndNoColor(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	for _, size := range [][2]int{{60, 24}, {80, 24}, {120, 36}, {180, 45}} {
		for _, plain := range []bool{true, false} {
			for _, screen := range []screen{screenHome, screenMaintenance, screenInspect, screenPackage, screenRunning} {
				m := studioFixture()
				m.width, m.height = size[0], size[1]
				m.screen = screen
				m.styles = newUIStyles(plain)
				m.packageFlow = newPackageFlow(m.width)
				m.queue = []runner.WorkItem{{Name: "nix", Args: []string{"flake", "update"}}, {Name: "darwin-rebuild", Args: []string{"switch"}}}
				m.queuePos = 1
				m.runStart = time.Now()
				out := m.View()
				if lipgloss.Width(out) > m.width || lipgloss.Height(out) > m.height {
					t.Fatalf("screen %d plain=%v %dx%d rendered %dx%d:\n%s", screen, plain, m.width, m.height, lipgloss.Width(out), lipgloss.Height(out), out)
				}
				if plain && strings.Contains(out, "\x1b") {
					t.Fatalf("screen %d adds ANSI under NO_COLOR", screen)
				}
			}
		}
	}
}

func TestHelpDoesNotExecuteAndPreservesTextInput(t *testing.T) {
	m := studioFixture()
	m.width, m.height = 80, 24
	m.screen = screenReview
	m.reviewed = reviewedPlan{Items: []runner.WorkItem{{Name: "must-not-execute"}}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = next.(Model)
	if !m.helpVisible || cmd != nil {
		t.Fatal("help should open without executing")
	}
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.helpVisible || cmd != nil || m.screen != screenReview {
		t.Fatal("Enter in help executed or navigated")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.helpVisible || m.screen != screenReview {
		t.Fatal("help did not return to review")
	}
	m.openPackageFlow()
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = next.(Model)
	if m.helpVisible || m.packageFlow.query.Value() != "?" {
		t.Fatal("help intercepted query text")
	}
}

func TestLongReviewPagesExposeEveryCommand(t *testing.T) {
	m := testGuidedModel()
	m.styles = newUIStyles(true)
	m.width, m.height = 80, 24
	m.screen = screenReview
	for i := 0; i < 35; i++ {
		m.reviewed.Items = append(m.reviewed.Items, runner.WorkItem{Name: fmt.Sprintf("fixture-command-%02d", i)})
	}
	var seen strings.Builder
	for i := 0; i < 20; i++ {
		out := m.View()
		seen.WriteString(out)
		if lipgloss.Height(out) > 24 || !strings.Contains(out, "ENTER CONFIRM") {
			t.Fatal("paging lost footer or overflowed")
		}
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		if cmd != nil {
			t.Fatal("scroll started execution")
		}
		m = next.(Model)
	}
	for _, item := range m.reviewed.Items {
		if !strings.Contains(seen.String(), item.Name) {
			t.Errorf("command never visible: %s", item.Name)
		}
	}
}

func TestStyledTruncationKeepsEmojiAndANSIIntact(t *testing.T) {
	s := newUIStyles(false)
	for width := 1; width < 32; width++ {
		out := truncateVisible(s.active.Render("📦 👩🏽‍💻 package é description"), width)
		if lipgloss.Width(out) > width {
			t.Fatalf("width %d overflowed", width)
		}
		if strings.Contains(ansi.Strip(out), "\x1b") {
			t.Fatal("broken escape sequence")
		}
	}
}

// Optional fixture gallery: no probing, package search, updater, or real home.
func TestRenderStudioGallery(t *testing.T) {
	dir := os.Getenv("BOZO_RENDER_DIR")
	if dir == "" {
		t.Skip("set BOZO_RENDER_DIR to capture fixture views")
	}
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)
	type capture struct {
		Name          string
		Width, Height int
		ANSI          string
	}
	var captures []capture
	for _, size := range [][2]int{{80, 24}, {120, 36}} {
		for _, target := range []struct {
			name   string
			screen screen
		}{{"Home", screenHome}, {"Updates", screenMaintenance}, {"Inspect", screenInspect}, {"Packages", screenPackage}, {"Running", screenRunning}} {
			m := studioFixture()
			m.width, m.height = size[0], size[1]
			m.screen = target.screen
			m.packageFlow = newPackageFlow(m.width)
			m.queue = []runner.WorkItem{{Name: "nix", Args: []string{"flake", "update"}}, {Name: "darwin-rebuild", Args: []string{"switch", "--flake", ".#studio-mini"}}, {Name: "topgrade"}}
			m.logVP = viewport.New(m.width-6, 5)
			m.logVP.SetContent("✓ Version pins refreshed\n$ darwin-rebuild switch --flake .#studio-mini\nBuilding the system configuration…")
			m.queuePos = 1
			m.runStart = time.Now().Add(-42 * time.Second)
			captures = append(captures, capture{target.name, m.width, m.height, m.View()})
		}
	}
	data, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "screens.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestTinyTerminalBlocksHiddenConfirmation(t *testing.T) {
	m := testGuidedModel()
	m.width, m.height = 30, 8
	m.screen = screenReview
	m.reviewed = reviewedPlan{Items: []runner.WorkItem{{Name: "must-not-execute"}}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || next.(Model).screen != screenReview {
		t.Fatal("small terminal confirmed an invisible plan")
	}
	if out := m.View(); lipgloss.Width(out) > 30 || !strings.Contains(out, "Resize") {
		t.Fatalf("bad resize guidance: %q", out)
	}
}

func TestWriteCLIPipePreservesPlainText(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	want := "sys-bozo doctor\nhost: fixture\n"
	WriteCLI(writer, want)
	writer.Close()
	data := make([]byte, 1024)
	n, err := reader.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:n]) != want {
		t.Fatalf("pipe output changed: %q", data[:n])
	}
}

func TestLongMenuKeepsKeyboardFocusVisible(t *testing.T) {
	m := testGuidedModel()
	m.styles = newUIStyles(true)
	m.width, m.height = 80, 24
	m.screen = screenConfig
	for i := 0; i < 35; i++ {
		m.configFiles = append(m.configFiles, configFile{label: fmt.Sprintf("fixture-%02d.nix", i)})
	}
	for i := 0; i < 34; i++ {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		if cmd != nil {
			t.Fatal("moving focus invoked an action")
		}
		m = next.(Model)
	}
	if !strings.Contains(m.View(), "fixture-34.nix") {
		t.Fatal("focused config row is outside the visible page")
	}
}
