package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/snyderb-de/sys-bozo/internal/runner"
)

type ActionKind string

const (
	ActionInspect ActionKind = "inspect"
	ActionEdit    ActionKind = "edit"
	ActionCommand ActionKind = "command"
	ActionInstall ActionKind = "install"
	ActionRemove  ActionKind = "remove"
	ActionLink    ActionKind = "link"
)

type Action struct {
	Kind        ActionKind
	Title       string
	Description string
	Command     []string
	Targets     []string
	Mutates     bool
}

type Plan struct {
	Title   string
	Summary string
	Actions []Action
}

func (p Plan) MutatingActions() int {
	var count int
	for _, action := range p.Actions {
		if action.Mutates {
			count++
		}
	}
	return count
}

func (p Plan) Lines() []string {
	lines := []string{p.Title}
	if p.Summary != "" {
		lines = append(lines, p.Summary)
	}
	lines = append(lines, "")

	for i, action := range p.Actions {
		prefix := "inspect"
		if action.Mutates {
			prefix = "mutate"
		}
		lines = append(lines, fmt.Sprintf("%d. [%s] %s", i+1, prefix, action.Title))
		if action.Description != "" {
			lines = append(lines, "   "+action.Description)
		}
		if len(action.Command) > 0 {
			lines = append(lines, "   $ "+strings.Join(action.Command, " "))
		}
		for _, target := range action.Targets {
			lines = append(lines, "   target: "+target)
		}
	}

	return lines
}

type InstallOptions struct {
	Profile  string
	Host     string
	Exclude  []string
	Managers []string
}

func Install(opts InstallOptions) Plan {
	profile := valueOr(opts.Profile, "docs")
	host := valueOr(opts.Host, "current-host")
	exclusions := normalize(opts.Exclude)

	actions := []Action{
		{
			Kind:        ActionInspect,
			Title:       "Detect host facts",
			Description: "Detect OS, arch, user, shell, package managers, and XDG paths.",
		},
		{
			Kind:        ActionInspect,
			Title:       "Resolve profile",
			Description: fmt.Sprintf("Resolve profile %q for host %q with exclusions: %s.", profile, host, joinOrNone(exclusions)),
			Targets:     []string{"catalog/profiles.yaml", "catalog/tools.yaml"},
		},
		{
			Kind:        ActionEdit,
			Title:       "Prepare sys-bozo config directory",
			Description: "Create config/state/cache directories only when apply is confirmed.",
			Targets:     []string{"~/.config/sys-bozo/", "~/.local/state/sys-bozo/", "~/.cache/sys-bozo/"},
			Mutates:     true,
		},
	}

	if profile == "docs" {
		actions = append(actions, Action{
			Kind:        ActionInstall,
			Title:       "Install local docs",
			Description: "Copy or link docs into the selected local docs target.",
			Targets:     []string{"docs/", "~/.local/share/sys-bozo/docs/"},
			Mutates:     true,
		})
	}

	return Plan{
		Title:   "Install plan",
		Summary: fmt.Sprintf("Profile %q on %q. Nothing runs until apply exists and is confirmed.", profile, host),
		Actions: actions,
	}
}

