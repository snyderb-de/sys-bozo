package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	providers      []packageProviderState
	activeProvider int
	searchComplete bool
	animationFrame int
	scope          packages.Scope
	sections       []string
	section        int
	target         packages.Target
	placingSection bool
	err            error
	notice         string
}

type packageProviderState struct {
	Spec        packages.ProviderSpec
	Phase       packages.SearchPhase
	PhaseDetail string
	StartedAt   time.Time
	Elapsed     time.Duration
	Candidates  []packages.Candidate
	Err         error
	Selected    int
	Scroll      int
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
	m.reviewed.Package.Warning = "file changed; refresh and review again before applying; " + sanitizedApplyDetail(err)
	m.mode, m.screen = modeView, screenReview
	m.queue, m.queuePos = nil, 0
	m.runErr = nil
	m.initPackageDiffViewport()
	return true
}

func sanitizedApplyDetail(err error) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, err.Error())
}

type packageSearchEventMsg struct {
	requestID uint64
	events    <-chan packages.SearchEvent
	event     packages.SearchEvent
	ok        bool
}
type packageAnimationTickMsg struct{ requestID uint64 }
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

func waitPackageSearchEvent(requestID uint64, events <-chan packages.SearchEvent) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		return packageSearchEventMsg{requestID: requestID, events: events, event: event, ok: ok}
	}
}

func packageAnimationTick(requestID uint64) tea.Cmd {
	return tea.Tick(90*time.Millisecond, func(time.Time) tea.Msg {
		return packageAnimationTickMsg{requestID: requestID}
	})
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
			search := m.startPackageSearch
			if search == nil {
				return m, nil
			}
			if m.packageSearchCancel != nil {
				m.packageSearchCancel()
			}
			m.packageSearchRequest++
			requestID := m.packageSearchRequest
			specs := packages.DetectProviderSpecs(packages.HostCapabilities{
				OS: m.runCtx.OS, OSID: m.runCtx.OSID, NixBin: m.runCtx.NixBin,
				BrewBin: m.runCtx.BrewBin, DnfBin: m.runCtx.DnfBin, AptCacheBin: m.runCtx.AptCacheBin,
			})
			providers := make([]packageProviderState, len(specs))
			active := -1
			for i, spec := range specs {
				providers[i].Spec = spec
				if active < 0 && spec.Enabled {
					active = i
				}
				if spec.Provider == packages.ProviderNix && spec.Enabled {
					active = i
				}
			}
			if active < 0 {
				active = 0
			}
			base, baseCancel := context.WithCancel(context.Background())
			timeout := m.packageSearchTimeout
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			searchCtx, timeoutCancel := context.WithTimeout(base, timeout)
			m.packageSearchCancel = func() { timeoutCancel(); baseCancel() }
			m.packageFlow.stage = packageSearching
			m.packageFlow.providers = providers
			m.packageFlow.activeProvider = active
			m.packageFlow.searchComplete = false
			m.packageFlow.animationFrame = 0
			events := search(searchCtx, packages.SearchRequest{RequestID: requestID, Query: query}, specs)
			return m, tea.Batch(waitPackageSearchEvent(requestID, events), packageAnimationTick(requestID))
		}
		var cmd tea.Cmd
		m.packageFlow.query, cmd = m.packageFlow.query.Update(msg)
		return m, cmd
	case packageSearching, packageChoose:
		switch msg.String() {
		case "tab":
			m.movePackageProvider(1)
		case "shift+tab":
			m.movePackageProvider(-1)
		case "j", "down":
			m.movePackageCandidate(1)
		case "k", "up":
			m.movePackageCandidate(-1)
		case "esc":
			if m.packageFlow.stage == packageSearching {
				m.stopPackageSearch()
				m.packageFlow.stage = packageChoose
			} else {
				m.cancelPackageSearch()
				m.packageFlow = newPackageFlow(m.width)
			}
			return m, nil
		case "q", "ctrl+c":
			m.cancelPackageSearch()
			return m, tea.Quit
		case "enter":
			provider, ok := m.activePackageProvider()
			if ok && provider.Phase == packages.SearchDone && len(provider.Candidates) > 0 {
				m.beginPackagePlacement()
			}
		}
		return m, nil
	case packagePlacement:
		return m.handlePackagePlacementKey(msg)
	}
	return m, nil
}

func (m *Model) cancelPackageSearch() {
	m.stopPackageSearch()
	m.packageSearchRequest++
}

func (m *Model) stopPackageSearch() {
	if m.packageSearchCancel != nil {
		m.packageSearchCancel()
		m.packageSearchCancel = nil
	}
}

func (m *Model) movePackageProvider(delta int) {
	n := len(m.packageFlow.providers)
	if n == 0 || (delta != 1 && delta != -1) {
		return
	}
	start := m.packageFlow.activeProvider
	if start < 0 || start >= n {
		start = 0
	}
	for step := 1; step <= n; step++ {
		i := (start + delta*step%n + n) % n
		provider := m.packageFlow.providers[i]
		if provider.Spec.Enabled || provider.Phase == packages.SearchFailed {
			m.packageFlow.activeProvider = i
			return
		}
	}
}

