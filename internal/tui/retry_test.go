package tui

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/snyderb-de/sys-bozo/internal/runner"
)

func TestRerunCommandPreservesLiteralArgumentsAndDirectory(t *testing.T) {
	for _, shellName := range []string{"sh", "bash", "zsh"} {
		t.Run(shellName, func(t *testing.T) {
			shell, err := exec.LookPath(shellName)
			if err != nil {
				if shellName == "sh" {
					t.Fatal(err)
				}
				t.Skipf("optional shell unavailable: %s", shellName)
			}
			for _, scenario := range []string{"ordinary", "arguments", "executable", "directory", "relative-directory", "symlink-directory"} {
				t.Run(scenario, func(t *testing.T) {
					root, err := filepath.EvalSymlinks(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					name := "capture"
					if scenario == "executable" {
						name = "capture ' $(touch injected); `touch injected`"
					}
					program := filepath.Join(root, name)
					if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf '%s\\000' \"$PWD\" \"$@\"\n"), 0o700); err != nil {
						t.Fatal(err)
					}
					item := runner.WorkItem{Name: program, Args: []string{"flake", "update"}}
					if scenario == "arguments" {
						item.Args = []string{"", "two words", "two  spaces", "'quoted'", "$(touch injected)", "`touch injected`", "; touch injected;", "*.txt", "$HOME", "!event", "-n", "日本語.txt"}
					}
					wantDir := root
					switch scenario {
					case "directory":
						item.Dir = filepath.Join(root, "dir ' $(touch injected); `touch injected`")
						wantDir = item.Dir
					case "relative-directory":
						item.Dir = "-work"
						wantDir = filepath.Join(root, item.Dir)
					case "symlink-directory":
						if err := os.MkdirAll(filepath.Join(root, "target", "child"), 0o700); err != nil {
							t.Fatal(err)
						}
						if err := os.Symlink(filepath.Join(root, "target", "child"), filepath.Join(root, "link")); err != nil {
							t.Fatal(err)
						}
						item.Dir = "link/.."
						wantDir = filepath.Join(root, "target")
					}
					if err := os.MkdirAll(wantDir, 0o700); err != nil {
						t.Fatal(err)
					}
					trap := filepath.Join(root, "cdpath")
					if err := os.MkdirAll(filepath.Join(trap, "-work"), 0o700); err != nil {
						t.Fatal(err)
					}
					shellArgs := []string{"-c", rerunCommand(item)}
					if shellName == "zsh" {
						shellArgs = append([]string{"-f"}, shellArgs...)
					}
					cmd := exec.Command(shell, shellArgs...)
					cmd.Dir = root
					cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "CDPATH=" + trap}
					out, err := cmd.Output()
					if err != nil {
						t.Fatalf("retry failed: %v", err)
					}
					want := strings.Join(append([]string{wantDir}, item.Args...), "\x00") + "\x00"
					if !bytes.Equal(out, []byte(want)) {
						t.Fatalf("retry changed literal argv or directory: got %q, want %q", out, want)
					}
					if _, err := os.Stat(filepath.Join(root, "injected")); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("shell syntax executed: marker error %v", err)
					}
				})
			}
		})
	}
}

func TestRerunCommandRejectsUnrenderableWords(t *testing.T) {
	for _, word := range []string{"nul\x00", "line\nfeed", "carriage\rreturn", "tab\t", "escape\x1b[0m", "delete\x7f", "bidi\u202e", "line\u2028separator", "paragraph\u2029separator", "invalid\xff"} {
		for _, field := range []string{"name", "argument", "directory"} {
			item := runner.WorkItem{Name: "git", Args: []string{"status"}}
			switch field {
			case "name":
				item.Name = word
			case "argument":
				item.Args = append(item.Args, word)
			case "directory":
				item.Dir = word
			}
			if got := rerunCommand(item); got != "" {
				t.Errorf("unsafe %s %q produced retry %q", field, word, got)
			}
		}
	}
	if got := rerunCommand(runner.WorkItem{}); got != "" {
		t.Fatalf("empty command produced retry %q", got)
	}
}

func TestFailureRetryIsCompleteOrOmitted(t *testing.T) {
	item := runner.WorkItem{Name: "git", Args: []string{"add", "--", "two  spaces ' $(touch injected)-very-long-filename.txt"}}
	for _, plain := range []bool{false, true} {
		for _, width := range []int{80, 240} {
			rows := stepFailureRows(newUIStyles(plain), stepResult{Item: item}, width, true)
			rendered := ansi.Strip(strings.Join(rows, "\n"))
			if width == 240 {
				if !strings.Contains(rendered, "Rerun by hand: "+rerunCommand(item)) {
					t.Fatalf("full retry missing: %q", rendered)
				}
			} else if strings.Contains(rendered, "Rerun by hand:") || !strings.Contains(rendered, "widen terminal") {
				t.Fatalf("narrow screen advertised partial retry: %q", rendered)
			}
			for _, row := range rows {
				if lipgloss.Width(row) > width || (plain && strings.Contains(row, "\x1b")) {
					t.Fatalf("invalid failure layout: %q", row)
				}
			}
		}
	}
}
