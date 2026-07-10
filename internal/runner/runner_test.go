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
