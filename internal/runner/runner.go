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
	User           string
	Hostname       string
	OS             string
	NixBin         string
	BrewBin        string
	HomeManager    string
	DarwinRebuild  string
	SopsAgeKeyFile string
}

// Step is one command within a Task.
type Step struct {
	Cmd func(ctx Context) (name string, args []string)
}

// Task is a runnable action shown in the Actions tab.
type Task struct {
	ID        string
	Group     string
	Label     string
	Desc      string
	Hint      string // when to run, shown faint alongside Desc
	Available func(ctx Context) bool
	Steps     []Step
	Dir       func(ctx Context) string
	Env       func(ctx Context) []string
}

// PrimeCredentials caches sudo credentials before entering the TUI so the
// password prompt appears on a clean terminal, not mid-render.
func PrimeCredentials(ctx Context) {
	if ctx.DarwinRebuild == "" || ctx.OS != "darwin" {
		return
	}
	_ = exec.Command("sudo", "-v").Run()
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

	repo := os.Getenv("DOTFILES_REPO")
	if repo == "" {
		repo = filepath.Join(home, "code", "dotfiles")
	}

	sopsKey := os.Getenv("SOPS_AGE_KEY_FILE")
	if sopsKey == "" {
		sopsKey = filepath.Join(home, ".config", "sops", "age", "keys.txt")
	}

	hmFallbacks := []string{
		filepath.Join(home, ".nix-profile", "bin", "home-manager"),
		filepath.Join("/nix/var/nix/profiles/per-user", user, "profile/bin/home-manager"),
	}

	return Context{
		Repo:           repo,
		User:           user,
		Hostname:       hostname,
		OS:             runtime.GOOS,
		NixBin:         findExe("nix", "/nix/var/nix/profiles/default/bin/nix"),
		BrewBin:        findExe("brew", "/opt/homebrew/bin/brew", "/usr/local/bin/brew"),
		HomeManager:    findExe("home-manager", hmFallbacks...),
		DarwinRebuild:  findExe("darwin-rebuild", "/run/current-system/sw/bin/darwin-rebuild"),
		SopsAgeKeyFile: sopsKey,
	}
}

func flakeSwitch(ctx Context) []string { return []string{"switch", "--flake", ".#" + HMConfigKey(ctx.User, ctx.Hostname)} }
func flakeUpdate() []string            { return []string{"flake", "update"} }

