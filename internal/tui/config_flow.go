package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/fileedit"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

type configReview struct {
	Proposal    fileedit.Proposal
	Applied     *fileedit.AppliedEdit
	EditApplied bool
	Warning     string
	CleanupErr  error
	DiffVP      viewport.Model
}

type configAppliedMsg struct {
	edit fileedit.AppliedEdit
	err  error
}

func (m Model) openConfigEditor(file configFile) tea.Cmd {
	original, err := os.ReadFile(file.path)
	if err != nil {
		return func() tea.Msg { return configEditorDoneMsg{file: file, err: fmt.Errorf("read config file: %w", err)} }
	}
	request := configEditorRequest{file: file, original: bytes.Clone(original)}
	if m.configEditor != nil {
		return m.configEditor(request)
	}
	dir, err := os.MkdirTemp("", "sys-bozo-config-edit-*")
	if err != nil {
		return func() tea.Msg {
			return configEditorDoneMsg{file: file, err: fmt.Errorf("create config edit directory: %w", err)}
		}
	}
	tempPath := filepath.Join(dir, filepath.Base(file.path))
	if err := os.WriteFile(tempPath, original, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return func() tea.Msg {
			return configEditorDoneMsg{file: file, err: fmt.Errorf("write config edit copy: %w", err)}
		}
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	argv, err := parseEditorArgv(editor)
	if err != nil {
		_ = os.RemoveAll(dir)
		return func() tea.Msg { return configEditorDoneMsg{file: file, err: err} }
	}
	cmd := exec.Command(argv[0], append(argv[1:], tempPath)...)
	execProcess := m.configExecProcess
	if execProcess == nil {
		execProcess = tea.ExecProcess
	}
	return execProcess(cmd, func(editorErr error) tea.Msg {
		defer os.RemoveAll(dir)
		if editorErr != nil {
			return configEditorDoneMsg{file: file, err: editorErr}
		}
		proposed, readErr := os.ReadFile(tempPath)
		return configEditorDoneMsg{file: file, original: request.original, proposed: proposed, err: readErr}
	})
}

func (m *Model) buildConfigReview(proposal fileedit.Proposal, actions ...string) bool {
	var items []runner.WorkItem
	for _, action := range actions {
		found := false
		for _, task := range m.tasks {
			if task.ID == action && task.Available(m.runCtx) {
				items = append(items, runner.BuildQueue(task, m.runCtx)...)
				found = true
				break
			}
		}
		if !found {
			m.configNotice = fmt.Sprintf("rebuild action %q is unavailable", action)
			m.screen = screenConfig
			return false
		}
	}
	if len(items) == 0 {
		return false
	}
	m.reviewed = reviewedPlan{Action: "config:" + joinActions(actions), Items: cloneWorkItems(items), Config: cloneConfigReview(&configReview{Proposal: proposal})}
	m.initConfigDiffViewport()
	m.screen = screenReview
	m.applyPrompt = false
	m.pendingConfig = fileedit.Proposal{}
	return true
}

func joinActions(actions []string) string {
	if len(actions) == 0 {
		return ""
	}
	result := actions[0]
	for _, action := range actions[1:] {
		result += "+" + action
	}
	return result
}

func cloneConfigReview(review *configReview) *configReview {
	if review == nil {
		return nil
	}
	cloned := *review
	cloned.Proposal.Original = bytes.Clone(review.Proposal.Original)
	cloned.Proposal.Proposed = bytes.Clone(review.Proposal.Proposed)
	if review.Applied != nil {
		edit := *review.Applied
		edit.Before, edit.After = bytes.Clone(edit.Before), bytes.Clone(edit.After)
		cloned.Applied = &edit
	}
	return &cloned
}

func (m Model) applyConfigCmd(proposal fileedit.Proposal) tea.Cmd {
	apply := m.applyConfig
	if apply == nil {
		apply = fileedit.Apply
	}
	proposal.Original, proposal.Proposed = bytes.Clone(proposal.Original), bytes.Clone(proposal.Proposed)
	return func() tea.Msg { edit, err := apply(proposal); return configAppliedMsg{edit: edit, err: err} }
}

func (m *Model) configApplyStale(err error) bool {
	if !errors.Is(err, fileedit.ErrStaleFile) || m.reviewed.Config == nil {
		return false
	}
	m.reviewed.Config = cloneConfigReview(m.reviewed.Config)
	m.reviewed.Config.Warning = "file changed; refresh and review again before applying"
	m.mode, m.screen = modeView, screenReview
	m.queue, m.queuePos = nil, 0
	m.runErr = nil
	m.initConfigDiffViewport()
	return true
}

func (m Model) viewConfigReview() string {
	review := m.reviewed.Config
	width := primaryContentWidth(m.width)
	rows := []string{m.styles.major.Render("REVIEW/CONFIG"), m.styles.label.Render("REVIEWED FILE REPLACEMENT"), majorRule(m.styles, width, true), ""}
	rows = append(rows, packageLabeledValueRows(m.styles, "FILE", review.Proposal.Path, width)...)
	if review.Warning != "" {
		rows = append(rows, m.styles.danger.Render("WARNING  "+review.Warning))
	}
	vp := review.DiffVP
	if vp.Width <= 0 || vp.Height <= 0 {
		vp = m.newConfigDiffViewport(review, 0)
	}
	top := min(vp.TotalLineCount(), vp.YOffset+1)
	bottom := min(vp.TotalLineCount(), vp.YOffset+vp.VisibleLineCount())
	status := "READY"
	statusKind := statusMuted
	if review.EditApplied {
		status, statusKind = "DONE", statusSuccess
	}
	rows = append(rows, "", m.styles.label.Render(fmt.Sprintf("EDIT %02d-%02d/%02d", top, bottom, vp.TotalLineCount()))+"  "+statusText(m.styles, status, statusKind), vp.View(), "", m.styles.label.Render("REBUILD"))
	for i, item := range m.reviewed.Items {
		rows = append(rows, reviewCommandRows(m.styles, fmt.Sprintf("%02d", i+1), runner.CmdLabel(item), statusText(m.styles, "READY", statusMuted), width)...)
	}
	rows = append(rows, "", majorRule(m.styles, width, false), "", m.styles.muted.Render("J/K SCROLL   PGUP/PGDN PAGE   ESCAPE BACK")+"   "+m.styles.active.Render("ENTER CONFIRM"))
	return primaryFrame(m.styles, m.width, strings.Join(rows, "\n"))
}

func (m Model) newConfigDiffViewport(review *configReview, offset int) viewport.Model {
	width := primaryContentWidth(m.width)
	height := 8
	if m.height > 0 {
		height = max(1, m.height-18-len(m.reviewed.Items))
	}
	vp := viewport.New(width, height)
	vp.SetContent(renderPackageDiffContent(m.styles, review.Proposal.Diff, width))
	vp.SetYOffset(offset)
	return vp
}

func (m *Model) initConfigDiffViewport() {
	if m.reviewed.Config == nil {
		return
	}
	m.reviewed.Config = cloneConfigReview(m.reviewed.Config)
	m.reviewed.Config.DiffVP = m.newConfigDiffViewport(m.reviewed.Config, 0)
}

func (m *Model) resizeConfigDiffViewport() {
	if m.reviewed.Config == nil {
		return
	}
	offset := m.reviewed.Config.DiffVP.YOffset
	m.reviewed.Config = cloneConfigReview(m.reviewed.Config)
	m.reviewed.Config.DiffVP = m.newConfigDiffViewport(m.reviewed.Config, offset)
}

func (m *Model) scrollConfigDiff(key string) bool {
	if m.reviewed.Config == nil {
		return false
	}
	m.reviewed.Config = cloneConfigReview(m.reviewed.Config)
	if m.reviewed.Config.DiffVP.Width <= 0 {
		m.reviewed.Config.DiffVP = m.newConfigDiffViewport(m.reviewed.Config, 0)
	}
	switch key {
	case "j", "down":
		m.reviewed.Config.DiffVP.ScrollDown(1)
	case "k", "up":
		m.reviewed.Config.DiffVP.ScrollUp(1)
	case "pgdown":
		m.reviewed.Config.DiffVP.PageDown()
	case "pgup":
		m.reviewed.Config.DiffVP.PageUp()
	default:
		return false
	}
	return true
}
