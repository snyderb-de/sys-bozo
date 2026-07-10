package packages

import (
	"bytes"
	"crypto/sha256"
	"path/filepath"
	"strconv"
	"strings"
)

type sourceLine struct {
	body   []byte
	ending []byte
}

type parsedList struct {
	lines      []sourceLine
	assignment int
	closing    int
	sections   []parsedSection
}

type parsedSection struct {
	name   Section
	line   int
	indent string
}

func ResolveTarget(repo, goos string, provider Provider, kind Kind, scope Scope) (Target, error) {
	switch {
	case provider == ProviderNix && scope == ScopeShared:
		return Target{Path: filepath.Join(repo, "home/modules/packages.nix"), Assignment: "home.packages", ApplyAction: "hms"}, nil
	case provider == ProviderNix && scope == ScopePlatform && goos == "darwin":
		return Target{Path: filepath.Join(repo, "home/darwin/default.nix"), Assignment: "home.packages", ApplyAction: "hms"}, nil
	case provider == ProviderNix && scope == ScopePlatform && goos == "linux":
		return Target{Path: filepath.Join(repo, "home/linux/default.nix"), Assignment: "home.packages", ApplyAction: "hms"}, nil
	case provider == ProviderBrew && scope == ScopeShared && kind == KindFormula:
		return Target{Path: filepath.Join(repo, "homebrew.nix"), Assignment: "brews", Quoted: true, ApplyAction: "nds"}, nil
	case provider == ProviderBrew && scope == ScopeShared && kind == KindCask:
		return Target{Path: filepath.Join(repo, "homebrew.nix"), Assignment: "casks", Quoted: true, ApplyAction: "nds"}, nil
	default:
		return Target{}, ErrAmbiguousTarget
	}
}

func Sections(original []byte, target Target) ([]Section, error) {
	list, err := parseList(original, target)
	if err != nil {
		return nil, err
	}
	sections := make([]Section, len(list.sections))
	for i, section := range list.sections {
		sections[i] = section.name
	}
	return sections, nil
}

func ProposeAdd(original []byte, target Target, sectionName, item string) (Proposal, error) {
	list, err := parseList(original, target)
	if err != nil {
		return Proposal{}, err
	}

	section, next, ok := list.findSection(Section(sectionName))
	if !ok {
		return Proposal{}, ErrSectionNotFound
	}

	rendered := item
	if target.Quoted {
		rendered = strconv.Quote(item)
	}
	normalized := section.indent + rendered
	for i := list.assignment + 1; i < list.closing; i++ {
		if string(bytes.TrimSpace(list.lines[i].body)) == rendered {
			return Proposal{}, ErrAlreadyDeclared
		}
	}

	insertAt := next
	for insertAt > section.line+1 && len(bytes.TrimSpace(list.lines[insertAt-1].body)) == 0 {
		insertAt--
	}
	ending := insertionEnding(list.lines, section.line, insertAt)
	inserted := sourceLine{body: []byte(normalized), ending: ending}
	proposedLines := make([]sourceLine, 0, len(list.lines)+1)
	proposedLines = append(proposedLines, list.lines[:insertAt]...)
	proposedLines = append(proposedLines, inserted)
	proposedLines = append(proposedLines, list.lines[insertAt:]...)

	originalCopy := bytes.Clone(original)
	proposed := joinLines(proposedLines)
	return Proposal{
		Target:       target,
		Original:     originalCopy,
		Proposed:     proposed,
		OriginalHash: sha256.Sum256(originalCopy),
		ProposedHash: sha256.Sum256(proposed),
		Diff:         unifiedInsertionDiff(list.lines, inserted, insertAt),
	}, nil
}

