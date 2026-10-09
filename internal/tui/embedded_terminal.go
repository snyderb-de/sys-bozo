package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/terminal"
)

type terminalStartedMsg struct {
	generation uint64
	session    *terminal.Session
	err        error
}
type terminalTickMsg struct{ generation uint64 }

// Close reaps this TUI's PTY children even when Program.Run exits on a signal
// or error rather than returning through a keyboard handler.
func (m Model) Close() {
	if m.terminalGroup != nil {
		m.terminalGroup.Close()
	}
}

func (m Model) terminalDimensions() (int, int) {
	height := m.height
	if height <= 0 {
		height = 24
	}
	return primaryContentWidth(m.width), max(1, height-9)
}
func (m *Model) startEmbeddedTerminal(item runner.WorkItem) tea.Cmd {
	if m.terminalGroup == nil {
		m.terminalGroup = terminal.NewGroup()
	}
	m.terminalGeneration++
	generation, group := m.terminalGeneration, m.terminalGroup
	width, height := m.terminalDimensions()
	m.terminalStarting, m.terminalFocused, m.embeddedStep = true, true, true
	m.terminalQuit, m.terminalCancelling = false, false
	m.terminalNotice = ""
	m.termCapture = nil
	return func() tea.Msg {
		session, err := group.Start(runner.Command(item), width, height)
		return terminalStartedMsg{generation, session, err}
	}
}
func terminalTick(generation uint64) tea.Cmd {
	return tea.Tick(time.Second/30, func(time.Time) tea.Msg { return terminalTickMsg{generation} })
}
func (m Model) acceptTerminalStart(msg terminalStartedMsg) (tea.Model, tea.Cmd) {
	if msg.generation != m.terminalGeneration || !m.terminalStarting {
		if msg.session != nil {
			msg.session.Cancel()
		}
		return m, nil
	}
	m.terminalStarting = false
	if msg.err != nil {
		return m.Update(stepDoneMsg{err: msg.err, elapsed: time.Since(m.stepStart), cancelled: errors.Is(msg.err, context.Canceled)})
	}
	m.activeTerminal = msg.session
	width, height := m.terminalDimensions()
	_ = m.activeTerminal.Resize(width, height)
	if m.terminalCancelling {
		m.activeTerminal.Cancel()
	}
	return m, terminalTick(m.terminalGeneration)
}
func (m Model) acceptTerminalTick(msg terminalTickMsg) (tea.Model, tea.Cmd) {
	if msg.generation != m.terminalGeneration || m.activeTerminal == nil {
		return m, nil
	}
	select {
	case <-m.activeTerminal.Done():
		session := m.activeTerminal
		// Retain a bounded in-memory transcript for the result/log view. Never
		// append input or the private PTY transcript to persistent run history.
		for _, line := range strings.Split(session.Transcript(), "\n") {
			m.logLines = append(m.logLines, logLine{kind: logOutput, text: line})
		}
		err := session.Wait()
		cancelled := session.Cancelled() || m.terminalCancelling
		if cancelled {
			err = context.Canceled
		}
		return m.Update(stepDoneMsg{err: err, elapsed: time.Since(m.stepStart), cancelled: cancelled})
	default:
		return m, terminalTick(m.terminalGeneration)
	}
}
func (m Model) embeddedTerminalActive() bool {
	return m.mode == modeRunning && (m.terminalStarting || m.activeTerminal != nil)
}

func (m Model) handleTerminalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl-C remains the global quit key. Stop and reap the child before quit.
	if msg.Type == tea.KeyCtrlC {
		m.terminalQuit, m.terminalCancelling = true, true
		m.terminalNotice = "Stopping command before exit…"
		if m.activeTerminal != nil {
			m.activeTerminal.Cancel()
		}
		group := m.terminalGroup
		return m, func() tea.Msg {
			if group != nil {
				group.Close()
			}
			return nil
		}
	}
	if msg.String() == "ctrl+]" {
		m.terminalFocused = !m.terminalFocused
		if m.activeTerminal != nil {
			_ = m.activeTerminal.Follow()
		}
		return m, nil
	}
	if msg.Type == tea.KeyEsc && !msg.Alt {
		m.terminalFocused = false
		return m, nil
	}
	if m.terminalTooSmall() || m.terminalStarting || m.terminalCancelling {
		return m, nil
	}
	var err error
	if m.terminalFocused {
		if msg.Paste {
			err = m.activeTerminal.Paste(string(msg.Runes))
		} else {
			keys := terminalKeys(msg)
			if len(keys) > 0 {
				err = m.activeTerminal.Keys(keys...)
			}
		}
	} else {
		switch msg.String() {
		case "enter":
			m.terminalFocused = true
			err = m.activeTerminal.Follow()
		case "pgup":
			_, height := m.terminalDimensions()
			err = m.activeTerminal.Scroll(height)
		case "pgdown":
			_, height := m.terminalDimensions()
			err = m.activeTerminal.Scroll(-height)
		case "up", "k":
			err = m.activeTerminal.Scroll(1)
		case "down", "j":
			err = m.activeTerminal.Scroll(-1)
		case "f":
			err = m.activeTerminal.Follow()
		case "x":
			m.terminalCancelling = true
			m.terminalNotice = "Cancelling command…"
			m.activeTerminal.Cancel()
		}
	}
	if err != nil {
		m.terminalNotice = err.Error()
	} else if !m.terminalCancelling {
		m.terminalNotice = ""
	}
	return m, nil
}

