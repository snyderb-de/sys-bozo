package system

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Facts struct {
	Hostname      string
	User          string
	OS            string
	Arch          string
	Shell         string
	WorkingDir    string
	GitDirtyCount int
	NixPath       string
	BrewPath      string
	HomeManager   string
	DarwinRebuild string
	BrewOutdated  int
}

func Probe() Facts {
	hostname, _ := os.Hostname()
	wd, _ := os.Getwd()

	facts := Facts{
		Hostname:   hostname,
		User:       os.Getenv("USER"),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		Shell:      os.Getenv("SHELL"),
		WorkingDir: wd,
	}

	facts.NixPath, _ = exec.LookPath("nix")
	facts.BrewPath, _ = exec.LookPath("brew")
	facts.HomeManager, _ = exec.LookPath("home-manager")
	facts.DarwinRebuild, _ = exec.LookPath("darwin-rebuild")
	facts.GitDirtyCount = gitDirtyCount(wd)
	if facts.BrewPath != "" {
		facts.BrewOutdated = brewOutdatedCount(facts.BrewPath)
	}

	return facts
}

func (f Facts) ManagerStatus() []string {
	status := []string{
		statusLine("nix", f.NixPath),
		statusLine("brew", f.BrewPath),
		statusLine("home-manager", f.HomeManager),
	}
	if f.OS == "darwin" {
		status = append(status, statusLine("nix-darwin", f.DarwinRebuild))
	}
	return status
}

func statusLine(name, path string) string {
	if path == "" {
		return name + ": missing"
	}
	return name + ": " + path
}

func gitDirtyCount(dir string) int {
	if dir == "" {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "status", "--short")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return 0
	}
	return len(strings.Split(text, "\n"))
}

func brewOutdatedCount(brew string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, brew, "outdated", "--quiet")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return 0
	}
	return len(strings.Split(text, "\n"))
}
