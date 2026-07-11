package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/snyderb-de/sys-bozo/internal/repostate"
)

func (m Model) viewRepoTriage() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	filesTab, diffTab := "FILES", "DIFF"
	if m.repoFlow.tab == repoFiles {
		filesTab = "[FILES]"
	} else {
		diffTab = "[DIFF]"
	}
	rows := []string{
		s.major.Render("REPO/TRIAGE"),
		s.label.Render("EXACT WORKTREE STATE") + "   " + s.text.Render(filesTab+"  "+diffTab),
		majorRule(s, contentWidth, true),
	}

	if m.repoFlow.status.Err != nil {
		rows = append(rows, "", statusText(s, "STATUS UNAVAILABLE", statusDanger), s.muted.Render("Git status failed; no clean state assumed."))
	} else if m.repoFlow.loading && len(m.repoFlow.status.Entries) == 0 {
		rows = append(rows, "", s.muted.Render("SCANNING EXACT PATHS…"))
	} else if m.repoFlow.tab == repoDiff {
		rows = append(rows, "", m.repoFlow.diffVP.View())
	} else if len(m.repoFlow.status.Entries) == 0 {
		rows = append(rows, "", statusText(s, "CLEAN", statusSuccess), s.muted.Render("No tracked or untracked changes."))
	} else {
		visible := min(12, len(m.repoFlow.status.Entries))
		start := 0
		if m.repoFlow.cursor >= visible {
			start = m.repoFlow.cursor - visible + 1
		}
		for i := start; i < start+visible; i++ {
			entry := m.repoFlow.status.Entries[i]
			marker := "[ ]"
			if m.repoFlow.selected[repoEntryID(entry)] {
				marker = "[x]"
			}
			pointer := " "
			if i == m.repoFlow.cursor {
				pointer = ">"
			}
			state := repoStateLabel(entry)
			pathWidth := max(8, contentWidth-len(marker)-len(pointer)-len(state)-8)
			path := truncateVisible(displayRepoPath(entry.Path), pathWidth)
			rows = append(rows, fmt.Sprintf("%s %s %-*s  %s", pointer, marker, pathWidth, path, state))
		}
	}

	if m.repoFlow.notice != "" {
		rows = append(rows, s.attention.Render(truncateVisible(m.repoFlow.notice, contentWidth)))
	}
	footer := "J/K ↑/↓ MOVE   SPACE SELECT   ENTER DIFF   TAB SWITCH   R REFRESH   ESC BACK"
	if m.repoFlow.tab == repoDiff {
		footer = "J/K ↑/↓ SCROLL   TAB FILES   R REFRESH   ESC BACK"
	}
	rows = append(rows, majorRule(s, contentWidth, false), s.muted.Render(footer))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func repoStateLabel(entry repostate.Entry) string {
	if entry.Index == repostate.StateUntracked || entry.Worktree == repostate.StateUntracked {
		return "UNTRACKED"
	}
	if entry.Index == repostate.StateUnmerged || entry.Worktree == repostate.StateUnmerged || entry.Kind == 'u' {
		return "CONFLICT"
	}
	state := entry.Worktree
	if state == repostate.StateUnmodified {
		state = entry.Index
	}
	switch state {
	case repostate.StateModified:
		return "MODIFIED"
	case repostate.StateAdded:
		return "ADDED"
	case repostate.StateDeleted:
		return "DELETED"
	case repostate.StateRenamed:
		return "RENAMED"
	case repostate.StateCopied:
		return "COPIED"
	default:
		return fmt.Sprintf("INDEX %c / WORKTREE %c", entry.Index, entry.Worktree)
	}
}

func displayRepoPath(path string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, path)
}

func renderRepoPreview(preview repostate.Preview) string {
	if preview.Detail != "" && preview.Staged == "" && preview.Unstaged == "" {
		return strings.ToUpper(preview.Detail)
	}
	var rows []string
	if preview.Staged != "" {
		rows = append(rows, "STAGED", preview.Staged)
	}
	if preview.Unstaged != "" {
		if len(rows) > 0 {
			rows = append(rows, "")
		}
		rows = append(rows, "UNSTAGED", preview.Unstaged)
	}
	if len(rows) == 0 {
		return "NO TEXTUAL DIFF"
	}
	return strings.Join(rows, "\n")
}