func parseList(original []byte, target Target) (parsedList, error) {
	lines := splitLines(original)
	prefix := target.Assignment + " ="
	assignment := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(string(line.body)), prefix) {
			if assignment >= 0 {
				return parsedList{}, ErrAmbiguousTarget
			}
			assignment = i
		}
	}
	if target.Assignment == "" || assignment < 0 || !hasListOpening(lines[assignment].body, prefix) {
		return parsedList{}, ErrAmbiguousTarget
	}

	closing := -1
	for i := assignment + 1; i < len(lines); i++ {
		if strings.TrimSpace(string(lines[i].body)) == "];" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return parsedList{}, ErrAmbiguousTarget
	}

	sections := make([]parsedSection, 0)
	sectionNames := make(map[Section]struct{})
	for i := assignment + 1; i < closing; i++ {
		trimmed := strings.TrimSpace(string(lines[i].body))
		if !strings.HasPrefix(trimmed, "# ") || len(strings.TrimSpace(trimmed[2:])) == 0 {
			continue
		}
		indentLength := len(lines[i].body) - len(bytes.TrimLeft(lines[i].body, " \t"))
		name := Section(strings.TrimSpace(trimmed[2:]))
		if _, exists := sectionNames[name]; exists {
			return parsedList{}, ErrAmbiguousTarget
		}
		sectionNames[name] = struct{}{}
		sections = append(sections, parsedSection{
			name:   name,
			line:   i,
			indent: string(lines[i].body[:indentLength]),
		})
	}

	return parsedList{lines: lines, assignment: assignment, closing: closing, sections: sections}, nil
}

func hasListOpening(line []byte, prefix string) bool {
	remainder := strings.TrimPrefix(strings.TrimSpace(string(line)), prefix)
	if strings.HasPrefix(remainder, "=") {
		return false
	}
	if comment := strings.IndexByte(remainder, '#'); comment >= 0 {
		remainder = remainder[:comment]
	}
	return strings.Count(remainder, "[") == 1 && !strings.Contains(remainder, "]")
}

func (l parsedList) findSection(name Section) (parsedSection, int, bool) {
	var found parsedSection
	next := l.closing
	matches := 0
	for i, section := range l.sections {
		if section.name != name {
			continue
		}
		matches++
		found = section
		if i+1 < len(l.sections) {
			next = l.sections[i+1].line
		}
	}
	return found, next, matches == 1
}

func splitLines(content []byte) []sourceLine {
	lines := make([]sourceLine, 0, bytes.Count(content, []byte("\n"))+1)
	for len(content) > 0 {
		newline := bytes.IndexByte(content, '\n')
		if newline < 0 {
			lines = append(lines, sourceLine{body: bytes.Clone(content)})
			break
		}
		bodyEnd := newline
		ending := []byte{'\n'}
		if newline > 0 && content[newline-1] == '\r' {
			bodyEnd--
			ending = []byte{'\r', '\n'}
		}
		lines = append(lines, sourceLine{
			body:   bytes.Clone(content[:bodyEnd]),
			ending: bytes.Clone(ending),
		})
		content = content[newline+1:]
	}
	return lines
}

func joinLines(lines []sourceLine) []byte {
	var content []byte
	for _, line := range lines {
		content = append(content, line.body...)
		content = append(content, line.ending...)
	}
	return content
}

func insertionEnding(lines []sourceLine, sectionLine, insertAt int) []byte {
	for i := insertAt - 1; i >= sectionLine; i-- {
		if len(lines[i].ending) > 0 {
			return bytes.Clone(lines[i].ending)
		}
	}
	for i := insertAt; i < len(lines); i++ {
		if len(lines[i].ending) > 0 {
			return bytes.Clone(lines[i].ending)
		}
	}
	return []byte("\n")
}

func unifiedInsertionDiff(original []sourceLine, inserted sourceLine, insertAt int) string {
	var diff strings.Builder
	diff.WriteString("--- original\n")
	diff.WriteString("+++ proposed\n")
	diff.WriteString("@@ -1,")
	diff.WriteString(strconv.Itoa(len(original)))
	diff.WriteString(" +1,")
	diff.WriteString(strconv.Itoa(len(original) + 1))
	diff.WriteString(" @@\n")
	for i, line := range original {
		if i == insertAt {
			writeDiffLine(&diff, '+', inserted)
		}
		writeDiffLine(&diff, ' ', line)
	}
	if insertAt == len(original) {
		writeDiffLine(&diff, '+', inserted)
	}
	return diff.String()
}

func writeDiffLine(diff *strings.Builder, prefix byte, line sourceLine) {
	diff.WriteByte(prefix)
	diff.Write(line.body)
	diff.WriteByte('\n')
}
