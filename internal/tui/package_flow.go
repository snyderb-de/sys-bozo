package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/packages"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

type packageStage uint8

const (
	packageSearch packageStage = iota
	packageSearching
	packageChoose
	packagePlacement
)

type packageFlow struct {
	stage          packageStage
	query          textinput.Model
	result         packages.SearchResult
	scope          packages.Scope
	sections       []string
	section        int
	target         packages.Target
	placingSection bool
	err            error
	notice         string
}

type packageReview struct {
	Proposal            packages.Proposal
	Verify              packages.VerifySpec
	Applied             *packages.AppliedEdit
	EditApplied         bool
	Result              *packages.VerifyResult
	verificationStarted bool
	Revert              bool
	Warning             string
	CleanupErr          error
	DiffVP              viewport.Model
}

func (m *Model) packageApplyStale(err error) bool {
	if !errors.Is(err, packages.ErrStaleFile) || m.reviewed.Package == nil {
		return false
	}
	m.reviewed.Package = clonePackageReview(m.reviewed.Package)
	m.reviewed.Package.Warning = "file changed; refresh and review again before applying"
	m.mode, m.screen = modeView, screenReview
	m.queue, m.queuePos = nil, 0
	m.runErr = nil
	m.initPackageDiffViewport()
	return true
}

type packageSearchMsg struct {
	requestID uint64
	result    packages.SearchResult
}
type packageAppliedMsg struct {
	edit packages.AppliedEdit
	err  error
}
type packageVerifiedMsg struct{ result packages.VerifyResult }
type packageEditorRequest struct {
	target    packages.Target
	original  []byte
	candidate packages.Candidate
}
type packageEditorDoneMsg struct {
	target             packages.Target
	original, proposed []byte
	candidate          packages.Candidate
	err                error
}

func newPackageFlow(width int) packageFlow {
	query := textinput.New()
	query.Prompt = ""
	query.Placeholder = "Search nixpkgs + Homebrew"
	query.CharLimit = 120
	query.Width = max(1, primaryContentWidth(width)-2)
	query.Focus()
	return packageFlow{stage: packageSearch, query: query, scope: packages.ScopeShared}
}

func (m *Model) openPackageFlow() {
	m.packageFlow = newPackageFlow(m.width)
	m.reviewed = reviewedPlan{}
	m.queue = nil
	m.queuePos = 0
	m.mode = modeView
	m.screen = screenPackage
}

func (m Model) handlePackageKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.packageFlow.stage {
	case packageSearch:
		switch msg.String() {
		case "esc":
			m.screen = screenHome
			return m, nil
		case "enter":
			query := strings.TrimSpace(m.packageFlow.query.Value())
			if query == "" {
				return m, nil
			}
			search := m.searchPackage
			if search == nil {
				return m, nil
			}
			if m.packageSearchCancel != nil {
				m.packageSearchCancel()
			}
			m.packageSearchRequest++
			requestID := m.packageSearchRequest
			base, baseCancel := context.WithCancel(context.Background())
			timeout := m.packageSearchTimeout
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			searchCtx, timeoutCancel := context.WithTimeout(base, timeout)
			m.packageSearchCancel = func() { timeoutCancel(); baseCancel() }
			m.packageFlow.stage = packageSearching
			return m, func() tea.Msg { return packageSearchMsg{requestID: requestID, result: search(searchCtx, query)} }
		}
		var cmd tea.Cmd
		m.packageFlow.query, cmd = m.packageFlow.query.Update(msg)
		return m, cmd
	case packageSearching:
		switch msg.String() {
		case "esc":
			m.cancelPackageSearch()
			m.packageFlow = newPackageFlow(m.width)
			return m, nil
		case "q", "ctrl+c":
			m.cancelPackageSearch()
			return m, tea.Quit
		}
		return m, nil
	case packageChoose:
		n := len(m.packageFlow.result.Candidates)
		if n == 0 {
			if msg.String() == "esc" {
				m.packageFlow = newPackageFlow(m.width)
			}
			return m, nil
		}
		switch msg.String() {
		case "j", "down":
			m.packageFlow.result.Selected = (m.packageFlow.result.Selected + 1) % n
		case "k", "up":
			m.packageFlow.result.Selected = (m.packageFlow.result.Selected - 1 + n) % n
		case "esc":
			m.packageFlow = newPackageFlow(m.width)
		case "enter":
			m.beginPackagePlacement()
		}
		return m, nil
	case packagePlacement:
		return m.handlePackagePlacementKey(msg)
	}
	return m, nil
}

