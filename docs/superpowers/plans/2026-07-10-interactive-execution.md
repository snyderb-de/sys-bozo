# Interactive Execution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route commands that may prompt for credentials or installer input through a real terminal, fixing the iLok/Homebrew password failure in both the TUI and CLI.

**Architecture:** Add execution mode metadata to runner steps and propagate it into immutable work items. Streamed work keeps the existing pipe runner; interactive work uses Bubble Tea `tea.ExecProcess` in the TUI and native stdio in the CLI.

**Tech Stack:** Go 1.24.2, Bubble Tea 1.3.10, standard `os/exec`, existing Go test harness.

## Global Constraints

- Password and terminal input must never enter sys-bozo state, logs, or history.
- Every command shown at Review must be the same immutable work item later executed.
- Do not add a pseudo-terminal dependency or ANSI terminal emulator.
- Interactive failure or `Ctrl+C` must return control to the caller and stop the remaining queue.
- Keep streamed command output behavior unchanged.
- Use Nix-provided Go for verification when the ambient Go version is not 1.24.2 or newer.

## File Map

- `internal/runner/runner.go`: execution modes, command construction, streamed runner, native terminal runner.
- `internal/runner/runner_test.go`: metadata propagation and native stdio tests.
- `internal/tui/model.go`: branch queue advancement into streamed or terminal-handoff commands.
- `internal/tui/model_test.go`: terminal-handoff regression and restoration transitions.
- `cmd/sys-bozo/main.go`: CLI dispatch by execution mode; remove broad startup credential priming.
- `cmd/sys-bozo/main_test.go`: CLI interactive/streamed dispatch tests using injected executor functions.

---

### Task 1: Execution Mode Metadata

**Files:**
- Modify: `internal/runner/runner.go:30-84`
- Modify: `internal/runner/runner.go:145-375`
- Modify: `internal/runner/runner_test.go`

**Interfaces:**
- Produces: `type ExecutionMode uint8`, `ExecutionStreamed`, `ExecutionInteractive`.
- Produces: `Step.Mode ExecutionMode` and `WorkItem.Mode ExecutionMode`.
- Consumes: existing `Task`, `Step`, `WorkItem`, and `BuildQueue` APIs.

- [ ] **Step 1: Write failing propagation and classification tests**

Append tests that prove mode survives queue construction and prompting task steps are classified explicitly:

```go
func TestBuildQueuePropagatesExecutionMode(t *testing.T) {
	task := Task{
		ID: "prompting",
		Available: func(Context) bool { return true },
		Steps: []Step{{
			Mode: ExecutionInteractive,
			Cmd:  func(Context) (string, []string) { return "sudo", []string{"-v"} },
		}},
	}

	queue := BuildQueue(task, Context{})
	if len(queue) != 1 || queue[0].Mode != ExecutionInteractive {
		t.Fatalf("expected interactive work item, got %#v", queue)
	}
}

func TestPromptingDefaultStepsAreInteractive(t *testing.T) {
	ctx := Context{
		OS: "darwin", Hostname: "mini", BrewBin: "brew",
		DarwinRebuild: "darwin-rebuild", SudoBin: "sudo",
		NixBin: "nix", HomeManager: "home-manager",
	}
	tasks := DefaultTasks(ctx)

	wantInteractive := map[string]bool{"nds": true, "ndu": true, "ndR": true, "brew": true}
	for _, task := range tasks {
		if !wantInteractive[task.ID] {
			continue
		}
		found := false
		for _, step := range task.Steps {
			name, _ := step.Cmd(ctx)
			if name == "sudo" || task.ID == "brew" && step.Title == "Upgrade Homebrew packages" {
				found = true
				if step.Mode != ExecutionInteractive {
					t.Fatalf("%s prompting step is not interactive", task.ID)
				}
			}
		}
		if !found {
			t.Fatalf("%s has no prompting step", task.ID)
		}
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/runner -run 'TestBuildQueuePropagatesExecutionMode|TestPromptingDefaultStepsAreInteractive' -v`

Expected: compile failure because `ExecutionInteractive`, `Step.Mode`, and `WorkItem.Mode` do not exist.

- [ ] **Step 3: Add execution mode and propagate it**

Add these definitions and fields:

```go
type ExecutionMode uint8

const (
	ExecutionStreamed ExecutionMode = iota
	ExecutionInteractive
)

type Step struct {
	Title       string
	Description string
	Targets     []string
	Mode        ExecutionMode
	Cmd         func(ctx Context) (name string, args []string)
}

type WorkItem struct {
	TaskLabel string
	TaskFirst bool
	Name      string
	Args      []string
	Dir       string
	EnvExtra  []string
	Mode      ExecutionMode
}
```

Copy `step.Mode` in `BuildQueue`:

```go
		items[j] = WorkItem{
			TaskLabel: task.Label,
			TaskFirst: j == 0,
			Name:      name,
			Args:      args,
			Dir:       dir,
			EnvExtra:  env,
			Mode:      step.Mode,
		}
```

