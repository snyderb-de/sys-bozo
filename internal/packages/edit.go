package packages

import (
	"bytes"
	"crypto/sha256"
	"path/filepath"
	"strconv"
	"strings"
)

const uncategorizedSection Section = "Uncategorized"

type sourceLine struct {
	body   []byte
	ending []byte
}

type parsedList struct {
	lines      []sourceLine
	assignment int
	opening    int
	closing    int
	closeAt    int
	nested     bool
	inline     bool
	badSuffix  bool
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
	list, err := scanList(original, target)
	if err != nil {
		return Proposal{}, err
	}

	rendered := item
	if target.Quoted {
		rendered = strconv.Quote(item)
	}
	duplicate := list.containsItem(rendered)
	if list.inline && !list.nested && !list.badSuffix && duplicate {
		return Proposal{}, ErrAlreadyDeclared
	}
	if list.invalid() {
		return Proposal{}, ErrAmbiguousTarget
	}
	if duplicate {
		return Proposal{}, ErrAlreadyDeclared
	}

	section, next, ok := list.findSection(Section(sectionName))
	if !ok {
		return Proposal{}, ErrSectionNotFound
	}
	normalized := section.indent + rendered

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
	list, err := scanList(original, target)
	if err != nil {
		return parsedList{}, err
	}
	if list.invalid() {
		return parsedList{}, ErrAmbiguousTarget
	}
	return list, nil
}

func scanList(original []byte, target Target) (parsedList, error) {
	lines := splitLines(original)
	prefix := target.Assignment + " ="
	assignment := -1
	state := lexicalState{}
	for i, line := range lines {
		if state == (lexicalState{}) && strings.HasPrefix(strings.TrimSpace(string(line.body)), prefix) {
			if assignment >= 0 {
				return parsedList{}, ErrAmbiguousTarget
			}
			assignment = i
		}
		scanCode(line.body, 0, &state, func(_ int, _ byte) bool { return true })
	}
	if target.Assignment == "" || assignment < 0 {
		return parsedList{}, ErrAmbiguousTarget
	}

	opening, closing, closeAt, nested, inline, badSuffix, err := listBounds(lines, assignment, prefix)
	if err != nil {
		return parsedList{}, ErrAmbiguousTarget
	}

	sections, err := discoverSections(lines, assignment, opening, closing, closeAt)
	if err != nil {
		return parsedList{}, err
	}
	if len(sections) == 0 && !nested && !inline && !badSuffix {
		closingIndent := len(lines[closing].body) - len(bytes.TrimLeft(lines[closing].body, " \t"))
		sections = append(sections, parsedSection{
			name:   uncategorizedSection,
			line:   assignment,
			indent: string(lines[closing].body[:closingIndent]) + "  ",
		})
	}

	return parsedList{
		lines:      lines,
		assignment: assignment,
		opening:    opening,
		closing:    closing,
		closeAt:    closeAt,
		nested:     nested,
		inline:     inline,
		badSuffix:  badSuffix,
		sections:   sections,
	}, nil
}

func discoverSections(lines []sourceLine, assignment, opening, closing, closeAt int) ([]parsedSection, error) {
	sections := make([]parsedSection, 0)
	names := make(map[Section]struct{})
	state := lexicalState{}
	for lineIndex := assignment; lineIndex <= closing; lineIndex++ {
		start := 0
		end := len(lines[lineIndex].body)
		if lineIndex == assignment {
			start = opening + 1
		}
		if lineIndex == closing {
			end = closeAt
		}
		segment := lines[lineIndex].body[start:end]
		trimmed := strings.TrimSpace(string(segment))
		if state == (lexicalState{}) && strings.HasPrefix(trimmed, "# ") && len(strings.TrimSpace(trimmed[2:])) > 0 {
			name := Section(strings.TrimSpace(trimmed[2:]))
			if _, exists := names[name]; exists {
				return nil, ErrAmbiguousTarget
			}
			names[name] = struct{}{}
			indentLength := len(lines[lineIndex].body) - len(bytes.TrimLeft(lines[lineIndex].body, " \t"))
			sections = append(sections, parsedSection{
				name:   name,
				line:   lineIndex,
				indent: string(lines[lineIndex].body[:indentLength]),
			})
		}
		scanCode(lines[lineIndex].body, start, &state, func(_ int, _ byte) bool { return true })
	}
	return sections, nil
}

type lexicalState struct {
	quote         byte
	escaped       bool
	blockComment  bool
	indentedQuote bool
}