func (m *Model) cancelPackageSearch() {
	if m.packageSearchCancel != nil {
		m.packageSearchCancel()
		m.packageSearchCancel = nil
	}
	m.packageSearchRequest++
}

var packageScopes = []packages.Scope{packages.ScopeShared, packages.ScopePlatform, packages.ScopeHost}

func (m *Model) beginPackagePlacement() {
	m.packageFlow.stage = packagePlacement
	m.packageFlow.scope = packages.ScopeShared
	m.packageFlow.sections = nil
	m.packageFlow.section = 0
	m.packageFlow.placingSection = false
	m.packageFlow.err = nil
}

func (m Model) handlePackagePlacementKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.packageFlow.placingSection {
		scopeIndex := packageScopeIndex(m.packageFlow.scope)
		switch msg.String() {
		case "j", "down":
			scopeIndex = (scopeIndex + 1) % len(packageScopes)
			m.packageFlow.scope = packageScopes[scopeIndex]
			m.packageFlow.err = nil
		case "k", "up":
			scopeIndex = (scopeIndex - 1 + len(packageScopes)) % len(packageScopes)
			m.packageFlow.scope = packageScopes[scopeIndex]
			m.packageFlow.err = nil
		case "esc":
			m.packageFlow.stage = packageChoose
		case "enter":
			cmd, err := m.preparePackageTarget()
			if err != nil {
				m.packageFlow.err = err
			}
			return m, cmd
		}
		return m, nil
	}

	n := len(m.packageFlow.sections)
	switch msg.String() {
	case "j", "down":
		if n > 0 {
			m.packageFlow.section = (m.packageFlow.section + 1) % n
		}
	case "k", "up":
		if n > 0 {
			m.packageFlow.section = (m.packageFlow.section - 1 + n) % n
		}
	case "esc":
		m.packageFlow.placingSection = false
		m.packageFlow.err = nil
	case "enter":
		if n == 0 {
			return m, nil
		}
		candidate, ok := m.selectedPackageCandidate()
		if !ok {
			return m, nil
		}
		original, err := os.ReadFile(m.packageFlow.target.Path)
		if err != nil {
			m.packageFlow.err = fmt.Errorf("read declaration file: %w", err)
			return m, nil
		}
		proposal, err := packages.ProposeAdd(original, m.packageFlow.target, m.packageFlow.sections[m.packageFlow.section], candidate.ID)
		if err != nil {
			if errors.Is(err, packages.ErrAlreadyDeclared) {
				m.packageFlow.err = nil
				m.packageFlow.notice = fmt.Sprintf("%s is already declared; no edit needed", candidate.ID)
				return m, nil
			}
			m.packageFlow.err = nil
			return m, m.openPackageEditor(packageEditorRequest{target: m.packageFlow.target, original: original, candidate: candidate})
		}
		m.buildPackageReview(proposal, packageVerifySpec(candidate, m.runCtx))
	}
	return m, nil
}

func packageScopeIndex(scope packages.Scope) int {
	for i, candidate := range packageScopes {
		if candidate == scope {
			return i
		}
	}
	return 0
}

