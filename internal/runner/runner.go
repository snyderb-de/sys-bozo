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
	NixStoreBin    string
	NixSystem      string
	BrewBin        string
	HomeManager    string
	DarwinRebuild  string
	Topgrade       string
	SopsAgeKeyFile string
}

type ExecutionMode uint8

const (
	// ExecutionStreamed is the zero value: output is captured and streamed
	// into the TUI without terminal ownership or interactive input.
	ExecutionStreamed ExecutionMode = iota
	ExecutionInteractive
)

// Step is one command within a Task.
type Step struct {
	Title       string
	Description string
	Targets     []string
	Mode        ExecutionMode
	Retryable   bool
	Cmd         func(ctx Context) (name string, args []string)
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

// WorkItem is a flattened (task, step) pair ready for execution.
type WorkItem struct {
	TaskLabel string
	TaskFirst bool // first step of a new task — triggers header line in log
	Name      string
	Args      []string
	Dir       string
	EnvExtra  []string
	Mode      ExecutionMode
	Retryable bool
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
		NixStoreBin:    findExe("nix-store", "/nix/var/nix/profiles/default/bin/nix-store"),
		NixSystem:      runtime.GOARCH + "-" + runtime.GOOS,
		BrewBin:        findExe("brew", "/opt/homebrew/bin/brew", "/usr/local/bin/brew"),
		HomeManager:    findExe("home-manager", hmFallbacks...),
		DarwinRebuild:  findExe("darwin-rebuild", "/run/current-system/sw/bin/darwin-rebuild"),
		Topgrade:       findExe("topgrade"),
		SopsAgeKeyFile: sopsKey,
	}
}

func flakeSwitch(ctx Context) []string {
	return []string{"switch", "--flake", ".#" + HMConfigKey(ctx.User, ctx.Hostname)}
}
func flakeUpdate() []string { return []string{"flake", "update"} }

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
				{Mode: ExecutionInteractive, Retryable: true, Cmd: func(c Context) (string, []string) {
					return "sudo", []string{"-H", c.DarwinRebuild, "switch", "--flake", ".#" + c.Hostname, "--impure"}
				}},
			},
			Dir: repo,
		},
		{
			ID:    "ndu",
			Group: "nix-darwin",
			Label: "ndu",
			Desc:  "update inputs + apply system",
			Hint:  "weekly pull or before flake changes",
			Available: func(c Context) bool {
				return c.NixBin != "" && c.DarwinRebuild != "" && c.OS == "darwin"
			},
			Steps: []Step{
				{Retryable: true, Cmd: func(c Context) (string, []string) { return c.NixBin, flakeUpdate() }},
				{Mode: ExecutionInteractive, Retryable: true, Cmd: func(c Context) (string, []string) {
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
				{Retryable: true, Cmd: func(c Context) (string, []string) {
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
				{Mode: ExecutionInteractive, Cmd: func(c Context) (string, []string) {
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
				{Retryable: true, Cmd: func(c Context) (string, []string) { return c.HomeManager, flakeSwitch(c) }},
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
				{Retryable: true, Cmd: func(c Context) (string, []string) { return c.NixBin, flakeUpdate() }},
				{Retryable: true, Cmd: func(c Context) (string, []string) { return c.HomeManager, flakeSwitch(c) }},
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

		// ── fedora (system level on Linux) ────────────────────────────────
		{
			ID:    "fedora-upgrade",
			Group: "fedora",
			Label: "fedora-upgrade",
			Desc:  "upgrade RPM packages",
			Hint:  "system-level update on Fedora Linux",
			Available: func(c Context) bool {
				return c.OS == "linux" && c.OSID == "fedora" && c.SudoBin != "" && c.DnfBin != ""
			},
			Steps: []Step{
				{Mode: ExecutionInteractive, Retryable: true, Cmd: func(c Context) (string, []string) {
					return c.SudoBin, []string{c.DnfBin, "upgrade", "--refresh", "-y"}
				}},
			},
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
				{Title: "Refresh Homebrew metadata", Retryable: true, Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"update"} }},
				{Title: "Upgrade Homebrew packages", Mode: ExecutionInteractive, Retryable: true, Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"upgrade"} }},
				{Title: "Remove unused Homebrew dependencies", Retryable: true, Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"autoremove"} }},
			},
		},

		// ── misc ─────────────────────────────────────────────────────────
		{
			ID:        "topgrade",
			Group:     "misc",
			Label:     "topgrade",
			Desc:      "ecosystem sweep",
			Hint:      "update tool ecosystems not owned by other tasks",
			Available: func(c Context) bool { return c.Topgrade != "" },
			Steps: []Step{
				{Retryable: true, Cmd: func(c Context) (string, []string) {
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

		// ── combined ──────────────────────────────────────────────────────
		buildAllTask(ctx),
	}
}

// buildAllTask constructs the "all" task with steps for every available manager.
// This runs at DefaultTasks time so the step list matches the current host.
func buildAllTask(ctx Context) Task {
	var steps []Step
	desc := "update inputs"

	if ctx.NixBin != "" {
		steps = append(steps, Step{Retryable: true, Cmd: func(c Context) (string, []string) { return c.NixBin, flakeUpdate() }})
	}
	if ctx.HomeManager != "" {
		steps = append(steps, Step{Retryable: true, Cmd: func(c Context) (string, []string) { return c.HomeManager, flakeSwitch(c) }})
		desc += " → hms"
	}
	if ctx.DarwinRebuild != "" && ctx.OS == "darwin" {
		steps = append(steps, Step{Mode: ExecutionInteractive, Retryable: true, Cmd: func(c Context) (string, []string) {
			return "sudo", []string{"-H", c.DarwinRebuild, "switch", "--flake", ".#" + c.Hostname, "--impure"}
		}})
		desc += " → nds"
	}
	if ctx.BrewBin != "" {
		steps = append(steps,
			Step{Retryable: true, Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"update"} }},
			Step{Mode: ExecutionInteractive, Retryable: true, Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"upgrade"} }},
			Step{Retryable: true, Cmd: func(c Context) (string, []string) { return c.BrewBin, []string{"autoremove"} }},
		)
		desc += " → brew"
	}

	return Task{
		ID:        "all",
		Group:     "combined",
		Label:     "all",
		Desc:      desc,
		Hint:      "full weekly maintenance run",
		Available: func(c Context) bool { return c.NixBin != "" },
		Steps:     steps,
		Dir:       func(c Context) string { return c.Repo },
		Env: func(c Context) []string {
			if ctx.SopsAgeKeyFile != "" {
				return []string{"SOPS_AGE_KEY_FILE=" + ctx.SopsAgeKeyFile}
			}
			return nil
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
			Mode:      step.Mode,
			Retryable: step.Retryable,
		}
	}
	return items
}

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

// StartWork launches one WorkItem and returns a scanner over its combined output,
// a wait func (blocks until process exits, returns exit error), and any start error.
func StartWork(w WorkItem) (*bufio.Scanner, func() error, error) {
	cmd := Command(w)

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
