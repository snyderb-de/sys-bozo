package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
)

// WriteCLI preserves plain, pipe-friendly output and styles interactive output
// with the same palette as the TUI. It never inspects environment values.
func WriteCLI(w *os.File, content string) {
	if !term.IsTerminal(w.Fd()) {
		fmt.Fprintln(w, strings.TrimRight(content, "\n"))
		return
	}
	s := newUIStyles(os.Getenv("NO_COLOR") != "")
	width, _, err := term.GetSize(w.Fd())
	if err != nil || width < 20 {
		width = 80
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	var out []string
	for i, line := range lines {
		if i == 0 {
			out = append(out, s.badge.Render("✦ "+line), "")
			continue
		}
		if strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " ") {
			out = append(out, s.title.Render(line))
			continue
		}
		if label, value, found := strings.Cut(line, ":"); found && !strings.HasPrefix(line, " ") {
			out = append(out, s.label.Render(label+":")+s.text.Render(value))
			continue
		}
		out = append(out, s.text.Render(line))
	}
	fmt.Fprintln(w, strings.Join(out, "\n"))
	fmt.Fprintln(w, s.rule.Render(strings.Repeat("─", min(width, 80))))
}
