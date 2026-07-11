package tui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/repostate"
	"github.com/snyderb-de/sys-bozo/internal/system"
)

type repoTab uint8

const (
	repoFiles repoTab = iota
	repoDiff
)

type repoFlow struct {
	status       repostate.Status
	requestID    uint64
	loading      bool
	cursor       int
	selected     map[[32]byte]bool
	tab          repoTab
	preview      repostate.Preview
	diffVP       viewport.Model
	notice       string
	stage        repoStage
	commitInput  textinput.Model
	deleteInput  textinput.Model
	deleteDryRun string
}

type repoStage uint8

const (
	repoBrowse repoStage = iota
	repoCommitMessage
	repoDeleteConfirm
)

type repoReview struct {
	Operation  repostate.Operation
	Validating bool
	Notice     string
}

type repoStatusMsg struct {
	requestID uint64
	status    repostate.Status
}

type repoPreviewMsg struct {
	requestID   uint64
	fingerprint [32]byte
	preview     repostate.Preview
}

type repoActionPreparedMsg struct {
	requestID uint64
	operation repostate.Operation
	err       error
}

type repoDeleteDryRunMsg struct {
	requestID uint64
	output    string
	err       error
}

type repoValidatedMsg struct {
	requestID uint64
	err       error
}

func factsRepoActionable(facts system.Facts) bool {
	return facts.DotfilesDirty > 0 || facts.DotfilesStatusUnavailable
}

func (m Model) repoPath() string {
	if m.facts.DotfilesRepo != "" {
		return m.facts.DotfilesRepo
	}
	return m.runCtx.Repo
}

func (m *Model) openRepoTriage(returnTo screen) tea.Cmd {
	m.screen = screenRepoTriage
	m.repoReturn = returnTo
	if m.repoFlow.selected == nil {
		m.repoFlow.selected = map[[32]byte]bool{}
	}
	m.resizeRepoViewport()
	return m.refreshRepoStatus()
}

func (m *Model) refreshRepoStatus() tea.Cmd {
	m.repoFlow.requestID++
	m.repoFlow.loading = true
	m.repoFlow.notice = ""
	requestID := m.repoFlow.requestID
	inspect := m.inspectRepo
	repo := m.repoPath()
	if inspect == nil {
		m.repoFlow.loading = false
		return nil
	}
	return func() tea.Msg {
		return repoStatusMsg{requestID: requestID, status: inspect(context.Background(), repo)}
	}
}

func (m *Model) acceptRepoStatus(msg repoStatusMsg) {
	if msg.requestID != m.repoFlow.requestID {
		return
	}
	m.repoFlow.loading = false
	m.repoFlow.status = msg.status
	available := make(map[[32]byte]bool, len(msg.status.Entries))
	for _, entry := range msg.status.Entries {
		available[repoEntryID(entry)] = true
	}
	for id := range m.repoFlow.selected {
		if !available[id] {
			delete(m.repoFlow.selected, id)
		}
	}
	if len(msg.status.Entries) == 0 {
		m.repoFlow.cursor = 0
	} else if m.repoFlow.cursor >= len(msg.status.Entries) {
		m.repoFlow.cursor = len(msg.status.Entries) - 1
	}
}

func (m *Model) requestRepoPreview() tea.Cmd {
	entry, ok := m.focusedRepoEntry()
	if !ok || m.loadRepoPreview == nil {
		return nil
	}
	m.repoFlow.tab = repoDiff
	m.repoFlow.preview = repostate.Preview{Detail: "loading preview…"}
	m.resizeRepoViewport()
	requestID := m.repoFlow.requestID
	id := repoEntryID(entry)
	load := m.loadRepoPreview
	repo := m.repoPath()
	return func() tea.Msg {
		return repoPreviewMsg{requestID: requestID, fingerprint: id, preview: load(context.Background(), repo, entry)}
	}
}

func (m *Model) acceptRepoPreview(msg repoPreviewMsg) {
	if msg.requestID != m.repoFlow.requestID {
		return
	}
	entry, ok := m.focusedRepoEntry()
	if !ok || repoEntryID(entry) != msg.fingerprint {
		return
	}
	m.repoFlow.preview = msg.preview
	m.repoFlow.diffVP.SetContent(renderRepoPreview(msg.preview))
	m.repoFlow.diffVP.GotoTop()
}

func (m Model) focusedRepoEntry() (repostate.Entry, bool) {
	if m.repoFlow.cursor < 0 || m.repoFlow.cursor >= len(m.repoFlow.status.Entries) {
		return repostate.Entry{}, false
	}
	return m.repoFlow.status.Entries[m.repoFlow.cursor], true
}

