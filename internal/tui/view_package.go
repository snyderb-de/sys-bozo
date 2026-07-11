package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/snyderb-de/sys-bozo/internal/packages"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

func (m Model) viewPackage() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	rows := []string{
		s.major.Render("ADD/PACKAGE"),
		s.label.Render("DECLARATIVE INSTALL"),
		majorRule(s, contentWidth, true),
		"",
	}
	switch m.packageFlow.stage {
	case packageSearch:
		query := m.packageFlow.query
		query.Width = max(1, contentWidth)
		rows = append(rows,
			s.label.Render("SEARCH"),
			query.View(),
			s.active.Render(strings.Repeat("━", contentWidth)),
			"",
			s.muted.Render("Searches nixpkgs and Homebrew. Nix remains default when available."),
			"",
			s.muted.Render("ESCAPE BACK")+"   "+s.active.Render("ENTER SEARCH"),
		)
	case packageSearching:
		rows = append(rows, renderPackagePipeline(m, contentWidth)...)
		rows = append(rows, renderPackageTabs(m, contentWidth))
		if provider, ok := m.activePackageProvider(); ok {
			rows = append(rows, renderProviderResults(m, provider, contentWidth, packageVisibleResultLimit(m.height))...)
		}
		rows = append(rows, "", s.muted.Render("TAB SOURCE   J/K MOVE   ESC CANCEL")+"   "+s.active.Render("ENTER PLACE"))
	case packageChoose:
		rows = append(rows, renderPackagePipeline(m, contentWidth)...)
		rows = append(rows, renderPackageTabs(m, contentWidth))
		if provider, ok := m.activePackageProvider(); ok {
			rows = append(rows, renderProviderResults(m, provider, contentWidth, packageVisibleResultLimit(m.height))...)
		}
		rows = append(rows, "", s.muted.Render("TAB SOURCE   J/K MOVE   ESC SEARCH")+"   "+s.active.Render("ENTER PLACE"))
	case packagePlacement:
		candidate, _ := m.selectedPackageCandidate()
		name := candidate.Name
		if name == "" {
			name = candidate.ID
		}
		rows = append(rows,
			s.label.Render("PLACEMENT"),
			s.label.Render("PACKAGE")+"  "+s.text.Render(name),
			s.label.Render("PROVIDER")+"  "+s.text.Render(strings.ToUpper(string(candidate.Provider))+" / "+strings.ToUpper(string(candidate.Kind))),
			"",
			s.label.Render("SCOPE"),
		)
		for i, scope := range packageScopes {
			status := ""
			if scope == m.packageFlow.scope {
				status = statusText(s, "SELECTED", statusActive)
			}
			rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), strings.ToUpper(string(scope)), status, contentWidth, !m.packageFlow.placingSection && scope == m.packageFlow.scope))
		}
		if m.packageFlow.placingSection {
			rows = append(rows, "", s.label.Render("SECTION"))
			start, end := packageVisibleWindow(len(m.packageFlow.sections), m.packageFlow.section, packageVisibleSectionLimit(m.height))
			for i := start; i < end; i++ {
				section := m.packageFlow.sections[i]
				rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), section, "", contentWidth, i == m.packageFlow.section))
			}
		}
		if m.packageFlow.err != nil {
			rows = append(rows, s.danger.Render(truncateVisible("! "+m.packageFlow.err.Error(), contentWidth)))
		}
		if m.packageFlow.notice != "" {
			rows = append(rows, s.active.Render(truncateVisible(m.packageFlow.notice, contentWidth)))
		}
		rows = append(rows, "", s.muted.Render("ESCAPE BACK   ↑/↓ MOVE")+"   "+s.active.Render("ENTER SELECT"))
	}
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func renderPackagePipeline(m Model, width int) []string {
	s := m.styles
	host := strings.ToUpper(strings.TrimSpace(m.runCtx.OSID))
	if host == "" {
		host = strings.ToUpper(strings.TrimSpace(m.runCtx.OS))
	}
	if host == "" {
		host = "UNKNOWN"
	}
	arch := strings.ToUpper(strings.TrimSpace(m.runCtx.NixSystem))
	if prefix, _, ok := strings.Cut(arch, "-"); ok {
		arch = prefix
	}
	if arch == "" {
		arch = "UNKNOWN"
	}

	rows := []string{
		s.label.Render("DISCOVERY PIPELINE"),
		packagePipelineRow(s, "01  DETECT", host+" / "+arch, width, statusSuccess),
		packagePipelineRow(s, "02  DISPATCH", "", width, statusMuted),
	}
	for _, provider := range m.packageFlow.providers {
		phase := packagePhaseLabel(provider.Phase)
		kind := statusActive
		detail := ""
		switch {
		case !provider.Spec.Enabled:
			phase = "DISABLED"
			kind = statusMuted
			detail = provider.Spec.DisabledReason
		case provider.Phase == packages.SearchDone:
			kind = statusSuccess
			detail = fmt.Sprintf("%d FOUND  %s", len(provider.Candidates), packageElapsedLabel(provider.Elapsed))
		case provider.Phase == packages.SearchFailed || provider.Phase == packages.SearchTimedOut:
			kind = statusDanger
			if provider.Err != nil {
				detail = truncateVisible(sanitizedApplyDetail(provider.Err), 32)
			}
			detail = strings.TrimSpace(detail + "  ESC SEARCH AGAIN")
		case provider.Phase == packages.SearchCancelled:
			kind = statusAttention
		default:
			detail = packageSweep(m.packageFlow.animationFrame, 5)
		}
		status := strings.TrimSpace(strings.Join([]string{phase, detail}, "  "))
		label := "    " + packageProviderLabel(provider)
		rows = append(rows, packagePipelineRow(s, label, status, width, kind))
	}

	rank := "DONE"
	rankKind := statusSuccess
	if m.packageFlow.stage == packageSearching {
		rank = "SEARCHING"
		rankKind = statusActive
	}
	present := "READY"
	if provider, ok := m.activePackageProvider(); ok {
		present = packageProviderLabel(provider)
	}
	rows = append(rows,
		packagePipelineRow(s, "03  RANK", rank, width, rankKind),
		packagePipelineRow(s, "04  PRESENT", present, width, statusActive),
	)
	return rows
}

