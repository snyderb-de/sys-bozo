package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snyderb-de/sys-bozo/internal/runner"
)

func TestRunWorkItemDispatchesInteractiveMode(t *testing.T) {
	oldInteractive := runInteractive
	oldStreamed := startStreamed
	t.Cleanup(func() { runInteractive, startStreamed = oldInteractive, oldStreamed })

	interactiveCalls, streamedCalls := 0, 0
	runInteractive = func(runner.WorkItem) error { interactiveCalls++; return nil }
	startStreamed = func(runner.WorkItem) (*bufio.Scanner, func() error, error) {
		streamedCalls++
		return bufio.NewScanner(strings.NewReader("")), func() error { return nil }, nil
	}

	err := runWorkItem(runner.WorkItem{Mode: runner.ExecutionInteractive})
	if err != nil || interactiveCalls != 1 || streamedCalls != 0 {
		t.Fatalf("err=%v interactive=%d streamed=%d", err, interactiveCalls, streamedCalls)
	}
}

func TestRunWorkItemStreamedPreservesLargePhysicalLineByteExact(t *testing.T) {
	oldStreamed, oldStdout := startStreamed, os.Stdout
	t.Cleanup(func() { startStreamed, os.Stdout = oldStreamed, oldStdout })
	startStreamed = runner.StartWork
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	captured := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(r); captured <- b }()
	want := append(bytes.Repeat([]byte{'x'}, 131072), '\n')
	err = runWorkItem(runner.WorkItem{Name: "sh", Args: []string{"-c", `head -c 131072 /dev/zero | tr '\0' x; printf '\n'`}, Mode: runner.ExecutionStreamed})
	_ = w.Close()
	got := <-captured
	_ = r.Close()
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("err=%v got=%d want=%d equal=%v", err, len(got), len(want), bytes.Equal(got, want))
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestRunWorkItemStreamedPropagatesScannerAndWaitErrors(t *testing.T) {
	old := startStreamed
	t.Cleanup(func() { startStreamed = old })
	readErr := errors.New("fixture read failed")
	waitErr := errors.New("fixture wait failed")
	startStreamed = func(runner.WorkItem) (*bufio.Scanner, func() error, error) {
		return bufio.NewScanner(failingReader{err: readErr}), func() error { return waitErr }, nil
	}
	err := runWorkItem(runner.WorkItem{Name: "fixture", Mode: runner.ExecutionStreamed})
	if !errors.Is(err, readErr) || !errors.Is(err, waitErr) {
		t.Fatalf("err=%v", err)
	}
}

func TestRunWorkItemDispatchesStreamedMode(t *testing.T) {
	oldInteractive := runInteractive
	oldStreamed := startStreamed
	t.Cleanup(func() { runInteractive, startStreamed = oldInteractive, oldStreamed })

	interactiveCalls, streamedCalls := 0, 0
	runInteractive = func(runner.WorkItem) error { interactiveCalls++; return nil }
	startStreamed = func(runner.WorkItem) (*bufio.Scanner, func() error, error) {
		streamedCalls++
		return bufio.NewScanner(strings.NewReader("line\n")), func() error { return nil }, nil
	}

	err := runWorkItem(runner.WorkItem{Mode: runner.ExecutionStreamed})
	if err != nil || interactiveCalls != 0 || streamedCalls != 1 {
		t.Fatalf("err=%v interactive=%d streamed=%d", err, interactiveCalls, streamedCalls)
	}
}

func TestRunWorkItemStreamedStartErrorHasSingleCommandPrefix(t *testing.T) {
	oldStreamed := startStreamed
	t.Cleanup(func() { startStreamed = oldStreamed })
	startStreamed = runner.StartWork

	item := runner.WorkItem{
		Name: filepath.Join(t.TempDir(), "missing-command"),
		Mode: runner.ExecutionStreamed,
	}
	err := runWorkItem(item)
	if err == nil {
		t.Fatal("runWorkItem returned nil error")
	}

	got := err.Error()
	prefix := item.Name + ": "
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("error = %q, want prefix %q", got, prefix)
	}
	if strings.HasPrefix(strings.TrimPrefix(got, prefix), prefix) {
		t.Fatalf("error = %q, duplicate command prefix %q", got, prefix)
	}
}

func TestRunWorkItemInteractiveErrorHasSingleCommandPrefix(t *testing.T) {
	oldInteractive := runInteractive
	t.Cleanup(func() { runInteractive = oldInteractive })
	runInteractive = func(runner.WorkItem) error { return errors.New("exit status 1") }

	err := runWorkItem(runner.WorkItem{Name: "sudo", Mode: runner.ExecutionInteractive})
	if err == nil {
		t.Fatal("runWorkItem returned nil error")
	}
	if got, want := err.Error(), "sudo: exit status 1"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