func (m *Model) preparePackageTarget() (tea.Cmd, error) {
	candidate, ok := m.selectedPackageCandidate()
	if !ok {
		return nil, fmt.Errorf("select a package candidate")
	}
	target, err := packages.ResolveTarget(m.runCtx.Repo, m.runCtx.OS, candidate.Provider, candidate.Kind, m.packageFlow.scope)
	if err != nil {
		target, err = packages.ResolveEditorTarget(m.runCtx.Repo, m.runCtx.OS, m.runCtx.Hostname, candidate.Provider, candidate.Kind, m.packageFlow.scope)
		if err != nil {
			return nil, fmt.Errorf("scope %q: %w", m.packageFlow.scope, err)
		}
		original, readErr := os.ReadFile(target.Path)
		if readErr != nil {
			return nil, fmt.Errorf("read declaration file for editor handoff: %w", readErr)
		}
		return m.openPackageEditor(packageEditorRequest{target: target, original: original, candidate: candidate}), nil
	}
	original, err := os.ReadFile(target.Path)
	if err != nil {
		return nil, fmt.Errorf("read declaration file: %w", err)
	}
	if target.Assignment == "" {
		return m.openPackageEditor(packageEditorRequest{target: target, original: original, candidate: candidate}), nil
	}
	sections, err := packages.Sections(original, target)
	if err != nil {
		return m.openPackageEditor(packageEditorRequest{target: target, original: original, candidate: candidate}), nil
	}
	if len(sections) == 0 {
		return m.openPackageEditor(packageEditorRequest{target: target, original: original, candidate: candidate}), nil
	}
	m.packageFlow.target = target
	m.packageFlow.sections = append([]string(nil), sections...)
	m.packageFlow.section = 0
	for i, section := range sections {
		if section == "Misc" {
			m.packageFlow.section = i
			break
		}
	}
	m.packageFlow.placingSection = true
	m.packageFlow.err = nil
	return nil, nil
}

func (m Model) openPackageEditor(request packageEditorRequest) tea.Cmd {
	request.original = append([]byte(nil), request.original...)
	if m.packageEditor != nil {
		return m.packageEditor(request)
	}
	temp, err := os.CreateTemp("", "sys-bozo-package-*")
	if err != nil {
		return func() tea.Msg { return packageEditorDoneMsg{err: fmt.Errorf("create package editor copy: %w", err)} }
	}
	tempPath := temp.Name()
	if _, err := temp.Write(request.original); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempPath)
		return func() tea.Msg { return packageEditorDoneMsg{err: fmt.Errorf("write package editor copy: %w", err)} }
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return func() tea.Msg { return packageEditorDoneMsg{err: fmt.Errorf("close package editor copy: %w", err)} }
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	argv, err := parseEditorArgv(editor)
	if err != nil {
		_ = os.Remove(tempPath)
		return func() tea.Msg { return packageEditorDoneMsg{err: err} }
	}
	cmd := exec.Command(argv[0], append(argv[1:], tempPath)...)
	return tea.ExecProcess(cmd, func(editorErr error) tea.Msg {
		defer os.Remove(tempPath)
		if editorErr != nil {
			return packageEditorDoneMsg{err: editorErr}
		}
		proposed, readErr := os.ReadFile(tempPath)
		return packageEditorDoneMsg{
			target: request.target, original: request.original, proposed: proposed,
			candidate: request.candidate, err: readErr,
		}
	})
}

func (m Model) selectedPackageCandidate() (packages.Candidate, bool) {
	selected := m.packageFlow.result.Selected
	if selected < 0 || selected >= len(m.packageFlow.result.Candidates) {
		return packages.Candidate{}, false
	}
	return m.packageFlow.result.Candidates[selected], true
}

func packageVerifySpec(candidate packages.Candidate, ctx runner.Context) packages.VerifySpec {
	spec := packages.VerifySpec{
		Provider:    candidate.Provider,
		Kind:        candidate.Kind,
		Token:       candidate.ID,
		PName:       candidate.Name,
		Version:     candidate.Version,
		Executable:  candidate.Executable,
		VersionArgs: append([]string(nil), candidate.VersionArgs...),
		BrewBin:     ctx.BrewBin,
		NixStoreBin: ctx.NixStoreBin,
		ProfilePath: ctx.NixProfilePath,
	}
	return spec
}

func (m *Model) acceptPackageSearch(result packages.SearchResult) {
	result.Candidates = append([]packages.Candidate(nil), result.Candidates...)
	result.Selected = 0
	for i, candidate := range result.Candidates {
		if candidate.Provider == packages.ProviderNix {
			result.Selected = i
			break
		}
	}
	m.packageFlow.result = result
	m.packageFlow.stage = packageChoose
}

