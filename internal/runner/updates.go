package runner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/snyderb-de/sys-bozo/internal/repostate"
)

const MacMiniHost = "bags-Mac-mini"

// This first update workflow describes the inspected Mini dotfiles setup.
// Other machines keep their existing actions until their drift is reviewed.
func IsMacMini(c Context) bool {
	host, _, _ := strings.Cut(c.Hostname, ".")
	return c.OS == "darwin" && strings.EqualFold(host, MacMiniHost)
}

type UpdateOption struct {
	ID, Label, Description, Detail   string
	Recommended, Recovery, Available bool
}

func MiniUpdateOptions(c Context) []UpdateOption {
	if !IsMacMini(c) {
		return nil
	}
	darwin := c.NixBin != "" && c.DarwinRebuild != "" && c.SudoBin != "" && c.NixStoreBin != ""
	return []UpdateOption{
		{"nix-update", "Update Nix version pins", "Refresh flake.lock; installed packages stay unchanged until Apply.", "Recommended first. Changes the dotfiles lock file, not your live profile.", true, false, c.NixBin != ""},
		{"nds", "Apply Mac mini configuration", "Build and activate system + user configuration; includes Homebrew review.", "Password and app prompts use the terminal. Declined upgrades stay declined; removals require separate prompts.", true, false, darwin},
		{"topgrade", "Run Topgrade", "Update other tools using your Topgrade configuration.", "Bozo also skips Nix, Home Manager, Brew, system updates, restart checks and remote hosts. Prompts use the terminal.", true, false, c.Topgrade != ""},
		{"brew", "Upgrade Homebrew only", "Upgrade formulae and reviewed casks without a workstation rebuild.", "Alternative to Apply. DisplayLink is excluded; no dependency cleanup is included.", false, false, c.BrewBin != ""},
		{"displaylink", "Upgrade DisplayLink", "Upgrade the display driver explicitly; a restart may be needed.", "The workstation rebuild can also offer this upgrade in its native Homebrew prompts.", false, false, c.BrewBin != "" && displayLinkPending(c)},
		{"brew-cleanup", "Remove unused dependencies", "Run Homebrew autoremove after update checks succeed.", "Optional cleanup. Does not remove Nix generations or purge app data.", false, false, c.BrewBin != ""},
		{"ndsd", "Preview Homebrew changes", "Build the Mini configuration and preview its Homebrew activation.", "Creates Nix build/cache output but does not activate the system or install apps.", false, false, c.NixBin != "" && c.BrewBin != ""},
		{"ndR", "Roll back workstation configuration", "Activate the previous system generation, including its user profile.", "Recovery only. Does not revert flake.lock or guarantee rollback of Homebrew apps.", false, true, darwin},
	}
}

func RecommendedMiniUpdates(c Context) []string {
	var ids []string
	for _, option := range MiniUpdateOptions(c) {
		if option.Recommended && option.Available {
			ids = append(ids, option.ID)
		}
	}
	return ids
}

type UpdatePlan struct {
	Items []WorkItem
	Notes []string
}