func repoEntryID(entry repostate.Entry) [32]byte {
	if entry.DisplayFingerprint != ([32]byte{}) {
		return entry.DisplayFingerprint
	}
	return sha256.Sum256([]byte(fmt.Sprintf("%c\x00%c\x00%s\x00%s", entry.Index, entry.Worktree, entry.Path, entry.OriginalPath)))
}

func (m *Model) moveRepoCursor(delta int) {
	n := len(m.repoFlow.status.Entries)
	if n == 0 {
		return
	}
	m.repoFlow.cursor = (m.repoFlow.cursor + delta + n) % n
}

func (m *Model) toggleRepoSelection() {
	entry, ok := m.focusedRepoEntry()
	if !ok {
		return
	}
	if m.repoFlow.selected == nil {
		m.repoFlow.selected = map[[32]byte]bool{}
	}
	id := repoEntryID(entry)
	if m.repoFlow.selected[id] {
		delete(m.repoFlow.selected, id)
	} else {
		m.repoFlow.selected[id] = true
	}
}

func (m Model) handleRepoKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.repoFlow.stage == repoCommitMessage {
		switch msg.String() {
		case "esc":
			m.repoFlow.stage = repoBrowse
			return m, nil
		case "enter":
			return m, m.prepareRepoAction(repostate.ActionCommit, m.repoFlow.commitInput.Value(), false)
		}
		var cmd tea.Cmd
		m.repoFlow.commitInput, cmd = m.repoFlow.commitInput.Update(msg)
		return m, cmd
	}
	if m.repoFlow.stage == repoDeleteConfirm {
		switch msg.String() {
		case "esc":
			m.repoFlow.stage = repoBrowse
			return m, nil
		case "enter":
			if m.repoFlow.deleteInput.Value() != "DELETE UNTRACKED" {
				m.repoFlow.notice = "type DELETE UNTRACKED exactly"
				return m, nil
			}
			return m, m.prepareRepoAction(repostate.ActionDeleteUntracked, "", true)
		}
		var cmd tea.Cmd
		m.repoFlow.deleteInput, cmd = m.repoFlow.deleteInput.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "esc":
		m.screen = m.repoReturn
		if m.screen == screenRepoTriage {
			m.screen = screenHome
		}
		return m, nil
	case "R":
		return m, m.refreshRepoStatus()
	case "tab", "shift+tab":
		if m.repoFlow.tab == repoFiles {
			m.repoFlow.tab = repoDiff
		} else {
			m.repoFlow.tab = repoFiles
		}
		return m, nil
	case "c":
		if !m.repoActionAllowed(repostate.ActionCommit) {
			m.repoFlow.notice = "COMMIT disabled for this selection"
			return m, nil
		}
		input := textinput.New()
		input.Prompt = "MESSAGE  "
		input.CharLimit = 200
		input.Focus()
		m.repoFlow.commitInput = input
		m.repoFlow.stage = repoCommitMessage
		m.repoFlow.notice = ""
		return m, textinput.Blink
	case "s":
		if !m.repoActionAllowed(repostate.ActionStash) {
			m.repoFlow.notice = "STASH disabled for this selection"
			return m, nil
		}
		return m, m.prepareRepoAction(repostate.ActionStash, "", false)
	case "r":
		if !m.repoActionAllowed(repostate.ActionRestore) {
			m.repoFlow.notice = "RESTORE disabled for this selection"
			return m, nil
		}
		return m, m.prepareRepoAction(repostate.ActionRestore, "", false)
	case "d":
		if !m.repoActionAllowed(repostate.ActionDeleteUntracked) {
			m.repoFlow.notice = "DELETE UNTRACKED disabled for this selection"
			return m, nil
		}
		input := textinput.New()
		input.Prompt = "> "
		input.CharLimit = len("DELETE UNTRACKED")
		input.Focus()
		m.repoFlow.deleteInput = input
		m.repoFlow.deleteDryRun = "loading git clean -nd preview…"
		m.repoFlow.stage = repoDeleteConfirm
		m.repoFlow.notice = ""
		return m, m.repoDeleteDryRunCmd()
	}

	if m.repoFlow.tab == repoFiles {
		switch msg.String() {
		case "j", "down":
			m.moveRepoCursor(1)
		case "k", "up":
			m.moveRepoCursor(-1)
		case " ":
			m.toggleRepoSelection()
		case "enter":
			return m, m.requestRepoPreview()
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.repoFlow.diffVP, cmd = m.repoFlow.diffVP.Update(msg)
	return m, cmd
}

func (m Model) selectedRepoEntries() []repostate.Entry {
	var entries []repostate.Entry
	for _, entry := range m.repoFlow.status.Entries {
		if m.repoFlow.selected[repoEntryID(entry)] {
			entries = append(entries, entry)
		}
	}
	return entries
}

func (m Model) repoActionAllowed(kind repostate.ActionKind) bool {
	entries := m.selectedRepoEntries()
	if len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		conflict := entry.Kind == 'u' || entry.Index == repostate.StateUnmerged || entry.Worktree == repostate.StateUnmerged
		untracked := entry.Index == repostate.StateUntracked || entry.Worktree == repostate.StateUntracked
		if conflict {
			return false
		}
		if kind == repostate.ActionRestore && untracked {
			return false
		}
		if kind == repostate.ActionDeleteUntracked && !untracked {
			return false
		}
	}
	return true
}