func (m *Model) buildPackageReview(proposal packages.Proposal, verify packages.VerifySpec) bool {
	var items []runner.WorkItem
	found := false
	for _, task := range m.tasks {
		if task.ID == proposal.Target.ApplyAction && task.Available(m.runCtx) {
			items = runner.BuildQueue(task, m.runCtx)
			found = true
			break
		}
	}
	if !found || len(items) == 0 {
		m.packageFlow.err = fmt.Errorf("apply action %q is unavailable", proposal.Target.ApplyAction)
		m.screen = screenPackage
		return false
	}
	m.reviewed = reviewedPlan{
		Action:  proposal.Target.ApplyAction,
		Items:   cloneWorkItems(items),
		Package: clonePackageReview(&packageReview{Proposal: proposal, Verify: verify}),
	}
	m.initPackageDiffViewport()
	m.screen = screenReview
	return true
}

func clonePackageReview(review *packageReview) *packageReview {
	if review == nil {
		return nil
	}
	cloned := *review
	cloned.Proposal = clonePackageProposal(review.Proposal)
	cloned.Verify = cloneVerifySpec(review.Verify)
	if review.Applied != nil {
		edit := cloneAppliedEdit(*review.Applied)
		cloned.Applied = &edit
	}
	if review.Result != nil {
		result := *review.Result
		cloned.Result = &result
	}
	return &cloned
}

func clonePackageProposal(proposal packages.Proposal) packages.Proposal {
	proposal.Original = append([]byte(nil), proposal.Original...)
	proposal.Proposed = append([]byte(nil), proposal.Proposed...)
	return proposal
}

func cloneVerifySpec(spec packages.VerifySpec) packages.VerifySpec {
	spec.VersionArgs = append([]string(nil), spec.VersionArgs...)
	return spec
}

func (m Model) applyPackageCmd(proposal packages.Proposal) tea.Cmd {
	apply := m.applyPackage
	if apply == nil {
		apply = packages.Apply
	}
	proposal = clonePackageProposal(proposal)
	return func() tea.Msg {
		edit, err := apply(proposal)
		return packageAppliedMsg{edit: edit, err: err}
	}
}

func (m Model) verifyPackageCmd(spec packages.VerifySpec) tea.Cmd {
	verify := m.verifyPackage
	if verify == nil {
		return func() tea.Msg {
			return packageVerifiedMsg{result: packages.VerifyResult{Err: fmt.Errorf("package verifier is unavailable")}}
		}
	}
	spec = cloneVerifySpec(spec)
	return func() tea.Msg { return packageVerifiedMsg{result: verify(spec)} }
}

func cloneAppliedEdit(edit packages.AppliedEdit) packages.AppliedEdit {
	edit.Before = append([]byte(nil), edit.Before...)
	edit.After = append([]byte(nil), edit.After...)
	return edit
}

func (m Model) packageCanRevert() bool {
	return m.runErr != nil && m.reviewed.Package != nil && m.reviewed.Package.EditApplied && m.reviewed.Package.Applied != nil &&
		!m.reviewed.Package.Revert && m.reviewed.Package.Result == nil
}

func (m *Model) reviewPackageRevert() {
	if !m.packageCanRevert() {
		return
	}
	propose := m.proposePackageRevert
	if propose == nil {
		propose = packages.ProposeRevert
	}
	proposal, err := propose(cloneAppliedEdit(*m.reviewed.Package.Applied))
	if err != nil {
		m.revertErr = err
		return
	}
	proposal.Target.ApplyAction = m.reviewed.Package.Proposal.Target.ApplyAction
	if !m.buildPackageReview(proposal, packages.VerifySpec{}) {
		return
	}
	m.reviewed.Package.Revert = true
	m.initPackageDiffViewport()
	m.mode = modeView
	m.queue = nil
	m.queuePos = 0
	m.runErr = nil
	m.runCancelled = false
	m.runElapsed = 0
	m.stepResults = nil
	m.resultLogVisible = false
	m.logLines = nil
	m.revertErr = nil
}

func packageVerificationLabel(spec packages.VerifySpec) string {
	if spec.Executable != "" {
		return fmt.Sprintf("executable %s on PATH", spec.Executable)
	}
	if spec.Token != "" {
		if spec.Provider == packages.ProviderNix {
			return fmt.Sprintf("nix direct profile reference for %s %s", spec.PName, spec.Version)
		}
		return fmt.Sprintf("%s %s receipt", spec.Provider, spec.Token)
	}
	return "package availability"
}