func Update(selected []string) Plan {
	tasks := normalizeKeepOrder(selected)
	if len(tasks) == 0 {
		tasks = []string{"sys-bozo-self-update", "brew-update", "brew-upgrade", "nix-flake-update", "home-manager-apply", "topgrade"}
	}

	actions := []Action{}
	for _, task := range tasks {
		switch task {
		case "sys-bozo-self-update":
			actions = append(actions,
				Action{
					Kind:        ActionCommand,
					Title:       "Update sys-bozo source",
					Description: "Fast-forward the sys-bozo source repo before rebuilding the local binary.",
					Command:     []string{"git", "pull", "--ff-only"},
					Targets:     []string{"~/code/sys-bozo"},
					Mutates:     true,
				},
				Action{
					Kind:        ActionCommand,
					Title:       "Rebuild sys-bozo",
					Description: "Install the freshly built binary for the next sys-bozo run.",
					Command:     []string{"go", "build", "-o", "~/.local/bin/sys-bozo", "./cmd/sys-bozo"},
					Targets:     []string{"~/.local/bin/sys-bozo"},
					Mutates:     true,
				},
			)
		case "brew-update":
			actions = append(actions, Action{Kind: ActionCommand, Title: "Refresh Homebrew metadata", Command: []string{"brew", "update"}, Mutates: true})
		case "brew-upgrade":
			actions = append(actions, Action{Kind: ActionCommand, Title: "Upgrade selected Homebrew packages", Description: "Picker will pass explicit formulae/casks once package selection lands.", Command: []string{"brew", "upgrade"}, Mutates: true})
		case "brew-autoremove":
			actions = append(actions, Action{Kind: ActionCommand, Title: "Remove unused Homebrew dependencies", Command: []string{"brew", "autoremove"}, Mutates: true})
		case "nix-flake-update":
			actions = append(actions, Action{Kind: ActionCommand, Title: "Update Nix flake inputs", Description: "Nix usually updates by input, not individual package.", Command: []string{"nix", "flake", "update"}, Targets: []string{"flake.lock"}, Mutates: true})
		case "home-manager-apply":
			actions = append(actions, Action{Kind: ActionCommand, Title: "Apply Home Manager profile", Command: []string{"home-manager", "switch", "--flake", "."}, Mutates: true})
		case "darwin-apply":
			actions = append(actions, Action{Kind: ActionCommand, Title: "Apply nix-darwin host", Command: []string{"darwin-rebuild", "switch", "--flake", "."}, Mutates: true})
		case "topgrade":
			actions = append(actions, Action{
				Kind:        ActionCommand,
				Title:       "Run Topgrade ecosystem sweep",
				Description: "Skip Nix, Home Manager, Brew, system package upgrades, and restart checks because sys-bozo owns those paths.",
				Command: []string{
					"topgrade",
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
				},
				Mutates: true,
			})
		}
	}

	return Plan{Title: "Update plan", Summary: "Selected update actions. Preview only; executor not wired yet.", Actions: actions}
}

func UpdateForContext(selected []string, ctx runner.Context) Plan {
	tasks := runner.DefaultTasks(ctx)
	actions := updateActionsForTasks(selected, tasks, ctx)
	host := ctx.Hostname
	if strings.TrimSpace(host) == "" {
		host = "current host"
	}

	return Plan{
		Title:   "Update plan",
		Summary: fmt.Sprintf("Available update actions for %s. Preview only; run the TUI Updates tab to execute.", host),
		Actions: actions,
	}
}

func updateActionsForTasks(selected []string, tasks []runner.Task, ctx runner.Context) []Action {
	requested := normalizeKeepOrder(selected)
	if len(requested) > 0 {
		var actions []Action
		for _, id := range requested {
			task, ok := findTask(tasks, id)
			if !ok {
				continue
			}
			actions = append(actions, actionsForTask(task, ctx, true)...)
		}
		return actions
	}

	var actions []Action
	for _, task := range tasks {
		if !task.Available(ctx) {
			continue
		}
		actions = append(actions, actionsForTask(task, ctx, false)...)
	}
	return actions
}

func findTask(tasks []runner.Task, id string) (runner.Task, bool) {
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return runner.Task{}, false
}