Set `Mode: ExecutionInteractive` on every direct `sudo` step, the Brew `Upgrade Homebrew packages` step, the Fedora sudo step, and the corresponding sudo/Brew steps built by `buildAllTask`. Leave Nix, Home Manager, Git, Go build, Brew update, Brew autoremove, and Topgrade streamed.

- [ ] **Step 4: Run runner tests and verify GREEN**

Run: `go test ./internal/runner -v`

Expected: all runner tests pass.

- [ ] **Step 5: Commit metadata**

```bash
git add internal/runner/runner.go internal/runner/runner_test.go
git commit -m "feat(runner): classify interactive work"
```

---

### Task 2: Shared Command Construction and Native CLI Stdio

**Files:**
- Modify: `internal/runner/runner.go:393-425`
- Modify: `internal/runner/runner_test.go`

**Interfaces:**
- Produces: `func Command(WorkItem) *exec.Cmd`.
- Produces: `func RunInteractive(WorkItem, io.Reader, io.Writer, io.Writer) error`.
- Consumes: `WorkItem` and existing `StartWork`.

- [ ] **Step 1: Write failing command and stdio tests**

```go
func TestCommandAppliesDirAndEnvironment(t *testing.T) {
	t.Setenv("SYS_BOZO_BASE", "present")
	w := WorkItem{Name: "sh", Args: []string{"-c", "true"}, Dir: t.TempDir(), EnvExtra: []string{"EXTRA=value"}}
	cmd := Command(w)
	if cmd.Dir != w.Dir {
		t.Fatalf("dir = %q, want %q", cmd.Dir, w.Dir)
	}
	joined := strings.Join(cmd.Env, "\n")
	for _, want := range []string{"SYS_BOZO_BASE=present", "EXTRA=value"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("environment missing %q", want)
		}
	}
}

func TestRunInteractiveUsesProvidedStdio(t *testing.T) {
	var stdout, stderr strings.Builder
	w := WorkItem{Name: "sh", Args: []string{"-c", `read value; printf 'out:%s' "$value"; printf 'err:%s' "$value" >&2`}}
	err := RunInteractive(w, strings.NewReader("secretless-test\n"), &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "out:secretless-test" || stderr.String() != "err:secretless-test" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/runner -run 'TestCommandAppliesDirAndEnvironment|TestRunInteractiveUsesProvidedStdio' -v`

Expected: compile failure because `Command` and `RunInteractive` do not exist.

- [ ] **Step 3: Extract command construction and native runner**

```go
func Command(w WorkItem) *exec.Cmd {
	cmd := exec.Command(w.Name, w.Args...)
	if w.Dir != "" {
		cmd.Dir = w.Dir
	}
	cmd.Env = append([]string{}, os.Environ()...)
	cmd.Env = append(cmd.Env, w.EnvExtra...)
	return cmd
}

func RunInteractive(w WorkItem, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := Command(w)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
```

Replace duplicated `exec.Command`, `Dir`, and `Env` setup at the top of `StartWork` with `cmd := Command(w)`. Keep its pipe setup and wait goroutine unchanged.

- [ ] **Step 4: Run runner tests and verify GREEN**

Run: `go test ./internal/runner -v`

Expected: all runner tests pass.

- [ ] **Step 5: Commit shared execution**

```bash
git add internal/runner/runner.go internal/runner/runner_test.go
git commit -m "refactor(runner): share command construction"
```

---

### Task 3: Bubble Tea Terminal Handoff

**Files:**
- Modify: `internal/tui/model.go:125-330`
- Modify: `internal/tui/model.go:452-585`
- Modify: `internal/tui/model_test.go`

**Interfaces:**
- Consumes: `runner.ExecutionInteractive` and `runner.Command` from Tasks 1-2.
- Produces: `terminalExec func(runner.WorkItem, time.Time) tea.Cmd` dependency on `Model`.
- Produces: `func runInteractiveWork(runner.WorkItem, time.Time) tea.Cmd`.

- [ ] **Step 1: Write failing handoff regression tests**

```go
func TestAdvanceQueueUsesTerminalHandoffForInteractiveWork(t *testing.T) {
	called := false
	m := Model{
		mode: modeRunning,
		queue: []runner.WorkItem{{Name: "sudo", Args: []string{"-v"}, Mode: runner.ExecutionInteractive}},
		terminalExec: func(item runner.WorkItem, start time.Time) tea.Cmd {
			called = true
			return func() tea.Msg { return stepDoneMsg{elapsed: time.Second} }
		},
	}

	cmd := m.advanceQueue()
	if !called || cmd == nil {
		t.Fatal("interactive work did not use terminal handoff")
	}
	if m.activeScanner != nil {
		t.Fatal("interactive work must not create captured scanner")
	}
}

func TestInteractiveFailureStopsQueueAndRestoresDoneState(t *testing.T) {
	m := Model{mode: modeRunning, runAction: "brew", runStart: time.Now()}
	next, _ := m.Update(stepDoneMsg{err: errors.New("exit status 1"), elapsed: time.Second})
	got := next.(Model)
	if got.mode != modeDone || !strings.Contains(got.renderLog(), "exit status 1") {
		t.Fatalf("mode=%v log=%q", got.mode, got.renderLog())
	}
}
```

