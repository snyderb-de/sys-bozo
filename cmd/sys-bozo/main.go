package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/plan"
	"github.com/snyderb-de/sys-bozo/internal/runner"
	"github.com/snyderb-de/sys-bozo/internal/system"
	"github.com/snyderb-de/sys-bozo/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sys-bozo:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		_, err := tea.NewProgram(tui.New(), tea.WithAltScreen()).Run()
		return err
	}

	switch args[0] {
	case "help", "-h", "--help":
		printHelp()
	case "doctor":
		printDoctor()
	case "plan":
		return runPlan(args[1:])
	case "run":
		return runAction(args[1:])
	case "version":
		fmt.Println("sys-bozo dev")
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}

	return nil
}

var runInteractive = func(item runner.WorkItem) error {
	return runner.RunInteractive(item, os.Stdin, os.Stdout, os.Stderr)
}

var startStreamed = runner.StartWork

func runWorkItem(item runner.WorkItem) error {
	if item.Mode == runner.ExecutionInteractive {
		if err := runInteractive(item); err != nil {
			return fmt.Errorf("%s: %w", item.Name, err)
		}
		return nil
	}
	scanner, wait, err := startStreamed(item)
	if err != nil {
		return err
	}
	for scanner.Scan() {
		fmt.Fprintln(os.Stdout, scanner.Text())
	}
	if err := wait(); err != nil {
		return fmt.Errorf("%s: %w", item.Name, err)
	}
	return nil
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	profile := fs.String("profile", "docs", "profile to plan")
	host := fs.String("host", "", "host name")
	exclude := fs.String("exclude", "", "comma-separated tools/groups to exclude")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var p plan.Plan
	rest := fs.Args()
	if len(rest) == 0 {
		p = plan.Install(plan.InstallOptions{Profile: *profile, Host: *host, Exclude: splitCSV(*exclude)})
	} else {
		switch rest[0] {
		case "update":
			p = plan.UpdateForContext(rest[1:], runner.Build())
		case "package":
			p = plan.PackageSearch(strings.Join(rest[1:], " "))
		case "move":
			name := argOr(rest, 1, "")
			from := argOr(rest, 2, "brew")
			to := argOr(rest, 3, "nix")
			p = plan.MovePackage(name, from, to)
		case "tarball":
			p = plan.Tarball(argOr(rest, 1, ""), argOr(rest, 2, ""), argOr(rest, 3, ""))
		case "config":
			p = plan.ConfigEdit(argOr(rest, 1, ""), argOr(rest, 2, ""))
		default:
			return fmt.Errorf("unknown plan target %q", rest[0])
		}
	}

	fmt.Println(strings.Join(p.Lines(), "\n"))
	if p.MutatingActions() > 0 {
		fmt.Printf("\n%d mutating action(s) require a future explicit apply step.\n", p.MutatingActions())
	}
	return nil
}

func runAction(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: sys-bozo run <action>")
	}
	id := args[0]
	ctx := runner.Build()
	tasks := runner.DefaultTasks(ctx)

	var found *runner.Task
	for i := range tasks {
		if tasks[i].ID == id {
			found = &tasks[i]
			break
		}
	}
	if found == nil {
		var ids []string
		for _, t := range tasks {
			ids = append(ids, t.ID)
		}
		return fmt.Errorf("unknown action %q — available: %s", id, strings.Join(ids, ", "))
	}
	if !found.Available(ctx) {
		return fmt.Errorf("action %q is not available on this host", id)
	}

	queue := runner.BuildQueue(*found, ctx)
	for _, item := range queue {
		fmt.Fprintf(os.Stderr, "$ %s\n", runner.CmdLabel(item))
		if err := runWorkItem(item); err != nil {
			return err
		}
	}
	return nil
}

func runActionList(w io.Writer, tasks []runner.Task, ctx runner.Context) {
	lastGroup := ""
	for _, t := range tasks {
		if t.Group != lastGroup {
			fmt.Fprintf(w, "\n  %s\n", t.Group)
			lastGroup = t.Group
		}
		avail := ""
		if !t.Available(ctx) {
			avail = "  (unavailable)"
		}
		fmt.Fprintf(w, "    %-8s  %s%s\n", t.ID, t.Desc, avail)
	}
}

func printDoctor() {
	facts := system.Probe()
	fmt.Println("sys-bozo doctor")
	fmt.Println("host:          ", value(facts.Hostname))
	fmt.Println("user:          ", value(facts.User))
	fmt.Println("os:            ", facts.OS+"/"+facts.Arch)
	if facts.OSID != "" {
		fmt.Println("os id:         ", facts.OSID)
	}
	fmt.Println("dotfiles repo: ", value(facts.DotfilesRepo))
	fmt.Println("branch:        ", value(facts.DotfilesBranch))
	fmt.Println("dirty files:   ", facts.DotfilesDirty)
	fmt.Println("hm generation: ", value(facts.HMGeneration))
	fmt.Println("age key:       ", facts.AgeKeyExists)
	fmt.Println("github key:    ", facts.GitHubKeyExists)
	if facts.TailscaleIP != "" {
		fmt.Println("tailscale ip:  ", facts.TailscaleIP)
	}
	fmt.Println()
	for _, status := range facts.ManagerStatus() {
		fmt.Println(status)
	}
}

func printHelp() {
	ctx := runner.Build()
	tasks := runner.DefaultTasks(ctx)
	var sb strings.Builder
	fmt.Fprintf(&sb, `sys-bozo

Usage:
  sys-bozo                         launch TUI
  sys-bozo run <action>            run an action non-interactively
  sys-bozo doctor                  inspect host facts
  sys-bozo plan [flags]            preview install/profile actions
  sys-bozo version

Actions:`)
	runActionList(&sb, tasks, ctx)
	fmt.Println(sb.String())
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Split(value, ",")
}

func argOr(args []string, index int, fallback string) string {
	if index >= len(args) {
		return fallback
	}
	return args[index]
}

func value(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}
