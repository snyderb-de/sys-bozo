package terminal

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

func fixture(t *testing.T, script string) *Session {
	t.Helper()
	s, err := Start(context.Background(), exec.Command("/bin/sh", "-c", script), 60, 12)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Cancel()
		select {
		case <-s.Done():
		case <-time.After(3 * time.Second):
			t.Error("child leaked")
		}
	})
	return s
}
func waitFor(t *testing.T, s *Session, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.Snapshot().Plain, want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("missing %q: %q", want, s.Snapshot().Plain)
}
func waitExit(t *testing.T, s *Session) error {
	t.Helper()
	select {
	case <-s.Done():
		return s.Wait()
	case <-time.After(3 * time.Second):
		t.Fatal("exit timed out")
		return nil
	}
}

func TestPromptWithoutNewlineAndNoEchoInput(t *testing.T) {
	s := fixture(t, `test -t 0 && test -t 1 && test -t 2 || exit 2
stty -echo
printf 'Password: '
IFS= read -r value
stty echo
if [ "$value" = 'fixture-secret' ]; then printf '\r\nAUTH_OK\r\n'; else exit 3; fi`)
	waitFor(t, s, "Password:")
	if err := s.Paste("fixture-secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Keys(uv.KeyPressEvent{Code: uv.KeyEnter}); err != nil {
		t.Fatal(err)
	}
	if err := waitExit(t, s); err != nil {
		t.Fatal(err)
	}
	if got := s.Transcript(); !strings.Contains(got, "AUTH_OK") || strings.Contains(got, "fixture-secret") {
		t.Fatalf("bad capture: %q", got)
	}
}
func TestTerminalRedrawsAreContained(t *testing.T) {
	s := fixture(t, `printf '\033[2J\033[Hstale\r\033[2Kfresh\r\nsecond\r\n\033]52;c;ignored\007'`)
	if err := waitExit(t, s); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if strings.Contains(snap.Plain, "stale") || !strings.Contains(snap.Plain, "fresh\nsecond") {
		t.Fatalf("bad terminal screen: %q", snap.Plain)
	}
	for _, control := range []string{"\x1b[2J", "\x1b[H", "\x1b]52"} {
		if strings.Contains(snap.View, control) {
			t.Fatalf("escaped pane: %q", snap.View)
		}
	}
}
func TestResizeReachesChild(t *testing.T) {
	s := fixture(t, `printf 'READY\n'; read -r reply; stty size`)
	waitFor(t, s, "READY")
	if err := s.Resize(83, 17); err != nil {
		t.Fatal(err)
	}
	if err := s.Keys(uv.KeyPressEvent{Code: uv.KeyEnter}); err != nil {
		t.Fatal(err)
	}
	if err := waitExit(t, s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Transcript(), "17 83") {
		t.Fatalf("wrong child size: %q", s.Transcript())
	}
}
func TestCancellationKillsStubbornChild(t *testing.T) {
	s := fixture(t, `trap '' TERM HUP; printf 'READY\n'; while :; do sleep 1; done`)
	waitFor(t, s, "READY")
	s.Cancel()
	if err := waitExit(t, s); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if !s.Cancelled() {
		t.Fatal("cancel status lost")
	}
}
func TestStartFailureAndClosedGroup(t *testing.T) {
	g := NewGroup()
	defer g.Close()
	if _, err := g.Start(exec.Command("/nonexistent/fixture"), 80, 24); err == nil {
		t.Fatal("missing launch error")
	}
	g.Close()
	if _, err := g.Start(exec.Command("/bin/sh", "-c", "exit 0"), 80, 24); !errors.Is(err, context.Canceled) {
		t.Fatalf("closed group start=%v", err)
	}
}
func TestExitCodeAndFinalOutput(t *testing.T) {
	s := fixture(t, `printf 'last error\n' >&2; exit 7`)
	var exit *exec.ExitError
	if err := waitExit(t, s); !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit=%v", err)
	}
	if !strings.Contains(s.Transcript(), "last error") {
		t.Fatal("lost final output")
	}
}

func TestTerminalAnswersCursorQuery(t *testing.T) {
	s := fixture(t, `stty raw -echo; printf '\033[6n'; reply=$(dd bs=1 count=6 2>/dev/null); printf '\r\nREPLY:%s\r\n' "${#reply}"`)
	if err := waitExit(t, s); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Transcript(), "REPLY:6") {
		t.Fatalf("query reply missing: %q", s.Transcript())
	}
}

func TestScrollbackAndFollow(t *testing.T) {
	s := fixture(t, `i=0; while [ "$i" -lt 50 ]; do printf 'row-%02d\n' "$i"; i=$((i+1)); done; printf 'READY\n'; read -r value`)
	waitFor(t, s, "READY")
	if err := s.Scroll(1000); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "row-00")
	if s.Snapshot().Offset == 0 {
		t.Fatal("scroll offset lost")
	}
	if err := s.Follow(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, "READY")
	if s.Snapshot().Offset != 0 {
		t.Fatal("follow did not return to live output")
	}
}
