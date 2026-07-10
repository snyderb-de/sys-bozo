package tui

import (
	"fmt"
	"strings"

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
		rows = append(rows,
			s.label.Render("SEARCH"),
			s.active.Render("SEARCHING")+"  "+s.text.Render(m.packageFlow.query.Value()),
			"",
			s.muted.Render("Provider queries are read-only."),
		)
	case packageChoose:
		rows = append(rows, s.label.Render("RESULTS"), "")
		if len(m.packageFlow.result.Candidates) == 0 {
			rows = append(rows, s.danger.Render("NO PACKAGE MATCHES"))
		}
		defaultNix := -1
		start, end := packageVisibleWindow(len(m.packageFlow.result.Candidates), m.packageFlow.result.Selected, packageVisibleResultLimit(m.height))
		for i := start; i < end; i++ {
			candidate := m.packageFlow.result.Candidates[i]
			if defaultNix < 0 && candidate.Provider == packages.ProviderNix {
				defaultNix = i
			}
			provider := strings.ToUpper(string(candidate.Provider))
			kind := strings.ToUpper(string(candidate.Kind))
			meta := strings.TrimSpace(provider + " / " + kind + "  " + candidate.Version)
			label := candidate.Name
			if label == "" {
				label = candidate.ID
			}
			status := statusText(s, "READY", statusMuted)
			if i == defaultNix {
				status = statusText(s, "DEFAULT", statusActive)
			}
			rows = append(rows, numberedRow(s, fmt.Sprintf("%02d", i+1), label, status, contentWidth, i == m.packageFlow.result.Selected))
			detail := strings.TrimSpace(meta + "  " + candidate.Description)
			rows = append(rows, "     "+s.muted.Render(truncateVisible(detail, max(1, contentWidth-5))))
		}
		if m.packageFlow.result.NixErr != nil {
			rows = append(rows, s.danger.Render(truncateVisible("NIX SEARCH WARNING  "+m.packageFlow.result.NixErr.Error(), contentWidth)))
		}
		if m.packageFlow.result.BrewErr != nil {
			rows = append(rows, s.danger.Render(truncateVisible("BREW SEARCH WARNING  "+m.packageFlow.result.BrewErr.Error(), contentWidth)))
		}
		rows = append(rows, "", s.muted.Render("ESCAPE SEARCH   ↑/↓ MOVE")+"   "+s.active.Render("ENTER PLACE"))
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
		rows = append(rows, "", s.muted.Render("ESCAPE BACK   ↑/↓ MOVE")+"   "+s.active.Render("ENTER SELECT"))
	}
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}

func packageVisibleResultLimit(height int) int {
	if height > 0 && height <= 24 {
		return 6
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
	rows := []string{
		s.major.Render("REVIEW/PACKAGE"),
		s.label.Render("DECLARATIVE INSTALL"),
		majorRule(s, contentWidth, true),
		"",
	}
	rows = append(rows, fileRows...)
	rows = append(rows, "", s.label.Render("DIFF"))
	diffLines := strings.Split(strings.TrimSuffix(packagePlan.Proposal.Diff, "\n"), "\n")
	compact := m.height > 0 && m.height <= 24
	if compact {
		limit := max(3, 6-(len(fileRows)-1)-(len(verifyRows)-1)-(len(commandRows)-len(m.reviewed.Items)))
		if len(diffLines) > limit {
			diffLines = compactPackageDiff(diffLines, limit)
		}
	}
	for _, line := range diffLines {
		style := s.muted
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			style = s.active
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			style = s.danger
		}
		if compact {
			rows = append(rows, style.Render(truncateVisible(line, contentWidth)))
			continue
		}
		for _, wrapped := range wrapText(line, contentWidth) {
			rows = append(rows, style.Render(wrapped))
		}
	}
	rows = append(rows, "", s.label.Render("APPLY"))
	rows = append(rows, commandRows...)
	rows = append(rows,
		"",
	)
	rows = append(rows, verifyRows...)
	rows = append(rows, "", majorRule(s, contentWidth, false), "", s.muted.Render("ESCAPE BACK")+"   "+s.active.Render("ENTER CONFIRM"))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
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

func compactPackageDiff(lines []string, limit int) []string {
	if len(lines) <= limit {
		return lines
	}
	selected := make([]bool, len(lines))
	count := 0
	selectLine := func(index int) {
		if index >= 0 && index < len(lines) && !selected[index] && count < limit {
			selected[index] = true
			count++
		}
	}
	selectLine(0)
	selectLine(1)
	changed := -1
	for i := 2; i < len(lines); i++ {
		if (strings.HasPrefix(lines[i], "+") && !strings.HasPrefix(lines[i], "+++")) ||
			(strings.HasPrefix(lines[i], "-") && !strings.HasPrefix(lines[i], "---")) {
			changed = i
			break
		}
	}
	if changed >= 0 {
		for i := changed; i >= 2; i-- {
			if strings.HasPrefix(lines[i], "@@") {
				selectLine(i)
				break
			}
		}
		selectLine(changed)
		for distance := 1; count < limit; distance++ {
			if changed-distance < 2 && changed+distance >= len(lines) {
				break
			}
			selectLine(changed - distance)
			selectLine(changed + distance)
		}
	}
	for i := range lines {
		selectLine(i)
	}
	out := make([]string, 0, limit)
	for i, line := range lines {
		if selected[i] {
			out = append(out, line)
		}
	}
	return out
}
