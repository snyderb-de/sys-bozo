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
	p, err := BuildMacUpdates(miniContext(), []string{"brew-cleanup", "topgrade", "brew"})
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
		{"other host", func(c *Context) { c.Hostname = "unreviewed-mac" }, []string{"recommended"}},
		{"missing Topgrade", func(c *Context) { c.Topgrade = "" }, []string{"topgrade"}},
		{"mixed recovery", func(*Context) {}, []string{"ndR", "nds"}},
		{"unknown", func(*Context) {}, []string{"surprise"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := miniContext()
			tc.change(&c)
			if p, err := BuildMacUpdates(c, tc.ids); err == nil || len(p.Items) != 0 {
				t.Fatal("unsupported plan was accepted")
			}
		})
	}
	c := miniContext()
	c.Topgrade = ""
	for _, id := range RecommendedMacUpdates(c) {
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

func TestManagedMacHostResolution(t *testing.T) {
	for _, tc := range []struct{ os, host, want string }{
		{"darwin", "bagbook-pro", MacBookHost},
		{"darwin", "BAGBOOK-PRO.local", MacBookHost},
		{"darwin", "bags-Mac-mini.local", MacMiniHost},
		{"darwin", "unreviewed-mac", ""},
		{"linux", "bagbook-pro", ""},
	} {
		if got := ManagedMacHost(Context{OS: tc.os, Hostname: tc.host}); got != tc.want {
			t.Errorf("%s/%s: got %q, want %q", tc.os, tc.host, got, tc.want)
		}
	}
}

func TestMacBookWorkflowUsesOneSystemOwner(t *testing.T) {
	c := miniContext()
	c.Hostname = "BAGBOOK-PRO.local"
	p, err := BuildMacUpdates(c, []string{"all", "hms", "hmu", "ndu", "brew"})
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	for _, item := range p.Items {
		commands = append(commands, CmdLabel(item))
	}
	text := strings.Join(commands, "\n")
	for _, want := range []string{"nix flake update", "brew update", "darwin-rebuild switch --flake .#bagbook-pro", "topgrade ", "brew missing"} {
		if strings.Count(text, want) != 1 {
			t.Errorf("expected one %q in:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"home-manager switch", "brew upgrade", "brew autoremove", MacMiniHost, "--yes"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("unexpected %q in:\n%s", unwanted, text)
		}
	}
	preview, err := BuildMacUpdates(c, []string{"ndsd"})
	if err != nil || len(preview.Items) != 1 {
		t.Fatalf("preview: %#v, %v", preview, err)
	}
	if got := preview.Items[0].Args; len(got) != 2 || got[1] != MacBookHost {
		t.Fatalf("wrong preview target: %q", got)
	}
}
