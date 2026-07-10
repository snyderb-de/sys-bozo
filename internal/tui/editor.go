package tui

import (
	"fmt"
	"strings"
	"unicode"
)

// parseEditorArgv parses the quoting and backslash forms commonly used in
// EDITOR. It deliberately does not perform shell expansion or interpolation.
func parseEditorArgv(value string) ([]string, error) {
	var argv []string
	var word strings.Builder
	var quote rune
	escaped := false
	started := false
	flush := func() {
		if started {
			argv = append(argv, word.String())
			word.Reset()
			started = false
		}
	}
	for _, r := range value {
		if escaped {
			word.WriteRune(r)
			escaped = false
			started = true
			continue
		}
		if quote == '\'' {
			if r == '\'' {
				quote = 0
			} else {
				word.WriteRune(r)
			}
			started = true
			continue
		}
		if quote == '"' {
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				word.WriteRune(r)
			}
			started = true
			continue
		}
		switch {
		case r == '\\':
			escaped = true
			started = true
		case r == '\'' || r == '"':
			quote = r
			started = true
		case unicode.IsSpace(r):
			flush()
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("malformed EDITOR: trailing backslash")
	}
	if quote != 0 {
		return nil, fmt.Errorf("malformed EDITOR: unterminated quote")
	}
	flush()
	if len(argv) == 0 || argv[0] == "" {
		return nil, fmt.Errorf("malformed EDITOR: missing executable")
	}
	return argv, nil
}