func actionsForTask(task runner.Task, ctx runner.Context, explicit bool) []Action {
	if task.Available != nil && !task.Available(ctx) {
		if !explicit {
			return nil
		}
		return []Action{{
			Kind:        ActionInspect,
			Title:       "Skip " + task.Label,
			Description: "Task is not available on this host.",
		}}
	}

	var actions []Action
	for i, step := range task.Steps {
		name, args := step.Cmd(ctx)
		if strings.TrimSpace(name) == "" {
			continue
		}
		title := step.Title
		if title == "" {
			title = task.Label
			if len(task.Steps) > 1 {
				title = fmt.Sprintf("%s step %d", task.Label, i+1)
			}
		}
		description := step.Description
		if description == "" {
			description = task.Desc
		}
		actions = append(actions, Action{
			Kind:        ActionCommand,
			Title:       title,
			Description: description,
			Command:     append([]string{name}, args...),
			Targets:     step.Targets,
			Mutates:     true,
		})
	}
	return actions
}

func PackageSearch(query string) Plan {
	query = strings.TrimSpace(query)
	if query == "" {
		query = "<package>"
	}

	return Plan{
		Title:   "Package search plan",
		Summary: "Search both package managers, then ask where the package belongs.",
		Actions: []Action{
			{Kind: ActionInspect, Title: "Search nixpkgs", Command: []string{"nix", "search", "nixpkgs", query}},
			{Kind: ActionInspect, Title: "Search Homebrew", Command: []string{"brew", "search", query}},
			{Kind: ActionEdit, Title: "Offer catalog/profile edit", Description: "Ask whether to add package to catalog and current host profile.", Targets: []string{"catalog/tools.yaml", "catalog/hosts.yaml"}, Mutates: true},
		},
	}
}

func MovePackage(name, from, to string) Plan {
	name = valueOr(name, "<package>")
	from = valueOr(from, "brew")
	to = valueOr(to, "nix")

	return Plan{
		Title:   "Package move plan",
		Summary: fmt.Sprintf("Move %s from %s to %s. Old provider removed only after verification.", name, from, to),
		Actions: []Action{
			{Kind: ActionInspect, Title: "Identify current provider", Description: "Find binary/app owner and current version."},
			{Kind: ActionInstall, Title: "Install replacement provider", Description: fmt.Sprintf("Install %s through %s.", name, to), Mutates: true},
			{Kind: ActionInspect, Title: "Verify replacement", Description: "Confirm command/app resolves to new provider and reports a version."},
			{Kind: ActionRemove, Title: "Offer old provider removal", Description: fmt.Sprintf("Remove %s from %s only after verification.", name, from), Mutates: true},
		},
	}
}

func Tarball(name, version, target string) Plan {
	name = valueOr(name, "<name>")
	version = valueOr(version, "<version>")
	target = valueOr(target, fmt.Sprintf("~/.local/opt/%s/%s", name, version))

	return Plan{
		Title:   "Tarball install plan",
		Summary: "Tarballs stay reversible with a manifest and explicit target.",
		Actions: []Action{
			{Kind: ActionInspect, Title: "Validate archive and checksum", Description: "Prefer checksum verification when source provides one."},
			{Kind: ActionInstall, Title: "Unpack tarball", Targets: []string{target}, Mutates: true},
			{Kind: ActionLink, Title: "Link commands", Targets: []string{"~/.local/bin/"}, Mutates: true},
			{Kind: ActionEdit, Title: "Write uninstall manifest", Targets: []string{"~/.local/state/sys-bozo/tarballs/" + name + ".json"}, Mutates: true},
		},
	}
}

func ConfigEdit(name, path string) Plan {
	name = valueOr(name, "config")
	path = valueOr(path, "~/.config/"+name)

	return Plan{
		Title:   "Config edit plan",
		Summary: "Open editor, then validate and show changed files.",
		Actions: []Action{
			{Kind: ActionEdit, Title: "Open " + name, Command: []string{"$EDITOR", path}, Targets: []string{path}, Mutates: true},
			{Kind: ActionInspect, Title: "Validate " + name, Description: "Run config-specific checks after editor exits."},
			{Kind: ActionInspect, Title: "Show changed files", Command: []string{"git", "status", "--short"}},
		},
	}
}

func valueOr(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalize(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

func normalizeKeepOrder(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}
