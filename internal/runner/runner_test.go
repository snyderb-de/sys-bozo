package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultTasksIncludeSelfUpdateAndTopgradeSweep(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "sys-bozo"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := Context{
		SysBozoRepo: repo,
		SysBozoBin:  filepath.Join(repo, "sys-bozo"),
		GitBin:      "git",
		GoBin:       "go",
		Topgrade:    "topgrade",
	}

	tasks := DefaultTasks(ctx)
	if len(tasks) == 0 || tasks[0].ID != "sys-bozo-self-update" {
		t.Fatalf("first task should update sys-bozo, got %#v", tasks)
	}
	if !tasks[0].Available(ctx) || !tasks[0].Selected {
		t.Fatal("sys-bozo self-update task should be available and selected")
	}

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
	if !topgrade.Available(ctx) || !topgrade.Selected {
		t.Fatal("topgrade task should be available and selected")
	}

	name, args := topgrade.Steps[0].Cmd(ctx)
	text := name + " " + strings.Join(args, " ")
	for _, want := range []string{"topgrade", "--skip-notify", "--no-self-update", "home_manager", "brew_formula", "system"} {
		if !strings.Contains(text, want) {
			t.Fatalf("topgrade command missing %q: %s", want, text)
		}
	}
}
