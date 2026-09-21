package tui

import (
	"bytes"
	"strings"
	"sync"
)

// terminalCapture tees an interactive step's stderr so a failure can be
// explained after the fact. Interactive steps hand the terminal to the child
// process, so nothing they print reaches the log pane and a failure arrives as
// a bare "exit status 1". Stdin and stdout stay wired to the real terminal —
// only stderr is duplicated here — so prompts, passwords and progress output
// behave exactly as they did before.
type terminalCapture struct {
	mu      sync.Mutex
	pending []byte
	lines   []string
	limit   int
}

func newTerminalCapture(limit int) *terminalCapture {
	if limit < 1 {
		limit = 1
	}
	return &terminalCapture{limit: limit}
}

// Write never fails: losing a capture line must not kill the running step.
func (c *terminalCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = append(c.pending, p...)
	for {
		i := bytes.IndexByte(c.pending, '\n')
		if i < 0 {
			break
		}
		c.append(string(c.pending[:i]))
		c.pending = c.pending[i+1:]
	}
	// A prompt without a trailing newline would otherwise be held forever.
	if len(c.pending) > 4096 {
		c.append(string(c.pending))
		c.pending = nil
	}
	return len(p), nil
}

func (c *terminalCapture) append(line string) {
	line = strings.TrimRight(line, "\r")
	if strings.TrimSpace(line) == "" {
		return
	}
	c.lines = append(c.lines, strings.TrimSpace(line))
	if len(c.lines) > c.limit {
		c.lines = c.lines[len(c.lines)-c.limit:]
	}
}

// Lines returns the captured tail, including any unterminated trailing text.
func (c *terminalCapture) Lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]string(nil), c.lines...)
	if trailing := strings.TrimSpace(string(c.pending)); trailing != "" {
		out = append(out, trailing)
	}
	if len(out) > c.limit {
		out = out[len(out)-c.limit:]
	}
	return out
}