func renderPackageTabs(m Model, width int) string {
	tabs := make([]string, 0, len(m.packageFlow.providers))
	for i, provider := range m.packageFlow.providers {
		state := packagePhaseLabel(provider.Phase)
		switch {
		case !provider.Spec.Enabled:
			state = "DISABLED"
		case provider.Phase == packages.SearchDone:
			state = fmt.Sprintf("%d", len(provider.Candidates))
		case provider.Phase == packages.SearchFailed || provider.Phase == packages.SearchTimedOut:
			state = "FAILED"
		case !packageSearchTerminal(provider.Phase):
			state = "…"
		}
		label := fmt.Sprintf("[ %s %s ]", packageProviderLabel(provider), state)
		style := m.styles.muted
		if i == m.packageFlow.activeProvider {
			style = m.styles.active
		}
		tabs = append(tabs, style.Render(label))
	}
	if len(tabs) == 0 {
		return m.styles.danger.Render("NO PACKAGE PROVIDERS")
	}
	return truncateVisible(m.styles.label.Render("RESULTS")+"  "+strings.Join(tabs, "  "), width)
}

func renderProviderResults(m Model, provider packageProviderState, width, limit int) []string {
	s := m.styles
	if len(provider.Candidates) == 0 {
		message := "NO PACKAGE MATCHES"
		switch {
		case !provider.Spec.Enabled:
			message = strings.TrimSpace("DISABLED  " + provider.Spec.DisabledReason)
		case !packageSearchTerminal(provider.Phase):
			message = "NO RESULTS YET"
		case provider.Phase == packages.SearchFailed || provider.Phase == packages.SearchTimedOut:
			message = "FAILED  ESC SEARCH AGAIN"
		}
		return []string{s.danger.Render(truncateVisible(message, width))}
	}
	rows := make([]string, 0, limit)
	defaultNix := -1
	start, end := packageScrolledWindow(len(provider.Candidates), provider.Scroll, limit)
	for i := start; i < end; i++ {
		candidate := provider.Candidates[i]
		if defaultNix < 0 && candidate.Provider == packages.ProviderNix {
			defaultNix = i
		}
		providerName := strings.ToUpper(string(candidate.Provider))
		kind := strings.ToUpper(string(candidate.Kind))
		meta := strings.TrimSpace(providerName + " / " + kind + "  " + candidate.Version)
		label := candidate.Name
		if label == "" {
			label = candidate.ID
		}
		if meta != "" {
			label += "  " + meta
		}
		status := statusText(s, "READY", statusMuted)
		if i == defaultNix {
			status = statusText(s, "DEFAULT", statusActive)
		}
		labelWidth := max(1, width-lipgloss.Width(status)-8)
		rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), truncateVisible(label, labelWidth), status, width, i == provider.Selected))
	}
	return rows
}