// DefaultTasks returns all known actions in display order.
func DefaultTasks(ctx Context) []Task {
	hmEnv := func(c Context) []string {
		if c.SopsAgeKeyFile != "" {
			return []string{"SOPS_AGE_KEY_FILE=" + c.SopsAgeKeyFile}
		}
		return nil
	}
	hmAvail := func(c Context) bool { return c.HomeManager != "" }
	darwinAvail := func(c Context) bool { return c.DarwinRebuild != "" && c.OS == "darwin" }
	repo := func(c Context) string { return c.Repo }

	return []Task{
		// ── nix-darwin (system level — touches packages, services, defaults) ─
		{
			ID:        "nds",
			Group:     "nix-darwin",
			Label:     "nds",
			Desc:      "apply system config",
			Hint:      "after editing flake.nix",
			Available: darwinAvail,
			Steps: []Step{
				{Cmd: func(c Context) (string, []string) {
					return "sudo", []string{"-H", c.DarwinRebuild, "switch", "--flake", ".#" + c.Hostname, "--impure"}
				}},
			},
			Dir: repo,
		},
		{
			ID:        "ndu",
			Group:     "nix-darwin",
			Label:     "ndu",
			Desc:      "update inputs + apply system",
			Hint:      "weekly pull or before flake changes",
			Available: func(c Context) bool { return c.NixBin != "" && c.DarwinRebuild != "" && c.OS == "darwin" },
			Steps: []Step{
				{Cmd: func(c Context) (string, []string) { return c.NixBin, flakeUpdate() }},
				{Cmd: func(c Context) (string, []string) {
					return "sudo", []string{"-H", c.DarwinRebuild, "switch", "--flake", ".#" + c.Hostname, "--impure"}
				}},
			},
			Dir: repo,
		},
		{
			ID:        "ndsd",
			Group:     "nix-darwin",
			Label:     "ndsd",
			Desc:      "dry-run brew pre-flight",
			Hint:      "preview what nds would change in Homebrew",
			Available: func(c Context) bool { return c.OS == "darwin" && c.NixBin != "" },
			Steps: []Step{
				{Cmd: func(c Context) (string, []string) {
					return "bash", []string{filepath.Join(c.Repo, "scripts", "nds-dryrun"), c.Hostname}
				}},
			},
			Dir: repo,
		},
		{
			ID:        "ndR",
			Group:     "nix-darwin",
			Label:     "ndR",
			Desc:      "rollback system",
			Hint:      "undo last nds if it broke something",
			Available: darwinAvail,
			Steps: []Step{
				{Cmd: func(c Context) (string, []string) {
					return "sudo", []string{"-H", c.DarwinRebuild, "--rollback"}
				}},
			},
		},

		// ── home-manager (user level — dotfiles, user packages, shell) ────
		{
			ID:        "hms",
			Group:     "home-manager",
			Label:     "hms",
			Desc:      "apply user profile",
			Hint:      "after editing home.nix",
			Available: hmAvail,
			Steps: []Step{
				{Cmd: func(c Context) (string, []string) { return c.HomeManager, flakeSwitch(c) }},
			},
			Dir: repo,
			Env: hmEnv,
		},
		{
			ID:        "hmu",
			Group:     "home-manager",
			Label:     "hmu",
			Desc:      "update inputs + apply user profile",
			Hint:      "weekly pull or before home.nix changes",
			Available: func(c Context) bool { return c.NixBin != "" && c.HomeManager != "" },
			Steps: []Step{
				{Cmd: func(c Context) (string, []string) { return c.NixBin, flakeUpdate() }},
				{Cmd: func(c Context) (string, []string) { return c.HomeManager, flakeSwitch(c) }},
			},
			Dir: repo,
			Env: hmEnv,
		},
		{
			ID:        "hmr",
			Group:     "home-manager",
			Label:     "hmr",
			Desc:      "rollback user profile",
			Hint:      "undo last hms if shell/tools broke",
			Available: hmAvail,
			Steps: []Step{
				{Cmd: func(c Context) (string, []string) { return c.HomeManager, []string{"switch", "--rollback"} }},
			},
			Dir: repo,
			Env: hmEnv,
		},

		// ── brew (GUI apps and packages outside nixpkgs) ─────────────────
		{
			ID:        "brew",
			Group:     "brew",
			Label:     "brew",
			Desc:      "update + upgrade + autoremove",
			Hint:      "sync GUI apps and brew-only packages",
			Available: func(c Context) bool { return c.BrewBin != "" },
			Steps: []Step{
				{Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"update"} }},
				{Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"upgrade"} }},
				{Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"autoremove"} }},
			},
		},

		// ── combined ──────────────────────────────────────────────────────
		{
			ID:    "all",
			Group: "combined",
			Label: "all",
			Desc:  "update inputs → system → profile → brew",
			Hint:  "full weekly maintenance run",
			Available: func(c Context) bool {
				return c.NixBin != "" && c.HomeManager != "" && c.DarwinRebuild != "" && c.OS == "darwin" && c.BrewBin != ""
			},
			Steps: []Step{
				{Cmd: func(c Context) (string, []string) { return c.NixBin, flakeUpdate() }},
				{Cmd: func(c Context) (string, []string) { return c.HomeManager, flakeSwitch(c) }},
				{Cmd: func(c Context) (string, []string) {
					return "sudo", []string{"-H", c.DarwinRebuild, "switch", "--flake", ".#" + c.Hostname, "--impure"}
				}},
				{Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"update"} }},
				{Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"upgrade"} }},
				{Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"autoremove"} }},
			},
			Dir: repo,
			Env: func(c Context) []string {
				if c.SopsAgeKeyFile != "" {
					return []string{"SOPS_AGE_KEY_FILE=" + c.SopsAgeKeyFile}
				}
				return nil
			},
		},
	}
}

// BuildQueue expands a single task into a flat WorkItem slice.
func BuildQueue(task Task, ctx Context) []WorkItem {
	if !task.Available(ctx) {
		return nil
	}
	var dir string
	if task.Dir != nil {
		dir = task.Dir(ctx)
	}
	var env []string
	if task.Env != nil {
		env = task.Env(ctx)
	}
	items := make([]WorkItem, len(task.Steps))
	for j, step := range task.Steps {
		name, args := step.Cmd(ctx)
		items[j] = WorkItem{
			TaskLabel: task.Label,
			TaskFirst: j == 0,
			Name:      name,
			Args:      args,
			Dir:       dir,
			EnvExtra:  env,
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