// BuildMiniUpdates expands aliases into one ordered set of operations. A system
// activation owns its Homebrew pass, so a second brew pass must not reverse skips.
func BuildMiniUpdates(c Context, ids []string) (UpdatePlan, error) {
	if !IsMacMini(c) {
		return UpdatePlan{}, fmt.Errorf("recommended updates are currently scoped to %s", MacMiniHost)
	}
	selected := map[string]bool{}
	for _, id := range ids {
		switch id {
		case "all", "recommended":
			for _, recommended := range RecommendedMiniUpdates(c) {
				selected[recommended] = true
			}
		case "ndu", "hmu":
			selected["nix-update"], selected["nds"] = true, true
		case "hms":
			selected["nds"] = true
		default:
			selected[id] = true
		}
	}
	options := map[string]UpdateOption{}
	for _, option := range MiniUpdateOptions(c) {
		options[option.ID] = option
	}
	for id := range selected {
		option, exists := options[id]
		if !exists {
			return UpdatePlan{}, fmt.Errorf("unknown Mini update option %q", id)
		}
		if !option.Available {
			return UpdatePlan{}, fmt.Errorf("%s is unavailable on this host", option.Label)
		}
	}
	if len(selected) == 0 {
		return UpdatePlan{}, fmt.Errorf("select at least one update option")
	}
	if selected["ndR"] && len(selected) > 1 {
		return UpdatePlan{}, fmt.Errorf("run recovery separately from updates")
	}
	p := UpdatePlan{}
	add := func(title, description, name string, args []string, interactive, readOnly, retryable bool, dir string) {
		mode := ExecutionStreamed
		if interactive {
			mode = ExecutionInteractive
		}
		p.Items = append(p.Items, WorkItem{Title: title, Description: description, ReadOnly: readOnly, TaskLabel: title, TaskFirst: true, Name: name, Args: append([]string(nil), args...), Dir: dir, Mode: mode, Retryable: retryable})
	}
	if selected["ndR"] {
		add(options["ndR"].Label, options["ndR"].Detail, c.SudoBin, []string{"-H", c.DarwinRebuild, "switch", "--rollback"}, true, false, false, c.Repo)
		return p, nil
	}
	if selected["nix-update"] {
		add(options["nix-update"].Label, options["nix-update"].Description, c.NixBin, flakeUpdate(), false, false, true, c.Repo)
	}
	if c.BrewBin != "" && (selected["nds"] || selected["brew"] || selected["displaylink"]) {
		add("Refresh Homebrew metadata", "Refresh available versions before the Homebrew review or selected upgrades.", c.BrewBin, []string{"update"}, false, false, true, "")
	}
	if selected["ndsd"] {
		add(options["ndsd"].Label, options["ndsd"].Detail, "bash", []string{filepath.Join(c.Repo, "scripts", "nds-dryrun"), MacMiniHost}, true, false, true, c.Repo)
	}
	if selected["nds"] {
		add(options["nds"].Label, options["nds"].Description+" "+options["nds"].Detail, c.SudoBin, []string{"-H", c.DarwinRebuild, "switch", "--flake", ".#" + MacMiniHost, "--impure"}, true, false, true, c.Repo)
		if c.SopsAgeKeyFile != "" {
			p.Items[len(p.Items)-1].EnvExtra = []string{"SOPS_AGE_KEY_FILE=" + c.SopsAgeKeyFile}
		}
		p.Notes = append(p.Notes, "Apply already includes Home Manager and interactive Homebrew review. A second Homebrew upgrade is omitted.")
	}
	if selected["brew"] && !selected["nds"] {
		for _, step := range brewMaintenanceSteps(c) {
			name, args := step.Cmd(c)
			if args[0] != "upgrade" {
				continue
			}
			add(step.Title, options["brew"].Detail, name, args, true, false, true, "")
		}
	}
	if selected["displaylink"] {
		add(options["displaylink"].Label, options["displaylink"].Description, c.BrewBin, []string{"upgrade", "--cask", "displaylink"}, true, false, true, "")
	}
	if selected["topgrade"] {
		add(options["topgrade"].Label, options["topgrade"].Description+" "+options["topgrade"].Detail, c.Topgrade, []string{"--skip-notify", "--no-retry", "--no-self-update", "--disable", "nix", "home_manager", "brew_formula", "brew_cask", "system", "restarts", "remotes"}, true, false, true, "")
	}
	if selected["nds"] {
		add("Check system generation", "Query the selected Nix system profile; this does not test every application.", c.NixStoreBin, []string{"--query", "--deriver", "/nix/var/nix/profiles/system"}, false, true, true, "")
	}
	if c.BrewBin != "" && (selected["nds"] || selected["brew"] || selected["displaylink"] || selected["brew-cleanup"]) {
		add("Check Homebrew dependencies", "Report missing formula dependencies. Skipped app upgrades can still remain.", c.BrewBin, []string{"missing"}, false, true, true, "")
	}
	if selected["brew-cleanup"] {
		add(options["brew-cleanup"].Label, options["brew-cleanup"].Description, c.BrewBin, []string{"autoremove"}, false, false, true, "")
	}
	return p, nil
}

type UpdateReadiness struct {
	Dirty      int
	StatusHash [32]byte
}

// CheckUpdateReadiness is read-only and bounded; it never invokes an updater.
func CheckUpdateReadiness(c Context, items []WorkItem) (UpdateReadiness, error) {
	if !IsMacMini(c) {
		return UpdateReadiness{}, fmt.Errorf("this update workflow is only for %s", MacMiniHost)
	}
	if c.Repo == "" {
		return UpdateReadiness{}, fmt.Errorf("dotfiles repository is not configured")
	}
	info, err := os.Stat(filepath.Join(c.Repo, "flake.nix"))
	if err != nil || !info.Mode().IsRegular() {
		return UpdateReadiness{}, fmt.Errorf("dotfiles flake.nix is missing or unreadable")
	}
	for _, item := range items {
		if _, err := exec.LookPath(item.Name); err != nil {
			return UpdateReadiness{}, fmt.Errorf("required command is unavailable: %s", filepath.Base(item.Name))
		}
		if item.Name == c.SudoBin && c.DarwinRebuild != "" {
			if _, err := exec.LookPath(c.DarwinRebuild); err != nil {
				return UpdateReadiness{}, fmt.Errorf("darwin-rebuild is unavailable")
			}
		}
	}
	git := c.GitBin
	if git == "" {
		git = "git"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := (repostate.ExecRunner{}).Output(ctx, c.Repo, git, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return UpdateReadiness{}, fmt.Errorf("repository status is unavailable; no update has run")
	}
	entries, err := repostate.ParsePorcelainV2(out)
	if err != nil {
		return UpdateReadiness{}, err
	}
	for _, entry := range entries {
		if entry.Kind == 'u' {
			return UpdateReadiness{}, fmt.Errorf("resolve repository conflicts before updating")
		}
	}
	return UpdateReadiness{Dirty: len(entries), StatusHash: sha256.Sum256(out)}, nil
}
