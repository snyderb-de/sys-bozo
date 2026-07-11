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
	if m.repoFlow.stage == repoCommitMessage {
		rows = append(rows, "", s.title.Render("COMMIT MESSAGE"), s.muted.Render("One line, 200 UTF-8 bytes maximum."), m.repoFlow.commitInput.View(), "", s.muted.Render("ENTER CONTINUE TO REVIEW   ESC CANCEL"))
		if m.repoFlow.notice != "" {
			rows = append(rows, s.attention.Render(truncateVisible(m.repoFlow.notice, contentWidth)))
		}
		return primaryFrame(s, m.width, strings.Join(rows, "\n"))
	}
	if m.repoFlow.stage == repoDeleteConfirm {
		rows = append(rows, "", statusText(s, "DESTRUCTIVE — UNTRACKED ONLY", statusDanger))
		for _, entry := range m.selectedRepoEntries() {
			rows = append(rows, "  "+truncateVisible(displayRepoPath(entry.Path), max(8, contentWidth-2)))
		}
		rows = append(rows, "", s.label.Render("DRY RUN"), truncateVisible(displayRepoPath(m.repoFlow.deleteDryRun), contentWidth), "", s.text.Render("Type DELETE UNTRACKED exactly, then Enter."), m.repoFlow.deleteInput.View())
		if m.repoFlow.notice != "" {
			rows = append(rows, s.attention.Render(m.repoFlow.notice))
		}
		rows = append(rows, s.muted.Render("ESC CANCEL"))
		return primaryFrame(s, m.width, strings.Join(rows, "\n"))
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
	actions := repoActionFooter(m)
	footer := "J/K ↑/↓ MOVE   SPACE SELECT   ENTER DIFF"
	if m.repoFlow.tab == repoDiff {
		footer = "J/K ↑/↓ SCROLL   TAB FILES"
	}
	rows = append(rows, majorRule(s, contentWidth, false), s.text.Render(truncateVisible(actions, contentWidth)), s.muted.Render(truncateVisible(footer+"   shift+R REFRESH   ESC BACK", contentWidth)))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func repoActionFooter(m Model) string {
	label := func(key, name string, kind repostate.ActionKind) string {
		if m.repoActionAllowed(kind) {
			return key + " " + name
		}
		return key + " " + name + " [disabled]"
	}
	return strings.Join([]string{
		label("C", "COMMIT", repostate.ActionCommit),
		label("S", "STASH", repostate.ActionStash),
		label("R", "RESTORE", repostate.ActionRestore),
		label("D", "DELETE", repostate.ActionDeleteUntracked),
	}, "   ")
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
