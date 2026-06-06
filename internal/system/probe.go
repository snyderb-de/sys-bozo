package system

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
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

	// Dotfiles repo
	DotfilesRepo   string
	DotfilesBranch string
	DotfilesDirty  int

	// Health
	HMGeneration    string
	AgeKeyExists    bool
	GitHubKeyExists bool
	TailscaleIP     string
}

func Probe() Facts {
	hostname, _ := os.Hostname()
	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	user := os.Getenv("USER")

	repo := os.Getenv("DOTFILES_REPO")
	if repo == "" {
		repo = filepath.Join(home, "code", "dotfiles")
	}

	facts := Facts{
		Hostname:    hostname,
		User:        user,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Shell:       os.Getenv("SHELL"),
		WorkingDir:  wd,
		DotfilesRepo: repo,
	}

	facts.NixPath, _ = exec.LookPath("nix")
	facts.BrewPath, _ = exec.LookPath("brew")
	facts.HomeManager, _ = exec.LookPath("home-manager")
	facts.DarwinRebuild, _ = exec.LookPath("darwin-rebuild")

	facts.GitDirtyCount = gitDirtyCount(wd)
	facts.DotfilesDirty = gitDirtyCount(repo)
	facts.DotfilesBranch = gitBranch(repo)

	if facts.BrewPath != "" {
		facts.BrewOutdated = brewOutdatedCount(facts.BrewPath)
	}

	facts.HMGeneration = hmGeneration(facts.HomeManager)

	ageKey := os.Getenv("SOPS_AGE_KEY_FILE")
	if ageKey == "" {
		ageKey = filepath.Join(home, ".config", "sops", "age", "keys.txt")
	}
	if _, err := os.Stat(ageKey); err == nil {
		facts.AgeKeyExists = true
	}

	for _, name := range []string{"id_ed25519_github", "github_snyderbde"} {
		if _, err := os.Stat(filepath.Join(home, ".ssh", name)); err == nil {
			facts.GitHubKeyExists = true
			break
		}
	}

	facts.TailscaleIP = tailscaleIP()

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

// AuditItem is one local file/tool check result.
type AuditItem struct {
	Name   string
	OK     bool
	Detail string
}

// LocalAudit checks key HM-managed files and tools are properly linked.
func LocalAudit() []AuditItem {
	home, _ := os.UserHomeDir()
	var items []AuditItem

	// Key config symlinks
	configs := []struct{ name, path string }{
		{"ghostty config", filepath.Join(home, ".config", "ghostty", "config")},
		{"kitty config", filepath.Join(home, ".config", "kitty", "kitty.conf")},
		{"ssh config", filepath.Join(home, ".ssh", "config")},
		{"starship.toml", filepath.Join(home, ".config", "starship.toml")},
		{"nvim config", filepath.Join(home, ".config", "nvim")},
		{"atuin config", filepath.Join(home, ".config", "atuin", "config.toml")},
	}
	for _, c := range configs {
		item := AuditItem{Name: c.name}
		info, err := os.Lstat(c.path)
		if err != nil {
			item.Detail = "missing"
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(c.path)
			if strings.Contains(target, "/nix/store/") {
				item.OK = true
				item.Detail = "→ nix store"
			} else {
				item.Detail = "→ " + target + " (not nix)"
			}
		} else {
			item.Detail = "unmanaged file"
		}
		items = append(items, item)
	}

	// Key tools resolve to nix-profile
	tools := []string{"bat", "jq", "gh", "lazygit", "starship", "atuin"}
	for _, t := range tools {
		item := AuditItem{Name: t}
		p, err := exec.LookPath(t)
		if err != nil {
			item.Detail = "not in PATH"
		} else if strings.Contains(p, ".nix-profile") || strings.Contains(p, "/nix/store/") {
			item.OK = true
			item.Detail = "nix"
		} else {
			item.Detail = p + " (not nix)"
		}
		items = append(items, item)
	}

	return items
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

func gitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func hmGeneration(hmBin string) string {
	if hmBin == "" {
		return "none"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, hmBin, "generations")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	if scanner.Scan() {
		line := scanner.Text()
		// format: "2026-06-06 12:41 : id 3 -> ..."
		// extract the id number
		parts := strings.Fields(line)
		for i, p := range parts {
			if p == "id" && i+1 < len(parts) {
				return "gen " + parts[i+1] + " · " + parts[0]
			}
		}
		return strings.TrimSpace(line)
	}
	return "none"
}

func tailscaleIP() string {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tailscale", "ip", "-4")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
