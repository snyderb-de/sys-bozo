package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TraceInput records input delivery and UI timings without recording typed text,
// rendered output, repository contents, or environment values. It is opt-in.
func TraceInput(model Model, input *os.File, output io.Writer) (tea.Model, io.Reader) {
	trace := &inputTrace{start: time.Now(), output: json.NewEncoder(output)}
	trace.record("start", nil)
	return tracedModel{Model: model, trace: trace}, &tracedInput{File: input, trace: trace}
}

type inputTrace struct {
	mu     sync.Mutex
	start  time.Time
	output *json.Encoder
}

func (t *inputTrace) record(event string, fields map[string]any) {
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["event"] = event
	fields["ms"] = float64(time.Since(t.start).Microseconds()) / 1000
	t.mu.Lock()
	defer t.mu.Unlock()
	// Diagnostics must never turn a logging error into a failed UI action.
	_ = t.output.Encode(fields)
}

type tracedInput struct {
	*os.File // Preserve Fd and Name so terminal setup and cancelreader are unchanged.
	trace    *inputTrace
}

func (r *tracedInput) Read(p []byte) (int, error) {
	n, err := r.File.Read(p)
	esc, ctrlC := 0, 0
	for _, b := range p[:n] {
		if b == 27 {
			esc++
		}
		if b == 3 {
			ctrlC++
		}
	}
	r.trace.record("input", map[string]any{"bytes": n, "escape_bytes": esc, "ctrl_c_bytes": ctrlC, "read_error": err != nil})
	return n, err
}

type tracedModel struct {
	Model
	trace *inputTrace
}

func (m tracedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	fields := map[string]any{"message": fmt.Sprintf("%T", msg), "screen": m.screen}
	if key, ok := msg.(tea.KeyMsg); ok {
		fields["key_type"], fields["alt"], fields["paste"] = int(key.Type), key.Alt, key.Paste
	}
	m.trace.record("update_start", fields)
	started := time.Now()
	next, cmd := m.Model.Update(msg)
	model := next.(Model)
	m.trace.record("update_done", map[string]any{"screen": model.screen, "help": model.helpVisible, "elapsed_us": time.Since(started).Microseconds()})
	return tracedModel{Model: model, trace: m.trace}, cmd
}

func (m tracedModel) View() string {
	m.trace.record("view_start", map[string]any{"screen": m.screen})
	started := time.Now()
	out := m.Model.View()
	m.trace.record("view_done", map[string]any{"screen": m.screen, "elapsed_us": time.Since(started).Microseconds()})
	return out
}
