package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/snyderb-de/sys-bozo/internal/plan"
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
	case "version":
		fmt.Println("sys-bozo dev")
	default:
		return fmt.Errorf("unknown command %q", args[0])
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
			p = plan.Update(rest[1:])
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

func printDoctor() {
	facts := system.Probe()
	fmt.Println("sys-bozo doctor")
	fmt.Println("host:", value(facts.Hostname))
	fmt.Println("user:", value(facts.User))
	fmt.Println("os:", facts.OS+"/"+facts.Arch)
	fmt.Println("shell:", value(facts.Shell))
	fmt.Println("workdir:", value(facts.WorkingDir))
	fmt.Println("repo dirty files:", facts.GitDirtyCount)
	fmt.Println("brew outdated:", facts.BrewOutdated)
	for _, status := range facts.ManagerStatus() {
		fmt.Println(status)
	}
}

func printHelp() {
	fmt.Println(`sys-bozo

Usage:
  sys-bozo                         launch TUI
  sys-bozo doctor                  inspect host facts
  sys-bozo plan [flags]            preview install/profile actions
  sys-bozo plan update [tasks...]  preview update actions
  sys-bozo plan package <name>     preview package search/catalog flow
  sys-bozo plan move <name> [from] [to]
  sys-bozo plan tarball <name> <version> [target]
  sys-bozo plan config <name> [path]

Flags:
  --profile <name>                 docs, shell-lite, cli-nix, home-manager, brew-apps, darwin-full, linux-home
  --host <name>                    canonical host name
  --exclude <list>                 comma-separated tools/groups

Nothing mutates yet. Plans first. Apply comes later.`)
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