Add `errors` and `time` imports to the test file.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./internal/tui -run 'TestAdvanceQueueUsesTerminalHandoffForInteractiveWork|TestInteractiveFailureStopsQueueAndRestoresDoneState' -v`

Expected: compile failure because `Model.terminalExec` does not exist.

- [ ] **Step 3: Add injectable terminal execution and queue branch**

Add to `Model`:

```go
	terminalExec func(runner.WorkItem, time.Time) tea.Cmd
```

Set it in `New`:

```go
		terminalExec: runInteractiveWork,
```

Add the production handoff:

```go
func runInteractiveWork(item runner.WorkItem, start time.Time) tea.Cmd {
	return tea.ExecProcess(runner.Command(item), func(err error) tea.Msg {
		return stepDoneMsg{err: err, elapsed: time.Since(start)}
	})
}
```

In `advanceQueue`, after logging the command and setting `m.stepStart`, branch before `runner.StartWork`:

```go
	if item.Mode == runner.ExecutionInteractive {
		m.logLines = append(m.logLines, logLine{
			kind: logOutput,
			text: "  ! terminal handoff — input stays outside sys-bozo",
		})
		m.logVP.SetContent(m.renderLog())
		m.logVP.GotoBottom()
		execInteractive := m.terminalExec
		if execInteractive == nil {
			execInteractive = runInteractiveWork
		}
		return execInteractive(item, m.stepStart)
	}
```

- [ ] **Step 4: Run TUI tests and full tests**

Run: `go test ./internal/tui -v && go test ./...`

Expected: all tests pass; interactive test never launches a real prompt.

- [ ] **Step 5: Commit TUI handoff**

```bash
git add internal/tui/model.go internal/tui/model_test.go
git commit -m "fix(tui): hand interactive work the terminal"
```

---

### Task 4: CLI Parity and Credential-Priming Removal

**Files:**
- Modify: `cmd/sys-bozo/main.go:24-31`
- Modify: `cmd/sys-bozo/main.go:108-133`
- Create: `cmd/sys-bozo/main_test.go`
- Modify: `internal/runner/runner.go:56-64`

**Interfaces:**
- Consumes: `runner.ExecutionInteractive`, `runner.RunInteractive`, and `runner.StartWork`.
- Produces: package-level `runInteractive` and `startStreamed` function variables for CLI tests.

- [ ] **Step 1: Write failing CLI dispatch tests**

Create `cmd/sys-bozo/main_test.go`:

```go
package main

import (
	"bufio"
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
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./cmd/sys-bozo -v`

Expected: compile failure because `runInteractive`, `startStreamed`, and `runWorkItem` do not exist.

- [ ] **Step 3: Add CLI dispatch and remove startup priming**

Add to `main.go`:

```go
var runInteractive = func(item runner.WorkItem) error {
	return runner.RunInteractive(item, os.Stdin, os.Stdout, os.Stderr)
}

var startStreamed = runner.StartWork

func runWorkItem(item runner.WorkItem) error {
	if item.Mode == runner.ExecutionInteractive {
		return runInteractive(item)
	}
	scanner, wait, err := startStreamed(item)
	if err != nil {
		return err
	}
	for scanner.Scan() {
		fmt.Fprintln(os.Stdout, scanner.Text())
	}
	return wait()
}
```

Replace the body of the queue loop in `runAction` with:

```go
	for _, item := range queue {
		fmt.Fprintf(os.Stderr, "$ %s\n", runner.CmdLabel(item))
		if err := runWorkItem(item); err != nil {
			return fmt.Errorf("%s: %w", item.Name, err)
		}
	}
```

Remove `runner.PrimeCredentials(runner.Build())` before TUI startup and delete `PrimeCredentials` from `runner.go`; interactive work now prompts only while it owns the terminal.

- [ ] **Step 4: Run all automated checks**

Run: `go test ./... && go vet ./...`

Expected: all tests and vet pass.

- [ ] **Step 5: Build through Nix**

Run: `nix build .#packages.$(nix eval --impure --raw --expr builtins.currentSystem).default`

Expected: exit 0 and `result/bin/sys-bozo` exists.

- [ ] **Step 6: Manual safe handoff smoke test**

Temporarily run the existing test helper through `go test` under a real terminal rather than invoking Homebrew:

```bash
go test ./internal/runner -run TestRunInteractiveUsesProvidedStdio -v
```

Then launch the TUI, select an interactive reviewed step only after the guided Review gate exists in the next plan, and confirm normal terminal restoration. Do not run `brew upgrade` merely to test input.

- [ ] **Step 7: Commit CLI parity**

```bash
git add cmd/sys-bozo/main.go cmd/sys-bozo/main_test.go internal/runner/runner.go
git commit -m "fix(cli): attach interactive work to terminal"
```