func packagePhaseLabel(phase packages.SearchPhase) string {
	if phase == "" {
		return "SEARCHING"
	}
	return strings.ToUpper(strings.ReplaceAll(string(phase), "-", " "))
}

func packageSweep(frame, width int) string {
	if width <= 0 {
		return ""
	}
	blocks := make([]rune, width)
	for i := range blocks {
		blocks[i] = '·'
	}
	blocks[frame%width] = '█'
	return string(blocks)
}

func packageElapsedLabel(elapsed time.Duration) string {
	if elapsed < time.Millisecond {
		return "0MS"
	}
	return strings.ToUpper(elapsed.Round(time.Millisecond).String())
}

func packageProviderLabel(provider packageProviderState) string {
	if provider.Spec.Label != "" {
		return provider.Spec.Label
	}
	return strings.ToUpper(string(provider.Spec.Provider))
}

func packageVisibleResultLimit(height int) int {
	if height > 0 && height <= 24 {
		return 4
	}
	return 12
}

func packageVisibleSectionLimit(height int) int {
	if height > 0 && height <= 24 {
		return 4
	}
	return 10
}

func packageVisibleWindow(total, selected, limit int) (int, int) {
	if total <= limit {
		return 0, total
	}
	start := selected - limit/2
	if start < 0 {
		start = 0
	}
	if start+limit > total {
		start = total - limit
	}
	return start, start + limit
}

func packageScrolledWindow(total, scroll, limit int) (int, int) {
	if total <= limit {
		return 0, total
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll+limit > total {
		scroll = total - limit
	}
	return scroll, scroll + limit
}

func (m Model) viewPackageReview() string {
	contentWidth := primaryContentWidth(m.width)
	s := m.styles
	packagePlan := m.reviewed.Package
	fileRows := packageLabeledValueRows(s, "FILE", packagePlan.Proposal.Target.Path, contentWidth)
	verification := packageVerificationLabel(packagePlan.Verify)
	if packagePlan.Revert {
		verification = "previous declaration restored after apply"
	}
	verifyRows := packageLabeledValueRows(s, "VERIFY", verification, contentWidth)
	commandRows := make([]string, 0, len(m.reviewed.Items))
	for i, item := range m.reviewed.Items {
		commandRows = append(commandRows, reviewCommandRows(s, fmt.Sprintf("%02d", i+1), runner.CmdLabel(item), statusText(s, "READY", statusMuted), contentWidth)...)
	}
	diffVP := packagePlan.DiffVP
	if diffVP.Width <= 0 || diffVP.Height <= 0 {
		diffVP = m.newPackageDiffViewport(packagePlan, 0)
	}
	diffTop := min(diffVP.TotalLineCount(), diffVP.YOffset+1)
	diffBottom := min(diffVP.TotalLineCount(), diffVP.YOffset+diffVP.VisibleLineCount())
	diffPosition := fmt.Sprintf("DIFF  %02d-%02d/%02d", diffTop, diffBottom, diffVP.TotalLineCount())
	rows := []string{
		s.major.Render("REVIEW/PACKAGE"),
		s.label.Render("DECLARATIVE INSTALL"),
		majorRule(s, contentWidth, true),
		"",
	}
	rows = append(rows, fileRows...)
	if packagePlan.Warning != "" {
		rows = append(rows, s.danger.Render("WARNING  "+packagePlan.Warning))
	}
	editStatus := statusText(s, "READY", statusMuted)
	editLabel := diffPosition
	if packagePlan.EditApplied {
		editStatus = statusText(s, "DONE", statusSuccess)
		editLabel = "EDIT CONTEXT  " + strings.TrimPrefix(diffPosition, "DIFF  ")
	}
	rows = append(rows, "", s.label.Render(editLabel)+"  "+editStatus, diffVP.View())
	rows = append(rows, "", s.label.Render("APPLY"))
	rows = append(rows, commandRows...)
	rows = append(rows,
		"",
	)
	rows = append(rows, verifyRows...)
	rows = append(rows, "", majorRule(s, contentWidth, false), "", s.muted.Render("J/K SCROLL   PGUP/PGDN PAGE   ESCAPE BACK")+"   "+s.active.Render("ENTER CONFIRM"))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func (m Model) newPackageDiffViewport(review *packageReview, offset int) viewport.Model {
	width := primaryContentWidth(m.width)
	vp := viewport.New(width, m.packageDiffViewportHeight(review))
	vp.SetContent(renderPackageDiffContent(m.styles, review.Proposal.Diff, width))
	vp.SetYOffset(offset)
	return vp
}

func (m Model) packageDiffViewportHeight(review *packageReview) int {
	if m.height <= 0 {
		return 8
	}
	contentWidth := primaryContentWidth(m.width)
	fileRows := packageLabeledValueRows(m.styles, "FILE", review.Proposal.Target.Path, contentWidth)
	verification := packageVerificationLabel(review.Verify)
	if review.Revert {
		verification = "previous declaration restored after apply"
	}
	verifyRows := packageLabeledValueRows(m.styles, "VERIFY", verification, contentWidth)
	commandRows := 0
	for i, item := range m.reviewed.Items {
		commandRows += len(reviewCommandRows(m.styles, fmt.Sprintf("%02d", i+1), runner.CmdLabel(item), statusText(m.styles, "READY", statusMuted), contentWidth))
	}
	const fixedRows = 18
	extras := len(fileRows) - 1 + len(verifyRows) - 1 + commandRows - len(m.reviewed.Items)
	return max(1, m.height-fixedRows-extras)
}

func renderPackageDiffContent(s uiStyles, diff string, width int) string {
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		style := s.muted
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			style = s.active
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			style = s.danger
		}
		for _, wrapped := range wrapExactText(line, width) {
			rendered = append(rendered, style.Render(wrapped))
		}
	}
	return strings.Join(rendered, "\n")
}