// Translate Bubble Tea's decoded input; the emulator chooses the sequence for
// the child's current cursor-key and bracketed-paste modes. Alt+Esc sends a
// literal Escape, while unmodified Escape returns to dashboard controls.
func terminalKeys(msg tea.KeyMsg) []uv.KeyPressEvent {
	mod := uv.KeyMod(0)
	if msg.Alt {
		mod |= uv.ModAlt
	}
	if msg.Type == tea.KeyRunes {
		keys := make([]uv.KeyPressEvent, len(msg.Runes))
		for i, r := range msg.Runes {
			keys[i] = uv.KeyPressEvent{Code: r, Mod: mod}
		}
		return keys
	}
	if msg.Type == tea.KeyEsc && msg.Alt {
		return []uv.KeyPressEvent{{Code: uv.KeyEscape}}
	}
	names := map[tea.KeyType]rune{
		tea.KeyEnter: uv.KeyEnter, tea.KeyTab: uv.KeyTab, tea.KeyBackspace: uv.KeyBackspace,
		tea.KeyEsc: uv.KeyEscape, tea.KeySpace: uv.KeySpace, tea.KeyUp: uv.KeyUp, tea.KeyDown: uv.KeyDown,
		tea.KeyLeft: uv.KeyLeft, tea.KeyRight: uv.KeyRight, tea.KeyHome: uv.KeyHome, tea.KeyEnd: uv.KeyEnd,
		tea.KeyPgUp: uv.KeyPgUp, tea.KeyPgDown: uv.KeyPgDown, tea.KeyDelete: uv.KeyDelete, tea.KeyInsert: uv.KeyInsert,
		tea.KeyF1: uv.KeyF1, tea.KeyF2: uv.KeyF2, tea.KeyF3: uv.KeyF3, tea.KeyF4: uv.KeyF4,
		tea.KeyF5: uv.KeyF5, tea.KeyF6: uv.KeyF6, tea.KeyF7: uv.KeyF7, tea.KeyF8: uv.KeyF8,
		tea.KeyF9: uv.KeyF9, tea.KeyF10: uv.KeyF10, tea.KeyF11: uv.KeyF11, tea.KeyF12: uv.KeyF12,
	}
	if code, ok := names[msg.Type]; ok {
		return []uv.KeyPressEvent{{Code: code, Mod: mod}}
	}
	if msg.Type == tea.KeyShiftTab {
		return []uv.KeyPressEvent{{Code: uv.KeyTab, Mod: mod | uv.ModShift}}
	}
	if msg.Type >= tea.KeyCtrlA && msg.Type <= tea.KeyCtrlZ {
		return []uv.KeyPressEvent{{Code: rune('a' + int(msg.Type) - int(tea.KeyCtrlA)), Mod: mod | uv.ModCtrl}}
	}
	return nil
}

func (m Model) viewEmbeddedTerminal() string {
	s, width := m.styles, primaryContentWidth(m.width)
	_, height := m.terminalDimensions()
	title := "Starting command…"
	if m.queuePos < len(m.queue) {
		title = runner.CmdLabel(m.queue[m.queuePos])
	}
	state := "Terminal · dashboard controls"
	if m.terminalFocused {
		state = "Terminal · input active"
	}
	if m.terminalStarting {
		state = "Terminal · starting"
	}
	if m.terminalNotice != "" {
		state = m.terminalNotice
	}
	rows := []string{
		screenTitle(s, "RUN/ACTIVE", width),
		dashboardPair(s.text.Render(fmt.Sprintf("Step %d / %d", m.queuePos+1, len(m.queue))), s.muted.Render(formatRunElapsed(time.Since(m.runStart))), width),
		majorRule(s, width, true), s.text.Render(truncateVisible(title, width)), s.active.Render(truncateVisible(state, width)),
	}
	pane := ""
	if m.activeTerminal != nil {
		snap := m.activeTerminal.Snapshot()
		pane = snap.View
		if m.terminalFocused {
			pane = snap.CursorView
		}
		if s.noColor {
			pane = snap.Plain
		}
	}
	lines := strings.Split(pane, "\n")
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = truncateVisible(lines[i], width)
		}
		rows = append(rows, line)
	}
	foot := "Ctrl+] dashboard · Esc dashboard · Ctrl+C quit"
	if !m.terminalFocused {
		foot = "Enter input · PgUp/PgDn scroll · X cancel · Ctrl+C quit"
	}
	rows = append(rows, majorRule(s, width, false), s.muted.Render(truncateVisible(foot, width)))
	return primaryFrame(s, m.width, strings.Join(rows, "\n"))
}
