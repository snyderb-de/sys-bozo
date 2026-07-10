package main

import (
	"bufio"
	"fmt"
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

	got := fmt.Errorf("%s: %w", item.Name, err).Error()
	duplicatePrefix := item.Name + ": " + item.Name + ":"
	if strings.HasPrefix(got, duplicatePrefix) {
		t.Fatalf("error = %q, duplicate command prefix %q", got, duplicatePrefix)
	}
}