func (m *Model) prepareRepoAction(kind repostate.ActionKind, message string, deleteConfirmed bool) tea.Cmd {
	entries := append([]repostate.Entry(nil), m.selectedRepoEntries()...)
	if len(entries) == 0 || m.repoRunner == nil || m.repoFS == nil {
		m.repoFlow.notice = "repository action unavailable"
		return nil
	}
	requestID := m.repoFlow.requestID
	runner := m.repoRunner
	filesystem := m.repoFS
	repo := m.repoPath()
	gitBin := m.runCtx.GitBin
	if gitBin == "" {
		gitBin = "git"
	}
	m.repoFlow.notice = "fingerprinting exact selected content…"
	return func() tea.Msg {
		fingerprints, err := repostate.FingerprintEntries(context.Background(), runner, filesystem, repo, gitBin, entries)
		if err != nil {
			return repoActionPreparedMsg{requestID: requestID, err: err}
		}
		operation, err := repostate.ProposeAction(repostate.ActionRequest{
			Repo: repo, GitBin: gitBin, Kind: kind, Message: message, Entries: entries,
			Fingerprints: fingerprints, DeleteConfirmed: deleteConfirmed,
		})
		return repoActionPreparedMsg{requestID: requestID, operation: operation, err: err}
	}
}

func (m *Model) acceptRepoAction(msg repoActionPreparedMsg) {
	if msg.requestID != m.repoFlow.requestID {
		return
	}
	if msg.err != nil {
		m.repoFlow.notice = msg.err.Error()
		return
	}
	m.repoFlow.stage = repoBrowse
	m.reviewed = reviewedPlan{
		Action: fmt.Sprintf("repo:%s:%d", msg.operation.Kind, len(msg.operation.Entries)),
		Repo:   &repoReview{Operation: cloneRepoOperation(msg.operation)},
	}
	m.screen = screenReview
}

func (m Model) repoDeleteDryRunCmd() tea.Cmd {
	entries := m.selectedRepoEntries()
	requestID := m.repoFlow.requestID
	runner := m.repoRunner
	repo := m.repoPath()
	gitBin := m.runCtx.GitBin
	if gitBin == "" {
		gitBin = "git"
	}
	if runner == nil {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	return func() tea.Msg {
		args := []string{"clean", "-nd", "--"}
		args = append(args, paths...)
		out, err := runner.Output(context.Background(), repo, gitBin, args...)
		return repoDeleteDryRunMsg{requestID: requestID, output: strings.TrimSpace(string(out)), err: err}
	}
}

func cloneRepoOperation(operation repostate.Operation) repostate.Operation {
	cloned := operation
	cloned.Entries = append([]repostate.Entry(nil), operation.Entries...)
	cloned.Fingerprints = append([]repostate.ActionFingerprint(nil), operation.Fingerprints...)
	cloned.Commands = make([]repostate.Command, len(operation.Commands))
	for i, command := range operation.Commands {
		cloned.Commands[i] = command
		cloned.Commands[i].Args = append([]string(nil), command.Args...)
	}
	return cloned
}

func (m *Model) resizeRepoViewport() {
	width := max(20, primaryContentWidth(m.width)-2)
	height := max(4, m.height-11)
	if m.height <= 24 {
		height = 10
	}
	if m.repoFlow.diffVP.Width == 0 {
		m.repoFlow.diffVP = viewport.New(width, height)
	} else {
		m.repoFlow.diffVP.Width = width
		m.repoFlow.diffVP.Height = height
	}
	m.repoFlow.diffVP.SetContent(renderRepoPreview(m.repoFlow.preview))
}
