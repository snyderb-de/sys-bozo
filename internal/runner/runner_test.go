package runner

import (
	"strings"
	"testing"
)

func TestDefaultTasksIncludeTopgradeSweep(t *testing.T) {
	ctx := Context{
		Topgrade: "topgrade",
	}

	tasks := DefaultTasks(ctx)

	var topgrade *Task
	for i := range tasks {
		if tasks[i].ID == "topgrade" {
			topgrade = &tasks[i]
			break
		}
	}
	if topgrade == nil {
		t.Fatal("missing topgrade task")
	}
	if !topgrade.Available(ctx) {
		t.Fatal("topgrade task should be available when topgrade binary is set")
	}

	name, args := topgrade.Steps[0].Cmd(ctx)
	text := name + " " + strings.Join(args, " ")
	for _, want := range []string{"topgrade", "--skip-notify", "--no-self-update", "home_manager", "brew_formula", "system"} {
		if !strings.Contains(text, want) {
			t.Fatalf("topgrade command missing %q: %s", want, text)
		}
	}
}

func TestDefaultTasksIncludeFedoraSystemUpgrade(t *testing.T) {
	ctx := Context{
		OS:      "linux",
		OSID:    "fedora",
		SudoBin: "sudo",
		DnfBin:  "dnf",
	}

	tasks := DefaultTasks(ctx)
	var fedora *Task
	for i := range tasks {
		if tasks[i].ID == "fedora-upgrade" {
			fedora = &tasks[i]
			break
		}
	}
	if fedora == nil {
		t.Fatal("missing fedora-upgrade task")
	}
	if !fedora.Available(ctx) {
		t.Fatal("fedora-upgrade task should be available on Fedora with dnf and sudo")
	}

	name, args := fedora.Steps[0].Cmd(ctx)
	text := name + " " + strings.Join(args, " ")
	for _, want := range []string{"sudo", "dnf", "upgrade", "--refresh", "-y"} {
		if !strings.Contains(text, want) {
			t.Fatalf("fedora command missing %q: %s", want, text)
		}
	}
}

func TestBuildQueuePropagatesExecutionMode(t *testing.T) {
	task := Task{
		ID:        "prompting",
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

func TestBuildQueuePropagatesRetryable(t *testing.T) {
	task := Task{
		ID:        "safe-retry",
		Available: func(Context) bool { return true },
		Steps: []Step{{
			Retryable: true,
			Cmd:       func(Context) (string, []string) { return "fixture", []string{"--safe"} },
		}},
	}

	queue := BuildQueue(task, Context{})
	if len(queue) != 1 || !queue[0].Retryable {
		t.Fatalf("retryable metadata not propagated: %#v", queue)
	}
}

func TestDefaultTaskRetryPolicyIsExplicit(t *testing.T) {
	ctx := Context{
		OS: "darwin", Hostname: "mini", BrewBin: "brew", DarwinRebuild: "darwin-rebuild",
		SudoBin: "sudo", NixBin: "nix", HomeManager: "home-manager", Topgrade: "topgrade",
	}
	wantRetryable := map[string][]bool{
		"nds": {true}, "ndu": {true, true}, "ndsd": {true}, "ndR": {false},
		"hms": {true}, "hmu": {true, true}, "hmr": {false},
		"fedora-upgrade": {true}, "brew": {true, true, true}, "topgrade": {true},
		"all": {true, true, true, true, true, true},
	}
	for _, task := range DefaultTasks(ctx) {
		want, known := wantRetryable[task.ID]
		if !known {
			t.Fatalf("default task %q has no explicit retry policy", task.ID)
		}
		if len(task.Steps) != len(want) {
			t.Fatalf("task %s has %d steps, retry policy covers %d", task.ID, len(task.Steps), len(want))
		}
		for i, step := range task.Steps {
			if step.Retryable != want[i] {
				t.Fatalf("task %s step %d retryable=%v want %v", task.ID, i, step.Retryable, want[i])
			}
		}
		delete(wantRetryable, task.ID)
	}
	if len(wantRetryable) != 0 {
		t.Fatalf("retry policy references missing default tasks: %v", wantRetryable)
	}
}

func TestPromptingDefaultStepsAreInteractive(t *testing.T) {
	ctx := Context{
		OS: "darwin", Hostname: "mini", BrewBin: "brew",
		DarwinRebuild: "darwin-rebuild", SudoBin: "sudo",
		NixBin: "nix", HomeManager: "home-manager",
	}
	tasks := DefaultTasks(ctx)

	wantInteractive := map[string]bool{"nds": true, "ndu": true, "ndR": true, "fedora-upgrade": true, "brew": true, "all": true}
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

func TestExecutionModeClassificationCoversFedoraAndCombinedPaths(t *testing.T) {
	tests := []struct {
		name string
		ctx  Context
		want map[string][]ExecutionMode
	}{
		{
			name: "fedora",
			ctx:  Context{OS: "linux", OSID: "fedora", NixBin: "nix", HomeManager: "home-manager", SudoBin: "sudo", DnfBin: "dnf", BrewBin: "brew"},
			want: map[string][]ExecutionMode{"fedora-upgrade": {ExecutionInteractive}, "all": {ExecutionStreamed, ExecutionStreamed, ExecutionStreamed, ExecutionInteractive, ExecutionStreamed}},
		},
		{
			name: "darwin",
			ctx:  Context{OS: "darwin", Hostname: "mini", NixBin: "nix", HomeManager: "home-manager", DarwinRebuild: "darwin-rebuild", BrewBin: "brew"},
			want: map[string][]ExecutionMode{"all": {ExecutionStreamed, ExecutionStreamed, ExecutionInteractive, ExecutionStreamed, ExecutionInteractive, ExecutionStreamed}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, task := range DefaultTasks(tt.ctx) {
				want, ok := tt.want[task.ID]
				if !ok {
					continue
				}
				queue := BuildQueue(task, tt.ctx)
				if len(queue) != len(want) {
					t.Fatalf("task=%s queue=%d want=%d", task.ID, len(queue), len(want))
				}
				for i := range want {
					if queue[i].Mode != want[i] {
						t.Fatalf("task=%s step=%d mode=%v want=%v", task.ID, i, queue[i].Mode, want[i])
					}
				}
			}
		})
	}
}

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
