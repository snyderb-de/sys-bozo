package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func miniContext() Context {
	return Context{OS: "darwin", Hostname: MacMiniHost, Repo: "/fixture", NixBin: "nix", NixStoreBin: "nix-store", SudoBin: "sudo", DarwinRebuild: "darwin-rebuild", HomeManager: "home-manager", BrewBin: "brew", Topgrade: "topgrade", BrewOutdatedCasks: []string{"displaylink", "zed"}}
}

func TestMiniQueueSeparatesCleanupAndKeepsTopgradeInteractive(t *testing.T) {
	p, err := BuildMiniUpdates(miniContext(), []string{"brew-cleanup", "topgrade", "brew"})
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, item := range p.Items {
		labels = append(labels, CmdLabel(item))
		if item.Name == "topgrade" && item.Mode != ExecutionInteractive {
			t.Fatal("Topgrade must receive a native terminal")
		}
		if strings.Contains(CmdLabel(item), "displaylink") {
			t.Fatal("Brew-only pass must exclude DisplayLink")
		}
	}
	if got := labels[len(labels)-2:]; strings.Join(got, ";") != "brew missing;brew autoremove" {
		t.Fatalf("cleanup must follow dependency checks: %q", labels)
	}
}

func TestMiniPlanRejectsOtherHostsUnavailableOptionsAndMixedRecovery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Context)
		ids    []string
	}{
		{"other host", func(c *Context) { c.Hostname = "bagbook-pro" }, []string{"recommended"}},
		{"missing Topgrade", func(c *Context) { c.Topgrade = "" }, []string{"topgrade"}},
		{"mixed recovery", func(*Context) {}, []string{"ndR", "nds"}},
		{"unknown", func(*Context) {}, []string{"surprise"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := miniContext()
			tc.change(&c)
			if p, err := BuildMiniUpdates(c, tc.ids); err == nil || len(p.Items) != 0 {
				t.Fatal("unsupported plan was accepted")
			}
		})
	}
	c := miniContext()
	c.Topgrade = ""
	for _, id := range RecommendedMiniUpdates(c) {
		if id == "topgrade" {
			t.Fatal("missing Topgrade was recommended")
		}
	}
}

func TestMiniReadinessDoesNotExecuteUpdaterAndRejectsMissingTools(t *testing.T) {
	c := miniContext()
	c.Repo = t.TempDir()
	if err := os.WriteFile(filepath.Join(c.Repo, "flake.nix"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	git := exec.Command("git", "init", "-q", c.Repo)
	git.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := git.CombinedOutput(); err != nil {
		t.Fatalf("init: %s %v", out, err)
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "executed")
	updater := filepath.Join(bin, "updater")
	// The updater would leave evidence if readiness accidentally ran it.
	if err := os.WriteFile(updater, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ready, err := CheckUpdateReadiness(c, []WorkItem{{Name: updater}})
	if err != nil || ready.Dirty != 1 {
		t.Fatalf("ready=%#v err=%v", ready, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("readiness executed an updater")
	}
	if _, err := CheckUpdateReadiness(c, []WorkItem{{Name: filepath.Join(bin, "missing")}}); err == nil {
		t.Fatal("missing executable was accepted")
	}
}
