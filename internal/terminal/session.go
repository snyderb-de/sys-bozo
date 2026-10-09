// Package terminal runs reviewed commands in an isolated pseudo-terminal.
// Child escape sequences are interpreted in memory, never sent to the host TTY.
package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Group owns pending starts and running children for one TUI invocation.
// Close must be deferred around Program.Run, including its error/signal paths.
type Group struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func NewGroup() *Group {
	ctx, cancel := context.WithCancel(context.Background())
	return &Group{ctx: ctx, cancel: cancel}
}

func (g *Group) Start(cmd *exec.Cmd, width, height int) (*Session, error) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, context.Canceled
	}
	g.wg.Add(1)
	g.mu.Unlock()
	s, err := Start(g.ctx, cmd, width, height)
	if err != nil {
		g.wg.Done()
		return nil, err
	}
	go func() { <-s.done; g.wg.Done() }()
	return s, nil
}

func (g *Group) Close() {
	g.mu.Lock()
	g.closed = true
	g.cancel()
	g.mu.Unlock()
	g.wg.Wait()
}

// Snapshot is immutable. Rendering never waits on a child blocked on input.
type Snapshot struct {
	View, CursorView, Plain string
	Width, Height, Offset   int
}

type input struct {
	keys                  []uv.KeyPressEvent
	paste                 *string
	width, height, scroll int
	resize, follow        bool
}

type Session struct {
	master    *os.File
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	done      chan struct{}
	actions   chan input
	snapshot  atomic.Pointer[Snapshot]
	cancelled atomic.Bool
	// Published before closing done.
	err        error
	transcript string
}

func Start(parent context.Context, cmd *exec.Cmd, width, height int) (*Session, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	width, height = dimensions(width, height)
	cmd.Env = append(cmd.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	if err != nil {
		return nil, fmt.Errorf("start terminal: %w", err)
	}
	// Re-wrap a nonblocking descriptor so Go registers it with its poller.
	// On macOS pty.Open returns a blocking NewFile: merely toggling O_NONBLOCK
	// afterward makes Read return EAGAIN instead of waiting for child output.
	fd, err := unix.FcntlInt(master.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err == nil {
		err = syscall.SetNonblock(fd, true)
		if err != nil {
			_ = syscall.Close(fd)
		}
	}
	if err != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = master.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("configure terminal: %w", err)
	}
	_ = master.Close()
	master = os.NewFile(uintptr(fd), "sys-bozo-pty")
	ctx, cancel := context.WithCancel(parent)
	s := &Session{master: master, cmd: cmd, cancel: cancel, done: make(chan struct{}), actions: make(chan input, 128)}
	e := vt.NewEmulator(width, height)
	e.SetScrollbackSize(1000)
	visible := true
	e.SetCallbacks(vt.Callbacks{CursorVisibility: func(v bool) { visible = v }})
	s.snapshot.Store(render(e, 0, visible))
	// The emulator's input pipe carries keys and terminal-query replies only.
	// Neither this stream nor typed input is copied to any log or history.
	pipe := e.InputPipe().(io.Closer)
	inputDone := make(chan struct{})
	go func() { defer close(inputDone); _, _ = io.Copy(master, e); _ = pipe.Close() }()
	output := make(chan []byte, 8)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(output)
		buf := make([]byte, 32768)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				data := append([]byte(nil), buf[:n]...)
				select {
				case output <- data:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// Cancellation must be able to unblock the emulator even if the child
	// refuses to read input. Signal the process group, then bound forced cleanup.
	cancelDone := make(chan struct{})
	processDone := make(chan struct{})
	go func() {
		defer close(cancelDone)
		select {
		case <-processDone:
			return
		case <-ctx.Done():
			s.cancelled.Store(true)
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			_ = pipe.Close()
			_ = master.Close()
			timer := time.NewTimer(750 * time.Millisecond)
			defer timer.Stop()
			// Also kill descendants still in our group after the leader exits.
			<-timer.C
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()
	go func() {
		offset := 0
		var drain <-chan time.Time
		var drainTimer *time.Timer
		childExited, outputEnded := false, false
		for !childExited || !outputEnded {
			select {
			case data, ok := <-output:
				if !ok {
					outputEnded = true
					output = nil
					continue
				}
				before := e.ScrollbackLen()
				_, _ = e.Write(data)
				if offset > 0 {
					offset += e.ScrollbackLen() - before
				}
			case a := <-s.actions:
				if a.resize {
					e.Resize(a.width, a.height)
					_ = resizePTY(master, a.width, a.height)
				}
				if a.follow {
					offset = 0
				}
				offset += a.scroll
				if a.paste != nil {
					e.Paste(*a.paste)
				}
				for _, key := range a.keys {
					e.SendKey(key)
				}
			case err := <-exited:
				s.err = err
				childExited = true
				exited = nil
				// Drain final output, but descendants holding the TTY open cannot hang
				// the queue indefinitely after the reviewed command has exited.
				drainTimer = time.NewTimer(500 * time.Millisecond)
				drain = drainTimer.C
			case <-drain:
				_ = master.Close()
				drain = nil
			}
			offset = max(0, min(offset, e.ScrollbackLen()))
			s.snapshot.Store(render(e, offset, visible))
		}
		if drainTimer != nil {
			drainTimer.Stop()
		}
		_ = pipe.Close()
		_ = master.Close()
		<-inputDone
		<-readDone
		s.transcript = transcript(e)
		s.snapshot.Store(render(e, 0, false))
		_ = e.Close()
		close(processDone)
		<-cancelDone
		if s.cancelled.Load() {
			s.err = context.Canceled
		}
		close(s.done)
		cancel()
	}()
	return s, nil
}

func dimensions(w, h int) (int, int) { return max(1, min(w, 1000)), max(1, min(h, 500)) }

// Control keeps the descriptor alive across ioctl even if cancellation closes
// the file concurrently. File.Fd (used by pty.Setsize) provides no such guard.
func resizePTY(file *os.File, width, height int) error {
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	err = conn.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(width), Row: uint16(height)})
	})
	if err != nil {
		return err
	}
	return ioctlErr
}

