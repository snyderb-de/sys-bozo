package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ── Config ────────────────────────────────────────────────────────────────

func (m Model) viewConfig(w int) string {
	cw := innerWidth(w)

	var rows []string
	rows = append(rows, styleTitle.Render("Config")+"  "+styleFaint.Render("enter edit · j/k move"))
	rows = append(rows, "")

	for i, f := range m.configFiles {
		cur := "  "
		var labelStyle lipgloss.Style
		if i == m.configCursor {
			cur = styleCursor.Render("▶ ")
			labelStyle = styleActionLabel
		} else {
			labelStyle = styleActionAvail
		}
		label := labelStyle.Render(fmt.Sprintf("%-42s", f.label))
		hint := styleFaint.Render(f.hint)
		rows = append(rows, cur+label+"  "+hint)
	}

	if m.applyPrompt {
		rows = append(rows, "")
		rows = append(rows, styleWarn.Render("  apply changes?")+"  "+
			styleFaint.Render("[h]ms  [n]ds  [b]oth  any other key = skip"))
	}

	return styleCard.Width(cw).Render(strings.Join(rows, "\n"))
}

// ── Audit ─────────────────────────────────────────────────────────────────

func (m Model) viewAudit(w int) string {
	cw := innerWidth(w)
	header := styleTitle.Render("Local Audit") + "  " + styleFaint.Render("a rescan")

	if !m.auditReady {
		content := strings.Join([]string{
			header,
			"",
			styleMuted.Render("  scanning..."),
		}, "\n")
		return styleCard.Width(cw).Render(content)
	}

	var rows []string
	rows = append(rows, header)
	rows = append(rows, "")

	rows = append(rows, stylePurple.Render("  Config files"))
	configCount := 0
	for _, item := range m.auditItems {
		if configCount == 6 {
			rows = append(rows, "")
			rows = append(rows, stylePurple.Render("  Tools"))
		}
		icon := styleGood.Render("  ✓")
		detail := styleFaint.Render(item.Detail)
		if !item.OK {
			icon = styleErr.Render("  ✗")
			detail = styleWarn.Render(item.Detail)
		}
		rows = append(rows, fmt.Sprintf("%s  %-18s %s", icon, item.Name, detail))
		if !item.OK {
			rows = append(rows, auditHelpRows("why", item.Description, cw-8, styleFaint)...)
			rows = append(rows, auditHelpRows("fix", item.Fix, cw-8, styleMuted)...)
		}
		configCount++
	}

	ok, fail := 0, 0
	for _, item := range m.auditItems {
		if item.OK {
			ok++
		} else {
			fail++
		}
	}
	rows = append(rows, "")
	summary := styleGood.Render(fmt.Sprintf("  %d ok", ok))
	if fail > 0 {
		summary += "  " + styleErr.Render(fmt.Sprintf("%d issues", fail))
	}
	rows = append(rows, summary)

	return styleCard.Width(cw).Render(strings.Join(rows, "\n"))
}

func auditHelpRows(label, text string, width int, style lipgloss.Style) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	prefix := "     " + label + ": "
	continuation := strings.Repeat(" ", lipgloss.Width(prefix))
	lineWidth := max(24, width-lipgloss.Width(prefix))
	wrapped := wrapWords(text, lineWidth)

	rows := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		if i == 0 {
			rows = append(rows, style.Render(prefix+line))
		} else {
			rows = append(rows, style.Render(continuation+line))
		}
	}
	return rows
}

func wrapWords(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		if lipgloss.Width(line)+1+lipgloss.Width(word) > width {
			lines = append(lines, line)
			line = word
			continue
		}
		line += " " + word
	}
	lines = append(lines, line)
	return lines
}

// ── Doctor ────────────────────────────────────────────────────────────────

func (m Model) viewDoctor(w int) string {
	cw := innerWidth(w)
	f := m.facts

	var rows []string
	rows = append(rows, styleTitle.Render("Doctor"))
	rows = append(rows, "")

	rows = append(rows, stylePurple.Render("  Dotfiles"))
	branch := f.DotfilesBranch
	if branch == "" {
		branch = styleMuted.Render("unknown")
	}
	rows = append(rows, rowStyled("    branch    ", branch))
	if f.DotfilesDirty == 0 {
		rows = append(rows, rowStyled("    dirty     ", styleGood.Render("clean")))
	} else {
		rows = append(rows, rowStyled("    dirty     ", styleWarn.Render(fmt.Sprintf("%d files", f.DotfilesDirty))))
	}
	rows = append(rows, "")

	rows = append(rows, stylePurple.Render("  Home Manager"))
	gen := f.HMGeneration
	if gen == "" || gen == "none" {
		rows = append(rows, rowStyled("    generation", styleErr.Render("none — run hms")))
	} else {
		rows = append(rows, rowStyled("    generation", styleGood.Render(gen)))
	}
	rows = append(rows, "")

	rows = append(rows, stylePurple.Render("  Secrets"))
	if f.AgeKeyExists {
		rows = append(rows, rowStyled("    age key   ", styleGood.Render("present")))
	} else {
		rows = append(rows, rowStyled("    age key   ", styleErr.Render("missing · ~/.config/sops/age/keys.txt")))
	}
	rows = append(rows, "")

	rows = append(rows, stylePurple.Render("  SSH"))
	if f.GitHubKeyExists {
		rows = append(rows, rowStyled("    github key", styleGood.Render("present")))
	} else {
		rows = append(rows, rowStyled("    github key", styleErr.Render("missing")))
	}
	rows = append(rows, "")

	rows = append(rows, stylePurple.Render("  Package Managers"))
	for _, check := range []struct{ name, path string }{
		{"nix         ", f.NixPath},
		{"home-manager", f.HomeManager},
		{"topgrade    ", f.Topgrade},
	} {
		if check.path == "" {
			rows = append(rows, rowStyled("    "+check.name, styleErr.Render("missing")))
		} else {
			rows = append(rows, rowStyled("    "+check.name, styleGood.Render(shortPath(check.path))))
		}
	}
	if f.OS == "linux" && f.OSID == "fedora" {
		for _, check := range []struct{ name, path string }{
			{"dnf         ", f.DnfPath},
			{"sudo        ", f.SudoPath},
		} {
			if check.path == "" {
				rows = append(rows, rowStyled("    "+check.name, styleErr.Render("missing")))
			} else {
				rows = append(rows, rowStyled("    "+check.name, styleGood.Render(shortPath(check.path))))
			}
		}
	}
	if f.OS == "darwin" {
		for _, check := range []struct{ name, path string }{
			{"brew        ", f.BrewPath},
			{"nix-darwin  ", f.DarwinRebuild},
		} {
			if check.path == "" {
				rows = append(rows, rowStyled("    "+check.name, styleErr.Render("missing")))
			} else {
				rows = append(rows, rowStyled("    "+check.name, styleGood.Render(shortPath(check.path))))
			}
		}
	}

	if f.TailscaleIP != "" {
		rows = append(rows, "")
		rows = append(rows, stylePurple.Render("  Network"))
		rows = append(rows, rowStyled("    tailscale ", styleGood.Render(f.TailscaleIP)))
	}

	return styleCard.Width(cw).Render(strings.Join(rows, "\n"))
}