func wrapExactText(text string, width int) []string {
	if width < 1 || lipgloss.Width(text) <= width {
		return []string{text}
	}
	runes := []rune(text)
	var lines []string
	for len(runes) > 0 {
		cut := 1
		for cut < len(runes) && lipgloss.Width(string(runes[:cut+1])) <= width {
			cut++
		}
		lines = append(lines, string(runes[:cut]))
		runes = runes[cut:]
	}
	return lines
}

func (m *Model) initPackageDiffViewport() {
	if m.reviewed.Package == nil {
		return
	}
	m.reviewed.Package = clonePackageReview(m.reviewed.Package)
	m.reviewed.Package.DiffVP = m.newPackageDiffViewport(m.reviewed.Package, 0)
}

func (m *Model) resizePackageDiffViewport() {
	if m.reviewed.Package == nil {
		return
	}
	offset := m.reviewed.Package.DiffVP.YOffset
	m.reviewed.Package = clonePackageReview(m.reviewed.Package)
	m.reviewed.Package.DiffVP = m.newPackageDiffViewport(m.reviewed.Package, offset)
}

func (m *Model) scrollPackageDiff(key string) bool {
	if m.reviewed.Package == nil {
		return false
	}
	m.reviewed.Package = clonePackageReview(m.reviewed.Package)
	if m.reviewed.Package.DiffVP.Width <= 0 {
		m.reviewed.Package.DiffVP = m.newPackageDiffViewport(m.reviewed.Package, 0)
	}
	switch key {
	case "j", "down":
		m.reviewed.Package.DiffVP.ScrollDown(1)
	case "k", "up":
		m.reviewed.Package.DiffVP.ScrollUp(1)
	case "pgdown":
		m.reviewed.Package.DiffVP.PageDown()
	case "pgup":
		m.reviewed.Package.DiffVP.PageUp()
	default:
		return false
	}
	return true
}

func packageLabeledValueRows(s uiStyles, label, value string, width int) []string {
	prefix := s.label.Render(label) + "  "
	valueWidth := max(1, width-len(label)-2)
	wrapped := wrapText(value, valueWidth)
	rows := []string{prefix + s.text.Render(wrapped[0])}
	indent := strings.Repeat(" ", len(label)+2)
	for _, line := range wrapped[1:] {
		rows = append(rows, indent+s.text.Render(line))
	}
	return rows
}
