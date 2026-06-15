package runner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Context holds resolved binary paths and host identity for task execution.
type Context struct {
	Repo           string
	SysBozoRepo    string
	SysBozoBin     string
	User           string
	Hostname       string
	OS             string
	OSID           string
	GitBin         string
	GoBin          string
	SudoBin        string
	DnfBin         string
	NixBin         string
	BrewBin        string
	HomeManager    string
	DarwinRebuild  string
	Topgrade       string
	SopsAgeKeyFile string
}

// Step is one command within a Task.
type Step struct {
	Title       string
	Description string
	Targets     []string
	Cmd         func(ctx Context) (name string, args []string)
}

// Task is a selectable unit of work shown in the Updates tab.
type Task struct {
	ID        string
	Label     string
	Desc      string
	Available func(ctx Context) bool
	Steps     []Step
	Dir       func(ctx Context) string
	Env       func(ctx Context) []string
	Selected  bool
}

// WorkItem is a flattened (task, step) pair ready for execution.
type WorkItem struct {
	TaskLabel string
	TaskFirst bool // first step of a new task — triggers header line in log
	Name      string
	Args      []string
	Dir       string
	EnvExtra  []string
}

// HMConfigKey returns the flake homeConfigurations key for this host.
// Linux: "user@host"  Darwin: "user"
func HMConfigKey(user, hostname string) string {
	if runtime.GOOS == "linux" {
		return user + "@" + hostname
	}
	return user
}

// Build resolves all paths and identity from the environment.
func Build() Context {
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("LOGNAME")
	}
	hostname, _ := os.Hostname()
	home, _ := os.UserHomeDir()
	osID := osReleaseID()

	repo := os.Getenv("DOTFILES_REPO")
	if repo == "" {
		repo = filepath.Join(home, "code", "dotfiles")
	}
	sysBozoRepo := os.Getenv("SYS_BOZO_REPO")
	if sysBozoRepo == "" {
		sysBozoRepo = filepath.Join(home, "code", "sys-bozo")
	}
	sysBozoBin := filepath.Join(home, ".local", "bin", "sys-bozo")

	sopsKey := os.Getenv("SOPS_AGE_KEY_FILE")
	if sopsKey == "" {
		sopsKey = filepath.Join(home, ".config", "sops", "age", "keys.txt")
	}

	hmFallbacks := []string{
		filepath.Join(home, ".nix-profile", "bin", "home-manager"),
		filepath.Join("/nix/var/nix/profiles/per-user", user, "profile/bin/home-manager"),
	}

	sudoBin := findExe("sudo")

	return Context{
		Repo:           repo,
		SysBozoRepo:    sysBozoRepo,
		SysBozoBin:     sysBozoBin,
		User:           user,
		Hostname:       hostname,
		OS:             runtime.GOOS,
		OSID:           osID,
		GitBin:         findExe("git"),
		GoBin:          findExe("go"),
		SudoBin:        sudoBin,
		DnfBin:         findExe("dnf", "/usr/bin/dnf5"),
		NixBin:         findExe("nix", "/nix/var/nix/profiles/default/bin/nix"),
		BrewBin:        findExe("brew", "/opt/homebrew/bin/brew", "/usr/local/bin/brew"),
		HomeManager:    findExe("home-manager", hmFallbacks...),
		DarwinRebuild:  findExe("darwin-rebuild", "/run/current-system/sw/bin/darwin-rebuild"),
		Topgrade:       findExe("topgrade"),
		SopsAgeKeyFile: sopsKey,
	}
}

