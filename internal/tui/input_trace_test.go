package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestInputTracePreservesInputAndRedactsText(t *testing.T) {
	const privateText = "private-query-never-log-this"
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte(privateText), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	m := testGuidedModel()
	m.width, m.height = 80, 24
	m.styles = newUIStyles(true)
	m.openPackageFlow()
	var log bytes.Buffer
	model, input := TraceInput(m, file, &log)
	terminal := input.(interface {
		Fd() uintptr
		Name() string
	})
	if terminal.Fd() != file.Fd() || terminal.Name() != file.Name() {
		t.Fatal("trace changed the terminal identity")
	}
	buf := make([]byte, 128)
	n, err := input.Read(buf)
	if err != nil || string(buf[:n]) != privateText {
		t.Fatal("trace changed the input")
	}
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(privateText)})
	if next.(tracedModel).packageFlow.query.Value() != privateText {
		t.Fatal("trace changed the query")
	}
	next.View()
	_, quit := next.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if quit == nil {
		t.Fatal("trace swallowed quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("trace changed quit")
	}
	if strings.Contains(log.String(), privateText) || strings.Contains(log.String(), path) {
		t.Fatal("trace leaked input text or its path")
	}
	for _, event := range []string{"input", "update_start", "update_done", "view_start", "view_done"} {
		if !strings.Contains(log.String(), `"event":"`+event+`"`) {
			t.Fatalf("missing %s timing", event)
		}
	}
}