func listBounds(lines []sourceLine, assignment int, prefix string) (int, int, int, bool, bool, bool, error) {
	body := lines[assignment].body
	leading := len(body) - len(bytes.TrimLeft(body, " \t"))
	start := leading + len(prefix)
	if start > len(body) || (start < len(body) && body[start] == '=') {
		return 0, 0, 0, false, false, false, ErrAmbiguousTarget
	}

	state := lexicalState{}
	opening := -1
	openingLine := -1
	closing := -1
	closeAt := -1
	depth := 0
	nested := false
	unexpectedClose := false
	for lineIndex := assignment; lineIndex < len(lines) && closing < 0; lineIndex++ {
		lineStart := 0
		if lineIndex == assignment {
			lineStart = start
		}
		scanCode(lines[lineIndex].body, lineStart, &state, func(offset int, char byte) bool {
			switch char {
			case '[':
				if opening < 0 {
					opening = offset
					openingLine = lineIndex
					depth = 1
					return true
				}
				depth++
				nested = true
			case ']':
				if opening < 0 {
					unexpectedClose = true
					return false
				}
				depth--
				if depth == 0 {
					closing = lineIndex
					closeAt = offset
					return false
				}
			}
			return true
		})
	}

	if opening < 0 || openingLine != assignment || closing < 0 || unexpectedClose {
		return 0, 0, 0, false, false, false, ErrAmbiguousTarget
	}
	return opening, closing, closeAt, nested, closing == assignment, !validClosingSuffix(lines[closing].body[closeAt+1:]), nil
}

func (l parsedList) invalid() bool {
	return l.nested || l.inline || l.badSuffix
}

func (l parsedList) containsItem(item string) bool {
	for _, token := range l.tokens() {
		if token == item {
			return true
		}
	}
	return false
}

func (l parsedList) tokens() []string {
	var tokens []string
	var token strings.Builder
	state := lexicalState{}
	flush := func() {
		if token.Len() > 0 {
			tokens = append(tokens, token.String())
			token.Reset()
		}
	}

	for lineIndex := l.assignment; lineIndex <= l.closing; lineIndex++ {
		start := 0
		end := len(l.lines[lineIndex].body)
		if lineIndex == l.assignment {
			start = l.opening + 1
		}
		if lineIndex == l.closing {
			end = l.closeAt
		}
		line := l.lines[lineIndex].body
		for i := start; i < end; i++ {
			char := line[i]
			if state.blockComment {
				if char == '*' && i+1 < end && line[i+1] == '/' {
					state.blockComment = false
					i++
				}
				continue
			}
			if state.quote != 0 {
				token.WriteByte(char)
				if state.escaped {
					state.escaped = false
					continue
				}
				if char == '\\' {
					state.escaped = true
					continue
				}
				if char == state.quote {
					state.quote = 0
				}
				continue
			}

			switch {
			case char == '#':
				i = end
			case char == '/' && i+1 < end && line[i+1] == '*':
				flush()
				state.blockComment = true
				i++
			case char == '"':
				token.WriteByte(char)
				state.quote = char
			case char == '[' || char == ']' || char == ' ' || char == '\t' || char == '\r' || char == '\n':
				flush()
			default:
				token.WriteByte(char)
			}
		}
		flush()
	}
	return tokens
}

func scanCode(line []byte, start int, state *lexicalState, visit func(int, byte) bool) {
	for i := start; i < len(line); i++ {
		char := line[i]
		if state.blockComment {
			if char == '*' && i+1 < len(line) && line[i+1] == '/' {
				state.blockComment = false
				i++
			}
			continue
		}
		if state.indentedQuote {
			if char == '\'' && i+1 < len(line) && line[i+1] == '\'' {
				state.indentedQuote = false
				i++
			}
			continue
		}
		if state.quote != 0 {
			if state.escaped {
				state.escaped = false
				continue
			}
			if char == '\\' {
				state.escaped = true
				continue
			}
			if char == state.quote {
				state.quote = 0
			}
			continue
		}

		switch {
		case char == '#':
			return
		case char == '/' && i+1 < len(line) && line[i+1] == '*':
			state.blockComment = true
			i++
		case char == '\'' && i+1 < len(line) && line[i+1] == '\'':
			state.indentedQuote = true
			i++
		case char == '"':
			state.quote = char
		case !visit(i, char):
			return
		}
	}
}

func validClosingSuffix(suffix []byte) bool {
	i := 0
	skipWhitespace := func() {
		for i < len(suffix) && (suffix[i] == ' ' || suffix[i] == '\t') {
			i++
		}
	}
	skipWhitespace()
	if i >= len(suffix) || suffix[i] != ';' {
		return false
	}
	i++
	for {
		skipWhitespace()
		if i == len(suffix) || suffix[i] == '#' {
			return true
		}
		if i+1 >= len(suffix) || suffix[i] != '/' || suffix[i+1] != '*' {
			return false
		}
		end := bytes.Index(suffix[i+2:], []byte("*/"))
		if end < 0 {
			return false
		}
		i += end + 4
	}
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
	if len(line.ending) > 0 {
		diff.Write(line.ending)
		return
	}
	diff.WriteByte('\n')
	diff.WriteString("\\ No newline at end of file\n")
}