func (m *Model) movePackageCandidate(delta int) {
	i := m.packageFlow.activeProvider
	if i < 0 || i >= len(m.packageFlow.providers) {
		return
	}
	providers := append([]packageProviderState(nil), m.packageFlow.providers...)
	provider := providers[i]
	n := len(provider.Candidates)
	if n == 0 {
		return
	}
	provider.Selected = (provider.Selected + delta%n + n) % n
	limit := packageVisibleResultLimit(m.height)
	if provider.Selected < provider.Scroll {
		provider.Scroll = provider.Selected
	} else if provider.Selected >= provider.Scroll+limit {
		provider.Scroll = provider.Selected - limit + 1
	}
	providers[i] = provider
	m.packageFlow.providers = providers
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
		m.buildPackageReview(proposal, packageVerifySpec(candidate, m.runCtx, proposal.Target))
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
	dir, err := os.MkdirTemp("", "sys-bozo-package-edit-*")
	if err != nil {
		return func() tea.Msg { return packageEditorDoneMsg{err: fmt.Errorf("create package editor copy: %w", err)} }
	}
	tempPath := filepath.Join(dir, filepath.Base(request.target.Path))
	if err := os.WriteFile(tempPath, request.original, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return func() tea.Msg { return packageEditorDoneMsg{err: fmt.Errorf("write package editor copy: %w", err)} }
	}
	contextPath := filepath.Join(dir, "SYS-BOZO-CONTEXT.txt")
	contextText := fmt.Sprintf("EDITOR CONTEXT: %s\nprovider: %s\npackage-id: %s\npackage-name: %s\nscope: %s\nreal-target: %s\n", contextPath, request.candidate.Provider, request.candidate.ID, request.candidate.Name, m.packageFlow.scope, request.target.Path)
	if err := os.WriteFile(contextPath, []byte(contextText), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return func() tea.Msg { return packageEditorDoneMsg{err: err} }
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	argv, err := parseEditorArgv(editor)
	if err != nil {
		_ = os.RemoveAll(dir)
		return func() tea.Msg { return packageEditorDoneMsg{err: err} }
	}
	cmd := exec.Command(argv[0], append(argv[1:], tempPath, contextPath)...)
	execProcess := m.packageExecProcess
	if execProcess == nil {
		execProcess = tea.ExecProcess
	}
	return execProcess(cmd, func(editorErr error) tea.Msg {
		defer os.RemoveAll(dir)
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
	provider, ok := m.activePackageProvider()
	if !ok || provider.Selected < 0 || provider.Selected >= len(provider.Candidates) {
		return packages.Candidate{}, false
	}
	return provider.Candidates[provider.Selected], true
}

func (m Model) activePackageProvider() (packageProviderState, bool) {
	i := m.packageFlow.activeProvider
	if i < 0 || i >= len(m.packageFlow.providers) {
		return packageProviderState{}, false
	}
	return m.packageFlow.providers[i], true
}

func (m Model) providerIndex(provider packages.Provider) (int, bool) {
	for i, state := range m.packageFlow.providers {
		if state.Spec.Provider == provider {
			return i, true
		}
	}
	return 0, false
}

func (m Model) hasUnfinishedProviders() (bool, bool) {
	if len(m.packageFlow.providers) == 0 {
		return false, false
	}
	for _, provider := range m.packageFlow.providers {
		if provider.Spec.Enabled && !packageSearchTerminal(provider.Phase) {
			return true, true
		}
	}
	return false, true
}

func packageSearchTerminal(phase packages.SearchPhase) bool {
	switch phase {
	case packages.SearchDone, packages.SearchFailed, packages.SearchCancelled, packages.SearchTimedOut:
		return true
	default:
		return false
	}
}

func packageVerifySpec(candidate packages.Candidate, ctx runner.Context, target packages.Target) packages.VerifySpec {
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
		NixBin:      ctx.NixBin, HomeManagerBin: ctx.HomeManager, Repo: ctx.Repo, System: ctx.NixSystem,
		NixInput: target.NixInput, Attr: candidate.ID,
	}
	return spec
}

func (m *Model) acceptPackageSearchEvent(event packages.SearchEvent) {
	i, ok := m.providerIndex(event.Provider)
	if !ok {
		return
	}
	providers := append([]packageProviderState(nil), m.packageFlow.providers...)
	provider := providers[i]
	provider.Phase = event.Phase
	provider.PhaseDetail = string(event.Phase)
	if event.Phase == packages.SearchStarting {
		provider.StartedAt = event.At
	}
	if !provider.StartedAt.IsZero() && !event.At.IsZero() && !event.At.Before(provider.StartedAt) {
		provider.Elapsed = event.At.Sub(provider.StartedAt)
	}
	if event.Candidates != nil {
		provider.Candidates = clonePackageCandidates(event.Candidates)
	}
	provider.Err = event.Err
	if provider.Selected >= len(provider.Candidates) {
		provider.Selected = max(0, len(provider.Candidates)-1)
	}
	if provider.Scroll > provider.Selected {
		provider.Scroll = provider.Selected
	}
	providers[i] = provider
	m.packageFlow.providers = providers
}

func clonePackageCandidates(candidates []packages.Candidate) []packages.Candidate {
	if candidates == nil {
		return nil
	}
	cloned := make([]packages.Candidate, len(candidates))
	for i, candidate := range candidates {
		cloned[i] = candidate
		cloned[i].VersionArgs = append([]string(nil), candidate.VersionArgs...)
	}
	return cloned
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
			return fmt.Sprintf("exact %s#%s in applied Home Manager generation", spec.NixInput, spec.Attr)
		}
		return fmt.Sprintf("%s %s receipt", spec.Provider, spec.Token)
	}
	return "package availability"
}