// DefaultTasks returns all known tasks, each with platform availability gating.
func DefaultTasks(ctx Context) []Task {
	return []Task{
		{
			ID:    "sys-bozo-self-update",
			Label: "sys-bozo self update",
			Desc:  "Pull sys-bozo source and rebuild ~/.local/bin/sys-bozo",
			Available: func(c Context) bool {
				if c.GitBin == "" || c.GoBin == "" || c.SysBozoRepo == "" || c.SysBozoBin == "" {
					return false
				}
				if _, err := os.Stat(filepath.Join(c.SysBozoRepo, "cmd", "sys-bozo")); err != nil {
					return false
				}
				return true
			},
			Selected: true,
			Steps: []Step{
				{Title: "Update sys-bozo source", Description: "Fast-forward the sys-bozo source repo before rebuilding the local binary.", Targets: []string{"~/code/sys-bozo"}, Cmd: func(c Context) (string, []string) {
					return c.GitBin, []string{"pull", "--ff-only"}
				}},
				{Title: "Rebuild sys-bozo", Description: "Install the freshly built binary for the next sys-bozo run.", Targets: []string{"~/.local/bin/sys-bozo"}, Cmd: func(c Context) (string, []string) {
					return c.GoBin, []string{"build", "-o", c.SysBozoBin, "./cmd/sys-bozo"}
				}},
			},
			Dir: func(c Context) string { return c.SysBozoRepo },
		},
		{
			ID:        "nix-flake-update",
			Label:     "Nix flake update",
			Desc:      "Update all flake inputs (nixpkgs, home-manager, darwin)",
			Available: func(c Context) bool { return c.NixBin != "" },
			Selected:  true,
			Steps: []Step{
				{Title: "Update Nix flake inputs", Description: "Nix usually updates by input, not individual package.", Targets: []string{"flake.lock"}, Cmd: func(c Context) (string, []string) {
					return c.NixBin, []string{"flake", "update"}
				}},
			},
			Dir: func(c Context) string { return c.Repo },
		},
		{
			ID:        "home-manager-switch",
			Label:     "Home Manager switch",
			Desc:      "Apply user profile · " + HMConfigKey(ctx.User, ctx.Hostname),
			Available: func(c Context) bool { return c.HomeManager != "" },
			Selected:  true,
			Steps: []Step{
				{Title: "Apply Home Manager profile", Cmd: func(c Context) (string, []string) {
					return c.HomeManager, []string{
						"switch", "--flake", ".#" + HMConfigKey(c.User, c.Hostname),
					}
				}},
			},
			Dir: func(c Context) string { return c.Repo },
			Env: func(c Context) []string {
				if c.SopsAgeKeyFile != "" {
					return []string{"SOPS_AGE_KEY_FILE=" + c.SopsAgeKeyFile}
				}
				return nil
			},
		},
		{
			ID:    "darwin-rebuild-switch",
			Label: "nix-darwin switch",
			Desc:  "Apply macOS host profile",
			Available: func(c Context) bool {
				return c.DarwinRebuild != "" && c.SudoBin != "" && c.OS == "darwin"
			},
			Selected: true,
			Steps: []Step{
				{Title: "Apply nix-darwin host", Cmd: func(c Context) (string, []string) {
					return c.SudoBin, []string{
						"-H", c.DarwinRebuild, "switch",
						"--flake", ".#" + c.Hostname, "--impure",
					}
				}},
			},
			Dir: func(c Context) string { return c.Repo },
		},
		{
			ID:    "fedora-system-upgrade",
			Label: "Fedora system upgrade",
			Desc:  "Upgrade RPM packages with DNF after an interactive sudo preflight",
			Available: func(c Context) bool {
				return c.OS == "linux" && c.OSID == "fedora" && c.SudoBin != "" && c.DnfBin != ""
			},
			Selected: true,
			Steps: []Step{
				{Title: "Upgrade Fedora packages", Description: "sys-bozo runs sudo -v before the queue so this command can run without prompting inside the log pane.", Cmd: func(c Context) (string, []string) {
					return c.SudoBin, []string{c.DnfBin, "upgrade", "--refresh", "-y"}
				}},
			},
		},
		{
			ID:        "brew-update",
			Label:     "Brew update + upgrade",
			Desc:      "Refresh metadata, upgrade all formulae and casks",
			Available: func(c Context) bool { return c.BrewBin != "" },
			Selected:  true,
			Steps: []Step{
				{Title: "Refresh Homebrew metadata", Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"update"} }},
				{Title: "Upgrade Homebrew packages", Description: "Upgrade installed formulae and casks.", Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"upgrade"} }},
				{Title: "Remove unused Homebrew dependencies", Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"autoremove"} }},
			},
		},
		{
			ID:        "topgrade",
			Label:     "Topgrade ecosystem sweep",
			Desc:      "Update tool ecosystems not already owned by sys-bozo tasks",
			Available: func(c Context) bool { return c.Topgrade != "" },
			Selected:  true,
			Steps: []Step{
				{Title: "Run Topgrade ecosystem sweep", Description: "Skip Nix, Home Manager, Brew, system package upgrades, and restart checks because sys-bozo owns those paths.", Cmd: func(c Context) (string, []string) {
					return c.Topgrade, []string{
						"--yes",
						"--skip-notify",
						"--no-retry",
						"--no-self-update",
						"--disable",
						"nix",
						"home_manager",
						"brew_formula",
						"brew_cask",
						"system",
						"restarts",
					}
				}},
			},
		},
	}
}

// BuildQueue expands selected, available tasks into a flat WorkItem slice.
func BuildQueue(tasks []Task, ctx Context) []WorkItem {
	var items []WorkItem
	for _, t := range tasks {
		if !t.Selected || !t.Available(ctx) {
			continue
		}
		var dir string
		if t.Dir != nil {
			dir = t.Dir(ctx)
		}
		var env []string
		if t.Env != nil {
			env = t.Env(ctx)
		}
		for j, step := range t.Steps {
			name, args := step.Cmd(ctx)
			items = append(items, WorkItem{
				TaskLabel: t.Label,
				TaskFirst: j == 0,
				Name:      name,
				Args:      args,
				Dir:       dir,
				EnvExtra:  env,
			})
		}
	}
	return items
}

// StartWork launches one WorkItem and returns a scanner over its combined output,
// a wait func (blocks until process exits, returns exit error), and any start error.
func StartWork(w WorkItem) (*bufio.Scanner, func() error, error) {
	cmd := exec.Command(w.Name, w.Args...)
	if w.Dir != "" {
		cmd.Dir = w.Dir
	}
	cmd.Env = os.Environ()
	if len(w.EnvExtra) > 0 {
		cmd.Env = append(cmd.Env, w.EnvExtra...)
	}

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return nil, nil, fmt.Errorf("%s: %w", w.Name, err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
		pw.Close()
	}()

	return bufio.NewScanner(pr), func() error { return <-done }, nil
}

// CmdLabel returns a display string for a WorkItem's command.
func CmdLabel(w WorkItem) string {
	if len(w.Args) == 0 {
		return w.Name
	}
	return w.Name + " " + strings.Join(w.Args, " ")
}

func findExe(name string, fallbacks ...string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
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