func (s *Session) Snapshot() Snapshot    { return *s.snapshot.Load() }
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Wait() error           { <-s.done; return s.err }
func (s *Session) Transcript() string    { <-s.done; return s.transcript }
func (s *Session) Cancel() {
	select {
	case <-s.done:
		return
	default:
		s.cancel()
	}
}
func (s *Session) Cancelled() bool { return s.cancelled.Load() }

var ErrInputBusy = errors.New("terminal input queue is full; wait for the command to read input")

func (s *Session) enqueue(a input) error {
	select {
	case <-s.done:
		return io.ErrClosedPipe
	default:
	}
	select {
	case s.actions <- a:
		return nil
	case <-s.done:
		return io.ErrClosedPipe
	default:
		return ErrInputBusy
	}
}
func (s *Session) Keys(keys ...uv.KeyPressEvent) error { return s.enqueue(input{keys: keys}) }
func (s *Session) Paste(text string) error             { return s.enqueue(input{paste: &text}) }
func (s *Session) Resize(w, h int) error {
	w, h = dimensions(w, h)
	return s.enqueue(input{resize: true, width: w, height: h})
}
func (s *Session) Scroll(lines int) error { return s.enqueue(input{scroll: lines}) }
func (s *Session) Follow() error          { return s.enqueue(input{follow: true}) }

func render(e *vt.Emulator, offset int, visible bool) *Snapshot {
	w, h := e.Width(), e.Height()
	lines := make(uv.Lines, h)
	cursor := e.CursorPosition()
	for y := 0; y < h; y++ {
		lines[y] = uv.NewLine(w)
		logical := e.ScrollbackLen() - offset + y
		for x := 0; x < w; x++ {
			var c *uv.Cell
			if logical < e.ScrollbackLen() && !e.IsAltScreen() {
				c = e.ScrollbackCellAt(x, logical)
			} else {
				row := y - offset
				if e.IsAltScreen() {
					row = y
				}
				c = e.CellAt(x, row)
			}
			if c != nil {
				lines[y][x] = *c
				lines[y][x].Link = uv.Link{}
			}
		}
	}
	snap := &Snapshot{View: lines.Render(), Plain: lines.String(), Width: w, Height: h, Offset: offset}
	if offset == 0 && visible && cursor.Y >= 0 && cursor.Y < h && cursor.X >= 0 && cursor.X < w {
		c := &lines[cursor.Y][cursor.X]
		if c.Width == 0 {
			*c = uv.EmptyCell
		}
		c.Style.Attrs ^= uv.AttrReverse
	}
	snap.CursorView = lines.Render()
	return snap
}

func transcript(e *vt.Emulator) string {
	var lines []string
	for _, line := range e.Scrollback().Lines() {
		lines = append(lines, line.String())
	}
	lines = append(lines, e.String())
	return strings.TrimRight(strings.Join(lines, "\n"), " \n")
}
