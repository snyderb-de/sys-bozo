package tui

import (
	"context"
	"crypto/sha256"
	"fmt"

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
	status    repostate.Status
	requestID uint64
	loading   bool
	cursor    int
	selected  map[[32]byte]bool
	tab       repoTab
	preview   repostate.Preview
	diffVP    viewport.Model
	notice    string
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
	switch msg.String() {
	case "esc":
		m.screen = m.repoReturn
		if m.screen == screenRepoTriage {
			m.screen = screenHome
		}
		return m, nil
	case "r":
		return m, m.refreshRepoStatus()
	case "tab", "shift+tab":
		if m.repoFlow.tab == repoFiles {
			m.repoFlow.tab = repoDiff
		} else {
			m.repoFlow.tab = repoFiles
		}
		return m, nil
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
