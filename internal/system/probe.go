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
	OSID          string
	Arch          string
	Shell         string
	WorkingDir    string
	GitDirtyCount int
	SudoPath      string
	DnfPath       string
	NixPath       string
	BrewPath      string
	HomeManager   string
	DarwinRebuild string
	Topgrade      string
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
		Hostname:     hostname,
		User:         user,
		OS:           runtime.GOOS,
		OSID:         osReleaseID(),
		Arch:         runtime.GOARCH,
		Shell:        os.Getenv("SHELL"),
		WorkingDir:   wd,
		DotfilesRepo: repo,
	}

	facts.SudoPath, _ = exec.LookPath("sudo")
	facts.DnfPath, _ = exec.LookPath("dnf")
	if facts.DnfPath == "" {
		facts.DnfPath, _ = exec.LookPath("dnf5")
	}
	facts.NixPath, _ = exec.LookPath("nix")
	facts.BrewPath, _ = exec.LookPath("brew")
	facts.HomeManager, _ = exec.LookPath("home-manager")
	facts.DarwinRebuild, _ = exec.LookPath("darwin-rebuild")
	facts.Topgrade, _ = exec.LookPath("topgrade")

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
		statusLine("home-manager", f.HomeManager),
		statusLine("topgrade", f.Topgrade),
	}
	if f.OS == "linux" && f.OSID == "fedora" {
		status = append(status, statusLine("dnf", f.DnfPath))
		status = append(status, statusLine("sudo", f.SudoPath))
	}
	if f.OS == "darwin" {
		status = append(status, statusLine("brew", f.BrewPath))
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

func osReleaseID() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key != "ID" {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}

// AuditItem is one local file/tool check result.
type AuditItem struct {
	Name        string
	OK          bool
	Detail      string
	Description string
	Fix         string
}

// LocalAudit checks key HM-managed files and tools are properly linked.
func LocalAudit() []AuditItem {
	home, _ := os.UserHomeDir()
	var items []AuditItem

	// Key config symlinks
	configs := []struct {
		name        string
		path        string
		description string
		fix         string
	}{
		{
			name:        "ghostty config",
			path:        filepath.Join(home, ".config", "ghostty", "config"),
			description: "Terminal defaults should come from dotfiles so every host gets the same Ghostty behavior.",
			fix:         "Move local edits into configs/ghostty/config, then run home-manager switch.",
		},
		{
			name:        "kitty config",
			path:        filepath.Join(home, ".config", "kitty", "kitty.conf"),
			description: "Terminal defaults should come from dotfiles so every host gets the same Kitty behavior.",
			fix:         "Move local edits into configs/kitty/kitty.conf, then run home-manager switch.",
		},
		{
			name:        "ssh config",
			path:        filepath.Join(home, ".ssh", "config"),
			description: "SSH aliases and encrypted host includes should be managed by dotfiles/SOPS, not hand-edited per host.",
			fix:         "Compare with configs/ssh/config, move host entries into secrets/ssh-config.yaml or configs/ssh/config, then run home-manager switch.",
		},
		{
			name:        "starship.toml",
			path:        filepath.Join(home, ".config", "starship.toml"),
			description: "Prompt styling should come from dotfiles so shell behavior is consistent across machines.",
			fix:         "Move local edits into configs/starship/starship.toml, then run home-manager switch.",
		},
		{
			name:        "nvim config",
			path:        filepath.Join(home, ".config", "nvim"),
			description: "Editor config should be linked from dotfiles so plugin and keymap drift is visible in git.",
			fix:         "Move local edits into configs/nvim, remove the unmanaged path, then run home-manager switch.",
		},
		{
			name:        "atuin config",
			path:        filepath.Join(home, ".config", "atuin", "config.toml"),
			description: "Shell history sync settings should be explicitly managed instead of silently changed by the app.",
			fix:         "Move local edits into home/common/home.nix, then run home-manager switch.",
		},
	}
	for _, c := range configs {
		item := AuditItem{Name: c.name, Description: c.description, Fix: c.fix}
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
		} else if c.name == "ssh config" {
			item.OK, item.Detail = sshConfigCopyStatus(home, c.path, info)
		} else {
			item.Detail = "unmanaged file"
		}
		items = append(items, item)
	}

	// Key tools resolve to a nix-managed location. On nix-darwin, HM
	// user packages land in `/etc/profiles/per-user/<user>/bin/` and
	// system packages in `/run/current-system/sw/bin/`. On Linux HM
	// standalone, tools live in `~/.nix-profile/bin/`. We accept any of
	// these prefixes, and we also follow the symlink to the /nix/store
	// for cases where LookPath returns the linked path directly.
	tools := []string{"bat", "jq", "gh", "lazygit", "starship", "atuin"}
	for _, t := range tools {
		item := AuditItem{
			Name:        t,
			Description: "Command should resolve through the Nix/Home Manager profile so package ownership is reproducible.",
			Fix:         "Add or keep the package in home/common/home.nix, run home-manager switch, then remove any earlier PATH entry shadowing it.",
		}
		p, err := exec.LookPath(t)
		if err != nil {
			item.Detail = "not in PATH"
		} else if isNixManagedPath(p) {
			item.OK = true
			item.Detail = "nix"
		} else {
			item.Detail = p + " (not nix)"
		}
		items = append(items, item)
	}

	return items
}

func sshConfigCopyStatus(home, path string, info os.FileInfo) (bool, string) {
	if info.Mode().Perm()&0o077 != 0 {
		return false, "regular file but permissions are too open"
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return false, "regular file but unreadable"
	}

	source := filepath.Join(dotfilesRepo(home), "configs", "ssh", "config")
	if expected, err := os.ReadFile(source); err == nil {
		if string(content) == string(expected) {
			return true, "activation-managed copy"
		}
		return false, "regular file differs from dotfiles source"
	}

	text := string(content)
	if strings.Contains(text, "Include ~/.config/ssh/config.d/private.conf") {
		return true, "regular file with expected private include"
	}
	return false, "regular file missing expected private include"
}

func dotfilesRepo(home string) string {
	if repo := os.Getenv("DOTFILES_REPO"); repo != "" {
		return repo
	}
	return filepath.Join(home, "code", "dotfiles")
}

// isNixManagedPath returns true when p resolves to anything Nix owns. Covers:
//
//   - /nix/store/...                          — direct store path
//   - ~/.nix-profile/...                      — Linux HM standalone user profile
//   - /etc/profiles/per-user/<user>/bin/...   — nix-darwin HM user profile
//   - /run/current-system/sw/bin/...          — nix-darwin system profile
//
// We also dereference one level of symlink so that, e.g., a /etc/profiles
// entry pointing into the store still counts even on hosts where we only
// match the store prefix.
func isNixManagedPath(p string) bool {
	if p == "" {
		return false
	}
	prefixes := []string{
		"/nix/store/",
		"/etc/profiles/per-user/",
		"/run/current-system/sw/",
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		prefixes = append(prefixes, filepath.Join(home, ".nix-profile")+"/")
	}
	for _, pfx := range prefixes {
		if strings.HasPrefix(p, pfx) {
			return true
		}
	}
	// Resolve a single symlink hop and re-test.
	if resolved, err := os.Readlink(p); err == nil && resolved != p {
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(p), resolved)
		}
		for _, pfx := range prefixes {
			if strings.HasPrefix(resolved, pfx) {
				return true
			}
		}
	}
	return false
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
